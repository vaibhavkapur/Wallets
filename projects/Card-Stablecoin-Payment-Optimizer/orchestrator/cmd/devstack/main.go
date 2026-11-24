// Command devstack runs the whole optimizer locally with no external
// dependencies: an embedded PostgreSQL, the Rust engine as a child process,
// a seeded development tenant, and the API. It is for development only.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/api"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/app"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

const devEngineToken = "dev-engine-token-0123456789abcdef"

func main() {
	mode := flag.String("mode", "shadow", "optimizer mode: shadow or assisted")
	listen := flag.String("listen", "127.0.0.1:8080", "API listen address")
	engineBin := flag.String("engine", "", "path to the optimizer-engine binary (default: engine/target/debug/optimizer-engine)")
	root := flag.String("root", ".", "repository root")
	pgPort := flag.Uint("pg-port", 54329, "embedded PostgreSQL port")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*mode, *listen, *engineBin, *root, uint32(*pgPort), log); err != nil {
		log.Error("devstack stopped", "error", err)
		os.Exit(1)
	}
}

func run(mode, listen, engineBin, root string, pgPort uint32, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if engineBin == "" {
		engineBin = filepath.Join(root, "engine", "target", "debug", "optimizer-engine")
	}
	if _, err := os.Stat(engineBin); err != nil {
		return fmt.Errorf("engine binary not found at %s (run `make engine`)", engineBin)
	}

	// Embedded PostgreSQL with a persistent data directory under .dev/.
	dataDir := filepath.Join(root, ".dev", "postgres")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).Port(pgPort).
		Username("optimizer").Password("optimizer").Database("optimizer").
		RuntimePath(filepath.Join(root, ".dev", "pg-runtime")).DataPath(dataDir).
		CachePath(filepath.Join(os.TempDir(), "optimizer-embedded-postgres-cache")).
		StartTimeout(60 * time.Second).Logger(nil))
	if err := pg.Start(); err != nil {
		return fmt.Errorf("embedded postgres: %w", err)
	}
	defer pg.Stop() //nolint:errcheck
	dsn := fmt.Sprintf("postgres://optimizer:optimizer@127.0.0.1:%d/optimizer?sslmode=disable", pgPort)

	// Engine child process.
	engineAddr := freeAddr()
	engine := exec.CommandContext(ctx, engineBin)
	engine.Env = append(os.Environ(), "ENGINE_AUTH_TOKEN="+devEngineToken, "ENGINE_LISTEN_ADDR="+engineAddr)
	engine.Stderr = os.Stderr
	if err := engine.Start(); err != nil {
		return err
	}
	defer engine.Process.Kill() //nolint:errcheck
	if !waitHealthy("http://"+engineAddr+"/healthz", 20*time.Second) {
		return fmt.Errorf("engine did not become healthy")
	}

	// Seed the development tenant and key. The key is stable across restarts
	// so the web UI's dev proxy can use it from .dev/api-key.
	db, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := db.EnsureTenant(ctx, "ten_dev", "Development tenant", now); err != nil {
		return err
	}
	keyPath := filepath.Join(root, ".dev", "api-key")
	secret, err := os.ReadFile(keyPath)
	if err != nil {
		secret = []byte(ids.NewAPIKey())
		if err := os.WriteFile(keyPath, secret, 0o600); err != nil {
			return err
		}
	}
	if _, err := db.CreateAPIKey(ctx, "ten_dev", "devstack", api.AllScopes, string(secret), now); err != nil {
		return err
	}
	db.Close()
	log.Info("development API key", "path", keyPath, "tenant", "ten_dev")

	cfg := app.Config{
		ListenAddr:  listen,
		DatabaseURL: dsn,
		EngineURL:   "http://" + engineAddr,
		EngineToken: devEngineToken,
		Mode:        mode,
		PolicyFile:  filepath.Join(root, "config", "policy.v1.json"),
		RecordedDir: filepath.Join(root, "fixtures", "recorded", "v1"),
	}
	return app.Run(ctx, cfg, log, nil)
}

func freeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "127.0.0.1:8081"
	}
	defer l.Close()
	return l.Addr().String()
}

func waitHealthy(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}
