package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"unowallet/rewards/internal/rewards"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("standalone worker requires DATABASE_URL (PostgreSQL) for multi-process coordination")
	}
	store, err := rewards.NewPostgresStore(ctx, dbURL)
	if err != nil {
		log.Fatalf("postgres init failed: %v", err)
	}
	defer store.Close()

	svc := rewards.NewService(store, rewards.NewSandboxProvider(), os.Getenv("WEBHOOK_SECRET"))
	log.Println("standalone rewards reconciliation worker started")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("worker shutting down")
			return
		case <-ticker.C:
			tenants, err := store.Tenants(ctx)
			if err != nil {
				log.Printf("list tenants error: %v", err)
				continue
			}
			for _, tid := range tenants {
				rep, err := svc.RunWorkerTick(ctx, tid)
				if err != nil {
					log.Printf("worker tick tenant=%s error: %v", tid, err)
					continue
				}
				if rep.RewardsMadeEligible > 0 || rep.PayoutsReconciled > 0 || rep.NewExceptionsOpened > 0 {
					log.Printf("worker tick tenant=%s eligible=%d reconciled_payouts=%d new_exceptions=%d balanced=%v",
						tid, rep.RewardsMadeEligible, rep.PayoutsReconciled, rep.NewExceptionsOpened, rep.LedgerBalanced)
				}
			}
		}
	}
}
