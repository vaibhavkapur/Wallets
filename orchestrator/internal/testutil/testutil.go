// Package testutil starts the real calculation engine and an embedded
// PostgreSQL for integration tests. Tests never fake the engine: every cost in
// a test comes from the same binary that serves production.
package testutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// EngineToken is the shared secret used for test engines.
const EngineToken = "test-engine-token-0123456789abcdef"

// RepoRoot returns the repository root (the directory holding engine/ and orchestrator/).
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func freePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// EngineBinary locates the engine, building it when necessary.
func EngineBinary(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("ENGINE_BIN"); p != "" {
		return p
	}
	engineDir := filepath.Join(RepoRoot(), "engine")
	bin := filepath.Join(engineDir, "target", "debug", "optimizer-engine")
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	t.Logf("building the engine in %s", engineDir)
	cmd := exec.Command("cargo", "build", "-q", "-p", "optimizer-service")
	cmd.Dir = engineDir
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cargo build failed: %v (set ENGINE_BIN to a built optimizer-engine)", err)
	}
	return bin
}

// StartEngine runs the engine on a free port and returns its base URL.
func StartEngine(t testing.TB) string {
	t.Helper()
	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	cmd := exec.Command(EngineBinary(t))
	cmd.Env = append(os.Environ(), "ENGINE_AUTH_TOKEN="+EngineToken, "ENGINE_LISTEN_ADDR="+addr)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start engine: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})
	url := "http://" + addr
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return url
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("engine did not become healthy")
	return ""
}

// StartPostgres runs an embedded PostgreSQL and returns its DSN.
func StartPostgres(t testing.TB) string {
	t.Helper()
	port := uint32(freePort(t))
	runtimeDir, err := os.MkdirTemp("", "optimizer-pg-")
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(os.TempDir(), "optimizer-embedded-postgres-cache")
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(port).
		Username("optimizer").
		Password("optimizer").
		Database("optimizer").
		RuntimePath(runtimeDir).
		CachePath(cache).
		StartTimeout(60 * time.Second).
		Logger(nil))
	if err := pg.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	t.Cleanup(func() {
		pg.Stop() //nolint:errcheck
		os.RemoveAll(runtimeDir)
	})
	return fmt.Sprintf("postgres://optimizer:optimizer@127.0.0.1:%d/optimizer?sslmode=disable", port)
}

// WaitFor polls until cond is true or the timeout elapses.
func WaitFor(ctx context.Context, timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}
