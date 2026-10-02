package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"unowallet/checkout/internal/checkout"
)

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("the separate worker requires DATABASE_URL; file persistence only supports the embedded worker")
	}
	if mode := os.Getenv("PAYMENT_MODE"); mode != "" && mode != "sandbox" {
		log.Fatal("only sandbox payments are enabled")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	store, e := checkout.NewPostgresStore(ctx, url)
	if e != nil {
		log.Fatal(e)
	}
	defer store.Close()
	checkout.NewService(store).RunWorker(ctx, func(e error) { log.Printf("worker operation failed (%T); retry preserved", e) })
}
