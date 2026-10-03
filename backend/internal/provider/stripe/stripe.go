// Package stripe implements provider.Provider against the Stripe REST API
// using SetupIntents in setup mode with a Customer. It talks HTTPS directly
// with form-encoded requests so the dependency surface stays small and the
// API version can be pinned explicitly.
//
// The secret key, ephemeral keys, and client secrets are never logged.
package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mywallet/backend/internal/provider"
)

// Config configures the client.
type Config struct {
	// SecretKey is the restricted or standard secret key (sk_test_/sk_live_).
	SecretKey string
	// PublishableKey is returned to the app for the SDK.
	PublishableKey string
	// WebhookSecret is the signing secret of the webhook endpoint (whsec_).
	WebhookSecret string
	// APIVersion is pinned and must match what the iOS SDK expects for
	// ephemeral keys (see docs/getting-started.md).
	APIVersion string
	// BaseURL defaults to https://api.stripe.com. Tests point it at a stub.
	BaseURL string
	// HTTPClient defaults to a 15 second timeout client.
	HTTPClient *http.Client
}

// Client is the Stripe provider.
type Client struct {
	cfg  Config
	http *http.Client
}

// New validates the configuration and returns a client.
func New(cfg Config) (*Client, error) {
	if cfg.SecretKey == "" {
		return nil, errors.New("stripe: SecretKey is required")
	}
	if cfg.PublishableKey == "" {
		return nil, errors.New("stripe: PublishableKey is required")
	}
	if cfg.APIVersion == "" {
		return nil, errors.New("stripe: APIVersion is required")
	}
	if strings.HasPrefix(cfg.SecretKey, "sk_live_") != strings.HasPrefix(cfg.PublishableKey, "pk_live_") {
		return nil, errors.New("stripe: secret and publishable keys are from different modes")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.stripe.com"
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{cfg: cfg, http: hc}, nil
}

// Name implements provider.Provider.
func (c *Client) Name() string { return "stripe" }

// Live reports whether the client uses live-mode keys.
func (c *Client) Live() bool { return strings.HasPrefix(c.cfg.SecretKey, "sk_live_") }

// --- wire types -------------------------------------------------------------

type apiError struct {
	Type        string `json:"type"`
	Code        string `json:"code"`
	DeclineCode string `json:"decline_code"`
	Message     string `json:"message"`
	Param       string `json:"param"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type setupIntent struct {
	ID             string  `json:"id"`
	ClientSecret   string  `json:"client_secret"`
	Status         string  `json:"status"`
	Customer       string  `json:"customer"`
	PaymentMethod  string  `json:"payment_method"`
	LastSetupError *struct {
		Code        string `json:"code"`
		DeclineCode string `json:"decline_code"`
	} `json:"last_setup_error"`
}

type paymentMethod struct {
	ID       string  `json:"id"`
	Customer *string `json:"customer"`
	Type     string  `json:"type"`
	Card     *struct {
		Brand    string `json:"brand"`
		Last4    string `json:"last4"`
		ExpMonth int    `json:"exp_month"`
		ExpYear  int    `json:"exp_year"`
	} `json:"card"`
}

// Error is a non-transient Stripe API error.
type Error struct {
	Status int
	API    apiError
}

func (e *Error) Error() string {
	return fmt.Sprintf("stripe: %d %s %s", e.Status, e.API.Type, e.API.Code)
}

// --- http -------------------------------------------------------------------

func (c *Client) do(ctx context.Context, method, path string, form url.Values, headers map[string]string, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.SecretKey)
	req.Header.Set("Stripe-Version", c.cfg.APIVersion)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", provider.ErrUnavailable, redact(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: read body", provider.ErrUnavailable)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: status %d", provider.ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		var env errorEnvelope
		_ = json.Unmarshal(raw, &env)
		if resp.StatusCode == http.StatusNotFound || env.Error.Code == "resource_missing" {
			return provider.ErrNotFound
		}
		return &Error{Status: resp.StatusCode, API: env.Error}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("stripe: decode: %w", err)
		}
	}
	return nil
}

// redact strips anything after the host from URL errors so query strings or
// paths with identifiers do not reach logs.
func redact(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Op + " " + uerr.Err.Error()
	}
	return "request failed"
}

// --- provider.Provider ------------------------------------------------------

// CreateCustomer implements provider.Provider.
func (c *Client) CreateCustomer(ctx context.Context, userID string) (string, error) {
	form := url.Values{}
	form.Set("metadata[mywallet_user_id]", userID)
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/customers", form, map[string]string{"Idempotency-Key": "mywallet-customer-" + userID}, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// CreateSetup implements provider.Provider. Manual card entry only; usage
// is on_session because v1 grants no authority for unattended charges.
func (c *Client) CreateSetup(ctx context.Context, customerRef, idemKey string) (provider.Setup, error) {
	form := url.Values{}
	form.Set("customer", customerRef)
	form.Add("payment_method_types[]", "card")
	form.Set("usage", "on_session")
	form.Set("metadata[mywallet_purpose]", "save_card_for_display")
	var si setupIntent
	if err := c.do(ctx, http.MethodPost, "/v1/setup_intents", form, map[string]string{"Idempotency-Key": idemKey}, &si); err != nil {
		return provider.Setup{}, err
	}
	return toSetup(si), nil
}

// ClientSession implements provider.Provider.
func (c *Client) ClientSession(ctx context.Context, customerRef, setupID string) (provider.ClientSession, error) {
	setup, err := c.GetSetup(ctx, setupID)
	if err != nil {
		return provider.ClientSession{}, err
	}
	if setup.CustomerRef != customerRef {
		return provider.ClientSession{}, fmt.Errorf("stripe: setup %s does not belong to customer", setupID)
	}
	form := url.Values{}
	form.Set("customer", customerRef)
	var ek struct {
		Secret  string `json:"secret"`
		Expires int64  `json:"expires"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/ephemeral_keys", form, nil, &ek); err != nil {
		return provider.ClientSession{}, err
	}
	return provider.ClientSession{
		Setup:              setup,
		CustomerRef:        customerRef,
		EphemeralKeySecret: ek.Secret,
		EphemeralKeyExpiry: time.Unix(ek.Expires, 0).UTC(),
		PublishableKey:     c.cfg.PublishableKey,
	}, nil
}

