package service

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

// RefundDetails describes a refund as observed.
type RefundDetails struct {
	Amount contract.Amount `json:"amount"`
	// ConversionCost is what the customer lost converting the refund back,
	// when the provider reports it. Absent means unknown, never zero.
	ConversionCost *contract.Amount `json:"conversion_cost"`
	InitiatedAt    *time.Time       `json:"initiated_at"`
	CompletedAt    *time.Time       `json:"completed_at"`
}

// OutcomeInput is an observation reported by a provider, the execution
// service, or (in shadow mode) the customer.
type OutcomeInput struct {
	EvaluationID string  `json:"evaluation_id"`
	HandoffID    *string `json:"handoff_id"`
	// Source is provider, executor, or customer_report.
	Source string `json:"source"`
	// EventID is unique per source; a repeated event is recorded once.
	EventID string `json:"event_id"`
	// Type is payment or refund.
	Type string `json:"type"`
	// Status is succeeded, failed, or unknown.
	Status     string    `json:"status"`
	OptionID   string    `json:"option_id"`
	OccurredAt time.Time `json:"occurred_at"`
	// Charged is the amount charged to the payment method. Required for a
	// succeeded payment.
	Charged *contract.Amount `json:"charged"`
	Refund  *RefundDetails   `json:"refund"`
}

// Savings bases. Only the first is a realized saving.
const (
	BasisRealized           = "realized_vs_estimated_baseline"
	BasisRecommendationOnly = "recommendation_only"
	BasisNotSucceeded       = "payment_not_succeeded"
	BasisNoBaseline         = "no_baseline"
	BasisBaselineUsed       = "baseline_method_used"
	BasisChargeDiffers      = "charge_differs_from_estimate"
	BasisMethodNotEvaluated = "method_not_in_evaluation"
)

// Counterfactual names the baseline a saving is measured against. The baseline
// payment never happened, so its cost is always the estimate made at
// evaluation time.
type Counterfactual struct {
	BaselineOptionID              string `json:"baseline_option_id"`
	BaselineImmediateCostMicroUSD string `json:"baseline_immediate_cost_micro_usd"`
	Basis                         string `json:"basis"`
	Observed                      bool   `json:"observed"`
}

// PaymentDerived is what is concluded from a payment outcome.
type PaymentDerived struct {
	MatchesRecommendation bool `json:"matches_recommendation"`
	// ChargeDifferenceBaseUnits is charged minus the estimated amount due, in
	// the payment asset. Null when the two are not in the same asset.
	ChargeDifferenceBaseUnits *string          `json:"charge_difference_base_units"`
	EstimatedAmountDue        *contract.Amount `json:"estimated_amount_due"`
	// RealizedImmediateSavingsMicroUSD is set only for a settled, executed
	// payment whose charge equals the estimate. It excludes rewards.
	RealizedImmediateSavingsMicroUSD *string `json:"realized_immediate_savings_micro_usd"`
	SavingsBasis                     string  `json:"savings_basis"`
	// EstimatedRewardsMicroUSD is the estimate of future reward value for the
	// method used. It is reported separately and is not a realized saving.
	EstimatedRewardsMicroUSD *string         `json:"estimated_rewards_micro_usd"`
	Counterfactual           *Counterfactual `json:"counterfactual"`
}

// RefundDerived is what is concluded from a refund outcome.
type RefundDerived struct {
	RefundAssetMatchesCharge bool `json:"refund_asset_matches_charge"`
	// RefundedTotalBaseUnits sums settled refunds in the charged asset.
	RefundedTotalBaseUnits *string `json:"refunded_total_base_units"`
	ChargedBaseUnits       *string `json:"charged_base_units"`
	FullyRefunded          bool    `json:"fully_refunded"`
	ConversionCostKnown    bool    `json:"conversion_cost_known"`
	// SecondsToComplete is completed_at minus initiated_at when both are known.
	SecondsToComplete *int64 `json:"seconds_to_complete"`
}

