// Command worker runs the recovery, reconciliation and outbox loops as a
// separate process (for deployments that do not run them inside the API).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/store"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/workers"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "worker:", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	runner := &workers.Runner{Store: st, Cfg: workers.Defaults(), Log: log, Publisher: workers.LogPublisher{Log: log}}
	if fu := os.Getenv("FACILITATOR_URL"); fu != "" {
		runner.Verifier = workers.HTTPVerifier{BaseURL: strings.TrimRight(fu, "/")}
	}
	if v := os.Getenv("UNKNOWN_RELEASE_AFTER"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			runner.Cfg.UnknownReleaseAfter = d
		}
	}
	log.Info("worker started")
	runner.Run(ctx)
	return nil
}
