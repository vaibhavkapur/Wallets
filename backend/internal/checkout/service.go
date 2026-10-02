package checkout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Problem struct {
	Status        int
	Code, Message string
}

func (e *Problem) Error() string                     { return e.Message }
func problem(status int, code, message string) error { return &Problem{status, code, message} }
func missing() error                                 { return problem(404, "not_found", "This record was not found.") }
func conflict(message string) error                  { return problem(409, "conflict", message) }

type Service struct {
	Store    Store
	Merchant MerchantAdapter
	Provider PaymentAdapter
	Now      func() time.Time
}

func NewService(store Store) *Service {
	return &Service{store, SandboxMerchant{}, SandboxProvider{store}, func() time.Time { return time.Now().UTC() }}
}
func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func approvalFingerprint(c Checkout) string {
	return fingerprint(struct {
		Intent   Intent
		Quote    Quote
		CardID   string
		Version  int
		Rankings []Ranking
		Scenario string
	}{c.Intent, c.Quote, c.CardID, c.Version, c.Rankings, c.Scenario})
}
func (s *Service) CreateIntent(ctx context.Context, t string, in IntentInput) (Intent, error) {
	p := Product{}
	for _, item := range Catalog() {
		if item.ID == in.ProductID {
			p = item
		}
	}
	variant := false
	for _, v := range p.Variants {
		if v == in.Variant {
			variant = true
		}
	}
	if p.ID == "" || !variant || in.Quantity < 1 || in.Quantity > 5 || in.MaxTotal < 100 || in.MaxTotal > 100000 || !regexp.MustCompile(`^\d{5}$`).MatchString(in.PostalCode) {
		return Intent{}, problem(422, "invalid_intent", "Choose a supported model and color, 1–5 items, a $1–$1,000 limit, and a five-digit US ZIP code.")
	}
	out := Intent{id("intent"), in.ProductID, in.Variant, in.Quantity, in.MaxTotal, in.PostalCode, s.Now()}
	e := s.Store.Update(ctx, t, func(st *State) error {
		st.Intents[out.ID] = out
		st.event(out.ID, "intent.created", "Structured purchase request created")
		return nil
	})
	return out, e
}
func (s *Service) Quote(ctx context.Context, t, intent string) (Checkout, error) {
	st, e := s.Store.View(ctx, t)
	if e != nil {
		return Checkout{}, e
	}
	in, ok := st.Intents[intent]
	if !ok {
		return Checkout{}, missing()
	}
	qs, e := s.Merchant.Quotes(ctx, in)
	if e != nil {
		return Checkout{}, e
	}
	if len(qs) == 0 {
		return Checkout{}, conflict("No offers are available.")
	}
	var out Checkout
	e = s.Store.Update(ctx, t, func(st *State) error {
		for _, existing := range st.Checkouts {
			if existing.Intent.ID == intent && existing.State != "canceled" && existing.State != "expired" {
				out = existing
				return nil
			}
		}
		selected := qs[0]
		if selected.Total > in.MaxTotal {
			return problem(422, "over_budget", "The delivered price exceeds your limit. Increase it or choose another product.")
		}
		rankings := rank(st.Cards, selected)
		if len(rankings) == 0 || !rankings[0].Eligible {
			return conflict("Enroll a compatible card first.")
		}
		out = Checkout{ID: id("checkout"), Intent: in, Quotes: qs, Quote: selected, CardID: rankings[0].Card.ID, Rankings: rankings, Version: 1, State: "awaiting_approval", Scenario: "success", CreatedAt: s.Now()}
		st.Checkouts[out.ID] = out
		st.event(out.ID, "quote.created", "Delivered price and card eligibility confirmed")
		return nil
	})
	return out, e
}

type SelectionInput struct {
	Version    int    `json:"version"`
	MerchantID string `json:"merchant_id"`
	CardID     string `json:"card_id"`
	Scenario   string `json:"scenario"`
}

