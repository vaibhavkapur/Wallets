package rewards

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestHTTPAPISignedWebhooksRBACAndTenantIsolation(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.SeedPilotData(context.Background(), "tenant_a"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := NewHTTPHandler(svc, HTTPConfig{
		OperatorToken:   "secret-op-token",
		WebhookSecret:   "prov-webhook-secret",
		AllowUnsignedUI: false,
	})

	// 1. Operator-only endpoint rejects missing/wrong token.
	req := httptest.NewRequest(http.MethodGet, "/v1/reconciliation-exceptions", nil)
	req.Header.Set("X-Tenant-ID", "tenant_a")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without operator token, got %d", rr.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/reconciliation-exceptions", nil)
	req.Header.Set("X-Tenant-ID", "tenant_a")
	req.Header.Set("X-Operator-Token", "secret-op-token")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with operator token, got %d: %s", rr.Code, rr.Body.String())
	}

	// 2. Unsigned merchant event MUST be rejected (401).
	evtBody := []byte(`{"event_id":"evt_http_1","event_type":"order.paid","merchant_id":"mer_trail","campaign_id":"cmp_trail_gear","order_id":"ORD-HTTP-1","customer_id":"cust_alex","order_subtotal_minor":"10000","fulfilled":true}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/merchant-events", bytes.NewReader(evtBody))
	req.Header.Set("X-Tenant-ID", "tenant_a")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unsigned merchant event, got %d", rr.Code)
	}

	// 3. Properly HMAC-SHA256 signed merchant event succeeds (201).
	secret, err := svc.GetMerchantWebhookSecret(context.Background(), "tenant_a", "mer_trail")
	if err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(svc.Now().Unix(), 10)
	sig := ComputeHMAC(secret, ts, evtBody)

	req = httptest.NewRequest(http.MethodPost, "/v1/merchant-events", bytes.NewReader(evtBody))
	req.Header.Set("X-Tenant-ID", "tenant_a")
	req.Header.Set("X-Webhook-Timestamp", ts)
	req.Header.Set("X-Webhook-Signature", "sha256="+sig)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for signed merchant event, got %d: %s", rr.Code, rr.Body.String())
	}

	// 4. Tenant isolation: tenant_b must NOT see tenant_a campaigns or rewards!
	req = httptest.NewRequest(http.MethodGet, "/v1/campaigns", nil)
	req.Header.Set("X-Tenant-ID", "tenant_b")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	var campResp struct {
		Campaigns []CampaignView `json:"campaigns"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &campResp); err != nil {
		t.Fatal(err)
	}
	if len(campResp.Campaigns) != 0 {
		t.Fatalf("tenant isolation violated: tenant_b saw %d campaigns", len(campResp.Campaigns))
	}

	// 5. Quote + Payout via HTTP endpoints returns decimal strings for amounts.
	qBody := []byte(`{"customer_id":"cust_alex"}`)
	req = httptest.NewRequest(http.MethodPost, "/v1/payout-quotes", bytes.NewReader(qBody))
	req.Header.Set("X-Tenant-ID", "tenant_a")
	req.Header.Set("X-Customer-ID", "cust_alex")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create quote expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var quoteMap map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &quoteMap); err != nil {
		t.Fatal(err)
	}
	if _, isStr := quoteMap["net_payout_base"].(string); !isStr {
		t.Fatalf("expected net_payout_base serialized as JSON string, got %T", quoteMap["net_payout_base"])
	}

	payBody, _ := json.Marshal(map[string]any{
		"customer_id":            "cust_alex",
		"quote_id":               quoteMap["id"],
		"beneficiary_version_id": quoteMap["beneficiary_version_id"],
		"terms_approved":         true,
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/payouts", bytes.NewReader(payBody))
	req.Header.Set("X-Tenant-ID", "tenant_a")
	req.Header.Set("X-Customer-ID", "cust_alex")
	req.Header.Set("Idempotency-Key", "http-idem-001")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create payout expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
}
