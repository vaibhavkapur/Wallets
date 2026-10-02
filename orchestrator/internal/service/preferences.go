package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

// CustomerPreferences is what a customer controls. It is stored as an
// immutable version and applied to every evaluation for that customer.
type CustomerPreferences struct {
	// DefaultOptionID is the customer's stated default method and the baseline
	// against which savings are measured.
	DefaultOptionID *string `json:"default_option_id"`
	// MinSavingsThresholdMinor is the estimated saving, in USD cents, required
	// before recommending a method other than the default.
	MinSavingsThresholdMinor contract.Units `json:"min_savings_threshold_minor"`
	// ConsentedRails lists the rails the customer has agreed to pay with.
	ConsentedRails []string `json:"consented_rails"`
	// ConsentFundingStep allows an option that needs a separate, quoted
	// funding or conversion step to be considered.
	ConsentFundingStep bool `json:"consent_funding_step"`
	// AllowedRails, when not null, restricts recommendations to these rails.
	AllowedRails              []string `json:"allowed_rails"`
	RequirePurchaseProtection bool     `json:"require_purchase_protection"`
	AvoidCredit               bool     `json:"avoid_credit"`
	// LiquidityReserves are balances the customer wants left untouched.
	LiquidityReserves []contract.Amount         `json:"liquidity_reserves"`
	PointValuations   []contract.PointValuation `json:"point_valuations"`
}

// DefaultPreferences applies when a customer has stored nothing. It consents
// to card payments only: a stablecoin payment is never recommended to someone
// who has not opted in.
func DefaultPreferences() CustomerPreferences {
	return CustomerPreferences{
		MinSavingsThresholdMinor: "100",
		ConsentedRails:           []string{"card"},
		LiquidityReserves:        []contract.Amount{},
		PointValuations:          []contract.PointValuation{},
	}
}

const defaultPreferenceVersion = "default-v1"

// maxThresholdMinor bounds the threshold at USD 100,000.00.
const maxThresholdMinor = 10_000_000

func validRail(r string) bool { return r == "card" || r == "stablecoin" }

func (p *CustomerPreferences) validate() error {
	if _, err := contract.ParseUnits(string(p.MinSavingsThresholdMinor)); err != nil {
		return fmt.Errorf("min_savings_threshold_minor: %w", err)
	}
	if p.MinSavingsThresholdMinor.Big().Cmp(contract.Units(fmt.Sprint(maxThresholdMinor)).Big()) > 0 {
		return fmt.Errorf("min_savings_threshold_minor exceeds %d", maxThresholdMinor)
	}
	if p.DefaultOptionID != nil && !validIdentifier(*p.DefaultOptionID) {
		return fmt.Errorf("default_option_id is not a valid identifier")
	}
	for _, r := range p.ConsentedRails {
		if !validRail(r) {
			return fmt.Errorf("consented_rails: unknown rail %q", r)
		}
	}
	for _, r := range p.AllowedRails {
		if !validRail(r) {
			return fmt.Errorf("allowed_rails: unknown rail %q", r)
		}
	}
	if len(p.LiquidityReserves) > 8 || len(p.PointValuations) > 32 {
		return fmt.Errorf("too many entries")
	}
	for i, a := range p.LiquidityReserves {
		if _, err := contract.ParseUnits(string(a.BaseUnits)); err != nil || a.Asset.Code == "" || a.Asset.Decimals > 18 {
			return fmt.Errorf("liquidity_reserves[%d] is invalid", i)
		}
	}
	seen := map[string]bool{}
	for i, v := range p.PointValuations {
		if _, err := contract.ParseUnits(string(v.MicroUSDPerPoint)); err != nil {
			return fmt.Errorf("point_valuations[%d].micro_usd_per_point: %w", i, err)
		}
		if v.ProgramID == "" || seen[v.ProgramID] || v.ValuationVersion == "" {
			return fmt.Errorf("point_valuations[%d] needs a unique program_id and a valuation_version", i)
		}
		seen[v.ProgramID] = true
		if v.Stability != "stable" && v.Stability != "unstable" {
			return fmt.Errorf("point_valuations[%d].stability must be stable or unstable", i)
		}
	}
	return nil
}

// normalize makes the stored form canonical: no null arrays except
// allowed_rails, where null means "no restriction".
func (p *CustomerPreferences) normalize() {
	if p.ConsentedRails == nil {
		p.ConsentedRails = []string{}
	}
	if p.LiquidityReserves == nil {
		p.LiquidityReserves = []contract.Amount{}
	}
	if p.PointValuations == nil {
		p.PointValuations = []contract.PointValuation{}
	}
}

func (p *CustomerPreferences) consents(rail string) bool {
	for _, r := range p.ConsentedRails {
		if r == rail {
			return true
		}
	}
	return false
}

func (p *CustomerPreferences) reserveFor(asset contract.AssetRef) *contract.Amount {
	for i := range p.LiquidityReserves {
		if p.LiquidityReserves[i].Asset.Equal(asset) {
			reserve := p.LiquidityReserves[i]
			return &reserve
		}
	}
	return nil
}

// PreferencesView is the API representation of a customer's preferences.
type PreferencesView struct {
	CustomerID  string              `json:"customer_id"`
	PolicyID    *string             `json:"policy_id"`
	Version     int                 `json:"version"`
	IsDefault   bool                `json:"is_default"`
	Preferences CustomerPreferences `json:"preferences"`
}

// loadPreferences returns the customer's current preferences and the stored
// policy they came from (nil when the defaults apply).
func (s *Service) loadPreferences(ctx context.Context, tenantID, customerID string) (CustomerPreferences, *store.PreferencePolicy, error) {
	policy, err := s.store.LatestPreferences(ctx, tenantID, customerID)
	if err != nil {
		return CustomerPreferences{}, nil, err
	}
	if policy == nil {
		return DefaultPreferences(), nil, nil
	}
	var prefs CustomerPreferences
	if err := json.Unmarshal(policy.Content, &prefs); err != nil {
		return CustomerPreferences{}, nil, fmt.Errorf("stored preferences %s are unreadable: %w", policy.ID, err)
	}
	prefs.normalize()
	return prefs, policy, nil
}

// GetPreferences returns a customer's current preferences.
func (s *Service) GetPreferences(ctx context.Context, p *store.Principal, customerID string) (*PreferencesView, error) {
	if !validIdentifier(customerID) {
		return nil, invalid("customer_id is not a valid identifier")
	}
	prefs, policy, err := s.loadPreferences(ctx, p.TenantID, customerID)
	if err != nil {
		return nil, err
	}
	view := &PreferencesView{CustomerID: customerID, IsDefault: policy == nil, Preferences: prefs}
	if policy != nil {
		view.PolicyID, view.Version = &policy.ID, policy.Version
	}
	return view, nil
}

// PutPreferences stores a new immutable preference version.
func (s *Service) PutPreferences(ctx context.Context, p *store.Principal, customerID string, prefs CustomerPreferences) (*PreferencesView, error) {
	if !validIdentifier(customerID) {
		return nil, invalid("customer_id is not a valid identifier")
	}
	prefs.normalize()
	if err := prefs.validate(); err != nil {
		return nil, &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_preferences", Message: err.Error()}
	}
	content, err := json.Marshal(prefs)
	if err != nil {
		return nil, err
	}
	policy, err := s.store.PutPreferences(ctx, p.TenantID, customerID, content, s.now())
	if err != nil {
		return nil, err
	}
	return &PreferencesView{CustomerID: customerID, PolicyID: &policy.ID, Version: policy.Version, Preferences: prefs}, nil
}
