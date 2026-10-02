package checkout

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

var ErrUnknown = errors.New("provider outcome unknown")

type MerchantAdapter interface {
	Quotes(context.Context, Intent) ([]Quote, error)
	Revalidate(context.Context, Checkout) (Quote, error)
}
type PaymentAdapter interface {
	Pay(context.Context, string, Checkout, Attempt) (ProviderResult, error)
	Lookup(context.Context, string, string) (ProviderResult, error)
	Confirm(context.Context, string, string, bool) (ProviderResult, error)
	Refund(context.Context, string, Refund) (ProviderResult, error)
}
type SandboxMerchant struct{}

func (SandboxMerchant) Quotes(ctx context.Context, in Intent) ([]Quote, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	var product Product
	for _, p := range Catalog() {
		if p.ID == in.ProductID {
			product = p
		}
	}
	if product.ID == "" {
		return nil, problem(422, "unknown_product", "Choose a supported headphone model.")
	}
	out := []Quote{}
	for i, name := range []string{"Soundhouse", "Circuit Supply", "Audio Avenue"} {
		subtotal := (product.Price + Money(i*500)) * Money(in.Quantity)
		shipping := Money(0)
		if i == 1 {
			shipping = 499
		}
		tax := (subtotal*825 + 5000) / 10000
		networks := []string{"visa", "mastercard"}
		if i == 2 {
			networks = append(networks, "amex")
		}
		out = append(out, Quote{ID: id("quote"), MerchantID: fmt.Sprintf("merchant_%d", i), Merchant: name, Subtotal: subtotal, Shipping: shipping, Tax: tax, Total: subtotal + shipping + tax, Currency: "USD", Delivery: []string{"Arrives in 2–3 business days", "Arrives in 1–2 business days", "Arrives in 3–5 business days"}[i], ReturnTerms: "Free returns within 30 days of delivery. Original packaging required.", InStock: true, Networks: networks, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)})
	}
	return out, nil
}
func (m SandboxMerchant) Revalidate(ctx context.Context, c Checkout) (Quote, error) {
	quotes, e := m.Quotes(ctx, c.Intent)
	if e != nil {
		return Quote{}, e
	}
	for _, q := range quotes {
		if q.MerchantID == c.Quote.MerchantID {
			q.ID = c.Quote.ID
			q.ExpiresAt = c.Quote.ExpiresAt
			if c.Scenario == "price_changed" {
				q.Total += 1000
			}
			if c.Scenario == "out_of_stock" {
				q.InStock = false
			}
			return q, nil
		}
	}
	return Quote{}, errors.New("merchant disappeared")
}
func rank(cards []Card, q Quote) []Ranking {
	out := []Ranking{}
	for _, c := range cards {
		eligible := false
		for _, n := range q.Networks {
			if n == c.Network {
				eligible = true
			}
		}
		reason := "Eligible · sandbox rewards"
		if !eligible {
			reason = "This merchant does not accept this network"
		}
		if !c.Enrolled {
			eligible = false
			reason = "Enroll this sandbox card to use it"
		}
		reward := q.Total * Money(c.RateBPS) / 10000
		out = append(out, Ranking{c, reward, q.Total - reward, eligible, reason})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Eligible != out[j].Eligible {
			return out[i].Eligible
		}
		return out[i].Reward > out[j].Reward
	})
	return out
}

type SandboxProvider struct{ Store Store }

func (p SandboxProvider) Pay(ctx context.Context, t string, c Checkout, a Attempt) (ProviderResult, error) {
	var out ProviderResult
	first := false
	e := p.Store.Update(ctx, t, func(s *State) error {
		if old, ok := s.Provider[a.ID]; ok {
			out = old
			return nil
		}
		if a.Amount != c.Quote.Total || a.CardID != c.CardID || a.MerchantID != c.Quote.MerchantID || a.Currency != "USD" {
			return errors.New("scoped credential mismatch")
		}
		consent, valid := s.Consents[c.ApprovalID]
		if !valid || consent.Revoked || consent.Fingerprint != approvalFingerprint(c) || !time.Now().Before(consent.ExpiresAt) || !time.Now().Before(c.Quote.ExpiresAt) || s.Paused {
			out = ProviderResult{Status: "failed", OrderStatus: "not_placed", Amount: a.Amount}
			s.Provider[a.ID] = out
			return nil
		}
		first = true
		out = ProviderResult{Status: "succeeded", OrderStatus: "confirmed", Amount: a.Amount}
		switch c.Scenario {
		case "challenge":
			out.Status = "requires_customer_action"
			out.OrderStatus = "pending"
		case "declined":
			out.Status = "failed"
			out.OrderStatus = "not_placed"
		case "merchant_failure":
			out.OrderStatus = "failed"
		}
		s.Provider[a.ID] = out
		return nil
	})
	if e != nil {
		return out, e
	}
	if first && c.Scenario == "timeout" {
		return ProviderResult{}, ErrUnknown
	}
	return out, nil
}
func (p SandboxProvider) Lookup(ctx context.Context, t, attempt string) (ProviderResult, error) {
	s, e := p.Store.View(ctx, t)
	if e != nil {
		return ProviderResult{}, e
	}
	r, ok := s.Provider[attempt]
	if !ok {
		return ProviderResult{}, ErrUnknown
	}
	return r, nil
}
func (p SandboxProvider) Confirm(ctx context.Context, t, attempt string, success bool) (ProviderResult, error) {
	var out ProviderResult
	e := p.Store.Update(ctx, t, func(s *State) error {
		r, ok := s.Provider[attempt]
		if !ok {
			return ErrUnknown
		}
		if r.Status != "requires_customer_action" {
			out = r
			return nil
		}
		r.Status = "failed"
		r.OrderStatus = "not_placed"
		if success {
			r.Status = "succeeded"
			r.OrderStatus = "confirmed"
		}
		s.Provider[attempt] = r
		out = r
		return nil
	})
	return out, e
}
func (p SandboxProvider) Refund(ctx context.Context, t string, r Refund) (ProviderResult, error) {
	var out ProviderResult
	e := p.Store.Update(ctx, t, func(s *State) error {
		if old, ok := s.Provider[r.ID]; ok {
			out = old
			return nil
		}
		o, ok := s.Orders[r.OrderID]
		if !ok {
			return errors.New("order missing")
		}
		c := s.Checkouts[o.CheckoutID]
		payment, ok := s.Provider[c.AttemptID]
		if !ok || payment.Status != "succeeded" {
			return errors.New("payment not settled")
		}
		total := Money(0)
		for id, ref := range s.Refunds {
			if ref.OrderID == o.ID {
				if existing, ok := s.Provider[id]; ok && existing.Status == "succeeded" {
					total += ref.Amount
				}
			}
		}
		if total+r.Amount > payment.Amount {
			return errors.New("provider refund exceeds charge")
		}
		out = ProviderResult{Status: "succeeded", Amount: r.Amount}
		s.Provider[r.ID] = out
		return nil
	})
	return out, e
}
