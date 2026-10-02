// Package contract mirrors the engine's evaluation contract (v1).
//
// The orchestrator assembles requests and reads a few result fields; it never
// recomputes a cost. Amounts stay canonical decimal strings end to end, so no
// value passes through a floating-point number.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Version is the contract version this package implements.
const Version = "1"

// MaxDigits is the longest accepted decimal string (i128::MAX has 39 digits).
const MaxDigits = 39

var maxUnits = func() *big.Int {
	v := new(big.Int).Lsh(big.NewInt(1), 127)
	return v.Sub(v, big.NewInt(1))
}()

// ErrInvalidUnits reports a non-canonical or out-of-range decimal string.
var ErrInvalidUnits = errors.New("contract: amount must be a canonical non-negative decimal string within range")

// Units is a non-negative integer carried as a canonical decimal string.
type Units string

// ParseUnits validates s exactly as the engine does: ASCII digits, no sign,
// no leading zeros, at most 39 digits, within i128.
func ParseUnits(s string) (Units, error) {
	if s == "" || len(s) > MaxDigits || (len(s) > 1 && s[0] == '0') {
		return "", ErrInvalidUnits
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return "", ErrInvalidUnits
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Cmp(maxUnits) > 0 {
		return "", ErrInvalidUnits
	}
	return Units(s), nil
}

// Big returns the value as a big integer.
func (u Units) Big() *big.Int {
	v, _ := new(big.Int).SetString(string(u), 10)
	if v == nil {
		return new(big.Int)
	}
	return v
}

func (u Units) MarshalJSON() ([]byte, error) {
	if _, err := ParseUnits(string(u)); err != nil {
		return nil, err
	}
	return json.Marshal(string(u))
}

// UnmarshalJSON accepts only JSON strings; a JSON number is an error.
func (u *Units) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return ErrInvalidUnits
	}
	parsed, err := ParseUnits(s)
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

// AssetRef names an asset with explicit network and decimals. Fiat has no network.
type AssetRef struct {
	Code     string  `json:"code"`
	Network  *string `json:"network,omitempty"`
	Decimals uint8   `json:"decimals"`
}

// Equal reports whether two references name the same asset.
func (a AssetRef) Equal(b AssetRef) bool {
	if a.Code != b.Code || a.Decimals != b.Decimals {
		return false
	}
	if a.Network == nil || b.Network == nil {
		return a.Network == nil && b.Network == nil
	}
	return *a.Network == *b.Network
}

// Amount is integer base units of an explicit asset.
type Amount struct {
	Asset     AssetRef `json:"asset"`
	BaseUnits Units    `json:"base_units"`
}

// Equal reports whether two amounts have the same asset and value.
func (a Amount) Equal(b Amount) bool {
	return a.Asset.Equal(b.Asset) && a.BaseUnits == b.BaseUnits
}

// Rate is the USD value of one whole unit of an asset, usd_num / usd_den.
type Rate struct {
	USDNum Units `json:"usd_num"`
	USDDen Units `json:"usd_den"`
}

// Request is the engine's evaluation request.
type Request struct {
	ContractVersion string          `json:"contract_version"`
	EvaluatedAt     time.Time       `json:"evaluated_at"`
	Basket          Basket          `json:"basket"`
	Policy          Policy          `json:"policy"`
	Preferences     Preferences     `json:"preferences"`
	Options         []PaymentOption `json:"options"`
}

type Basket struct {
	BasketID   string `json:"basket_id"`
	MerchantID string `json:"merchant_id"`
	BasketHash string `json:"basket_hash"`
}

// Policy is the platform policy recorded with every evaluation.
type Policy struct {
	PolicyVersion           string           `json:"policy_version"`
	ComparisonCurrency      string           `json:"comparison_currency"`
	SupportedStablecoins    []AssetRef       `json:"supported_stablecoins"`
	RequiredFeeKinds        RequiredFeeKinds `json:"required_fee_kinds"`
	PermittedFundingSources []string         `json:"permitted_funding_sources"`
}

type RequiredFeeKinds struct {
	Card            []string `json:"card"`
	Stablecoin      []string `json:"stablecoin"`
	WithFundingStep []string `json:"with_funding_step"`
}

type Preferences struct {
	PreferenceVersion   string           `json:"preference_version"`
	DefaultOptionID     *string          `json:"default_option_id"`
	MinSavingsThreshold Amount           `json:"min_savings_threshold"`
	HardConstraints     HardConstraints  `json:"hard_constraints"`
	PointValuations     []PointValuation `json:"point_valuations"`
}

type HardConstraints struct {
	AllowedRails              []string `json:"allowed_rails"`
	RequirePurchaseProtection bool     `json:"require_purchase_protection"`
	AvoidCredit               bool     `json:"avoid_credit"`
}

type PointValuation struct {
	ProgramID        string `json:"program_id"`
	ValuationVersion string `json:"valuation_version"`
	MicroUSDPerPoint Units  `json:"micro_usd_per_point"`
	Stability        string `json:"stability"`
}

