package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"unowallet/rewards/internal/rewards"
)

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := envOr("ADDR", "127.0.0.1:8089")
	dbURL := os.Getenv("DATABASE_URL")
	statePath := envOr("STATE_FILE", ".local/rewards-state.json")
	operatorToken := envOr("OPERATOR_TOKEN", "local-operator")
	webhookSecret := envOr("WEBHOOK_SECRET", "local-webhook-secret")
	webDist := os.Getenv("WEB_DIST")
	appOrigin := envOr("APP_ORIGIN", "http://127.0.0.1:5189")
	seedDemo := envOr("SEED_PILOT_DATA", "true") == "true"

	var store rewards.Store
	var err error
	if dbURL != "" {
		log.Println("connecting to PostgreSQL store")
		store, err = rewards.NewPostgresStore(ctx, dbURL)
	} else {
		log.Printf("using local durable file store at %s", statePath)
		store, err = rewards.NewFileStore(statePath)
	}
	if err != nil {
		log.Fatalf("store init failed: %v", err)
	}
	defer store.Close()

	provider := rewards.NewSandboxProvider()
	svc := rewards.NewService(store, provider, webhookSecret)

	if seedDemo {
		if err := svc.SeedPilotData(ctx, "default"); err != nil {
			log.Fatalf("seed pilot data failed: %v", err)
		}
	}

	svc.StartBackgroundWorker(ctx, 5*time.Second)

	handler := rewards.NewHTTPHandler(svc, rewards.HTTPConfig{
		OperatorToken:   operatorToken,
		WebhookSecret:   webhookSecret,
		AllowUnsignedUI: envOr("SANDBOX_UI_SIMULATOR", "true") == "true",
		WebDistDir:      webDist,
		AppOrigin:       appOrigin,
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("merchant-funded-stablecoin-rewards API listening on http://%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
