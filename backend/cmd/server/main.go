// Command server runs the MyWallet API and, in-process, the retry worker.
//
// Environment:
//
//	DATABASE_URL           PostgreSQL DSN (required unless --dev-embedded-postgres)
//	LISTEN_ADDR            default :8080
//	PROVIDER               "stripe" (default) or "fake" (development fixture)
//	STRIPE_SECRET_KEY      sk_test_/sk_live_ (required for stripe)
//	STRIPE_PUBLISHABLE_KEY pk_test_/pk_live_ (required for stripe)
//	STRIPE_WEBHOOK_SECRET  whsec_ for /v1/webhooks/provider (required for stripe)
//	STRIPE_API_VERSION     pinned API version; must match the iOS SDK (required for stripe)
//	APPLE_BUNDLE_IDS       comma-separated Sign in with Apple audiences (required unless DEV_SIGN_IN)
//	DEV_SIGN_IN            "1" accepts "dev:<subject>" tokens; development only
//	SESSION_TTL            Go duration, default 720h
//	RUN_WORKER             "false" to disable the in-process worker
//
// PROVIDER=fake refuses to start when STRIPE_SECRET_KEY is set, so fixture
// mode cannot be enabled by accident on a host that holds real keys.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"mywallet/backend/internal/appleauth"
	"mywallet/backend/internal/devpg"
	"mywallet/backend/internal/enrollment"
	"mywallet/backend/internal/httpapi"
	"mywallet/backend/internal/provider"
	"mywallet/backend/internal/provider/fake"
	"mywallet/backend/internal/provider/stripe"
	"mywallet/backend/internal/store"
	"mywallet/backend/internal/workers"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run() error {
	devEmbedded := flag.Bool("dev-embedded-postgres", false, "start an embedded PostgreSQL (development only)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("DATABASE_URL")
	if *devEmbedded {
		log.Info("starting embedded postgres (development only)")
		inst, err := devpg.Start()
		if err != nil {
			return err
		}
		defer inst.Stop()
		dsn = inst.DSN
	}
	if dsn == "" {
		return errors.New("DATABASE_URL is required (or pass --dev-embedded-postgres)")
	}

	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	var pv provider.Provider
	var hooks httpapi.DevHooks
	switch name := envDefault("PROVIDER", "stripe"); name {
	case "stripe":
		client, err := stripe.New(stripe.Config{
			SecretKey:      os.Getenv("STRIPE_SECRET_KEY"),
			PublishableKey: os.Getenv("STRIPE_PUBLISHABLE_KEY"),
			WebhookSecret:  os.Getenv("STRIPE_WEBHOOK_SECRET"),
			APIVersion:     os.Getenv("STRIPE_API_VERSION"),
		})
		if err != nil {
			return err
		}
		if client.Live() {
			log.Warn("LIVE Stripe keys configured: real cards will be saved")
		}
		pv = client
	case "fake":
		if os.Getenv("STRIPE_SECRET_KEY") != "" {
			return errors.New("PROVIDER=fake refuses to start with STRIPE_SECRET_KEY set")
		}
		log.Warn("using the FAKE provider: cards are development fixtures, not real cards")
		f := fake.New()
		pv = f
		hooks = fake.Hooks{P: f}
	default:
		return fmt.Errorf("unknown PROVIDER %q", name)
	}

	var verifier appleauth.Verifier
	if os.Getenv("DEV_SIGN_IN") == "1" {
		log.Warn("DEV_SIGN_IN enabled: accepting dev:<subject> identity tokens")
		verifier = appleauth.Dev{}
	} else {
		auds := splitCSV(os.Getenv("APPLE_BUNDLE_IDS"))
		if len(auds) == 0 {
			return errors.New("APPLE_BUNDLE_IDS is required (or set DEV_SIGN_IN=1 for development)")
		}
		verifier, err = appleauth.NewApple(appleauth.Config{Audiences: auds})
		if err != nil {
			return err
		}
	}

	sessionTTL := 30 * 24 * time.Hour
	if v := os.Getenv("SESSION_TTL"); v != "" {
		if sessionTTL, err = time.ParseDuration(v); err != nil {
			return fmt.Errorf("SESSION_TTL: %w", err)
		}
	}

	enroll := enrollment.New(st, pv, log)
	api, err := httpapi.New(st, enroll, verifier, httpapi.Config{SessionTTL: sessionTTL, DevHooks: hooks, Logger: log})
	if err != nil {
		return err
	}

	if os.Getenv("RUN_WORKER") != "false" {
		w := workers.New(st, enroll, workers.Config{Logger: log})
		go w.Run(ctx)
	}

	addr := envDefault("LISTEN_ADDR", ":8080")
	srv := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr, "provider", pv.Name())
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func envDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
