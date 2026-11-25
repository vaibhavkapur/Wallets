package recurring

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ErrProviderTimeout signals an uncertain provider outcome that MUST be reconciled
// before retrying, switching payment methods, or releasing a budget reservation.
var ErrProviderTimeout = errors.New("provider_timeout_unknown_outcome")

// Catalog returns the narrow specialty coffee and household essentials fixture catalog.
func DefaultCatalog() []CatalogItem {
	return []CatalogItem{
		{
			SKU:              "COFFEE-ETHIOPIA-12OZ",
			Category:         "coffee",
			Brand:            "Blue Ridge Roasters",
			Title:            "Ethiopia Yirgacheffe Whole Bean Coffee",
			PackSizeValue:    12,
			PackSizeUnit:     "oz",
			DefaultUnitPrice: USD(1850),
			Merchants: []MerchantListing{
				{
					MerchantID:   "roaster_direct",
					MerchantName: "Roaster Direct",
					UnitPrice:    USD(1850),
					ShippingFee:  USD(499),
					TaxRateBps:   825,
					Protocol:     "ACP",
				},
				{
					MerchantID:   "pantry_prime",
					MerchantName: "Pantry Prime",
					UnitPrice:    USD(1925),
					ShippingFee:  USD(399),
					TaxRateBps:   825,
					Protocol:     "UCP",
				},
			},
			SuggestedSubstitutes: []string{"COFFEE-COLOMBIA-12OZ"},
		},
		{
			SKU:              "COFFEE-COLOMBIA-12OZ",
			Category:         "coffee",
			Brand:            "Blue Ridge Roasters",
			Title:            "Colombia Supremo Whole Bean Coffee",
			PackSizeValue:    12,
			PackSizeUnit:     "oz",
			DefaultUnitPrice: USD(1795),
			Merchants: []MerchantListing{
				{
					MerchantID:   "roaster_direct",
					MerchantName: "Roaster Direct",
					UnitPrice:    USD(1795),
					ShippingFee:  USD(499),
					TaxRateBps:   825,
					Protocol:     "ACP",
				},
				{
					MerchantID:   "pantry_prime",
					MerchantName: "Pantry Prime",
					UnitPrice:    USD(1845),
					ShippingFee:  USD(399),
					TaxRateBps:   825,
					Protocol:     "UCP",
				},
			},
			SuggestedSubstitutes: []string{"COFFEE-ETHIOPIA-12OZ"},
		},
		{
			SKU:              "COFFEE-ESPRESSO-16OZ",
			Category:         "coffee",
			Brand:            "Velvet Crema",
			Title:            "Velvet Crema Espresso Blend Whole Bean",
			PackSizeValue:    16,
			PackSizeUnit:     "oz",
			DefaultUnitPrice: USD(2200),
			Merchants: []MerchantListing{
				{
					MerchantID:   "roaster_direct",
					MerchantName: "Roaster Direct",
					UnitPrice:    USD(2200),
					ShippingFee:  USD(450),
					TaxRateBps:   825,
					Protocol:     "ACP",
				},
			},
			SuggestedSubstitutes: []string{"COFFEE-ETHIOPIA-12OZ"},
		},
		{
			SKU:              "HOUSE-DETERGENT-64OZ",
			Category:         "household",
			Brand:            "ClearClean",
			Title:            "Free & Gentle Liquid Laundry Detergent",
			PackSizeValue:    64,
			PackSizeUnit:     "oz",
			DefaultUnitPrice: USD(1499),
			Merchants: []MerchantListing{
				{
					MerchantID:   "pantry_prime",
					MerchantName: "Pantry Prime",
					UnitPrice:    USD(1499),
					ShippingFee:  USD(499),
					TaxRateBps:   825,
					Protocol:     "UCP",
				},
				{
					MerchantID:   "hearth_home",
					MerchantName: "Hearth & Home Supply",
					UnitPrice:    USD(1549),
					ShippingFee:  USD(399),
					TaxRateBps:   825,
					Protocol:     "ACP",
				},
			},
			SuggestedSubstitutes: []string{"HOUSE-DISHPODS-42CT"},
		},
		{
			SKU:              "HOUSE-TOWELS-6ROLL",
			Category:         "household",
			Brand:            "PureFiber",
			Title:            "PureFiber Select-a-Size Paper Towels",
			PackSizeValue:    6,
			PackSizeUnit:     "rolls",
			DefaultUnitPrice: USD(1349),
			Merchants: []MerchantListing{
				{
					MerchantID:   "pantry_prime",
					MerchantName: "Pantry Prime",
					UnitPrice:    USD(1349),
					ShippingFee:  USD(550),
					TaxRateBps:   825,
					Protocol:     "UCP",
				},
				{
					MerchantID:   "hearth_home",
					MerchantName: "Hearth & Home Supply",
					UnitPrice:    USD(1399),
					ShippingFee:  USD(450),
					TaxRateBps:   825,
					Protocol:     "ACP",
				},
			},
		},
		{
			SKU:              "HOUSE-DISHPODS-42CT",
			Category:         "household",
			Brand:            "SparklePure",
			Title:            "SparklePure Unscented Dishwasher Pods",
			PackSizeValue:    42,
			PackSizeUnit:     "count",
			DefaultUnitPrice: USD(1599),
			Merchants: []MerchantListing{
				{
					MerchantID:   "pantry_prime",
					MerchantName: "Pantry Prime",
					UnitPrice:    USD(1599),
					ShippingFee:  USD(399),
					TaxRateBps:   825,
					Protocol:     "UCP",
				},
				{
					MerchantID:   "hearth_home",
					MerchantName: "Hearth & Home Supply",
					UnitPrice:    USD(1625),
					ShippingFee:  USD(349),
					TaxRateBps:   825,
					Protocol:     "ACP",
				},
			},
		},
	}
}

