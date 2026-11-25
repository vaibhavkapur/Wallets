package main

import (
	"context"
	"log"
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

	var store recurring.Store
	var err error
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		store, err = recurring.NewPgStore(ctx, dbURL)
		if err != nil {
			log.Fatalf("postgres store init: %v", err)
		}
	} else {
		dataPath := envOr("DATA_PATH", filepath.Join(".local", "recurring-state.json"))
		store, err = recurring.NewFileStore(dataPath)
		if err != nil {
			log.Fatalf("file store init: %v", err)
		}
	}
	defer store.Close()

	var checkoutAdapter recurring.CheckoutAdapter = recurring.NewSandboxCheckoutAdapter()
	if checkoutURL := os.Getenv("AGENT_CHECKOUT_URL"); checkoutURL != "" {
		checkoutAdapter = recurring.NewAgentCheckoutHTTPAdapter(checkoutURL)
	}

	svc := recurring.NewService(store, recurring.RealClock{}, recurring.NewSandboxMerchantAdapter(), checkoutAdapter, nil)
	worker := recurring.NewWorker(svc, envOr("WORKER_ID", "standalone-worker"))
	worker.PollInterval = 2 * time.Second

	log.Printf("recurring-purchase-agent worker %s started", worker.WorkerID)
	worker.Start(ctx)
	log.Printf("worker %s stopped cleanly", worker.WorkerID)
}