func (s *Service) Select(ctx context.Context, t, cid string, in SelectionInput) (Checkout, error) {
	var out Checkout
	e := s.Store.Update(ctx, t, func(st *State) error {
		c, ok := st.Checkouts[cid]
		if !ok {
			return missing()
		}
		if c.Version != in.Version {
			return conflict("Checkout changed. Reload and review the latest version.")
		}
		if c.AttemptID != "" || c.State == "canceled" {
			return conflict("An executing or closed checkout cannot be changed.")
		}
		found := false
		for _, q := range c.Quotes {
			if q.MerchantID == in.MerchantID {
				c.Quote = q
				found = true
			}
		}
		if !found {
			return problem(422, "invalid_merchant", "Choose a quoted merchant.")
		}
		if c.Quote.Total > c.Intent.MaxTotal {
			return conflict("This offer exceeds your purchase limit.")
		}
		c.Rankings = rank(st.Cards, c.Quote)
		eligible := false
		for _, r := range c.Rankings {
			if r.Card.ID == in.CardID && r.Eligible {
				eligible = true
			}
		}
		if !eligible {
			return problem(422, "unsupported_card", "Choose an enrolled card accepted by this merchant.")
		}
		allowed := map[string]bool{"success": true, "challenge": true, "timeout": true, "declined": true, "merchant_failure": true, "price_changed": true, "out_of_stock": true}
		if !allowed[in.Scenario] {
			return problem(422, "invalid_scenario", "Unknown sandbox scenario.")
		}
		c.CardID = in.CardID
		c.Scenario = in.Scenario
		c.Version++
		c.State = "awaiting_approval"
		if c.ApprovalID != "" {
			a := st.Consents[c.ApprovalID]
			a.Revoked = true
			st.Consents[a.ID] = a
		}
		c.ApprovalID = ""
		st.Checkouts[c.ID] = c
		st.event(c.ID, "approval.invalidated", "Checkout selection changed; a new approval is required")
		out = c
		return nil
	})
	return out, e
}
func (s *Service) Approve(ctx context.Context, t, cid string, version int) (Consent, error) {
	var out Consent
	e := s.Store.Update(ctx, t, func(st *State) error {
		c, ok := st.Checkouts[cid]
		if !ok {
			return missing()
		}
		if st.Paused {
			return conflict("Purchases are paused by the sandbox operator.")
		}
		if c.Version != version || c.AttemptID != "" || c.State == "canceled" {
			return conflict("This checkout cannot be approved. Review the latest details.")
		}
		if !s.Now().Before(c.Quote.ExpiresAt) {
			return conflict("This quote expired. Start a new checkout for a fresh price.")
		}
		if c.ApprovalID != "" {
			old := st.Consents[c.ApprovalID]
			if !old.Revoked && s.Now().Before(old.ExpiresAt) {
				out = old
				return nil
			}
		}
		out = Consent{ID: id("approval"), CheckoutID: cid, Version: version, Fingerprint: approvalFingerprint(c), ExpiresAt: s.Now().Add(5 * time.Minute), CreatedAt: s.Now()}
		st.Consents[out.ID] = out
		c.ApprovalID = out.ID
		c.State = "approved"
		st.Checkouts[cid] = c
		st.event(cid, "approval.granted", fmt.Sprintf("Customer approved exact checkout version %d", version))
		return nil
	})
	return out, e
}
func (s *Service) Revoke(ctx context.Context, t, cid string) error {
	return s.Store.Update(ctx, t, func(st *State) error {
		c, ok := st.Checkouts[cid]
		if !ok {
			return missing()
		}
		if c.AttemptID != "" {
			return conflict("Payment has already been submitted. Track the outcome before requesting cancellation.")
		}
		if c.ApprovalID != "" {
			a := st.Consents[c.ApprovalID]
			a.Revoked = true
			st.Consents[a.ID] = a
		}
		c.State = "canceled"
		st.Checkouts[cid] = c
		st.event(cid, "approval.revoked", "Customer canceled checkout and revoked approval")
		return nil
	})
}

type ExecuteInput struct {
	ApprovalID string `json:"approval_id"`
}