// DefaultPaymentMethods lists scoped sandbox credentials (never raw card details or keys).
func DefaultPaymentMethods() []PaymentMethod {
	return []PaymentMethod{
		{
			ID:                 "pm_card_everyday",
			Label:              "Everyday Cash Visa •••• 4242",
			Network:            "Visa",
			Last4:              "4242",
			Rail:               "card",
			ScopedCredential:   true,
			SupportsDelegation: true,
		},
		{
			ID:                 "pm_card_household",
			Label:              "Household Rewards Mastercard •••• 8810",
			Network:            "Mastercard",
			Last4:              "8810",
			Rail:               "card",
			ScopedCredential:   true,
			SupportsDelegation: false,
		},
		{
			ID:                 "pm_usdc_base",
			Label:              "Scoped USDC Allowance (Base •••• 9f2a)",
			Network:            "Base",
			Last4:              "9f2a",
			Rail:               "stablecoin",
			ScopedCredential:   true,
			SupportsDelegation: false,
		},
	}
}

func FindCatalogItem(sku string) (CatalogItem, bool) {
	for _, item := range DefaultCatalog() {
		if item.SKU == sku {
			return item, true
		}
	}
	return CatalogItem{}, false
}

func FindPaymentMethod(id string) (PaymentMethod, bool) {
	for _, pm := range DefaultPaymentMethods() {
		if pm.ID == id {
			return pm, true
		}
	}
	return PaymentMethod{}, false
}

// QuoteRequest requests a fresh merchant quote for an occurrence.
type QuoteRequest struct {
	OccurrenceID        string
	SKU                 string
	PackSizeValue       int
	PackSizeUnit        string
	Quantity            int
	PreferredMerchantID string
	ShippingPostalCode  string
	Now                 time.Time
	TTL                 time.Duration
}

// MerchantAdapter obtains fresh merchant quotes (ACP/UCP or sandbox fixture).
type MerchantAdapter interface {
	Quote(ctx context.Context, req QuoteRequest, faults SandboxFaultConfig) (*MerchantQuote, error)
}

// SandboxMerchantAdapter implements deterministic merchant quotes and scenario injection.
type SandboxMerchantAdapter struct{}

func NewSandboxMerchantAdapter() *SandboxMerchantAdapter {
	return &SandboxMerchantAdapter{}
}

