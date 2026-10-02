package checkout

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Money is an integer number of USD cents. JSON never carries a floating point amount.
type Money int64

func (m Money) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.FormatInt(int64(m), 10)) }
func (m *Money) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("amount must be a decimal string")
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 || n > 1000000000 {
		return fmt.Errorf("amount outside supported range")
	}
	*m = Money(n)
	return nil
}
func id(prefix string) string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return prefix + "_" + hex.EncodeToString(b)
}

type Product struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Brand       string   `json:"brand"`
	Description string   `json:"description"`
	Price       Money    `json:"price"`
	Variants    []string `json:"variants"`
}
type Card struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Network     string `json:"network"`
	Last4       string `json:"last4"`
	Color       string `json:"color"`
	RateBPS     int64  `json:"rate_bps"`
	Protection  string `json:"protection"`
	Enrolled    bool   `json:"enrolled"`
	RuleVersion string `json:"rule_version"`
}
type Intent struct {
	ID         string    `json:"id"`
	ProductID  string    `json:"product_id"`
	Variant    string    `json:"variant"`
	Quantity   int       `json:"quantity"`
	MaxTotal   Money     `json:"max_total"`
	PostalCode string    `json:"postal_code"`
	CreatedAt  time.Time `json:"created_at"`
}
type IntentInput struct {
	ProductID  string `json:"product_id"`
	Variant    string `json:"variant"`
	Quantity   int    `json:"quantity"`
	MaxTotal   Money  `json:"max_total"`
	PostalCode string `json:"postal_code"`
}
type Quote struct {
	ID          string    `json:"id"`
	MerchantID  string    `json:"merchant_id"`
	Merchant    string    `json:"merchant"`
	Subtotal    Money     `json:"subtotal"`
	Shipping    Money     `json:"shipping"`
	Tax         Money     `json:"tax"`
	Total       Money     `json:"total"`
	Currency    string    `json:"currency"`
	Delivery    string    `json:"delivery"`
	ReturnTerms string    `json:"return_terms"`
	InStock     bool      `json:"in_stock"`
	Networks    []string  `json:"networks"`
	ExpiresAt   time.Time `json:"expires_at"`
}
type Ranking struct {
	Card           Card   `json:"card"`
	Reward         Money  `json:"reward"`
	EffectiveTotal Money  `json:"effective_total"`
	Eligible       bool   `json:"eligible"`
	Reason         string `json:"reason"`
}
type Checkout struct {
	ID         string    `json:"id"`
	Intent     Intent    `json:"intent"`
	Quotes     []Quote   `json:"quotes"`
	Quote      Quote     `json:"quote"`
	CardID     string    `json:"card_id"`
	Rankings   []Ranking `json:"rankings"`
	Version    int       `json:"version"`
	State      string    `json:"state"`
	ApprovalID string    `json:"approval_id"`
	AttemptID  string    `json:"attempt_id"`
	OrderID    string    `json:"order_id"`
	Scenario   string    `json:"scenario"`
	CreatedAt  time.Time `json:"created_at"`
}
type Consent struct {
	ID          string    `json:"id"`
	CheckoutID  string    `json:"checkout_id"`
	Version     int       `json:"version"`
	Fingerprint string    `json:"fingerprint"`
	ExpiresAt   time.Time `json:"expires_at"`
	Revoked     bool      `json:"revoked"`
	CreatedAt   time.Time `json:"created_at"`
}
type Attempt struct {
	ID         string    `json:"id"`
	CheckoutID string    `json:"checkout_id"`
	Status     string    `json:"status"`
	Amount     Money     `json:"amount"`
	CardID     string    `json:"card_id"`
	MerchantID string    `json:"merchant_id"`
	Currency   string    `json:"currency"`
	Reserved   bool      `json:"reserved"`
	CreatedAt  time.Time `json:"created_at"`
}
type Order struct {
	ID                string    `json:"id"`
	CheckoutID        string    `json:"checkout_id"`
	Product           string    `json:"product"`
	Variant           string    `json:"variant"`
	Quantity          int       `json:"quantity"`
	Merchant          string    `json:"merchant"`
	Total             Money     `json:"total"`
	Reward            Money     `json:"reward"`
	Refunded          Money     `json:"refunded"`
	PaymentStatus     string    `json:"payment_status"`
	FulfillmentStatus string    `json:"fulfillment_status"`
	CardName          string    `json:"card_name"`
	CardLast4         string    `json:"card_last4"`
	Delivery          string    `json:"delivery"`
	DeliveredAt       time.Time `json:"delivered_at"`
	CreatedAt         time.Time `json:"created_at"`
}
type Refund struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"order_id"`
	Amount    Money     `json:"amount"`
	Reason    string    `json:"reason"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