// OutcomeView is the API representation of a recorded outcome.
type OutcomeView struct {
	ID           string          `json:"id"`
	EvaluationID string          `json:"evaluation_id"`
	HandoffID    *string         `json:"handoff_id"`
	Source       string          `json:"source"`
	EventID      string          `json:"event_id"`
	Type         string          `json:"type"`
	Status       string          `json:"status"`
	OptionID     string          `json:"option_id"`
	OccurredAt   time.Time       `json:"occurred_at"`
	RecordedAt   time.Time       `json:"recorded_at"`
	Derived      json.RawMessage `json:"derived"`
}

func outcomeView(o *store.Outcome) *OutcomeView {
	return &OutcomeView{
		ID: o.ID, EvaluationID: o.EvaluationID, HandoffID: o.HandoffID, Source: o.Source, EventID: o.EventID,
		Type: o.Type, Status: o.Status, OptionID: o.OptionID, OccurredAt: o.OccurredAt, RecordedAt: o.RecordedAt,
		Derived: o.Derived,
	}
}

func oneOf(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

func (in *OutcomeInput) validate() *Error {
	if !validIdentifier(in.EvaluationID) || !validIdentifier(in.EventID) || !validIdentifier(in.OptionID) {
		return invalid("evaluation_id, event_id and option_id must be valid identifiers")
	}
	if in.HandoffID != nil && !validIdentifier(*in.HandoffID) {
		return invalid("handoff_id is not a valid identifier")
	}
	if !oneOf(in.Source, "provider", "executor", "customer_report") {
		return invalid("source must be provider, executor or customer_report")
	}
	if !oneOf(in.Type, "payment", "refund") || !oneOf(in.Status, "succeeded", "failed", "unknown") {
		return invalid("type must be payment or refund and status must be succeeded, failed or unknown")
	}
	if in.OccurredAt.IsZero() {
		return invalid("occurred_at is required")
	}
	if in.Source == "customer_report" && in.HandoffID != nil {
		return invalid("a customer report cannot resolve an execution handoff")
	}
	if in.Source != "customer_report" && in.HandoffID == nil {
		return invalid("provider and executor outcomes must reference a handoff")
	}
	switch in.Type {
	case "payment":
		if in.Refund != nil {
			return invalid("a payment outcome must not carry refund details")
		}
		if in.Status == "succeeded" && in.Charged == nil && in.HandoffID != nil {
			return invalid("a succeeded payment must report the amount charged")
		}
	case "refund":
		if in.Refund == nil || in.HandoffID == nil || in.Charged != nil {
			return invalid("a refund outcome must reference a handoff and carry refund details only")
		}
	}
	for _, a := range []*contract.Amount{in.Charged, refundAmount(in.Refund), refundCost(in.Refund)} {
		if a == nil {
			continue
		}
		if _, err := contract.ParseUnits(string(a.BaseUnits)); err != nil || a.Asset.Code == "" || a.Asset.Decimals > 18 {
			return invalid("amounts must be canonical decimal strings with an explicit asset")
		}
	}
	return nil
}

func refundAmount(r *RefundDetails) *contract.Amount {
	if r == nil {
		return nil
	}
	return &r.Amount
}

func refundCost(r *RefundDetails) *contract.Amount {
	if r == nil {
		return nil
	}
	return r.ConversionCost
}

// RecordOutcome stores an observation exactly once and updates the handoff it
// resolves. The bool reports a replay of an already recorded event.
func (s *Service) RecordOutcome(ctx context.Context, p *store.Principal, in OutcomeInput) (*OutcomeView, bool, error) {
	if verr := in.validate(); verr != nil {
		return nil, false, verr
	}
	ev, err := s.store.GetEvaluation(ctx, p.TenantID, in.EvaluationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, notFound("evaluation")
	}
	if err != nil {
		return nil, false, err
	}
	var result contract.Result
	if err := json.Unmarshal(ev.EngineResult, &result); err != nil {
		return nil, false, err
	}
	content, err := json.Marshal(in)
	if err != nil {
		return nil, false, err
	}
	now := s.now()
	record := &store.Outcome{
		ID: ids.New("out"), TenantID: p.TenantID, EvaluationID: ev.ID, HandoffID: in.HandoffID,
		Source: in.Source, EventID: in.EventID, Type: in.Type, Status: in.Status, OptionID: in.OptionID,
		Content: content, PayloadFingerprint: ids.Fingerprint(content), OccurredAt: in.OccurredAt, RecordedAt: now,
	}

	stored, replayed, err := s.store.RecordOutcome(ctx, record, func(h *store.Handoff, prior []store.Outcome) (*store.OutcomeDecision, error) {
		if h != nil && h.EvaluationID != ev.ID {
			return nil, conflict("handoff_mismatch", "the handoff belongs to a different evaluation")
		}
		if in.Type == "refund" {
			return decideRefund(&in, h, prior)
		}
		return decidePayment(&in, &result, h)
	})
	var serviceErr *Error
	switch {
	case errors.As(err, &serviceErr):
		return nil, false, serviceErr
	case errors.Is(err, store.ErrNotFound):
		return nil, false, notFound("handoff")
	case errors.Is(err, store.ErrPayloadMismatch):
		return nil, false, conflict("event_id_reused", "this event was already recorded with different contents")
	case err != nil:
		return nil, false, err
	}
	return outcomeView(stored), replayed, nil
}

func decidePayment(in *OutcomeInput, result *contract.Result, h *store.Handoff) (*store.OutcomeDecision, error) {
	decision := &store.OutcomeDecision{}
	if h != nil {
		// The executor must pay with the approved method; anything else is not
		// an outcome of this handoff.
		if in.OptionID != h.OptionID {
			return nil, conflict("method_mismatch", "the outcome names a different payment method than the approved handoff")
		}
		switch h.Status {
		case store.HandoffCreated, store.HandoffUnknown:
			decision.NewHandoffStatus = in.Status
		case store.HandoffSucceeded, store.HandoffFailed:
			if in.Status != h.Status {
				return nil, conflict("conflicting_outcome", "the handoff is already "+h.Status+"; a contradicting outcome needs manual reconciliation")
			}
		}
	}

	derived := PaymentDerived{
		MatchesRecommendation: result.PreferredOptionID != nil && *result.PreferredOptionID == in.OptionID,
	}
	used := result.Option(in.OptionID)
	if used != nil && used.Breakdown != nil {
		due := used.Breakdown.AmountDue
		derived.EstimatedAmountDue = &due
		rewards := used.Breakdown.EstimatedRewardsMicroUSD
		derived.EstimatedRewardsMicroUSD = &rewards
		if in.Charged != nil && in.Charged.Asset.Equal(due.Asset) {
			diff := new(big.Int).Sub(in.Charged.BaseUnits.Big(), due.BaseUnits.Big()).String()
			derived.ChargeDifferenceBaseUnits = &diff
		}
	}

	var baseline *contract.OptionResult
	if result.BaselineOptionID != nil {
		baseline = result.Option(*result.BaselineOptionID)
	}
	switch {
	case h == nil:
		// No payment was executed through the optimizer, so nothing was realized.
		derived.SavingsBasis = BasisRecommendationOnly
	case in.Status != "succeeded":
		derived.SavingsBasis = BasisNotSucceeded
	case used == nil || used.Breakdown == nil:
		derived.SavingsBasis = BasisMethodNotEvaluated
	case baseline == nil || baseline.Breakdown == nil:
		derived.SavingsBasis = BasisNoBaseline
	case baseline.OptionID == used.OptionID:
		derived.SavingsBasis = BasisBaselineUsed
	case derived.ChargeDifferenceBaseUnits == nil || *derived.ChargeDifferenceBaseUnits != "0":
		// The engine's immediate cost no longer describes what was paid.
		derived.SavingsBasis = BasisChargeDiffers
	default:
		baseCost, ok1 := new(big.Int).SetString(baseline.Breakdown.ImmediateCostMicroUSD, 10)
		usedCost, ok2 := new(big.Int).SetString(used.Breakdown.ImmediateCostMicroUSD, 10)
		if !ok1 || !ok2 {
			return nil, errors.New("service: stored evaluation has unreadable costs")
		}
		saving := new(big.Int).Sub(baseCost, usedCost).String()
		derived.RealizedImmediateSavingsMicroUSD = &saving
		derived.SavingsBasis = BasisRealized
	}
	if baseline != nil && baseline.Breakdown != nil {
		derived.Counterfactual = &Counterfactual{
			BaselineOptionID:              baseline.OptionID,
			BaselineImmediateCostMicroUSD: baseline.Breakdown.ImmediateCostMicroUSD,
			Basis:                         "estimate_at_evaluation",
			Observed:                      false,
		}
	}

	data, err := json.Marshal(derived)
	if err != nil {
		return nil, err
	}
	decision.Derived = data
	return decision, nil
}

func decideRefund(in *OutcomeInput, h *store.Handoff, prior []store.Outcome) (*store.OutcomeDecision, error) {
	if h == nil || h.Status != store.HandoffSucceeded {
		return nil, conflict("no_settled_payment", "a refund can only follow a settled payment")
	}
	if in.OptionID != h.OptionID {
		return nil, conflict("method_mismatch", "the refund names a different payment method than the handoff")
	}

	// The settled charge and earlier refunds come from recorded outcomes.
	var charged *contract.Amount
	contents := make([]OutcomeInput, len(prior))
	for i, o := range prior {
		if err := json.Unmarshal(o.Content, &contents[i]); err != nil {
			return nil, err
		}
		if o.Type == "payment" && o.Status == "succeeded" && contents[i].Charged != nil && charged == nil {
			charged = contents[i].Charged
		}
	}
	refunded := new(big.Int)
	for i, o := range prior {
		content := contents[i]
		if o.Type == "refund" && o.Status == "succeeded" && content.Refund != nil &&
			charged != nil && content.Refund.Amount.Asset.Equal(charged.Asset) {
			refunded.Add(refunded, content.Refund.Amount.BaseUnits.Big())
		}
	}

	derived := RefundDerived{ConversionCostKnown: in.Refund.ConversionCost != nil}
	if charged != nil {
		chargedUnits := string(charged.BaseUnits)
		derived.ChargedBaseUnits = &chargedUnits
		derived.RefundAssetMatchesCharge = in.Refund.Amount.Asset.Equal(charged.Asset)
		if derived.RefundAssetMatchesCharge {
			if in.Status == "succeeded" {
				refunded.Add(refunded, in.Refund.Amount.BaseUnits.Big())
			}
			if refunded.Cmp(charged.BaseUnits.Big()) > 0 {
				return nil, &Error{Status: http.StatusConflict, Code: "refund_exceeds_charge",
					Message: "settled refunds would exceed the amount charged"}
			}
			total := refunded.String()
			derived.RefundedTotalBaseUnits = &total
			derived.FullyRefunded = refunded.Cmp(charged.BaseUnits.Big()) == 0
		}
	}
	if in.Refund.InitiatedAt != nil && in.Refund.CompletedAt != nil {
		seconds := int64(in.Refund.CompletedAt.Sub(*in.Refund.InitiatedAt).Seconds())
		derived.SecondsToComplete = &seconds
	}

	data, err := json.Marshal(derived)
	if err != nil {
		return nil, err
	}
	return &store.OutcomeDecision{Derived: data}, nil
}
