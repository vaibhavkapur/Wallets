package recurring

import (
	"context"
	"sort"
	"time"
)

// Worker executes durable database-backed jobs with leases, bounded retries,
// stale-lease recovery, due-rule scanning, proposal expiration, and reconciliation.
type Worker struct {
	Service       *Service
	WorkerID      string
	LeaseDuration time.Duration
	PollInterval  time.Duration
}

func NewWorker(svc *Service, workerID string) *Worker {
	if workerID == "" {
		workerID = newID("wrk")
	}
	return &Worker{
		Service:       svc,
		WorkerID:      workerID,
		LeaseDuration: 30 * time.Second,
		PollInterval:  2 * time.Second,
	}
}

// RunOnce executes a full deterministic recovery scan and processes all currently due jobs.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	tenants, err := w.Service.Store.Tenants(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if _, err := w.Service.ScanDueRules(ctx, tenantID); err != nil {
			return processed, err
		}
		if _, err := w.Service.ExpireStaleProposals(ctx, tenantID); err != nil {
			return processed, err
		}
		if err := w.reclaimExpiredLeases(ctx, tenantID); err != nil {
			return processed, err
		}
		n, err := w.drainJobsForTenant(ctx, tenantID)
		processed += n
		if err != nil {
			return processed, err
		}
	}
	return processed, nil
}

// Start runs the periodic worker loop until ctx is canceled.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()
	for {
		_, _ = w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Worker) reclaimExpiredLeases(ctx context.Context, tenantID string) error {
	now := w.Service.Clock.Now().UTC()
	return w.Service.Store.Update(ctx, tenantID, func(st *TenantState) error {
		for _, job := range st.Jobs {
			if job.Status == "leased" && job.LeaseUntil != nil && now.After(*job.LeaseUntil) {
				if job.Attempts >= job.MaxAttempts {
					job.Status = "dead"
					job.LastError = "lease expired after max attempts"
				} else {
					job.Status = "pending"
					job.LeaseOwner = ""
					job.LeaseUntil = nil
					job.NextAttemptAt = now
				}
				job.UpdatedAt = now
			}
		}
		return nil
	})
}

func (w *Worker) drainJobsForTenant(ctx context.Context, tenantID string) (int, error) {
	processed := 0
	for guard := 0; guard < 100; guard++ {
		job, err := w.acquireNextLease(ctx, tenantID)
		if err != nil || job == nil {
			return processed, err
		}
		execErr := w.executeJob(ctx, tenantID, job)
		if err := w.finishJob(ctx, tenantID, job.JobID, execErr); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func (w *Worker) acquireNextLease(ctx context.Context, tenantID string) (*WorkerJob, error) {
	now := w.Service.Clock.Now().UTC()
	var acquired *WorkerJob

	err := w.Service.Store.Update(ctx, tenantID, func(st *TenantState) error {
		var candidates []*WorkerJob
		for _, job := range st.Jobs {
			if job.Status == "pending" && !job.NextAttemptAt.After(now) {
				candidates = append(candidates, job)
			}
		}
		if len(candidates) == 0 {
			return nil
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		})
		job := candidates[0]
		until := now.Add(w.LeaseDuration)
		job.Status = "leased"
		job.LeaseOwner = w.WorkerID
		job.LeaseUntil = &until
		job.Attempts++
		job.UpdatedAt = now
		cp := *job
		acquired = &cp
		return nil
	})
	return acquired, err
}

func (w *Worker) executeJob(ctx context.Context, tenantID string, job *WorkerJob) error {
	switch job.Kind {
	case "quote_occurrence":
		_, err := w.Service.QuoteOccurrence(ctx, tenantID, job.ResourceID)
		return err
	case "execute_occurrence":
		_, err := w.Service.ExecuteOccurrence(ctx, tenantID, "worker:"+w.WorkerID, job.ResourceID)
		return err
	case "reconcile_checkout":
		_, err := w.Service.ReconcileOccurrence(ctx, tenantID, "worker:"+w.WorkerID, job.ResourceID)
		return err
	case "dispatch_notification":
		now := w.Service.Clock.Now().UTC()
		return w.Service.Store.Update(ctx, tenantID, func(st *TenantState) error {
			for _, ev := range st.Notifications {
				if ev.EventID == job.ResourceID && ev.Status == "pending" {
					ev.Status = "dispatched"
					ev.Attempts++
					t := now
					ev.DispatchedAt = &t
					break
				}
			}
			return nil
		})
	default:
		return nil
	}
}

func (w *Worker) finishJob(ctx context.Context, tenantID, jobID string, execErr error) error {
	now := w.Service.Clock.Now().UTC()
	return w.Service.Store.Update(ctx, tenantID, func(st *TenantState) error {
		job, ok := st.Jobs[jobID]
		if !ok {
			return nil
		}
		if execErr == nil {
			job.Status = "done"
			job.LeaseOwner = ""
			job.LeaseUntil = nil
			job.LastError = ""
			job.UpdatedAt = now
			return nil
		}
		job.LastError = execErr.Error()
		job.LeaseOwner = ""
		job.LeaseUntil = nil
		if job.Attempts >= job.MaxAttempts {
			job.Status = "dead"
		} else {
			job.Status = "pending"
			// Bounded exponential backoff: 5s, 10s, 20s, 40s...
			backoff := time.Duration(5*(1<<(job.Attempts-1))) * time.Second
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			job.NextAttemptAt = now.Add(backoff)
		}
		job.UpdatedAt = now
		return nil
	})
}
