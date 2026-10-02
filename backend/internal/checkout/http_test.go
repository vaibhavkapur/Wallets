package checkout

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testHTTP(t *testing.T) (*Server, http.Handler) {
	s := &Server{Service: setup(t), Config: HTTPConfig{SessionSecret: "session-secret-test", OperatorToken: "operator-test", WebhookSecret: "webhook-test", AllowedOrigin: "http://localhost:5173", Demo: true}}
	return s, s.Handler()
}
func request(h http.Handler, method, path, body, cookie string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != "" {
		r.Header.Set("Cookie", "uno_session="+cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndOperatorGate(t *testing.T) {
	s, h := testHTTP(t)
	if w := request(h, "GET", "/v1/dashboard", "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	cookie := s.cookie(principal{"alice", "customer", time.Now().Add(time.Hour).Unix()})
	if w := request(h, "POST", "/v1/operator/pause", `{"paused":true}`, cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/v1/session", `{"role":"operator","token":"wrong"}`, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(h, "POST", "/v1/session", `{"role":"operator","token":"operator-test"}`, ""); w.Code != 200 || !strings.Contains(w.Header().Get("Set-Cookie"), "HttpOnly") {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("POST", "/v1/session", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin mutation accepted")
	}
	if w = request(h, "GET", "/v1/dashboard", "", cookie+"x"); w.Code != 401 {
		t.Fatal("forged session accepted")
	}
}
func TestStrictInputs(t *testing.T) {
	s, h := testHTTP(t)
	cookie := s.cookie(principal{"alice", "customer", time.Now().Add(time.Hour).Unix()})
	for _, body := range []string{`{"product_id":"studio-pro","variant":"Midnight","quantity":1,"max_total":20000,"postal_code":"94103"}`, `{"pan":"4242424242424242"}`, `{} {}`} {
		w := request(h, "POST", "/v1/purchase-intents", body, cookie)
		if w.Code != 400 {
			t.Fatalf("accepted invalid input: %d", w.Code)
		}
	}
}
func TestVerifiedWebhookDeduplicatesAndReconciles(t *testing.T) {
	s, h := testHTTP(t)
	c, a := ready(t, s.Service, "alice", "timeout")
	execute(t, s.Service, "alice", c, a)
	work(t, s.Service, "alice")
	body := []byte(fmt.Sprintf(`{"id":"evt_provider_001","tenant":"alice","checkout_id":%q}`, c.ID))
	send := func(stamp int64, valid bool) int {
		r := httptest.NewRequest("POST", "/v1/webhooks/sandbox", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Webhook-Timestamp", fmt.Sprint(stamp))
		mac := hmac.New(sha256.New, []byte("webhook-test"))
		fmt.Fprintf(mac, "%d.", stamp)
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		if !valid {
			sig = "00"
		}
		r.Header.Set("X-Webhook-Signature", sig)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if send(time.Now().Unix(), false) != 401 || send(time.Now().Add(-10*time.Minute).Unix(), true) != 401 {
		t.Fatal("unverified or old webhook accepted")
	}
	for range 2 {
		if code := send(time.Now().Unix(), true); code != 202 {
			t.Fatal(code)
		}
	}
	st := getState(t, s.Service, "alice")
	if len(st.Webhooks) != 1 {
		t.Fatal("webhook not deduplicated")
	}
	s.Service.Now = func() time.Time { return time.Now().UTC().Add(20 * time.Second) }
	work(t, s.Service, "alice")
	st = getState(t, s.Service, "alice")
	if len(st.Orders) != 1 || len(st.Provider) != 1 {
		t.Fatal("webhook recovery duplicated payment")
	}
}
