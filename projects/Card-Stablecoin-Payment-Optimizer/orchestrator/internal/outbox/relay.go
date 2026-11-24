// Package outbox delivers committed events to the execution service. An event
// is claimed in one short transaction, delivered with no transaction open, and
// then marked. Delivery is at least once; consumers deduplicate on the id.
package outbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

// Publisher delivers one event.
type Publisher interface {
	Publish(ctx context.Context, event store.OutboxEvent) error
}

// Webhook posts events as JSON signed with HMAC-SHA256 over
// "<timestamp>.<body>", so the receiver can verify origin and freshness.
type Webhook struct {
	URL    string
	Secret []byte
	HTTP   *http.Client
	Now    func() time.Time
}

// Sign returns the hex signature for a timestamp and body.
func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (w *Webhook) Publish(ctx context.Context, event store.OutboxEvent) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(event.Payload))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(w.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Optimizer-Event-Id", strconv.FormatInt(event.ID, 10))
	req.Header.Set("X-Optimizer-Topic", event.Topic)
	req.Header.Set("X-Optimizer-Timestamp", timestamp)
	req.Header.Set("X-Optimizer-Signature", "v1="+Sign(w.Secret, timestamp, event.Payload))
	resp, err := w.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("outbox: receiver answered %d", resp.StatusCode)
	}
	return nil
}

// Relay polls the outbox and publishes pending events.
type Relay struct {
	Store     *store.Store
	Publisher Publisher
	Log       *slog.Logger
	Now       func() time.Time
	Batch     int
	Lease     time.Duration
}

// backoff grows with attempts and is capped at five minutes.
func backoff(attempts int) time.Duration {
	d := time.Second << min(attempts, 8)
	return min(d, 5*time.Minute)
}

// RunOnce claims and delivers one batch. It returns how many were delivered.
func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	events, err := r.Store.ClaimOutbox(ctx, r.Now(), r.Batch, r.Lease)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, event := range events {
		if err := r.Publisher.Publish(ctx, event); err != nil {
			r.Log.Warn("outbox delivery failed", "event_id", event.ID, "topic", event.Topic, "attempts", event.Attempts, "error", err)
			if err := r.Store.RetryLater(ctx, event.ID, r.Now().Add(backoff(event.Attempts))); err != nil {
				return delivered, err
			}
			continue
		}
		if err := r.Store.MarkDelivered(ctx, event.ID, r.Now()); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}

// Run polls until ctx is cancelled.
func (r *Relay) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := r.RunOnce(ctx); err != nil && ctx.Err() == nil {
			r.Log.Error("outbox relay failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
