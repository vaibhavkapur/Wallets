// Package workers runs the small retry loop that drives pending provider
// operations (detachment, account deletion, setup reconciliation) to
// completion and expires abandoned setup attempts.
package workers

import (
	"context"
	"log/slog"
	"time"

	"mywallet/backend/internal/enrollment"
	"mywallet/backend/internal/store"
)

// Config tunes the loop.
type Config struct {
	Interval       time.Duration // tick period (default 15s)
	Lease          time.Duration // how long a claimed op is hidden (default 2m)
	BatchSize      int           // ops per tick (default 20)
	MinBackoff     time.Duration // first retry delay (default 30s)
	MaxBackoff     time.Duration // retry cap (default 1h)
	ReconcileAfter time.Duration // open setups idle this long are re-checked (default 10m)
	ExpireAfter    time.Duration // open setups idle this long are canceled (default 24h)
	Logger         *slog.Logger
}

func (c *Config) defaults() {
	if c.Interval <= 0 {
		c.Interval = 15 * time.Second
	}
	if c.Lease <= 0 {
		c.Lease = 2 * time.Minute
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 20
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = 30 * time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = time.Hour
	}
	if c.ReconcileAfter <= 0 {
		c.ReconcileAfter = 10 * time.Minute
	}
	if c.ExpireAfter <= 0 {
		c.ExpireAfter = 24 * time.Hour
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Worker drives pending operations.
type Worker struct {
	st     *store.Store
	enroll *enrollment.Service
	cfg    Config
}

// New builds a worker.
func New(st *store.Store, enroll *enrollment.Service, cfg Config) *Worker {
	cfg.defaults()
	return &Worker{st: st, enroll: enroll, cfg: cfg}
}

// Run ticks until ctx is canceled.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.cfg.Interval)
	defer t.Stop()
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs one pass: due operations, then stale setup maintenance.
// Exported so tests can drive it deterministically.
func (w *Worker) Tick(ctx context.Context) {
	ops, err := w.st.ClaimDueOperations(ctx, w.cfg.BatchSize, w.cfg.Lease)
	if err != nil {
		w.cfg.Logger.Error("claim operations", "err", err)
	}
	for _, op := range ops {
		if err := w.enroll.RunOperation(ctx, op); err != nil {
			delay := w.backoff(op.Attempts)
			w.cfg.Logger.Warn("operation failed", "kind", op.Kind, "attempt", op.Attempts+1, "retry_in", delay, "err", err)
			if ferr := w.st.FailOperation(ctx, op.ID, truncate(err.Error(), 500), delay); ferr != nil {
				w.cfg.Logger.Error("record failure", "err", ferr)
			}
			continue
		}
		if err := w.st.CompleteOperation(ctx, nil, op.ID); err != nil {
			w.cfg.Logger.Error("complete operation", "err", err)
		}
	}
	if err := w.enroll.ExpireStaleSetups(ctx, w.cfg.ReconcileAfter, w.cfg.ExpireAfter, w.cfg.BatchSize); err != nil {
		w.cfg.Logger.Error("expire stale setups", "err", err)
	}
}

func (w *Worker) backoff(attempts int) time.Duration {
	d := w.cfg.MinBackoff
	for i := 0; i < attempts && d < w.cfg.MaxBackoff; i++ {
		d *= 2
	}
	if d > w.cfg.MaxBackoff {
		d = w.cfg.MaxBackoff
	}
	return d
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
