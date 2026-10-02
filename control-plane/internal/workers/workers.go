// Package workers runs the background loops: reservation recovery, payment
// reconciliation, and outbox publishing. Each loop is idempotent and safe to
// run on multiple instances because all state transitions are guarded by
// row locks and status checks in the store.
package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/store"
)

// VerifyStatus is what a settlement provider reports for a payment.
type VerifyStatus string

const (
	VerifySettled  VerifyStatus = "settled"
	VerifyNotFound VerifyStatus = "not_found"
	VerifyPending  VerifyStatus = "pending"
)

// VerifyResult is the provider's answer.
type VerifyResult struct {
	Status        VerifyStatus
	SubmissionRef string
	PayerRef      string
	SettledAmount money.Amount
}

// SettlementVerifier looks up a submitted payment at the facilitator/network.
type SettlementVerifier interface {
	Lookup(ctx context.Context, attempt domain.PaymentAttempt) (VerifyResult, error)
}

// Publisher receives outbox messages (webhooks, queues, notifications).
type Publisher interface {
	Publish(ctx context.Context, msg domain.OutboxMessage) error
}

// LogPublisher writes outbox messages to the log (development default).
type LogPublisher struct{ Log *slog.Logger }

func (p LogPublisher) Publish(_ context.Context, m domain.OutboxMessage) error {
	p.Log.Info("outbox", "topic", m.Topic, "id", m.ID)
	return nil
}

// Config tunes the loops.
type Config struct {
	Interval time.Duration
	// ExpiryMargin is added to a grant's expiry before an unsubmitted
	// authorization is released, covering clock skew between services.
	ExpiryMargin time.Duration
	// SubmittedGrace is how long a "submitted" attempt may go without an
	// outcome report before it is treated as unknown and reconciled.
	SubmittedGrace time.Duration
	// UnknownReleaseAfter is how long an unknown attempt may remain
	// not_found at the provider before its reservation is released.
	UnknownReleaseAfter time.Duration
}

// Defaults returns conservative settings.
func Defaults() Config {
	return Config{Interval: 5 * time.Second, ExpiryMargin: 30 * time.Second, SubmittedGrace: 2 * time.Minute, UnknownReleaseAfter: 24 * time.Hour}
}

// Runner owns the loops.
type Runner struct {
	Store     *store.Store
	Verifier  SettlementVerifier
	Publisher Publisher
	Cfg       Config
	Log       *slog.Logger
}

// Run blocks until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.Cfg.Interval)
	defer t.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick runs one pass of every loop (exported for tests).
func (r *Runner) Tick(ctx context.Context) {
	if err := r.RecoverExpiredAuthorizations(ctx); err != nil {
		r.Log.Error("recover expired authorizations", "err", err)
	}
	if err := r.Reconcile(ctx); err != nil {
		r.Log.Error("reconcile", "err", err)
	}
	if err := r.PublishOutbox(ctx); err != nil {
		r.Log.Error("publish outbox", "err", err)
	}
}

// RecoverExpiredAuthorizations releases holds whose grant expired without the
// gateway ever recording a submission. Because the gateway writes "submitted"
// BEFORE transmitting a payment, an attempt still in "authorized" past
// expiry (+margin) provably never paid, so releasing is safe.
func (r *Runner) RecoverExpiredAuthorizations(ctx context.Context) error {
	now := r.Store.Now()
	attempts, err := r.Store.ListExpiredAuthorized(ctx, now.Add(-r.Cfg.ExpiryMargin), 200)
	if err != nil {
		return err
	}
	for _, a := range attempts {
		if _, _, err := r.Store.ResolveAttempt(ctx, a.ID, store.Outcome{Status: domain.PaymentExpired, Error: "grant expired without submission", Actor: "worker"}); err != nil {
			r.Log.Warn("release expired authorization", "attempt", a.ID, "err", err)
			continue
		}
		r.Log.Info("released expired authorization", "attempt", a.ID, "reservation", a.ReservationID)
	}
	return nil
}

