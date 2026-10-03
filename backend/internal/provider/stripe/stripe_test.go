package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mywallet/backend/internal/provider"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Config{SecretKey: "sk_test_x", PublishableKey: "pk_test_x", WebhookSecret: "whsec_test", APIVersion: "2024-06-20", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRejectsMixedModes(t *testing.T) {
	if _, err := New(Config{SecretKey: "sk_live_x", PublishableKey: "pk_test_x", APIVersion: "v"}); err == nil {
		t.Fatal("expected mixed-mode rejection")
	}
	if _, err := New(Config{SecretKey: "sk_test_x", PublishableKey: "pk_test_x"}); err == nil {
		t.Fatal("expected missing API version rejection")
	}
}

func TestCreateSetupWireFormat(t *testing.T) {
	var gotAuth, gotIdem, gotVersion, gotBody string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/setup_intents" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotIdem = r.Header.Get("Idempotency-Key")
		gotVersion = r.Header.Get("Stripe-Version")
		_ = r.ParseForm()
		gotBody = r.PostForm.Encode()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "seti_1", "client_secret": "seti_1_secret_abc", "status": "requires_payment_method", "customer": "cus_1"})
	})
	s, err := c.CreateSetup(context.Background(), "cus_1", "idem-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk_test_x" || gotIdem != "idem-1" || gotVersion != "2024-06-20" {
		t.Fatalf("headers: %q %q %q", gotAuth, gotIdem, gotVersion)
	}
	for _, want := range []string{"customer=cus_1", "payment_method_types%5B%5D=card", "usage=on_session"} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body %q missing %q", gotBody, want)
		}
	}
	if s.ID != "seti_1" || s.ClientSecret != "seti_1_secret_abc" || s.State != provider.SetupRequiresPaymentMethod || s.CustomerRef != "cus_1" {
		t.Fatalf("setup: %+v", s)
	}
}

func TestErrorMapping(t *testing.T) {
	var mode atomic.Value
	mode.Store("500")
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load().(string) {
		case "500":
			w.WriteHeader(500)
		case "429":
			w.WriteHeader(429)
		case "404":
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"type":"invalid_request_error","code":"resource_missing"}}`)
		case "decline":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "seti_2", "status": "requires_payment_method", "customer": "cus_1",
				"last_setup_error": map[string]any{"code": "card_declined", "decline_code": "insufficient_funds"}})
		}
	})
	ctx := context.Background()
	if _, err := c.GetSetup(ctx, "seti_1"); !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("500 -> %v", err)
	}
	mode.Store("429")
	if _, err := c.GetSetup(ctx, "seti_1"); !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("429 -> %v", err)
	}
	mode.Store("404")
	if _, err := c.GetSetup(ctx, "seti_1"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("404 -> %v", err)
	}
	if err := c.DeleteCustomer(ctx, "cus_gone"); err != nil {
		t.Fatalf("deleting a missing customer should succeed: %v", err)
	}
	if err := c.DetachPaymentMethod(ctx, "pm_gone"); !errors.Is(err, provider.ErrAlreadyDetached) {
		t.Fatalf("detach missing -> %v", err)
	}
	mode.Store("decline")
	s, err := c.GetSetup(ctx, "seti_2")
	if err != nil || s.LastErrorCode != "insufficient_funds" {
		t.Fatalf("decline code: %+v %v", s, err)
	}
}

func TestDetachAlreadyDetachedViaRead(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/detach") {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"The payment method you provided is not attached to a customer so detachment is impossible."}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "pm_1", "customer": nil, "type": "card",
			"card": map[string]any{"brand": "visa", "last4": "4242", "exp_month": 12, "exp_year": 2030}})
	})
	if err := c.DetachPaymentMethod(context.Background(), "pm_1"); !errors.Is(err, provider.ErrAlreadyDetached) {
		t.Fatalf("expected already detached, got %v", err)
	}
}

func TestClientSessionRejectsForeignSetup(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "seti_1", "status": "requires_payment_method", "customer": "cus_other"})
	})
	if _, err := c.ClientSession(context.Background(), "cus_mine", "seti_1"); err == nil {
		t.Fatal("expected customer mismatch error")
	}
}

func TestWebhookSignature(t *testing.T) {
	c := newTestClient(t, nil)
	now := time.Unix(1_800_000_000, 0)
	payload := []byte(`{"id":"evt_1","type":"setup_intent.succeeded","data":{"object":{"id":"seti_1"}}}`)
	sig := Sign("whsec_test", now.Unix(), payload)

	ev, err := c.ParseWebhook(payload, fmt.Sprintf("t=%d,v1=%s", now.Unix(), sig), now)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "evt_1" || ev.Type != "setup_intent.succeeded" || ev.ObjectID != "seti_1" {
		t.Fatalf("event: %+v", ev)
	}
	// Multiple v1 entries (key rotation) where one matches.
	if _, err := c.ParseWebhook(payload, fmt.Sprintf("t=%d,v1=deadbeef,v1=%s", now.Unix(), sig), now); err != nil {
		t.Fatalf("rotation: %v", err)
	}
	cases := map[string]string{
		"wrong secret":   fmt.Sprintf("t=%d,v1=%s", now.Unix(), Sign("whsec_other", now.Unix(), payload)),
		"tampered":       fmt.Sprintf("t=%d,v1=%s", now.Unix(), Sign("whsec_test", now.Unix(), []byte(`{"id":"evt_2"}`))),
		"stale":          fmt.Sprintf("t=%d,v1=%s", now.Add(-10*time.Minute).Unix(), Sign("whsec_test", now.Add(-10*time.Minute).Unix(), payload)),
		"missing sig":    fmt.Sprintf("t=%d", now.Unix()),
		"missing ts":     "v1=" + sig,
		"garbage header": "nonsense",
	}
	for name, header := range cases {
		if _, err := c.ParseWebhook(payload, header, now); !errors.Is(err, provider.ErrInvalidSignature) {
			t.Errorf("%s: expected invalid signature, got %v", name, err)
		}
	}
}