func (a *SandboxMerchantAdapter) Quote(ctx context.Context, req QuoteRequest, faults SandboxFaultConfig) (*MerchantQuote, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targetSKU := req.SKU
	isSub := false
	subReason := ""

	if faults.ForceSubstituteSKU != "" && faults.ForceSubstituteSKU != req.SKU {
		targetSKU = faults.ForceSubstituteSKU
		isSub = true
		subReason = fmt.Sprintf("Requested %s unavailable; merchant offered %s.", req.SKU, targetSKU)
	}

	item, ok := FindCatalogItem(targetSKU)
	if !ok {
		return nil, problem(404, "unknown_sku", fmt.Sprintf("SKU %q not found in merchant catalog.", targetSKU))
	}

	listing := item.Merchants[0]
	for _, m := range item.Merchants {
		if m.MerchantID == req.PreferredMerchantID {
			listing = m
			break
		}
	}

	packVal := item.PackSizeValue
	packChanged := false
	if faults.ForcePackSizeValue > 0 && faults.ForcePackSizeValue != req.PackSizeValue {
		packVal = faults.ForcePackSizeValue
		packChanged = true
		if subReason == "" {
			subReason = fmt.Sprintf("Merchant pack size changed from %d %s to %d %s.",
				req.PackSizeValue, req.PackSizeUnit, packVal, item.PackSizeUnit)
		}
	} else if packVal != req.PackSizeValue || item.PackSizeUnit != req.PackSizeUnit {
		packChanged = true
		if subReason == "" {
			subReason = fmt.Sprintf("Pack size mismatch: requested %d %s, quoted %d %s.",
				req.PackSizeValue, req.PackSizeUnit, packVal, item.PackSizeUnit)
		}
	}

	mult := faults.PriceMultiplierBps
	if mult <= 0 {
		mult = 10000
	}
	unitPrice, err := listing.UnitPrice.MulBpsHalfUp(int64(mult))
	if err != nil {
		return nil, err
	}
	subtotal, err := unitPrice.MulInt(int64(req.Quantity))
	if err != nil {
		return nil, err
	}
	tax, err := subtotal.MulBpsHalfUp(int64(listing.TaxRateBps))
	if err != nil {
		return nil, err
	}
	subPlusShip, err := subtotal.Add(listing.ShippingFee)
	if err != nil {
		return nil, err
	}
	total, err := subPlusShip.Add(tax)
	if err != nil {
		return nil, err
	}

	stockStatus := StockInStock
	availQty := req.Quantity + 12
	switch faults.ForceStockStatus {
	case string(StockOutOfStock):
		if !isSub {
			stockStatus = StockOutOfStock
			availQty = 0
		}
	case string(StockPartialStock):
		stockStatus = StockPartialStock
		availQty = faults.ForceAvailableQty
		if availQty <= 0 {
			availQty = req.Quantity - 1
			if availQty < 1 {
				availQty = 1
			}
		}
		if availQty >= req.Quantity {
			availQty = req.Quantity - 1
		}
	}

	quotedAt := req.Now.UTC()
	expiresAt := quotedAt.Add(req.TTL)
	q := &MerchantQuote{
		QuoteID:            newID("qt"),
		OccurrenceID:       req.OccurrenceID,
		MerchantID:         listing.MerchantID,
		MerchantName:       listing.MerchantName,
		RequestedSKU:       req.SKU,
		QuotedSKU:          item.SKU,
		Title:              item.Title,
		PackSizeValue:      packVal,
		PackSizeUnit:       item.PackSizeUnit,
		Quantity:           req.Quantity,
		AvailableQuantity:  availQty,
		StockStatus:        stockStatus,
		ItemSubtotal:       subtotal,
		ShippingFee:        listing.ShippingFee,
		TaxAmount:          tax,
		DeliveredTotal:     total,
		IsSubstitution:     isSub,
		PackSizeChanged:    packChanged,
		SubstitutionReason: subReason,
		QuotedAt:           quotedAt,
		ExpiresAt:          expiresAt,
	}
	q.Digest = computeQuoteDigest(q)
	return q, nil
}