type Audit struct {
	ID       string    `json:"id"`
	EntityID string    `json:"entity_id"`
	Kind     string    `json:"kind"`
	Detail   string    `json:"detail"`
	At       time.Time `json:"at"`
}
type LedgerEntry struct {
	ID       string    `json:"id"`
	EntityID string    `json:"entity_id"`
	Kind     string    `json:"kind"`
	Amount   Money     `json:"amount"`
	At       time.Time `json:"at"`
}
type Job struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	EntityID   string    `json:"entity_id"`
	Status     string    `json:"status"`
	Attempts   int       `json:"attempts"`
	LeaseUntil time.Time `json:"lease_until"`
	NextAt     time.Time `json:"next_at"`
}
type Idempotency struct {
	Fingerprint string `json:"fingerprint"`
	EntityID    string `json:"entity_id"`
}

// ProviderResult represents the independently persisted sandbox provider's source of truth.
type ProviderResult struct {
	Status      string `json:"status"`
	OrderStatus string `json:"order_status"`
	Amount      Money  `json:"amount"`
}
type State struct {
	Intents   map[string]Intent         `json:"intents"`
	Checkouts map[string]Checkout       `json:"checkouts"`
	Consents  map[string]Consent        `json:"consents"`
	Attempts  map[string]Attempt        `json:"attempts"`
	Orders    map[string]Order          `json:"orders"`
	Refunds   map[string]Refund         `json:"refunds"`
	Jobs      map[string]Job            `json:"jobs"`
	Keys      map[string]Idempotency    `json:"keys"`
	Provider  map[string]ProviderResult `json:"provider"`
	Webhooks  map[string]bool           `json:"webhooks"`
	Cards     []Card                    `json:"cards"`
	Audit     []Audit                   `json:"audit"`
	Ledger    []LedgerEntry             `json:"ledger"`
	Limit     Money                     `json:"limit"`
	Reserved  Money                     `json:"reserved"`
	Spent     Money                     `json:"spent"`
	Paused    bool                      `json:"paused"`
}

func newState() *State {
	return &State{Intents: map[string]Intent{}, Checkouts: map[string]Checkout{}, Consents: map[string]Consent{}, Attempts: map[string]Attempt{}, Orders: map[string]Order{}, Refunds: map[string]Refund{}, Jobs: map[string]Job{}, Keys: map[string]Idempotency{}, Provider: map[string]ProviderResult{}, Webhooks: map[string]bool{}, Cards: defaultCards(), Audit: []Audit{}, Ledger: []LedgerEntry{}, Limit: 100000}
}
func (s *State) event(entity, kind, detail string) {
	s.Audit = append(s.Audit, Audit{id("evt"), entity, kind, detail, time.Now().UTC()})
}
func (s *State) entry(entity, kind string, amount Money) {
	s.Ledger = append(s.Ledger, LedgerEntry{id("led"), entity, kind, amount, time.Now().UTC()})
}
func (s *State) enqueue(kind, entity string) {
	for _, j := range s.Jobs {
		if j.Kind == kind && j.EntityID == entity && j.Status != "done" {
			return
		}
	}
	j := Job{ID: id("job"), Kind: kind, EntityID: entity, Status: "pending", NextAt: time.Now().UTC()}
	s.Jobs[j.ID] = j
}
func Catalog() []Product {
	return []Product{{"studio-pro", "Studio Pro", "SOUNDLAB", "Wireless over-ear · Adaptive noise cancellation", 16900, []string{"Midnight", "Cloud"}}, {"air-mini", "Air Mini", "SOUNDLAB", "True wireless · Compact charging case", 9900, []string{"Midnight", "Cloud"}}, {"reference-one", "Reference One", "FORM AUDIO", "Over-ear · 40 mm precision drivers", 22900, []string{"Graphite", "Silver"}}}
}
func defaultCards() []Card {
	return []Card{{"everyday", "Everyday Cash", "visa", "4242", "green", 300, "90-day purchase protection", true, "sandbox-2026-10-v1"}, {"travel", "Voyager Rewards", "mastercard", "5556", "navy", 200, "Extended warranty · 1 additional year", true, "sandbox-2026-10-v1"}, {"essential", "Essential", "amex", "0005", "sand", 150, "Standard card protections", false, "sandbox-2026-10-v1"}}
}
