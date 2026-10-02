// Command controlplane runs the Go control plane API and, optionally, the
// background workers in-process.
//
// Environment:
//
//	DATABASE_URL          PostgreSQL DSN (required unless --dev-embedded-postgres)
//	LISTEN_ADDR           default :8080
//	INTERNAL_TOKEN        shared secret for the Rust gateway (required)
//	OPERATOR_TOKEN        gates organization creation (optional; open if empty)
//	GRANT_SIGNING_SEED    base64/hex 32-byte Ed25519 seed (generated if empty)
//	ALLOWED_ORIGINS       comma-separated CORS origins for the dashboard
//	FACILITATOR_URL       settlement verifier base URL for reconciliation
//	RUN_WORKERS           "false" to disable in-process workers
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

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/devpg"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/grant"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/httpapi"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/store"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/workers"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "controlplane:", err)
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
		log.Info("embedded postgres ready", "dsn", dsn)
	}
	if dsn == "" {
		return errors.New("DATABASE_URL is required (or pass --dev-embedded-postgres)")
	}
	internalToken := os.Getenv("INTERNAL_TOKEN")
	if internalToken == "" {
		return errors.New("INTERNAL_TOKEN is required")
	}

	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	var signer *grant.Signer
	if seed := os.Getenv("GRANT_SIGNING_SEED"); seed != "" {
		b, err := grant.ParseSeed(seed)
		if err != nil {
			return err
		}
		if signer, err = grant.NewSigner(b); err != nil {
			return err
		}
	} else {
		if signer, err = grant.Generate(); err != nil {
			return err
		}
		log.Warn("GRANT_SIGNING_SEED not set; generated an ephemeral grant key (grants will not survive restart)")
	}
	log.Info("grant signing key", "kid", signer.KeyID(), "public_key_base64", signer.PublicKeyBase64())

	var origins []string
	if o := os.Getenv("ALLOWED_ORIGINS"); o != "" {
		for _, s := range strings.Split(o, ",") {
			origins = append(origins, strings.TrimSpace(s))
		}
	}
	api, err := httpapi.New(st, signer, httpapi.Config{
		InternalToken: internalToken, OperatorToken: os.Getenv("OPERATOR_TOKEN"), AllowedOrigins: origins, Logger: log,
	})
	if err != nil {
		return err
	}

	if os.Getenv("RUN_WORKERS") != "false" {
		runner := &workers.Runner{Store: st, Cfg: workers.Defaults(), Log: log, Publisher: workers.LogPublisher{Log: log}}
		if fu := os.Getenv("FACILITATOR_URL"); fu != "" {
			runner.Verifier = workers.HTTPVerifier{BaseURL: strings.TrimRight(fu, "/")}
		}
		if v := os.Getenv("UNKNOWN_RELEASE_AFTER"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				runner.Cfg.UnknownReleaseAfter = d
			}
		}
		if v := os.Getenv("SUBMITTED_GRACE"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				runner.Cfg.SubmittedGrace = d
			}
		}
		go runner.Run(ctx)
	}

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		log.Info("control plane listening", "addr", addr)
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
