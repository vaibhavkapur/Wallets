// Command optimizerd serves the Payment Optimizer API.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/app"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := app.FromEnv()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := app.Run(ctx, cfg, log, nil); err != nil {
		log.Error("optimizerd stopped", "error", err)
		os.Exit(1)
	}
}