func computeQuoteDigest(q *MerchantQuote) string {
	raw := fmt.Sprintf("%s|%s|%s|%d|%s|%d|%d|%s|%s|%s",
		q.MerchantID, q.QuotedSKU, q.RequestedSKU,
		q.PackSizeValue, q.PackSizeUnit, q.Quantity,
		q.DeliveredTotal.AmountMinor, q.DeliveredTotal.Asset,
		q.StockStatus, q.ExpiresAt.UTC().Format(time.RFC3339))
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

// CheckoutSubmitRequest is sent to the checkout/payment adapter outside any DB lock.
type CheckoutSubmitRequest struct {
	IdempotencyKey  string
	OccurrenceID    string
	ProposalID      string
	MerchantID      string
	SKU             string
	Quantity        int
	DeliveredTotal  Money
	PaymentMethodID string
	ConsentID       string
}

// CheckoutOutcome represents the authoritative checkout/payment result from the provider.
type CheckoutOutcome struct {
	Provider          string
	MerchantOrderID   string
	PaymentIntentID   string
	PaymentStatus     PaymentStatus
	FulfillmentStatus FulfillmentStatus
	ErrorMessage      string
}

// CancelOrderOutcome reports the merchant's response to canceling an already-submitted order.
type CancelOrderOutcome struct {
	Canceled          bool
	PaymentStatus     PaymentStatus
	FulfillmentStatus FulfillmentStatus
	Message           string
}

// CheckoutAdapter abstracts direct sandbox checkout or sibling Agent Checkout HTTP handoff.
type CheckoutAdapter interface {
	SubmitCheckout(ctx context.Context, req CheckoutSubmitRequest, faults SandboxFaultConfig) (*CheckoutOutcome, error)
	ReconcileCheckout(ctx context.Context, idempotencyKey string) (*CheckoutOutcome, error)
	CancelOrder(ctx context.Context, merchantOrderID string) (*CancelOrderOutcome, error)
}

type providerRecord struct {
	outcome CheckoutOutcome
	calls   int
}

// SandboxCheckoutAdapter provides a deterministic, idempotent checkout & payment fixture
// supporting timeout-unknown outcomes, declines, delayed fulfillment, and order cancellation.
type SandboxCheckoutAdapter struct {
	mu      sync.Mutex
	byIdem  map[string]*providerRecord
	byOrder map[string]*providerRecord
	// SubmitCount records how many distinct SubmitCheckout calls hit the provider (for race tests).
	SubmitCount int
}

func NewSandboxCheckoutAdapter() *SandboxCheckoutAdapter {
	return &SandboxCheckoutAdapter{
		byIdem:  map[string]*providerRecord{},
		byOrder: map[string]*providerRecord{},
	}
}

func (a *SandboxCheckoutAdapter) SubmitCheckout(ctx context.Context, req CheckoutSubmitRequest, faults SandboxFaultConfig) (*CheckoutOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.SubmitCount++

	if existing, ok := a.byIdem[req.IdempotencyKey]; ok {
		existing.calls++
		cp := existing.outcome
		return &cp, nil
	}

	switch faults.ForcePaymentBehavior {
	case "timeout_settled":
		// Payment actually settled on the provider, but the network timed out before returning!
		orderID := newID("ord")
		rec := &providerRecord{
			calls: 1,
			outcome: CheckoutOutcome{
				Provider:          "sandbox_direct",
				MerchantOrderID:   orderID,
				PaymentIntentID:   newID("pi"),
				PaymentStatus:     PaymentSettled,
				FulfillmentStatus: FulfillmentConfirmed,
			},
		}
		a.byIdem[req.IdempotencyKey] = rec
		a.byOrder[orderID] = rec
		return nil, ErrProviderTimeout

	case "timeout_failed":
		// Network timed out and the provider did not settle the charge.
		rec := &providerRecord{
			calls: 1,
			outcome: CheckoutOutcome{
				Provider:          "sandbox_direct",
				PaymentIntentID:   newID("pi"),
				PaymentStatus:     PaymentFailed,
				FulfillmentStatus: FulfillmentUnfulfilled,
				ErrorMessage:      "Provider gateway timed out before authorization.",
			},
		}
		a.byIdem[req.IdempotencyKey] = rec
		return nil, ErrProviderTimeout

	case "decline":
		rec := &providerRecord{
			calls: 1,
			outcome: CheckoutOutcome{
				Provider:          "sandbox_direct",
				PaymentIntentID:   newID("pi"),
				PaymentStatus:     PaymentFailed,
				FulfillmentStatus: FulfillmentUnfulfilled,
				ErrorMessage:      "Payment instrument declined by issuer.",
			},
		}
		a.byIdem[req.IdempotencyKey] = rec
		cp := rec.outcome
		return &cp, nil

	case "delayed_fulfillment":
		orderID := newID("ord")
		rec := &providerRecord{
			calls: 1,
			outcome: CheckoutOutcome{
				Provider:          "sandbox_direct",
				MerchantOrderID:   orderID,
				PaymentIntentID:   newID("pi"),
				PaymentStatus:     PaymentSettled,
				FulfillmentStatus: FulfillmentDelayed,
			},
		}
		a.byIdem[req.IdempotencyKey] = rec
		a.byOrder[orderID] = rec
		cp := rec.outcome
		return &cp, nil

	default:
		orderID := newID("ord")
		rec := &providerRecord{
			calls: 1,
			outcome: CheckoutOutcome{
				Provider:          "sandbox_direct",
				MerchantOrderID:   orderID,
				PaymentIntentID:   newID("pi"),
				PaymentStatus:     PaymentSettled,
				FulfillmentStatus: FulfillmentConfirmed,
			},
		}
		a.byIdem[req.IdempotencyKey] = rec
		a.byOrder[orderID] = rec
		cp := rec.outcome
		return &cp, nil
	}
}

func (a *SandboxCheckoutAdapter) ReconcileCheckout(ctx context.Context, idempotencyKey string) (*CheckoutOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.byIdem[idempotencyKey]
	if !ok {
		return &CheckoutOutcome{
			Provider:          "sandbox_direct",
			PaymentStatus:     PaymentFailed,
			FulfillmentStatus: FulfillmentUnfulfilled,
			ErrorMessage:      "No payment record found at provider for idempotency key.",
		}, nil
	}
	cp := rec.outcome
	return &cp, nil
}

func (a *SandboxCheckoutAdapter) CancelOrder(ctx context.Context, merchantOrderID string) (*CancelOrderOutcome, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rec, ok := a.byOrder[merchantOrderID]
	if !ok {
		return &CancelOrderOutcome{
			Canceled:          true,
			PaymentStatus:     PaymentRefunded,
			FulfillmentStatus: FulfillmentCanceled,
			Message:           "Merchant canceled order and initiated full refund.",
		}, nil
	}
	if rec.outcome.FulfillmentStatus == FulfillmentShipped || rec.outcome.FulfillmentStatus == FulfillmentDelivered {
		return &CancelOrderOutcome{
			Canceled:          false,
			PaymentStatus:     rec.outcome.PaymentStatus,
			FulfillmentStatus: rec.outcome.FulfillmentStatus,
			Message:           "Order has already shipped or been delivered; merchant cannot cancel.",
		}, nil
	}
	rec.outcome.FulfillmentStatus = FulfillmentCanceled
	rec.outcome.PaymentStatus = PaymentRefunded
	return &CancelOrderOutcome{
		Canceled:          true,
		PaymentStatus:     PaymentRefunded,
		FulfillmentStatus: FulfillmentCanceled,
		Message:           "Merchant confirmed cancellation and refunded payment.",
	}, nil
}

// UniqueOrderCount returns how many distinct merchant orders were created (for tests).
func (a *SandboxCheckoutAdapter) UniqueOrderCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.byOrder)
}

