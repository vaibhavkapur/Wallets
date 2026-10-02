package partners

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestACPContract(t *testing.T) {
	calls := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer merchant-test" || r.Header.Get("API-Version") != ACPVersion {
			t.Error("headers missing")
		}
		if r.Method == "POST" && r.Header.Get("Idempotency-Key") != "test-key-001" {
			t.Error("idempotency missing")
		}
		if r.URL.Path == "/checkout_sessions/cs_test/complete" {
			var in struct {
				PaymentData struct {
					Token    string `json:"token"`
					Provider string `json:"provider"`
				} `json:"payment_data"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if in.PaymentData.Token != "spt_test" || in.PaymentData.Provider != "stripe" {
				t.Error("invalid payment contract")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cs_test","status":"completed","currency":"usd","totals":[{"type":"total","amount":18294}],"order":{"id":"order_test"}}`))
	}))
	defer s.Close()
	c, e := NewACPClient(s.URL, "merchant-test")
	if e != nil {
		t.Fatal(e)
	}
	c.http = s.Client()
	in := CreateCheckout{Items: []Item{{"sku_test", 1}}, FulfillmentAddress: Address{Name: "Test Buyer", LineOne: "1 Test St", City: "Test", State: "CA", Country: "US", PostalCode: "94103"}}
	if _, e = c.Create(context.Background(), in, "test-key-001"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Get(context.Background(), "cs_test"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Update(context.Background(), "cs_test", in, "test-key-001"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Complete(context.Background(), "cs_test", "spt_test", "test-key-001"); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Cancel(context.Background(), "cs_test", "test-key-001"); e != nil {
		t.Fatal(e)
	}
	if calls != 5 {
		t.Fatal(calls)
	}
	if _, e = c.Get(context.Background(), "../secrets"); e == nil {
		t.Fatal("path traversal accepted")
	}
	if _, e = NewACPClient("http://merchant.example", "key"); e == nil {
		t.Fatal("insecure URL accepted")
	}
}
func TestStripeScopeContract(t *testing.T) {
	scope := TokenScope{"pm_test", "profile_test", "usd", 18294, time.Now().Add(5 * time.Minute).Truncate(time.Second)}
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		user, _, ok := r.BasicAuth()
		if !ok || user != "sk_test_example" || r.Header.Get("Stripe-Version") != StripeVersion || r.Header.Get("Idempotency-Key") != "scope-key-001" {
			t.Error("authentication/version/idempotency missing")
		}
		if r.Form.Get("usage_limits[max_amount]") != "18294" || r.Form.Get("seller_details[network_business_profile]") != "profile_test" {
			t.Error("scope missing")
		}
		token := ScopedToken{ID: "spt_test", Status: "active", PaymentMethod: "pm_test"}
		token.SellerDetails.Profile = "profile_test"
		token.UsageLimits.Amount = 18294
		token.UsageLimits.Currency = "usd"
		token.UsageLimits.ExpiresAt = scope.ExpiresAt.Unix()
		json.NewEncoder(w).Encode(token)
	}))
	defer s.Close()
	c, e := NewStripeTestClient("sk_test_example")
	if e != nil {
		t.Fatal(e)
	}
	c.baseURL = s.URL
	c.http = s.Client()
	out, e := c.Issue(context.Background(), scope, "scope-key-001")
	if e != nil || out.ID != "spt_test" {
		t.Fatal(e)
	}
	if _, e = NewStripeTestClient("sk_live_example"); e == nil {
		t.Fatal("live key accepted")
	}
}
func TestAmbiguousCompletionIsNeverAutomaticallyRetried(t *testing.T) {
	calls := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(503)
		w.Write([]byte("secret-token-not-for-logs"))
	}))
	defer s.Close()
	c, _ := NewACPClient(s.URL, "secret")
	c.http = s.Client()
	_, e := c.Complete(context.Background(), "cs_test", "spt_test", "same-attempt-key")
	if !errors.Is(e, ErrOutcomeUnknown) || calls != 1 {
		t.Fatal("ambiguous completion retried or exposed", e, calls)
	}
}