// GetSetup implements provider.Provider.
func (c *Client) GetSetup(ctx context.Context, setupID string) (provider.Setup, error) {
	var si setupIntent
	if err := c.do(ctx, http.MethodGet, "/v1/setup_intents/"+url.PathEscape(setupID), nil, nil, &si); err != nil {
		return provider.Setup{}, err
	}
	return toSetup(si), nil
}

// CancelSetup implements provider.Provider.
func (c *Client) CancelSetup(ctx context.Context, setupID string) error {
	err := c.do(ctx, http.MethodPost, "/v1/setup_intents/"+url.PathEscape(setupID)+"/cancel", url.Values{}, nil, nil)
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.API.Code == "setup_intent_unexpected_state" {
		// Already terminal; GetSetup reports the real state.
		return nil
	}
	return err
}

// GetPaymentMethod implements provider.Provider.
func (c *Client) GetPaymentMethod(ctx context.Context, id string) (provider.PaymentMethod, error) {
	var pm paymentMethod
	if err := c.do(ctx, http.MethodGet, "/v1/payment_methods/"+url.PathEscape(id), nil, nil, &pm); err != nil {
		return provider.PaymentMethod{}, err
	}
	if pm.Card == nil {
		return provider.PaymentMethod{}, fmt.Errorf("stripe: payment method %s is not a card", id)
	}
	out := provider.PaymentMethod{ID: pm.ID, Brand: pm.Card.Brand, Last4: pm.Card.Last4, ExpMonth: pm.Card.ExpMonth, ExpYear: pm.Card.ExpYear}
	if pm.Customer != nil {
		out.CustomerRef = *pm.Customer
	}
	return out, nil
}

// DetachPaymentMethod implements provider.Provider. Stripe detachment is
// permanent: the reference can never be attached again.
func (c *Client) DetachPaymentMethod(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodPost, "/v1/payment_methods/"+url.PathEscape(id)+"/detach", url.Values{}, nil, nil)
	if err == nil {
		return nil
	}
	if errors.Is(err, provider.ErrNotFound) {
		return provider.ErrAlreadyDetached
	}
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
		// Stripe rejects detaching an unattached method; confirm via a read.
		pm, gerr := c.GetPaymentMethod(ctx, id)
		if gerr == nil && pm.CustomerRef == "" {
			return provider.ErrAlreadyDetached
		}
	}
	return err
}

// DeleteCustomer implements provider.Provider.
func (c *Client) DeleteCustomer(ctx context.Context, customerRef string) error {
	err := c.do(ctx, http.MethodDelete, "/v1/customers/"+url.PathEscape(customerRef), nil, nil, nil)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	return err
}

func toSetup(si setupIntent) provider.Setup {
	s := provider.Setup{ID: si.ID, CustomerRef: si.Customer, PaymentMethodRef: si.PaymentMethod,
		State: provider.SetupState(si.Status), ClientSecret: si.ClientSecret}
	if si.LastSetupError != nil {
		s.LastErrorCode = si.LastSetupError.Code
		if si.LastSetupError.DeclineCode != "" {
			s.LastErrorCode = si.LastSetupError.DeclineCode
		}
	}
	return s
}
