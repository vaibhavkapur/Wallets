// Package quotes defines the quote-provider boundary. A provider returns, for
// one basket and one customer, every candidate payment option with its rail
// quote, fees, discounts, reward rules, funds and availability.
package quotes

import (
	"context"
	"errors"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
)

var (
	// ErrUnknownBasket means the provider has no quotes for the basket.
	ErrUnknownBasket = errors.New("quotes: unknown basket")
	// ErrUnavailable means the provider could not be reached or answered unusably.
	ErrUnavailable = errors.New("quotes: provider unavailable")
)

// Request identifies what to quote.
type Request struct {
	TenantID   string
	CustomerID string
	MerchantID string
	BasketRef  string
	Now        time.Time
	// Fresh bypasses any cache; used when a customer asks for a refresh.
	Fresh bool
}

// Line is one basket line, for display only.
type Line struct {
	SKU            string         `json:"sku"`
	Description    string         `json:"description"`
	Quantity       int            `json:"quantity"`
	UnitPriceMinor contract.Units `json:"unit_price_minor"`
}

// BasketInfo describes a quotable basket.
type BasketInfo struct {
	MerchantID   string `json:"merchant_id"`
	MerchantName string `json:"merchant_name"`
	BasketRef    string `json:"basket_ref"`
	Title        string `json:"title"`
	Lines        []Line `json:"lines"`
	// Synthetic is true for illustrative data that is not a provider quote.
	Synthetic bool `json:"synthetic"`
}

// Quotes is everything a provider knows about paying for one basket.
type Quotes struct {
	Provider   string
	BasketHash string
	Info       BasketInfo
	Options    []contract.PaymentOption
}

// Provider supplies comparable quotes for the same basket across rails.
type Provider interface {
	Name() string
	Catalog(ctx context.Context) ([]BasketInfo, error)
	Quote(ctx context.Context, req Request) (*Quotes, error)
}

// Balances reads spendable funds. It is separate from Provider so that a quote
// cache can never stand in for a balance.
type Balances interface {
	Funds(ctx context.Context, req Request, option contract.PaymentOption) (contract.Funds, error)
}

// Normalize replaces nil slices with empty ones. The engine requires arrays,
// and Go would otherwise encode nil slices as null.
func Normalize(options []contract.PaymentOption) {
	for i := range options {
		o := &options[i]
		if o.Fees == nil {
			o.Fees = []contract.Fee{}
		}
		if o.Discounts == nil {
			o.Discounts = []contract.Discount{}
		}
		if o.Rewards == nil {
			o.Rewards = []contract.RewardRule{}
		}
		if o.Tradeoffs.Notes == nil {
			o.Tradeoffs.Notes = []string{}
		}
	}
}