// Reconcile resolves submitted-but-unreported and unknown attempts by asking
// the settlement provider. Nothing is released on a lost response alone.
func (r *Runner) Reconcile(ctx context.Context) error {
	now := r.Store.Now()
	stale, err := r.Store.ListAttemptsForRecovery(ctx, []domain.PaymentStatus{domain.PaymentSubmitted}, now.Add(-r.Cfg.SubmittedGrace), 200)
	if err != nil {
		return err
	}
	for _, a := range stale {
		// Promote to unknown so operators can see it; the reservation stays held.
		if _, _, err := r.Store.ResolveAttempt(ctx, a.ID, store.Outcome{Status: domain.PaymentUnknown, Error: "no outcome reported by gateway", Actor: "worker"}); err != nil {
			r.Log.Warn("mark unknown", "attempt", a.ID, "err", err)
		}
	}
	if r.Verifier == nil {
		return nil
	}
	unknown, err := r.Store.ListAttemptsForRecovery(ctx, []domain.PaymentStatus{domain.PaymentUnknown}, now, 200)
	if err != nil {
		return err
	}
	for _, a := range unknown {
		res, err := r.Verifier.Lookup(ctx, a)
		if err != nil {
			r.Log.Warn("verifier lookup failed; keeping reservation", "attempt", a.ID, "err", err)
			continue
		}
		switch res.Status {
		case VerifySettled:
			amt := res.SettledAmount
			if a.Quote != nil && (amt.IsZero() || amt.Cmp(a.Quote.MaxAmountRequired) > 0) {
				amt = a.Quote.MaxAmountRequired
			}
			if _, _, err := r.Store.ResolveAttempt(ctx, a.ID, store.Outcome{Status: domain.PaymentSettled, SubmissionRef: res.SubmissionRef,
				PayerRef: res.PayerRef, SettledAmount: amt, FeeActual: money.Zero(), Actor: "worker"}); err != nil {
				r.Log.Warn("commit reconciled settlement", "attempt", a.ID, "err", err)
			} else {
				r.Log.Info("reconciled as settled", "attempt", a.ID, "ref", res.SubmissionRef)
			}
		case VerifyNotFound:
			if now.Sub(a.UpdatedAt) < r.Cfg.UnknownReleaseAfter {
				continue
			}
			if _, _, err := r.Store.ResolveAttempt(ctx, a.ID, store.Outcome{Status: domain.PaymentFailed, Error: "not found at settlement provider after reconciliation window", Actor: "worker"}); err != nil {
				r.Log.Warn("release reconciled failure", "attempt", a.ID, "err", err)
			} else {
				r.Log.Info("reconciled as failed", "attempt", a.ID)
			}
		default:
			// pending: keep waiting
		}
	}
	return nil
}

// PublishOutbox delivers unpublished messages in order.
func (r *Runner) PublishOutbox(ctx context.Context) error {
	if r.Publisher == nil {
		return nil
	}
	msgs, err := r.Store.ClaimOutbox(ctx, 100)
	if err != nil {
		return err
	}
	var done []string
	for _, m := range msgs {
		if err := r.Publisher.Publish(ctx, m); err != nil {
			r.Log.Warn("publish", "id", m.ID, "err", err)
			break // preserve ordering
		}
		done = append(done, m.ID)
	}
	return r.Store.MarkOutboxPublished(ctx, done)
}

// HTTPVerifier queries a facilitator-style endpoint:
//
//	GET {base}/settlements?payment_hash=<hash>  -> {"status":"settled|not_found|pending","transaction":"..","payer":"..","amount":".."}
type HTTPVerifier struct {
	BaseURL string
	Client  *http.Client
}

func (v HTTPVerifier) Lookup(ctx context.Context, a domain.PaymentAttempt) (VerifyResult, error) {
	if a.PaymentHash == "" {
		return VerifyResult{Status: VerifyNotFound}, nil
	}
	u := v.BaseURL + "/settlements?payment_hash=" + url.QueryEscape(a.PaymentHash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return VerifyResult{}, err
	}
	c := v.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return VerifyResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return VerifyResult{}, fmt.Errorf("verifier status %d", resp.StatusCode)
	}
	var body struct {
		Status      string       `json:"status"`
		Transaction string       `json:"transaction"`
		Payer       string       `json:"payer"`
		Amount      money.Amount `json:"amount"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64*1024)).Decode(&body); err != nil {
		return VerifyResult{}, err
	}
	switch body.Status {
	case "settled":
		return VerifyResult{Status: VerifySettled, SubmissionRef: body.Transaction, PayerRef: body.Payer, SettledAmount: body.Amount}, nil
	case "pending":
		return VerifyResult{Status: VerifyPending}, nil
	default:
		return VerifyResult{Status: VerifyNotFound}, nil
	}
}