type PaymentOption struct {
	OptionID     string       `json:"option_id"`
	Rail         string       `json:"rail"`
	Label        string       `json:"label"`
	PaymentAsset AssetRef     `json:"payment_asset"`
	Quote        RailQuote    `json:"quote"`
	Availability Availability `json:"availability"`
	Fees         []Fee        `json:"fees"`
	Discounts    []Discount   `json:"discounts"`
	Rewards      []RewardRule `json:"rewards"`
	Funds        Funds        `json:"funds"`
	FundingStep  *FundingStep `json:"funding_step"`
	Tradeoffs    Tradeoffs    `json:"tradeoffs"`
}

type RailQuote struct {
	QuoteID        string    `json:"quote_id"`
	Provider       string    `json:"provider"`
	BasketHash     string    `json:"basket_hash"`
	DeliveredPrice Amount    `json:"delivered_price"`
	AcceptedAsset  AssetRef  `json:"accepted_asset"`
	USDRate        *Rate     `json:"usd_rate"`
	ObtainedAt     time.Time `json:"obtained_at"`
	ValidUntil     time.Time `json:"valid_until"`
}

type Availability struct {
	MethodAvailable    bool   `json:"method_available"`
	Jurisdiction       string `json:"jurisdiction"`
	MerchantAcceptance string `json:"merchant_acceptance"`
	CustomerConsent    bool   `json:"customer_consent"`
}

type Fee struct {
	FeeID       string  `json:"fee_id"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	Amount      *Amount `json:"amount"`
	Description string  `json:"description"`
}

type Discount struct {
	DiscountID        string `json:"discount_id"`
	Amount            Amount `json:"amount"`
	IncludedInQuote   bool   `json:"included_in_quote"`
	ImmediatelyUsable bool   `json:"immediately_usable"`
	Eligibility       string `json:"eligibility"`
	Description       string `json:"description"`
}

type RewardRule struct {
	RuleID      string          `json:"rule_id"`
	RuleVersion string          `json:"rule_version"`
	Eligibility string          `json:"eligibility"`
	Earn        json.RawMessage `json:"earn"`
	Cap         *RewardCap      `json:"cap"`
	Description string          `json:"description"`
}

type RewardCap struct {
	Limit    Units `json:"limit"`
	Consumed Units `json:"consumed"`
}

type Funds struct {
	Status    string  `json:"status"`
	Available *Amount `json:"available"`
	Reserve   *Amount `json:"reserve"`
}

type FundingStep struct {
	Source          string    `json:"source"`
	Amount          Amount    `json:"amount"`
	QuoteID         string    `json:"quote_id"`
	ValidUntil      time.Time `json:"valid_until"`
	CustomerConsent bool      `json:"customer_consent"`
}

type Tradeoffs struct {
	PurchaseProtection string   `json:"purchase_protection"`
	UsesCredit         bool     `json:"uses_credit"`
	RefundAsset        string   `json:"refund_asset"`
	RefundTiming       string   `json:"refund_timing"`
	Settlement         string   `json:"settlement"`
	Notes              []string `json:"notes"`
}

// Result is the subset of the engine's response the orchestrator reads. The
// full response is stored and returned verbatim.
type Result struct {
	ContractVersion   string         `json:"contract_version"`
	EngineVersion     string         `json:"engine_version"`
	FormulaVersion    string         `json:"formula_version"`
	RoundingVersion   string         `json:"rounding_version"`
	PolicyVersion     string         `json:"policy_version"`
	PreferenceVersion string         `json:"preference_version"`
	Outcome           string         `json:"outcome"`
	Rationale         string         `json:"rationale"`
	PreferredOptionID *string        `json:"preferred_option_id"`
	BaselineOptionID  *string        `json:"baseline_option_id"`
	ExpiresAt         *time.Time     `json:"expires_at"`
	Options           []OptionResult `json:"options"`
}

type OptionResult struct {
	OptionID     string      `json:"option_id"`
	Rail         string      `json:"rail"`
	PaymentAsset AssetRef    `json:"payment_asset"`
	QuoteID      string      `json:"quote_id"`
	Status       string      `json:"status"`
	Exclusions   []Exclusion `json:"exclusions"`
	Breakdown    *Breakdown  `json:"breakdown"`
}

type Exclusion struct {
	Code string `json:"code"`
	Kind string `json:"kind"`
}

type Breakdown struct {
	ImmediateCostMicroUSD    string `json:"immediate_cost_micro_usd"`
	EstimatedRewardsMicroUSD string `json:"estimated_rewards_total_micro_usd"`
	EstimatedNetCostMicroUSD string `json:"estimated_net_cost_micro_usd"`
	AmountDue                Amount `json:"amount_due"`
}

// Option returns the result for an option id, or nil.
func (r *Result) Option(id string) *OptionResult {
	for i := range r.Options {
		if r.Options[i].OptionID == id {
			return &r.Options[i]
		}
	}
	return nil
}

// DecodeStrict decodes JSON and rejects unknown fields and trailing data.
func DecodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("contract: unexpected data after JSON value")
	}
	return nil
}
