// Package partners provides isolated, contract-tested HTTP clients for partner
// integration. The sandbox runtime does not enable them without onboarding.
package partners

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const ACPVersion = "2026-01-30"
const StripeVersion = "2026-04-22.preview"

var ErrOutcomeUnknown = errors.New("partner outcome unknown; reconcile with the same persisted identifiers")

// PartnerError deliberately omits response bodies, URLs, and secrets from logs.
type PartnerError struct{ Status int }

func (e PartnerError) Error() string { return fmt.Sprintf("partner returned HTTP %d", e.Status) }
func client() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func endpoint(base string) error {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("partner base URL must use HTTPS without userinfo, query, or fragment")
	}
	return nil
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type ACPClient struct {
	baseURL, token string
	http           *http.Client
}

func NewACPClient(baseURL, token string) (*ACPClient, error) {
	if e := endpoint(baseURL); e != nil {
		return nil, e
	}
	if token == "" {
		return nil, errors.New("merchant API token is required")
	}
	return &ACPClient{strings.TrimRight(baseURL, "/"), token, client()}, nil
}

type Item struct {
	ID       string `json:"id"`
	Quantity int    `json:"quantity"`
}
type Address struct {
	Name       string `json:"name"`
	LineOne    string `json:"line_one"`
	City       string `json:"city"`
	State      string `json:"state"`
	Country    string `json:"country"`
	PostalCode string `json:"postal_code"`
}
type CreateCheckout struct {
	Items              []Item  `json:"items"`
	FulfillmentAddress Address `json:"fulfillment_address"`
}
type Session struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Currency string `json:"currency"`
	Totals   []struct {
		Type   string `json:"type"`
		Amount int64  `json:"amount"`
	} `json:"totals"`
	Order    json.RawMessage   `json:"order,omitempty"`
	Messages []json.RawMessage `json:"messages,omitempty"`
}

func (c *ACPClient) call(ctx context.Context, method, path, key string, in any) (Session, error) {
	var out Session
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return out, e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if e != nil {
		return out, e
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("API-Version", ACPVersion)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	resp, e := c.http.Do(r)
	if e != nil {
		return out, ErrOutcomeUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return out, ErrOutcomeUnknown
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, PartnerError{resp.StatusCode}
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); e != nil || out.ID == "" {
		return Session{}, ErrOutcomeUnknown
	}
	return out, nil
}
func (c *ACPClient) Create(ctx context.Context, in CreateCheckout, key string) (Session, error) {
	if !safeID.MatchString(key) || len(in.Items) == 0 {
		return Session{}, errors.New("items and an idempotency key are required")
	}
	for _, i := range in.Items {
		if !safeID.MatchString(i.ID) || i.Quantity < 1 || i.Quantity > 5 {
			return Session{}, errors.New("invalid checkout item")
		}
	}
	return c.call(ctx, http.MethodPost, "/checkout_sessions", key, in)
}
func (c *ACPClient) Get(ctx context.Context, id string) (Session, error) {
	if !safeID.MatchString(id) {
		return Session{}, errors.New("invalid checkout ID")
	}
	return c.call(ctx, http.MethodGet, "/checkout_sessions/"+id, "", nil)
}
func (c *ACPClient) Update(ctx context.Context, id string, in CreateCheckout, key string) (Session, error) {
	if !safeID.MatchString(id) || !safeID.MatchString(key) {
		return Session{}, errors.New("invalid checkout or idempotency key")
	}
	return c.call(ctx, http.MethodPost, "/checkout_sessions/"+id, key, in)
}
func (c *ACPClient) Complete(ctx context.Context, id, scopedToken, key string) (Session, error) {
	if !safeID.MatchString(id) || !safeID.MatchString(key) || !strings.HasPrefix(scopedToken, "spt_") {
		return Session{}, errors.New("checkout ID, scoped token, and idempotency key required")
	}
	return c.call(ctx, http.MethodPost, "/checkout_sessions/"+id+"/complete", key, map[string]any{"payment_data": map[string]string{"provider": "stripe", "token": scopedToken}})
}
func (c *ACPClient) Cancel(ctx context.Context, id, key string) (Session, error) {
	if !safeID.MatchString(id) || !safeID.MatchString(key) {
		return Session{}, errors.New("invalid checkout or idempotency key")
	}
	return c.call(ctx, http.MethodPost, "/checkout_sessions/"+id+"/cancel", key, struct{}{})
}

type StripeTestClient struct {
	key, baseURL string
	http         *http.Client
}

// Only test keys are accepted. No live processor is wired into the demo server.
func NewStripeTestClient(key string) (*StripeTestClient, error) {
	if !strings.HasPrefix(key, "sk_test_") {
		return nil, errors.New("a Stripe test-mode key is required")
	}
	return &StripeTestClient{key, "https://api.stripe.com", client()}, nil
}

type TokenScope struct {
	PaymentMethod, SellerProfile, Currency string
	Amount                                 int64
	ExpiresAt                              time.Time
}
type ScopedToken struct {
	ID            string `json:"id"`
	LiveMode      bool   `json:"livemode"`
	Status        string `json:"status"`
	PaymentMethod string `json:"payment_method"`
	SellerDetails struct {
		Profile string `json:"network_business_profile"`
	} `json:"seller_details"`
	UsageLimits struct {
		Currency  string `json:"currency"`
		Amount    int64  `json:"max_amount"`
		ExpiresAt int64  `json:"expires_at"`
	} `json:"usage_limits"`
}

func (c *StripeTestClient) Issue(ctx context.Context, scope TokenScope, key string) (ScopedToken, error) {
	var out ScopedToken
	if !strings.HasPrefix(scope.PaymentMethod, "pm_") || !strings.HasPrefix(scope.SellerProfile, "profile_") || scope.Currency != "usd" || scope.Amount < 1 || scope.Amount > 100000 || !scope.ExpiresAt.After(time.Now()) || scope.ExpiresAt.After(time.Now().Add(15*time.Minute)) || !safeID.MatchString(key) {
		return out, errors.New("invalid test token scope")
	}
	form := url.Values{"payment_method": {scope.PaymentMethod}, "seller_details[network_business_profile]": {scope.SellerProfile}, "usage_limits[currency]": {scope.Currency}, "usage_limits[max_amount]": {strconv.FormatInt(scope.Amount, 10)}, "usage_limits[expires_at]": {strconv.FormatInt(scope.ExpiresAt.Unix(), 10)}}
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/shared_payment/issued_tokens", strings.NewReader(form.Encode()))
	if e != nil {
		return out, e
	}
	r.SetBasicAuth(c.key, "")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Stripe-Version", StripeVersion)
	r.Header.Set("Idempotency-Key", key)
	resp, e := c.http.Do(r)
	if e != nil {
		return out, ErrOutcomeUnknown
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return out, ErrOutcomeUnknown
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, PartnerError{resp.StatusCode}
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); e != nil {
		return ScopedToken{}, ErrOutcomeUnknown
	}
	if out.LiveMode || out.Status != "active" || !strings.HasPrefix(out.ID, "spt_") || out.PaymentMethod != scope.PaymentMethod || out.SellerDetails.Profile != scope.SellerProfile || out.UsageLimits.Currency != scope.Currency || out.UsageLimits.Amount != scope.Amount || out.UsageLimits.ExpiresAt != scope.ExpiresAt.Unix() {
		return ScopedToken{}, errors.New("provider returned a token outside the requested test scope")
	}
	return out, nil
}
