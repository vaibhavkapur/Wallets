package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"unowallet/recurring/internal/recurring"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	addr := envOr("ADDR", "127.0.0.1:8090")
	demo := envOr("DEMO_MODE", "true") == "true"

	var store recurring.Store
	var err error
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		store, err = recurring.NewPgStore(ctx, dbURL)
		if err != nil {
			log.Fatalf("postgres store init: %v", err)
		}
		log.Printf("using PostgreSQL store")
	} else {
		dataPath := envOr("DATA_PATH", filepath.Join(".local", "recurring-state.json"))
		store, err = recurring.NewFileStore(dataPath)
		if err != nil {
			log.Fatalf("file store init: %v", err)
		}
		log.Printf("using local durable file store at %s", dataPath)
	}
	defer store.Close()

	var clk recurring.Clock = recurring.RealClock{}
	if demo {
		clk = recurring.NewFakeClock(time.Now().UTC())
	}

	var checkoutAdapter recurring.CheckoutAdapter = recurring.NewSandboxCheckoutAdapter()
	if checkoutURL := os.Getenv("AGENT_CHECKOUT_URL"); checkoutURL != "" {
		checkoutAdapter = recurring.NewAgentCheckoutHTTPAdapter(checkoutURL)
		log.Printf("using Agent Checkout HTTP adapter at %s", checkoutURL)
	}

	optimizer := recurring.NewPaymentOptimizerClient(os.Getenv("OPTIMIZER_URL"))
	svc := recurring.NewService(store, clk, recurring.NewSandboxMerchantAdapter(), checkoutAdapter, optimizer)
	worker := recurring.NewWorker(svc, "server-embedded-worker")

	if envOr("RUN_EMBEDDED_WORKER", "true") == "true" {
		go worker.Start(ctx)
	}

	srv := recurring.NewServer(svc, worker, recurring.HTTPConfig{
		SessionSecret: envOr("SESSION_SECRET", "local-dev-recurring-secret"),
		OperatorToken: envOr("OPERATOR_TOKEN", "local-operator"),
		WebhookSecret: envOr("WEBHOOK_SECRET", "local-webhook-secret"),
		AllowedOrigin: envOr("ALLOWED_ORIGIN", "http://127.0.0.1:5190"),
		Demo:          demo,
		SecureCookies: envOr("SECURE_COOKIES", "false") == "true",
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("recurring-purchase-agent listening on http://%s (demo=%t)", addr, demo)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("http server failed: %v", err)
	}
}
