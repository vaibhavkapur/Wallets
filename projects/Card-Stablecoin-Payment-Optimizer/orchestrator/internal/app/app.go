// Package app wires configuration, storage, the engine client, the quote
// provider, the HTTP API and the background workers together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/api"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/engine"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/outbox"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/quotes"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/service"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

// Config is read from the environment.
type Config struct {
	ListenAddr    string
	DatabaseURL   string
	EngineURL     string
	EngineToken   string
	Mode          string
	PolicyFile    string
	RecordedDir   string
	WebhookURL    string
	WebhookSecret string
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// FromEnv reads and validates configuration.
func FromEnv() (Config, error) {
	c := Config{
		ListenAddr:    env("OPTIMIZER_LISTEN_ADDR", "127.0.0.1:8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		EngineURL:     env("ENGINE_URL", "http://127.0.0.1:8081"),
		EngineToken:   os.Getenv("ENGINE_AUTH_TOKEN"),
		Mode:          env("OPTIMIZER_MODE", service.ModeShadow),
		PolicyFile:    env("OPTIMIZER_POLICY_FILE", "config/policy.v1.json"),
		RecordedDir:   env("OPTIMIZER_RECORDED_DIR", "fixtures/recorded/v1"),
		WebhookURL:    os.Getenv("EXECUTOR_WEBHOOK_URL"),
		WebhookSecret: os.Getenv("EXECUTOR_WEBHOOK_SECRET"),
	}
	return c, c.Validate()
}

// Validate reports configuration that cannot work.
func (c Config) Validate() error {
	switch {
	case c.DatabaseURL == "":
		return errors.New("DATABASE_URL is required")
	case c.EngineToken == "":
		return errors.New("ENGINE_AUTH_TOKEN is required")
	case c.Mode != service.ModeShadow && c.Mode != service.ModeAssisted:
		return errors.New("OPTIMIZER_MODE must be shadow or assisted")
	case (c.WebhookURL == "") != (c.WebhookSecret == ""):
		return errors.New("EXECUTOR_WEBHOOK_URL and EXECUTOR_WEBHOOK_SECRET must be set together")
	case c.WebhookSecret != "" && len(c.WebhookSecret) < 24:
		return errors.New("EXECUTOR_WEBHOOK_SECRET must be at least 24 bytes")
	}
	return nil
}

// LoadPolicy reads a versioned policy file strictly.
func LoadPolicy(path string) (contract.Policy, error) {
	var policy contract.Policy
	data, err := os.ReadFile(path)
	if err != nil {
		return policy, err
	}
	if err := contract.DecodeStrict(data, &policy); err != nil {
		return policy, fmt.Errorf("%s: %w", path, err)
	}
	if policy.PolicyVersion == "" || policy.ComparisonCurrency != "USD" {
		return policy, fmt.Errorf("%s: a policy_version and USD comparison currency are required", path)
	}
	return policy, nil
}

// Run serves until ctx is cancelled. ready, when non-nil, receives the bound
// address once the listener is up.
func Run(ctx context.Context, c Config, log *slog.Logger, ready chan<- string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	policy, err := LoadPolicy(c.PolicyFile)
	if err != nil {
		return err
	}
	recorded, err := quotes.NewRecorded(c.RecordedDir)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	svc, err := service.New(service.Options{
		Store:    db,
		Engine:   engine.NewClient(c.EngineURL, c.EngineToken),
		Quotes:   quotes.NewCached(recorded, 30*time.Second),
		Balances: recorded,
		Policy:   policy,
		Mode:     c.Mode,
	})
	if err != nil {
		return err
	}

	now := func() time.Time { return time.Now().UTC() }
	if c.WebhookURL != "" {
		relay := &outbox.Relay{
			Store: db, Log: log, Now: now, Batch: 20, Lease: 30 * time.Second,
			Publisher: &outbox.Webhook{URL: c.WebhookURL, Secret: []byte(c.WebhookSecret), HTTP: &http.Client{Timeout: 10 * time.Second}, Now: now},
		}
		go relay.Run(ctx, 2*time.Second)
	} else {
		log.Info("no executor webhook configured; handoff events stay in the outbox")
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if n, err := svc.Sweep(ctx, time.Minute); err != nil {
					log.Error("handoff sweep failed", "error", err)
				} else if n > 0 {
					log.Warn("handoffs expired without an outcome and need reconciliation", "count", n)
				}
			}
		}
	}()

	listener, err := net.Listen("tcp", c.ListenAddr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           api.New(svc, db, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Info("optimizer listening", "addr", listener.Addr().String(), "mode", c.Mode, "policy", policy.PolicyVersion, "quotes", recorded.Name())
	if ready != nil {
		ready <- listener.Addr().String()
	}

	errc := make(chan error, 1)
	go func() { errc <- server.Serve(listener) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