// AgentCheckoutHTTPAdapter optionally integrates with the sibling Agent Checkout service
// via its HTTP contract when AGENT_CHECKOUT_URL is configured.
type AgentCheckoutHTTPAdapter struct {
	BaseURL string
	Client  *http.Client
}

func NewAgentCheckoutHTTPAdapter(baseURL string) *AgentCheckoutHTTPAdapter {
	return &AgentCheckoutHTTPAdapter{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 8 * time.Second},
	}
}

func (a *AgentCheckoutHTTPAdapter) SubmitCheckout(ctx context.Context, req CheckoutSubmitRequest, _ SandboxFaultConfig) (*CheckoutOutcome, error) {
	payload, _ := json.Marshal(map[string]any{
		"idempotency_key":   req.IdempotencyKey,
		"occurrence_id":     req.OccurrenceID,
		"proposal_id":       req.ProposalID,
		"merchant_id":       req.MerchantID,
		"sku":               req.SKU,
		"quantity":          req.Quantity,
		"delivered_total":   req.DeliveredTotal,
		"payment_method_id": req.PaymentMethodID,
		"consent_id":        req.ConsentID,
	})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/v1/recurring-handoff", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	resp, err := a.Client.Do(httpReq)
	if err != nil {
		return nil, ErrProviderTimeout
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, ErrProviderTimeout
	}
	var out CheckoutOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, ErrProviderTimeout
	}
	out.Provider = "agent_checkout_http"
	return &out, nil
}

func (a *AgentCheckoutHTTPAdapter) ReconcileCheckout(ctx context.Context, idempotencyKey string) (*CheckoutOutcome, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.BaseURL+"/v1/recurring-handoff/"+idempotencyKey, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Client.Do(httpReq)
	if err != nil {
		return nil, ErrProviderTimeout
	}
	defer resp.Body.Close()
	var out CheckoutOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, ErrProviderTimeout
	}
	out.Provider = "agent_checkout_http"
	return &out, nil
}

func (a *AgentCheckoutHTTPAdapter) CancelOrder(ctx context.Context, merchantOrderID string) (*CancelOrderOutcome, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/v1/orders/"+merchantOrderID+"/cancel", nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.Client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out CancelOrderOutcome
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OptimizerRecommendation is an optional response from the sibling Rust Payment Optimizer.
type OptimizerRecommendation struct {
	RecommendedRail string `json:"recommended_rail"`
	EstimatedSaving Money  `json:"estimated_saving"`
	Reason          string `json:"reason"`
	Source          string `json:"source"`
}

// PaymentOptimizerClient optionally calls the sibling Card-vs-Stablecoin Optimizer service.
type PaymentOptimizerClient struct {
	BaseURL string
	Client  *http.Client
}

func NewPaymentOptimizerClient(baseURL string) *PaymentOptimizerClient {
	if baseURL == "" {
		return nil
	}
	return &PaymentOptimizerClient{
		BaseURL: baseURL,
		Client:  &http.Client{Timeout: 3 * time.Second},
	}
}
