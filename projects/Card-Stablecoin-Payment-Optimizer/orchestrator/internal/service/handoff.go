package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/contract"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/ids"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

// TopicHandoffCreated is the outbox topic for a new execution handoff.
const TopicHandoffCreated = "execution_handoff.created"

// HandoffContractVersion versions the payload handed to the execution service.
const HandoffContractVersion = "1"

// approvalSkew tolerates small clock differences between the approving client
// and this service.
const approvalSkew = 30 * time.Second

// Approval is the customer's approval of one specific payment. It is bound to
// the evaluation, the option and the exact amount.
type Approval struct {
	CustomerID     string          `json:"customer_id"`
	ApprovedAt     time.Time       `json:"approved_at"`
	ApprovedAmount contract.Amount `json:"approved_amount"`
	// Reference identifies the approval in the approving system.
	Reference string `json:"reference"`
}

// HandoffInput asks for a handoff of the customer's approved choice.
type HandoffInput struct {
	OptionID string   `json:"option_id"`
	Approval Approval `json:"approval"`
}

// HandoffView is the API and outbox representation of a handoff. The id is a
// reference for the execution service; it is not a payment credential.
type HandoffView struct {
	ID            string          `json:"id"`
	EvaluationID  string          `json:"evaluation_id"`
	CustomerID    string          `json:"customer_id"`
	MerchantID    string          `json:"merchant_id"`
	BasketHash    string          `json:"basket_hash"`
	OptionID      string          `json:"option_id"`
	Rail          string          `json:"rail"`
	QuoteID       string          `json:"quote_id"`
	Amount        json.RawMessage `json:"amount"`
	Approval      json.RawMessage `json:"approval"`
	Status        string          `json:"status"`
	Executable    bool            `json:"executable"`
	NotExecutable *string         `json:"not_executable_reason"`
	ExpiresAt     time.Time       `json:"expires_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (s *Service) handoffView(ctx context.Context, h *store.Handoff) (*HandoffView, error) {
	view := &HandoffView{
		ID: h.ID, EvaluationID: h.EvaluationID, CustomerID: h.CustomerID, MerchantID: h.MerchantID,
		BasketHash: h.BasketHash, OptionID: h.OptionID, Rail: h.Rail, QuoteID: h.QuoteID,
		Amount: h.Amount, Approval: h.Approval, Status: h.Status,
		ExpiresAt: h.ExpiresAt, CreatedAt: h.CreatedAt, UpdatedAt: h.UpdatedAt,
	}
	// Revalidated on every read: the executor must check this before paying.
	reason := ""
	switch {
	case h.Status != store.HandoffCreated:
		reason = "status_" + h.Status
	case !s.now().Before(h.ExpiresAt):
		reason = "expired"
	default:
		paused, _, err := s.store.Paused(ctx, h.TenantID)
		if err != nil {
			return nil, err
		}
		if paused {
			reason = "paused"
		}
	}
	if reason == "" {
		view.Executable = true
	} else {
		view.NotExecutable = &reason
	}
	return view, nil
}

// CreateHandoff creates a constrained reference for the customer's approved
// choice after revalidating everything the approval depends on. It never
// substitutes another payment method.
func (s *Service) CreateHandoff(ctx context.Context, p *store.Principal, evaluationID string, in HandoffInput, idempotencyKey string) (*HandoffView, bool, error) {
	if !validIdentifier(idempotencyKey) {
		return nil, false, invalid("a valid Idempotency-Key header is required")
	}
	if !validIdentifier(in.OptionID) {
		return nil, false, invalid("option_id is not a valid identifier")
	}
	if !validIdentifier(in.Approval.Reference) {
		return nil, false, invalid("approval.reference is required")
	}
	fingerprintSource, err := json.Marshal(struct {
		EvaluationID string
		In           HandoffInput
	}{evaluationID, in})
	if err != nil {
		return nil, false, invalid("approval is not encodable")
	}
	fingerprint := ids.Fingerprint(fingerprintSource)

	replay := func() (*HandoffView, bool, error) {
		existing, err := s.store.GetHandoffByKey(ctx, p.TenantID, idempotencyKey)
		if err != nil {
			return nil, false, err
		}
		if existing.RequestFingerprint != fingerprint {
			return nil, false, conflict("idempotency_key_reused", "this idempotency key was used with a different request")
		}
		view, err := s.handoffView(ctx, existing)
		return view, true, err
	}
	if view, replayed, err := replay(); err == nil || !errors.Is(err, store.ErrNotFound) {
		return view, replayed, err
	}

	if s.mode != ModeAssisted {
		return nil, false, conflict("shadow_mode", "the optimizer is in shadow mode; recommendations cannot be executed")
	}
	if err := s.ensureNotPaused(ctx, p.TenantID); err != nil {
		return nil, false, err
	}

	ev, err := s.store.GetEvaluation(ctx, p.TenantID, evaluationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, notFound("evaluation")
	}
	if err != nil {
		return nil, false, err
	}
	if ev.Mode != ModeAssisted {
		return nil, false, conflict("evaluation_not_executable", "this evaluation was produced in shadow mode; request a new evaluation")
	}
	now := s.now()
	if ev.ExpiresAt == nil || !now.Before(*ev.ExpiresAt) {
		return nil, false, conflict("evaluation_expired", "the quotes behind this evaluation have expired; refresh and ask the customer to approve again")
	}

	var result contract.Result
	if err := json.Unmarshal(ev.EngineResult, &result); err != nil {
		return nil, false, err
	}
	option := result.Option(in.OptionID)
	if option == nil {
		return nil, false, &Error{Status: http.StatusUnprocessableEntity, Code: "unknown_option", Message: "the option is not part of this evaluation"}
	}
	if option.Status != "eligible" || option.Breakdown == nil {
		return nil, false, conflict("option_not_eligible", "the option was excluded by this evaluation and cannot be executed")
	}

	// The approval must be for this customer, this evaluation's amount, and
	// given after the evaluation existed.
	if in.Approval.CustomerID != ev.CustomerID {
		return nil, false, conflict("approval_mismatch", "the approval is for a different customer")
	}
	if !in.Approval.ApprovedAmount.Equal(option.Breakdown.AmountDue) {
		return nil, false, conflict("approval_mismatch", "the approved amount or asset differs from the evaluated amount due")
	}
	if in.Approval.ApprovedAt.Before(ev.EvaluatedAt.Add(-approvalSkew)) || in.Approval.ApprovedAt.After(now.Add(approvalSkew)) {
		return nil, false, conflict("approval_out_of_window", "the approval was not given for this evaluation while it was current")
	}

	amount, err := json.Marshal(option.Breakdown.AmountDue)
	if err != nil {
		return nil, false, err
	}
	approval, err := json.Marshal(in.Approval)
	if err != nil {
		return nil, false, err
	}
	h := &store.Handoff{
		ID: ids.New("hand"), TenantID: p.TenantID, EvaluationID: ev.ID, CustomerID: ev.CustomerID,
		MerchantID: ev.MerchantID, BasketHash: ev.BasketHash, OptionID: option.OptionID, Rail: option.Rail,
		QuoteID: option.QuoteID, Amount: amount, Approval: approval, Status: store.HandoffCreated,
		IdempotencyKey: idempotencyKey, RequestFingerprint: fingerprint,
		ExpiresAt: *ev.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	view, err := s.handoffView(ctx, h)
	if err != nil {
		return nil, false, err
	}
	payload, err := json.Marshal(struct {
		Version string       `json:"version"`
		Type    string       `json:"type"`
		Handoff *HandoffView `json:"handoff"`
	}{HandoffContractVersion, TopicHandoffCreated, view})
	if err != nil {
		return nil, false, err
	}

	err = s.store.CreateHandoff(ctx, h, TopicHandoffCreated, payload)
	switch {
	case errors.Is(err, store.ErrDuplicateKey):
		return replay()
	case errors.Is(err, store.ErrUnresolvedAttempt):
		return nil, false, conflict("prior_attempt_unresolved",
			"a payment attempt for this basket has not been reconciled; a new attempt on any method must wait for its outcome")
	case err != nil:
		return nil, false, err
	}
	return view, false, nil
}

// GetHandoff returns a handoff with its current executability.
func (s *Service) GetHandoff(ctx context.Context, p *store.Principal, id string) (*HandoffView, error) {
	h, err := s.store.GetHandoff(ctx, p.TenantID, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("handoff")
	}
	if err != nil {
		return nil, err
	}
	return s.handoffView(ctx, h)
}