func validKey(key string) bool {
	return len(key) >= 8 && len(key) <= 128 && regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(key)
}
func (s *Service) Execute(ctx context.Context, t, cid, approval, key string) (Checkout, error) {
	if !validKey(key) {
		return Checkout{}, problem(400, "idempotency_required", "Provide an Idempotency-Key with 8–128 letters, numbers, underscores or dashes.")
	}
	st, e := s.Store.View(ctx, t)
	if e != nil {
		return Checkout{}, e
	}
	c, ok := st.Checkouts[cid]
	if !ok {
		return Checkout{}, missing()
	}
	requestHash := fingerprint([]string{cid, approval})
	if old, ok := st.Keys["execute:"+key]; ok {
		if old.Fingerprint != requestHash {
			return Checkout{}, conflict("This idempotency key was used for another request.")
		}
		return c, nil
	}
	if c.AttemptID != "" {
		return Checkout{}, conflict("This checkout already has a payment attempt. Check its status.")
	}
	fresh, e := s.Merchant.Revalidate(ctx, c)
	if e != nil {
		return Checkout{}, e
	}
	if fingerprint(fresh) != fingerprint(c.Quote) || !fresh.InStock {
		return Checkout{}, conflict("The merchant changed the price or stock. Start a new checkout and approve the new details.")
	}
	var out Checkout
	e = s.Store.Update(ctx, t, func(st *State) error {
		c, ok := st.Checkouts[cid]
		if !ok {
			return missing()
		}
		if old, ok := st.Keys["execute:"+key]; ok {
			if old.Fingerprint != requestHash {
				return conflict("This idempotency key was used for another request.")
			}
			out = c
			return nil
		}
		if st.Paused {
			return conflict("Purchases are paused by the sandbox operator.")
		}
		if c.AttemptID != "" {
			return conflict("A payment attempt already exists; reconcile it before continuing.")
		}
		a, ok := st.Consents[approval]
		if !ok || a.CheckoutID != cid || c.ApprovalID != approval || a.Revoked || a.Version != c.Version || a.Fingerprint != approvalFingerprint(c) || !s.Now().Before(a.ExpiresAt) || !s.Now().Before(c.Quote.ExpiresAt) || c.State != "approved" {
			return conflict("Approval is missing, expired, revoked, or no longer matches this checkout.")
		}
		if fingerprint(fresh) != fingerprint(c.Quote) {
			return conflict("Checkout changed during revalidation.")
		}
		eligible := false
		for _, r := range rank(st.Cards, c.Quote) {
			if r.Card.ID == c.CardID && r.Eligible {
				eligible = true
			}
		}
		if !eligible {
			return conflict("The selected card is no longer eligible.")
		}
		if c.Quote.Total > c.Intent.MaxTotal || st.Spent+st.Reserved+c.Quote.Total > st.Limit {
			return conflict("The purchase would exceed your available sandbox budget.")
		}
		attempt := Attempt{ID: id("payment"), CheckoutID: cid, Status: "pending", Amount: c.Quote.Total, CardID: c.CardID, MerchantID: c.Quote.MerchantID, Currency: c.Quote.Currency, Reserved: true, CreatedAt: s.Now()}
		st.Attempts[attempt.ID] = attempt
		st.Reserved += attempt.Amount
		st.entry(attempt.ID, "reserve", attempt.Amount)
		c.State = "executing"
		c.AttemptID = attempt.ID
		st.Checkouts[cid] = c
		st.Keys["execute:"+key] = Idempotency{requestHash, cid}
		st.enqueue("pay", cid)
		st.event(cid, "payment.queued", "Budget reserved and payment queued atomically")
		out = c
		return nil
	})
	return out, e
}
func (s *Service) Confirm(ctx context.Context, t, cid string, success bool) error {
	st, e := s.Store.View(ctx, t)
	if e != nil {
		return e
	}
	c, ok := st.Checkouts[cid]
	if !ok {
		return missing()
	}
	a, ok := st.Attempts[c.AttemptID]
	if !ok || a.Status != "requires_customer_action" {
		return conflict("No issuer challenge is waiting for this checkout.")
	}
	r, e := s.Provider.Confirm(ctx, t, a.ID, success)
	if e != nil {
		return e
	}
	return s.applyPayment(ctx, t, cid, r)
}
func (s *Service) Reconcile(ctx context.Context, t, cid string) error {
	return s.Store.Update(ctx, t, func(st *State) error {
		c, ok := st.Checkouts[cid]
		if !ok {
			return missing()
		}
		a, ok := st.Attempts[c.AttemptID]
		if !ok {
			return conflict("No payment to reconcile.")
		}
		if a.Status == "succeeded" || a.Status == "failed" {
			return nil
		}
		st.enqueue("reconcile", cid)
		st.event(cid, "reconciliation.requested", "Provider lookup queued; no new charge will be created")
		return nil
	})
}
func (s *Service) Refund(ctx context.Context, t, oid, key string, amount Money, reason string, cancel bool) (Refund, error) {
	if !validKey(key) {
		return Refund{}, problem(400, "idempotency_required", "A valid Idempotency-Key is required.")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 300 {
		return Refund{}, problem(422, "invalid_reason", "Add a reason of up to 300 characters.")
	}
	hash := fingerprint(struct {
		O string
		A Money
		R string
		C bool
	}{oid, amount, reason, cancel})
	var out Refund
	e := s.Store.Update(ctx, t, func(st *State) error {
		if old, ok := st.Keys["refund:"+key]; ok {
			if old.Fingerprint != hash {
				return conflict("This idempotency key was already used for another refund.")
			}
			out = st.Refunds[old.EntityID]
			return nil
		}
		o, ok := st.Orders[oid]
		if !ok {
			return missing()
		}
		if o.PaymentStatus != "succeeded" && o.PaymentStatus != "partially_refunded" {
			return conflict("Only a settled payment can be refunded.")
		}
		if !o.DeliveredAt.IsZero() && s.Now().After(o.DeliveredAt.Add(30*24*time.Hour)) {
			return conflict("The sandbox return window has closed.")
		}
		pending := Money(0)
		for _, r := range st.Refunds {
			if r.OrderID == oid && r.Status != "failed" {
				pending += r.Amount
			}
		}
		if cancel {
			if o.FulfillmentStatus != "confirmed" && o.FulfillmentStatus != "failed" {
				return conflict("This order cannot be canceled.")
			}
			amount = o.Total - pending
		}
		if amount <= 0 || amount+pending > o.Total {
			return conflict("Refund amount exceeds the remaining refundable total.")
		}
		out = Refund{ID: id("refund"), OrderID: oid, Amount: amount, Reason: reason, Status: "pending", CreatedAt: s.Now()}
		st.Refunds[out.ID] = out
		st.Keys["refund:"+key] = Idempotency{hash, out.ID}
		if cancel {
			o.FulfillmentStatus = "cancellation_requested"
			st.Orders[oid] = o
		}
		st.enqueue("refund", out.ID)
		st.event(oid, "refund.requested", "Refund queued with provider idempotency")
		return nil
	})
	return out, e
}
func (s *Service) Enroll(ctx context.Context, t, card string) error {
	return s.Store.Update(ctx, t, func(st *State) error {
		for i, c := range st.Cards {
			if c.ID == card {
				st.Cards[i].Enrolled = true
				st.event(card, "card.enrolled", "Sandbox provider reference enrolled; no card details collected")
				return nil
			}
		}
		return missing()
	})
}

// AdvanceFulfillment simulates an authenticated merchant delivery update in the
// sandbox. It cannot change payment state or reverse a confirmed cancellation.
func (s *Service) AdvanceFulfillment(ctx context.Context, t, oid string) error {
	return s.Store.Update(ctx, t, func(st *State) error {
		o, ok := st.Orders[oid]
		if !ok {
			return missing()
		}
		if o.PaymentStatus == "refunded" {
			return conflict("A fully refunded order cannot advance fulfillment.")
		}
		switch o.FulfillmentStatus {
		case "confirmed":
			o.FulfillmentStatus = "shipped"
		case "shipped":
			o.FulfillmentStatus = "delivered"
			o.DeliveredAt = s.Now()
		default:
			return conflict("This order has no next fulfillment step.")
		}
		st.Orders[oid] = o
		st.event(oid, "order."+o.FulfillmentStatus, "Sandbox merchant fulfillment update confirmed")
		return nil
	})
}
func (s *Service) Pause(ctx context.Context, t string, paused bool) error {
	return s.Store.Update(ctx, t, func(st *State) error {
		st.Paused = paused
		st.event("operator", "purchases.pause_changed", fmt.Sprintf("Purchases paused: %t", paused))
		return nil
	})
}

type Dashboard struct {
	Mode      string        `json:"mode"`
	Products  []Product     `json:"products"`
	Cards     []Card        `json:"cards"`
	Checkouts []Checkout    `json:"checkouts"`
	Orders    []Order       `json:"orders"`
	Refunds   []Refund      `json:"refunds"`
	Attempts  []Attempt     `json:"attempts"`
	Audit     []Audit       `json:"audit"`
	Ledger    []LedgerEntry `json:"ledger"`
	Jobs      []Job         `json:"jobs"`
	Budget    Budget        `json:"budget"`
	Paused    bool          `json:"paused"`
}
type Budget struct {
	Limit     Money `json:"limit"`
	Reserved  Money `json:"reserved"`
	Spent     Money `json:"spent"`
	Available Money `json:"available"`
}

func (s *Service) Dashboard(ctx context.Context, t string) (Dashboard, error) {
	st, e := s.Store.View(ctx, t)
	if e != nil {
		return Dashboard{}, e
	}
	d := Dashboard{Mode: "sandbox", Products: Catalog(), Cards: st.Cards, Checkouts: []Checkout{}, Orders: []Order{}, Refunds: []Refund{}, Attempts: []Attempt{}, Audit: st.Audit, Ledger: st.Ledger, Jobs: []Job{}, Budget: Budget{st.Limit, st.Reserved, st.Spent, st.Limit - st.Spent - st.Reserved}, Paused: st.Paused}
	for _, v := range st.Checkouts {
		d.Checkouts = append(d.Checkouts, v)
	}
	for _, v := range st.Orders {
		d.Orders = append(d.Orders, v)
	}
	for _, v := range st.Refunds {
		d.Refunds = append(d.Refunds, v)
	}
	for _, v := range st.Attempts {
		d.Attempts = append(d.Attempts, v)
	}
	for _, v := range st.Jobs {
		d.Jobs = append(d.Jobs, v)
	}
	sort.Slice(d.Checkouts, func(i, j int) bool { return d.Checkouts[i].CreatedAt.After(d.Checkouts[j].CreatedAt) })
	sort.Slice(d.Orders, func(i, j int) bool { return d.Orders[i].CreatedAt.After(d.Orders[j].CreatedAt) })
	sort.Slice(d.Audit, func(i, j int) bool { return d.Audit[i].At.After(d.Audit[j].At) })
	return d, nil
}
