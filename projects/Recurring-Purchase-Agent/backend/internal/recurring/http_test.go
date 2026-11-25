package recurring

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func newTestHTTPHandler(t *testing.T) (http.Handler, *FakeClock, *SandboxCheckoutAdapter) {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	t.Cleanup(store.Close)
	clk := NewFakeClock(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	chk := NewSandboxCheckoutAdapter()
	svc := NewService(store, clk, NewSandboxMerchantAdapter(), chk, nil)
	wrk := NewWorker(svc, "http-test-worker")
	srv := NewServer(svc, wrk, HTTPConfig{
		SessionSecret: "test-secret",
		OperatorToken: "op-secret",
		WebhookSecret: "wh-secret",
		Demo:          true,
	})
	return srv.Handler(), clk, chk
}

func execReq(t *testing.T, h http.Handler, method, path string, cookie *http.Cookie, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	switch v := payload.(type) {
	case nil:
	case []byte:
		body = v
	default:
		body, _ = json.Marshal(v)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func openSession(t *testing.T, h http.Handler, tenant, role, opToken string) *http.Cookie {
	t.Helper()
	rec := execReq(t, h, "POST", "/v1/session", nil, map[string]string{
		"tenant":         tenant,
		"role":           role,
		"operator_token": opToken,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("session status: %d body=%s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "uno_recurring_session" {
			return c
		}
	}
	t.Fatalf("no session cookie returned")
	return nil
}

func TestHTTPWorkflowTenantIsolationOperatorRBACAndWebhooks(t *testing.T) {
	h, _, _ := newTestHTTPHandler(t)

	// Unauthenticated access rejected.
	rec := execReq(t, h, "GET", "/v1/workspace", nil, nil, nil)
	if rec.Code != 401 {
		t.Fatalf("expected 401 without session, got %d", rec.Code)
	}

	// Tenant A creates a rule.
	cookieA := openSession(t, h, "tenant-a", "customer", "")
	cookieB := openSession(t, h, "tenant-b", "customer", "")

	rec = execReq(t, h, "POST", "/v1/recurring-rules", cookieA, defaultCoffeeRuleReq(ApprovalModeAssistedReorder), nil)
	if rec.Code != 201 {
		t.Fatalf("expected 201 creating rule, got %d body=%s", rec.Code, rec.Body.String())
	}
	var rule RecurringRule
	_ = json.Unmarshal(rec.Body.Bytes(), &rule)

	// Tenant B cannot view Tenant A's rule occurrences (strict tenant isolation).
	rec = execReq(t, h, "GET", "/v1/recurring-rules/"+rule.RuleID+"/occurrences", cookieB, nil, nil)
	if rec.Code != 404 {
		t.Fatalf("expected 404 for cross-tenant rule access, got %d", rec.Code)
	}

	// Advance sandbox clock via tick and verify occurrence + proposal appear.
	rec = execReq(t, h, "POST", "/v1/sandbox/tick", cookieA, map[string]any{"advance_minutes": 180}, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200 tick, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec = execReq(t, h, "GET", "/v1/recurring-rules/"+rule.RuleID+"/occurrences", cookieA, nil, nil)
	var occList struct {
		Occurrences []*OccurrenceDetail `json:"occurrences"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &occList)
	if len(occList.Occurrences) != 1 || occList.Occurrences[0].Proposal == nil {
		t.Fatalf("expected 1 occurrence with proposal, got %+v", occList.Occurrences)
	}

	prop := occList.Occurrences[0].Proposal
	occID := occList.Occurrences[0].Occurrence.OccurrenceID

	// Approve proposal via POST /v1/proposals/{id}/approve.
	rec = execReq(t, h, "POST", "/v1/proposals/"+prop.ProposalID+"/approve", cookieA, map[string]any{
		"expected_quote_digest": prop.QuoteDigest,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200 approving proposal, got %d body=%s", rec.Code, rec.Body.String())
	}
	var approved OccurrenceDetail
	_ = json.Unmarshal(rec.Body.Bytes(), &approved)
	if approved.Occurrence.Status != OccurrenceCompleted || approved.Checkout == nil {
		t.Fatalf("expected completed occurrence, got %+v", approved)
	}

	// GET /v1/occurrences/{id} returns full status.
	rec = execReq(t, h, "GET", "/v1/occurrences/"+occID, cookieA, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200 getting occurrence, got %d", rec.Code)
	}

	// Verified webhook applies partial refund + shipped status.
	whPayload, _ := json.Marshal(MerchantWebhookEvent{
		TenantID:          "tenant-a",
		MerchantOrderID:   approved.Checkout.MerchantOrderID,
		FulfillmentStatus: FulfillmentShipped,
		PartialRefund:     &Money{AmountMinor: 499, Asset: "USD", Network: "fiat", Decimals: 2},
		Note:              "Shipping fee refunded for 1-day carrier delay.",
	})

	// Invalid signature must fail 401.
	rec = execReq(t, h, "POST", "/v1/webhooks/merchant", nil, whPayload, map[string]string{
		"X-Uno-Signature": "deadbeef",
	})
	if rec.Code != 401 {
		t.Fatalf("expected 401 for bad webhook signature, got %d", rec.Code)
	}

	// Valid HMAC signature succeeds.
	mac := hmac.New(sha256.New, []byte("wh-secret"))
	mac.Write(whPayload)
	sig := hex.EncodeToString(mac.Sum(nil))
	rec = execReq(t, h, "POST", "/v1/webhooks/merchant", nil, whPayload, map[string]string{
		"X-Uno-Signature": sig,
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200 for valid webhook, got %d body=%s", rec.Code, rec.Body.String())
	}
	var afterWH OccurrenceDetail
	_ = json.Unmarshal(rec.Body.Bytes(), &afterWH)
	if afterWH.Occurrence.PaymentStatus != PaymentPartiallyRefunded || afterWH.Occurrence.FulfillmentStatus != FulfillmentShipped {
		t.Fatalf("expected partially_refunded + shipped, got %s / %s", afterWH.Occurrence.PaymentStatus, afterWH.Occurrence.FulfillmentStatus)
	}

	// Customer cannot toggle operator-only global pause (403).
	rec = execReq(t, h, "POST", "/v1/operator/pause", cookieA, map[string]any{"paused": true, "reason": "test"}, nil)
	if rec.Code != 403 {
		t.Fatalf("expected 403 for customer toggling operator pause, got %d", rec.Code)
	}

	// Authenticated operator can toggle global pause (200).
	opCookie := openSession(t, h, "tenant-a", "operator", "op-secret")
	rec = execReq(t, h, "POST", "/v1/operator/pause", opCookie, map[string]any{"paused": true, "reason": "maintenance"}, nil)
	if rec.Code != 200 {
		t.Fatalf("expected 200 for operator global pause, got %d", rec.Code)
	}
}
