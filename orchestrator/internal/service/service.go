// Package service orchestrates evaluations: it gathers quotes and balances,
// applies the customer's preferences, asks the engine for the result, and
// persists immutable snapshots. It never computes a cost itself.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/engine"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/quotes"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

// Modes. In shadow mode recommendations are recorded but nothing can be
// executed; in assisted mode a customer-approved choice can be handed off.
const (
	ModeShadow   = "shadow"
	ModeAssisted = "assisted"
)

// DisclosureRecommendationOnly accompanies every evaluation: producing one
// charges nobody and realizes no saving.
const DisclosureRecommendationOnly = "recommendation_only"

// Error is a failure with a stable code and an HTTP status.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func invalid(message string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: message}
}

func conflict(code, message string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: message}
}

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:\-]{0,127}$`)

func validIdentifier(s string) bool { return identifierPattern.MatchString(s) }

// Service is the application core.
type Service struct {
	store    *store.Store
	engine   engine.Evaluator
	quotes   quotes.Provider
	balances quotes.Balances
	policy   contract.Policy
	mode     string
	now      func() time.Time
}

// Options configures a Service.
type Options struct {
	Store    *store.Store
	Engine   engine.Evaluator
	Quotes   quotes.Provider
	Balances quotes.Balances
	Policy   contract.Policy
	Mode     string
	Now      func() time.Time
}

// New validates the options and returns a Service.
func New(o Options) (*Service, error) {
	if o.Mode != ModeShadow && o.Mode != ModeAssisted {
		return nil, fmt.Errorf("service: mode must be %q or %q", ModeShadow, ModeAssisted)
	}
	if o.Store == nil || o.Engine == nil || o.Quotes == nil || o.Balances == nil {
		return nil, errors.New("service: store, engine, quotes and balances are required")
	}
	if o.Policy.PolicyVersion == "" {
		return nil, errors.New("service: a versioned policy is required")
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: o.Store, engine: o.Engine, quotes: o.Quotes, balances: o.Balances, policy: o.Policy, mode: o.Mode, now: o.Now}, nil
}

// Mode returns the configured mode.
func (s *Service) Mode() string { return s.mode }

func (s *Service) ensureNotPaused(ctx context.Context, tenantID string) error {
	paused, reason, err := s.store.Paused(ctx, tenantID)
	if err != nil {
		return err
	}
	if paused {
		return &Error{Status: http.StatusServiceUnavailable, Code: "paused", Message: "the optimizer is paused: " + reason}
	}
	return nil
}

// Catalog lists the baskets the quote provider can quote.
func (s *Service) Catalog(ctx context.Context) ([]quotes.BasketInfo, error) {
	return s.quotes.Catalog(ctx)
}

// ---------------------------------------------------------------------------
// Evaluations
// ---------------------------------------------------------------------------

// EvaluateInput asks for an evaluation of one basket for one customer.
type EvaluateInput struct {
	CustomerID string `json:"customer_id"`
	MerchantID string `json:"merchant_id"`
	BasketRef  string `json:"basket_ref"`
	// Attribution is opaque experiment metadata stored with the evaluation.
	Attribution json.RawMessage `json:"attribution,omitempty"`
}

// Versions identifies everything that determined a result.
type Versions struct {
	Contract   string `json:"contract"`
	Engine     string `json:"engine"`
	Formula    string `json:"formula"`
	Rounding   string `json:"rounding"`
	Policy     string `json:"policy"`
	Preference string `json:"preference"`
}

// EvaluationView is the API representation of an evaluation.
type EvaluationView struct {
	ID                 string          `json:"id"`
	CustomerID         string          `json:"customer_id"`
	MerchantID         string          `json:"merchant_id"`
	Basket             json.RawMessage `json:"basket"`
	Mode               string          `json:"mode"`
	Status             string          `json:"status"`
	Disclosure         string          `json:"disclosure"`
	RefreshedFrom      *string         `json:"refreshed_from"`
	PreferencePolicyID *string         `json:"preference_policy_id"`
	InputSHA256        string          `json:"input_sha256"`
	Versions           Versions        `json:"versions"`
	Warnings           []string        `json:"warnings"`
	EvaluatedAt        time.Time       `json:"evaluated_at"`
	ExpiresAt          *time.Time      `json:"expires_at"`
	CreatedAt          time.Time       `json:"created_at"`
	Result             json.RawMessage `json:"result"`
}

func (s *Service) view(e *store.Evaluation) *EvaluationView {
	status := "current"
	if e.ExpiresAt == nil || !s.now().Before(*e.ExpiresAt) {
		status = "expired"
	}
	warnings := e.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return &EvaluationView{
		ID: e.ID, CustomerID: e.CustomerID, MerchantID: e.MerchantID, Basket: e.Basket, Mode: e.Mode,
		Status: status, Disclosure: DisclosureRecommendationOnly,
		RefreshedFrom: e.RefreshedFrom, PreferencePolicyID: e.PreferencePolicyID, InputSHA256: e.InputSHA256,
		Versions: Versions{
			Contract: e.ContractVersion, Engine: e.EngineVersion, Formula: e.FormulaVersion,
			Rounding: e.RoundingVersion, Policy: e.PolicyVersion, Preference: e.PreferenceVersion,
		},
		Warnings: warnings, EvaluatedAt: e.EvaluatedAt, ExpiresAt: e.ExpiresAt, CreatedAt: e.CreatedAt,
		Result: e.EngineResult,
	}
}

// Evaluate creates an evaluation. With an idempotency key, a repeated request
// returns the evaluation created the first time; the bool reports a replay.
func (s *Service) Evaluate(ctx context.Context, p *store.Principal, in EvaluateInput, idempotencyKey string) (*EvaluationView, bool, error) {
	if !validIdentifier(in.CustomerID) || !validIdentifier(in.MerchantID) || !validIdentifier(in.BasketRef) {
		return nil, false, invalid("customer_id, merchant_id and basket_ref must be valid identifiers")
	}
	if len(in.Attribution) > 2048 {
		return nil, false, invalid("attribution is too large")
	}
	if len(in.Attribution) > 0 {
		var object map[string]any
		if err := json.Unmarshal(in.Attribution, &object); err != nil {
			return nil, false, invalid("attribution must be a JSON object")
		}
	}
	return s.evaluate(ctx, p, in, idempotencyKey, nil, false)
}

// Refresh obtains fresh quotes for an existing evaluation's basket and returns
// a new evaluation linked to it. The original is never modified.
func (s *Service) Refresh(ctx context.Context, p *store.Principal, evaluationID, idempotencyKey string) (*EvaluationView, bool, error) {
	original, err := s.store.GetEvaluation(ctx, p.TenantID, evaluationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, notFound("evaluation")
	}
	if err != nil {
		return nil, false, err
	}
	in := EvaluateInput{CustomerID: original.CustomerID, MerchantID: original.MerchantID, BasketRef: original.BasketRef, Attribution: original.Attribution}
	return s.evaluate(ctx, p, in, idempotencyKey, &original.ID, true)
}

func notFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: what + " not found"}
}

func (s *Service) replayEvaluation(ctx context.Context, tenantID, key, fingerprint string) (*EvaluationView, error) {
	existing, err := s.store.GetEvaluationByKey(ctx, tenantID, key)
	if err != nil {
		return nil, err
	}
	if existing.RequestFingerprint != fingerprint {
		return nil, conflict("idempotency_key_reused", "this idempotency key was used with a different request")
	}
	return s.view(existing), nil
}

func (s *Service) evaluate(ctx context.Context, p *store.Principal, in EvaluateInput, idempotencyKey string, refreshedFrom *string, fresh bool) (*EvaluationView, bool, error) {
	if idempotencyKey != "" && !validIdentifier(idempotencyKey) {
		return nil, false, invalid("Idempotency-Key is not a valid identifier")
	}
	fingerprintSource, err := json.Marshal(struct {
		In            EvaluateInput
		RefreshedFrom *string
	}{in, refreshedFrom})
	if err != nil {
		return nil, false, err
	}
	fingerprint := ids.Fingerprint(fingerprintSource)

	if idempotencyKey != "" {
		view, err := s.replayEvaluation(ctx, p.TenantID, idempotencyKey, fingerprint)
		if err == nil {
			return view, true, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, false, err
		}
	}
	if err := s.ensureNotPaused(ctx, p.TenantID); err != nil {
		return nil, false, err
	}

	prefs, prefPolicy, err := s.loadPreferences(ctx, p.TenantID, in.CustomerID)
	if err != nil {
		return nil, false, err
	}

	// Quotes and balances are read outside any database transaction.
	now := s.now().Truncate(time.Millisecond)
	quoteReq := quotes.Request{TenantID: p.TenantID, CustomerID: in.CustomerID, MerchantID: in.MerchantID, BasketRef: in.BasketRef, Now: now, Fresh: fresh}
	quoted, err := s.quotes.Quote(ctx, quoteReq)
	switch {
	case errors.Is(err, quotes.ErrUnknownBasket):
		return nil, false, notFound("basket")
	case err != nil:
		return nil, false, &Error{Status: http.StatusServiceUnavailable, Code: "quotes_unavailable", Message: "quotes could not be obtained"}
	}
	if len(quoted.Options) == 0 {
		return nil, false, &Error{Status: http.StatusBadGateway, Code: "invalid_quote_data", Message: "the quote provider returned no options"}
	}
	options := quoted.Options
	quotes.Normalize(options)
	for i := range options {
		o := &options[i]
		// A balance is always read fresh; it never comes from a quote cache.
		funds, err := s.balances.Funds(ctx, quoteReq, *o)
		if err != nil {
			funds = contract.Funds{Status: "unknown"}
		}
		funds.Reserve = prefs.reserveFor(o.PaymentAsset)
		o.Funds = funds
		// Consent requires both the provider's record and the customer's preference.
		o.Availability.CustomerConsent = o.Availability.CustomerConsent && prefs.consents(o.Rail)
		if o.FundingStep != nil {
			o.FundingStep.CustomerConsent = o.FundingStep.CustomerConsent && prefs.ConsentFundingStep
		}
	}

	evaluationID := ids.New("eval")
	snapshotID := ids.New("bsk")
	warnings := []string{}
	req := &contract.Request{
		ContractVersion: contract.Version,
		EvaluatedAt:     now,
		Basket:          contract.Basket{BasketID: snapshotID, MerchantID: in.MerchantID, BasketHash: quoted.BasketHash},
		Policy:          s.policy,
		Preferences: contract.Preferences{
			PreferenceVersion:   defaultPreferenceVersion,
			MinSavingsThreshold: contract.Amount{Asset: contract.AssetRef{Code: "USD", Decimals: 2}, BaseUnits: prefs.MinSavingsThresholdMinor},
			HardConstraints: contract.HardConstraints{
				AllowedRails:              prefs.AllowedRails,
				RequirePurchaseProtection: prefs.RequirePurchaseProtection,
				AvoidCredit:               prefs.AvoidCredit,
			},
			PointValuations: prefs.PointValuations,
		},
		Options: options,
	}
	var prefPolicyID *string
	if prefPolicy != nil {
		prefPolicyID = &prefPolicy.ID
		req.Preferences.PreferenceVersion = fmt.Sprintf("%s:v%d", prefPolicy.ID, prefPolicy.Version)
	}
	if prefs.DefaultOptionID != nil {
		offered := false
		for _, o := range options {
			offered = offered || o.OptionID == *prefs.DefaultOptionID
		}
		if offered {
			req.Preferences.DefaultOptionID = prefs.DefaultOptionID
		} else {
			// Without the default there is no baseline; say so instead of guessing one.
			warnings = append(warnings, "default_method_not_offered")
		}
	}

	evaluated, err := s.engine.Evaluate(ctx, req)
	var rejected *engine.RejectedError
	switch {
	case errors.As(err, &rejected):
		return nil, false, &Error{Status: http.StatusBadGateway, Code: "invalid_quote_data",
			Message: fmt.Sprintf("the engine rejected the assembled request (%s at %s)", rejected.Code, rejected.Field)}
	case err != nil:
		return nil, false, &Error{Status: http.StatusServiceUnavailable, Code: "engine_unavailable", Message: "the calculation engine is unavailable"}
	}

	basket, err := json.Marshal(struct {
		BasketID   string `json:"basket_id"`
		BasketHash string `json:"basket_hash"`
		Provider   string `json:"provider"`
		quotes.BasketInfo
	}{snapshotID, quoted.BasketHash, quoted.Provider, quoted.Info})
	if err != nil {
		return nil, false, err
	}

	record := &store.Evaluation{
		ID: evaluationID, TenantID: p.TenantID, CustomerID: in.CustomerID, MerchantID: in.MerchantID,
		BasketSnapshotID: snapshotID, BasketRef: in.BasketRef, BasketHash: quoted.BasketHash, Basket: basket,
		Mode: s.mode, PreferencePolicyID: prefPolicyID, RefreshedFrom: refreshedFrom,
		RequestFingerprint: fingerprint,
		EngineRequest:      string(evaluated.RequestJSON), InputSHA256: evaluated.InputSHA256, EngineResult: evaluated.ResultJSON,
		ContractVersion: evaluated.Result.ContractVersion, EngineVersion: evaluated.Result.EngineVersion,
		FormulaVersion: evaluated.Result.FormulaVersion, RoundingVersion: evaluated.Result.RoundingVersion,
		PolicyVersion: evaluated.Result.PolicyVersion, PreferenceVersion: evaluated.Result.PreferenceVersion,
		Outcome: evaluated.Result.Outcome, PreferredOptionID: evaluated.Result.PreferredOptionID,
		BaselineOptionID: evaluated.Result.BaselineOptionID, Warnings: warnings, Attribution: in.Attribution,
		EvaluatedAt: now, ExpiresAt: evaluated.Result.ExpiresAt, CreatedAt: s.now(),
	}
	if idempotencyKey != "" {
		record.IdempotencyKey = &idempotencyKey
	}

	var quoteRows []store.RailQuoteRow
	var ruleRows []store.RewardRuleRow
	for _, o := range options {
		content, err := json.Marshal(o)
		if err != nil {
			return nil, false, err
		}
		quoteRows = append(quoteRows, store.RailQuoteRow{
			OptionID: o.OptionID, Rail: o.Rail, Provider: o.Quote.Provider, QuoteID: o.Quote.QuoteID,
			ObtainedAt: o.Quote.ObtainedAt, ValidUntil: o.Quote.ValidUntil, Content: content,
		})
		for _, rule := range o.Rewards {
			// The cap's consumed amount changes over time; the rule itself must not.
			definition, err := json.Marshal(struct {
				RuleID      string          `json:"rule_id"`
				RuleVersion string          `json:"rule_version"`
				Earn        json.RawMessage `json:"earn"`
				CapLimit    *contract.Units `json:"cap_limit"`
				Description string          `json:"description"`
			}{rule.RuleID, rule.RuleVersion, canonicalJSON(rule.Earn), capLimit(rule.Cap), rule.Description})
			if err != nil {
				return nil, false, err
			}
			ruleRows = append(ruleRows, store.RewardRuleRow{RuleID: rule.RuleID, RuleVersion: rule.RuleVersion, Content: definition})
		}
	}

	err = s.store.InsertEvaluation(ctx, record, quoteRows, ruleRows)
	switch {
	case errors.Is(err, store.ErrDuplicateKey):
		// A concurrent request with the same key won; return its evaluation.
		view, err := s.replayEvaluation(ctx, p.TenantID, idempotencyKey, fingerprint)
		return view, err == nil, err
	case errors.Is(err, store.ErrRuleConflict):
		return nil, false, &Error{Status: http.StatusBadGateway, Code: "invalid_quote_data",
			Message: "a reward rule version arrived with different contents than previously recorded"}
	case err != nil:
		return nil, false, err
	}
	return s.view(record), false, nil
}

func capLimit(c *contract.RewardCap) *contract.Units {
	if c == nil {
		return nil
	}
	return &c.Limit
}

// canonicalJSON re-encodes a JSON value with sorted keys so equal values hash equally.
func canonicalJSON(raw json.RawMessage) json.RawMessage {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// GetEvaluation returns a stored evaluation.
func (s *Service) GetEvaluation(ctx context.Context, p *store.Principal, id string) (*EvaluationView, error) {
	e, err := s.store.GetEvaluation(ctx, p.TenantID, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("evaluation")
	}
	if err != nil {
		return nil, err
	}
	return s.view(e), nil
}

// Summary returns the measurement summary for the tenant.
func (s *Service) Summary(ctx context.Context, p *store.Principal, from, to time.Time) (*store.Summary, error) {
	if !to.After(from) {
		return nil, invalid("to must be after from")
	}
	return s.store.Summarize(ctx, p.TenantID, from, to)
}

// Sweep marks handoffs that expired with no reported outcome as unknown.
func (s *Service) Sweep(ctx context.Context, grace time.Duration) (int64, error) {
	return s.store.MarkExpiredHandoffsUnknown(ctx, s.now(), grace)
}
