package httpapi

import (
	"net/http"
	"strings"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/grant"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/ids"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/policy"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/store"
)

// This file is the contract between the Go control plane and the Rust
// gateway. The gateway never receives owner credentials or signing keys; it
// receives a per-request execution grant after Go has atomically reserved.

func (s *Server) handleGrantPublicKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"kid": s.signer.KeyID(), "alg": "EdDSA", "crv": "Ed25519", "public_key_base64": s.signer.PublicKeyBase64(),
		"issuer": grant.Issuer, "audience": grant.Audience,
	})
}

// BeginRequest is sent by the gateway before it contacts the resource server.
type BeginRequest struct {
	AgentKey       string `json:"agent_key"`
	TaskID         string `json:"task_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Method         string `json:"method"`
	URL            string `json:"url"`
}

// BeginContext tells the gateway what the policy pins so it can validate the
// 402 challenge locally before asking for authorization.
type BeginContext struct {
	OrgID           string              `json:"organization_id"`
	AgentID         string              `json:"agent_id"`
	TaskID          string              `json:"task_id"`
	PolicyVersionID string              `json:"policy_version_id"`
	Asset           domain.AssetSpec    `json:"asset"`
	PerRequestMax   money.Amount        `json:"per_request_max"`
	FeeAllowance    money.Amount        `json:"fee_allowance"`
	EndpointRule    domain.EndpointRule `json:"endpoint_rule"`
	GrantTTLSeconds int64               `json:"grant_ttl_seconds"`
}

type BeginResponse struct {
	// Replay is true when the idempotency key matched an existing attempt;
	// the gateway must then return that attempt's outcome instead of paying again.
	Replay  bool                  `json:"replay"`
	Attempt domain.PaymentAttempt `json:"attempt"`
	Context *BeginContext         `json:"context,omitempty"`
	Receipt *domain.Receipt       `json:"receipt,omitempty"`
}

func (s *Server) handleBegin(w http.ResponseWriter, r *http.Request) {
	var req BeginRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := s.authenticateAgent(r.Context(), req.AgentKey)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "idempotency_key is required (max 200 chars)")
		return
	}
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	u, err := policy.NormalizeURL(req.URL)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	task, err := s.store.GetTask(r.Context(), c.Org.ID, req.TaskID)
	if err != nil || task.AgentID != c.Agent.ID {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return
	}
	pol, err := s.store.GetPolicyVersion(r.Context(), c.Org.ID, task.PolicyVersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	now := s.store.Now()
	attempt, created, err := s.store.CreatePaymentAttempt(r.Context(), domain.PaymentAttempt{
		ID: ids.New("pay"), OrgID: c.Org.ID, AgentID: c.Agent.ID, TaskID: task.ID, PolicyVersionID: pol.ID,
		IdempotencyKey: req.IdempotencyKey, Method: req.Method, URL: u.String(), Status: domain.PaymentInitiated, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !created {
		// Idempotent replay: never pay twice for the same key. Return what we know.
		resp := BeginResponse{Replay: true, Attempt: attempt}
		if rs, err := s.store.ListReceipts(r.Context(), c.Org.ID, task.ID, 500); err == nil {
			for i := range rs {
				if rs[i].PaymentAttemptID == attempt.ID {
					resp.Receipt = &rs[i]
					break
				}
			}
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Pre-flight policy: principals and endpoint allowlist. The atomic check
	// happens again inside Reserve; this rejects early and audibly.
	if err := policy.CheckPrincipals(policy.Principals{Org: c.Org, Agent: c.Agent, Task: task}, now); err != nil {
		s.rejectAndWrite(w, r, attempt, err)
		return
	}
	if c.Agent.PolicyVersionID != task.PolicyVersionID {
		s.rejectAndWrite(w, r, attempt, &policy.Denial{Code: policy.CodeTaskClosed, Detail: "task policy version superseded; create a new task"})
		return
	}
	rule, err := policy.MatchEndpoint(pol, req.Method, u.String())
	if err != nil {
		s.rejectAndWrite(w, r, attempt, err)
		return
	}
	writeJSON(w, http.StatusCreated, BeginResponse{Attempt: attempt, Context: &BeginContext{
		OrgID: c.Org.ID, AgentID: c.Agent.ID, TaskID: task.ID, PolicyVersionID: pol.ID, Asset: pol.Asset,
		PerRequestMax: pol.PerRequestMax, FeeAllowance: pol.FeeAllowance, EndpointRule: *rule, GrantTTLSeconds: pol.GrantTTLSeconds,
	}})
}

func (s *Server) rejectAndWrite(w http.ResponseWriter, r *http.Request, attempt domain.PaymentAttempt, err error) {
	d, ok := policy.AsDenial(err)
	if !ok {
		writeStoreError(w, err)
		return
	}
	if rerr := s.store.RejectAttempt(r.Context(), attempt.ID, d.Code, d.Detail); rerr != nil {
		s.log.Warn("reject attempt", "attempt", attempt.ID, "err", rerr)
	}
	writeErrorDetails(w, http.StatusForbidden, string(d.Code), d.Detail, map[string]string{"attempt_id": attempt.ID})
}

// AuthorizeRequest carries the selected, gateway-validated payment requirement.
type AuthorizeRequest struct {
	Quote domain.ResourceQuote `json:"quote"`
}

type AuthorizeResponse struct {
	GrantToken    string       `json:"grant_token"`
	GrantID       string       `json:"grant_id"`
	ReservationID string       `json:"reservation_id"`
	AttemptID     string       `json:"attempt_id"`
	ExpiresAt     string       `json:"expires_at"`
	AmountMax     money.Amount `json:"amount_max"`
	FeeMax        money.Amount `json:"fee_max"`
}

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	var req AuthorizeRequest
	if !decode(w, r, &req) {
		return
	}
	attemptID := r.PathValue("id")
	q := req.Quote
	q.Scheme = strings.ToLower(strings.TrimSpace(q.Scheme))
	q.Network = strings.ToLower(strings.TrimSpace(q.Network))
	q.PayTo = strings.TrimSpace(q.PayTo)
	q.Asset = strings.TrimSpace(q.Asset)
	if q.PayTo == "" || q.Asset == "" || q.Scheme == "" || q.Network == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "quote requires scheme, network, asset and pay_to")
		return
	}
	// Both sides must compute the same canonical hash; a mismatch means the
	// gateway and control plane disagree about what is being authorized.
	expected := grant.RequirementsHash(q)
	if q.RequirementsHash != "" && !strings.EqualFold(q.RequirementsHash, expected) {
		writeError(w, http.StatusBadRequest, "requirements_hash_mismatch", "gateway and control plane computed different requirement hashes")
		return
	}
	q.RequirementsHash = expected

	res, err := s.store.Reserve(r.Context(), store.ReserveParams{AttemptID: attemptID, Quote: q})
	if err != nil {
		if d, ok := policy.AsDenial(err); ok {
			writeErrorDetails(w, http.StatusForbidden, string(d.Code), d.Detail, map[string]string{"attempt_id": attemptID})
			return
		}
		writeStoreError(w, err)
		return
	}
	token, err := s.signer.Issue(grant.Input{Grant: res.Grant, Attempt: res.Attempt, Reservation: res.Reservation, Policy: res.Policy, Quote: q})
	if err != nil {
		// Signing failed after the hold was taken: release it immediately and
		// surface the error. Nothing was transmitted, so release is safe.
		_, _, _ = s.store.ResolveAttempt(r.Context(), attemptID, store.Outcome{Status: domain.PaymentFailed, Error: "grant signing failed", Actor: "control-plane"})
		writeError(w, http.StatusInternalServerError, "internal", "grant signing failed")
		return
	}
	writeJSON(w, http.StatusCreated, AuthorizeResponse{
		GrantToken: token, GrantID: res.Grant.ID, ReservationID: res.Reservation.ID, AttemptID: res.Attempt.ID,
		ExpiresAt: res.Grant.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"), AmountMax: res.Reservation.Amount, FeeMax: res.Reservation.FeeAllowance,
	})
}

// AbandonRequest closes an initiated attempt that will never be paid: the
// resource did not demand payment, the challenge was unusable, or the
// resource failed before any payment was authorized. Nothing is held yet, so
// this is a pure bookkeeping transition.
type AbandonRequest struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func (s *Server) handleAbandon(w http.ResponseWriter, r *http.Request) {
	var req AbandonRequest
	if !decode(w, r, &req) {
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" || len(code) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_request", "code is required")
		return
	}
	if len(req.Detail) > 1000 {
		req.Detail = req.Detail[:1000]
	}
	if err := s.store.RejectAttempt(r.Context(), r.PathValue("id"), policy.Code(code), req.Detail); err != nil {
		writeStoreError(w, err)
		return
	}
	a, err := s.store.GetPaymentAttemptAny(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// SubmittedRequest is the gateway's write-ahead record: it is sent BEFORE the
// paid request leaves the gateway. If the gateway crashes afterwards, the
// attempt is reconciled rather than released.
type SubmittedRequest struct {
	GrantID     string `json:"grant_id"`
	PaymentHash string `json:"payment_hash"`
}

func (s *Server) handleSubmitted(w http.ResponseWriter, r *http.Request) {
	var req SubmittedRequest
	if !decode(w, r, &req) {
		return
	}
	a, err := s.store.MarkSubmitted(r.Context(), r.PathValue("id"), req.GrantID, req.PaymentHash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// OutcomeRequest reports settlement and service delivery separately.
type OutcomeRequest struct {
	Payment struct {
		Status        domain.PaymentStatus `json:"status"` // settled | failed | unknown
		SubmissionRef string               `json:"submission_ref,omitempty"`
		PayerRef      string               `json:"payer_ref,omitempty"`
		SettledAmount money.Amount         `json:"settled_amount"`
		FeeActual     money.Amount         `json:"fee_actual"`
		Error         string               `json:"error,omitempty"`
	} `json:"payment"`
	Service *domain.ServiceOutcome `json:"service,omitempty"`
}

type OutcomeResponse struct {
	Attempt domain.PaymentAttempt `json:"attempt"`
	Receipt *domain.Receipt       `json:"receipt,omitempty"`
}

func (s *Server) handleOutcome(w http.ResponseWriter, r *http.Request) {
	var req OutcomeRequest
	if !decode(w, r, &req) {
		return
	}
	switch req.Payment.Status {
	case domain.PaymentSettled, domain.PaymentFailed, domain.PaymentUnknown:
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "payment.status must be settled, failed or unknown")
		return
	}
	attempt, receipt, err := s.store.ResolveAttempt(r.Context(), r.PathValue("id"), store.Outcome{
		Status: req.Payment.Status, SubmissionRef: req.Payment.SubmissionRef, PayerRef: req.Payment.PayerRef,
		SettledAmount: req.Payment.SettledAmount, FeeActual: req.Payment.FeeActual, Error: req.Payment.Error, Service: req.Service, Actor: "gateway",
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, OutcomeResponse{Attempt: attempt, Receipt: receipt})
}
