// Package quotestest is the contract test every quote adapter must pass.
package quotestest

import (
	"context"
	"testing"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/quotes"
)

// RunProviderContract checks the guarantees the orchestrator relies on for
// every basket in the provider's catalog.
func RunProviderContract(t *testing.T, p quotes.Provider) {
	t.Helper()
	ctx := context.Background()
	catalog, err := p.Catalog(ctx)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(catalog) == 0 {
		t.Fatal("catalog is empty")
	}
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.UTC)
	for _, basket := range catalog {
		t.Run(basket.BasketRef, func(t *testing.T) {
			req := quotes.Request{TenantID: "t", CustomerID: "c", MerchantID: basket.MerchantID, BasketRef: basket.BasketRef, Now: now}
			q, err := p.Quote(ctx, req)
			if err != nil {
				t.Fatalf("quote: %v", err)
			}
			if q.BasketHash == "" || q.Provider == "" {
				t.Fatal("quotes must name their provider and basket hash")
			}
			if len(q.Options) == 0 {
				t.Fatal("no options")
			}
			seen := map[string]bool{}
			for _, o := range q.Options {
				if o.OptionID == "" || seen[o.OptionID] {
					t.Fatalf("option ids must be non-empty and unique, got %q", o.OptionID)
				}
				seen[o.OptionID] = true
				if o.Rail != "card" && o.Rail != "stablecoin" {
					t.Errorf("%s: unknown rail %q", o.OptionID, o.Rail)
				}
				if o.Quote.QuoteID == "" {
					t.Errorf("%s: quote has no id", o.OptionID)
				}
				if !o.Quote.ValidUntil.After(o.Quote.ObtainedAt) {
					t.Errorf("%s: quote expires before it was obtained", o.OptionID)
				}
				if o.Quote.ObtainedAt.After(now) {
					t.Errorf("%s: quote obtained in the future", o.OptionID)
				}
				if _, err := contract.ParseUnits(string(o.Quote.DeliveredPrice.BaseUnits)); err != nil {
					t.Errorf("%s: delivered price: %v", o.OptionID, err)
				}
				if o.Fees == nil || o.Discounts == nil || o.Rewards == nil || o.Tradeoffs.Notes == nil {
					t.Errorf("%s: slices must be normalized to non-nil", o.OptionID)
				}
				for _, fee := range o.Fees {
					if (fee.Status == "known") != (fee.Amount != nil) {
						t.Errorf("%s: fee %s: only known fees carry an amount", o.OptionID, fee.FeeID)
					}
				}
				for _, rule := range o.Rewards {
					if rule.RuleID == "" || rule.RuleVersion == "" {
						t.Errorf("%s: reward rules must be versioned", o.OptionID)
					}
				}
			}

			// The same request must be reproducible, and callers must not be able
			// to mutate provider state through a returned value.
			q.Options[0].Label = "mutated"
			again, err := p.Quote(ctx, req)
			if err != nil {
				t.Fatalf("second quote: %v", err)
			}
			if again.Options[0].Label == "mutated" {
				t.Error("provider returned shared mutable state")
			}
			if again.BasketHash != q.BasketHash {
				t.Error("basket hash changed between identical requests")
			}
		})
	}

	if _, err := p.Quote(ctx, quotes.Request{MerchantID: "no-such-merchant", BasketRef: "none", Now: now}); err != quotes.ErrUnknownBasket {
		t.Errorf("unknown basket: got %v, want ErrUnknownBasket", err)
	}
}
