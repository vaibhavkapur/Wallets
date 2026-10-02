package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/ids"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/policy"
)

// --- organizations ----------------------------------------------------------

type createOrgRequest struct {
	Name string `json:"name"`
}

type createOrgResponse struct {
	Organization domain.Organization `json:"organization"`
	OwnerAPIKey  string              `json:"owner_api_key"`
}

func (s *Server) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	var req createOrgRequest
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required (max 200 chars)")
		return
	}
	now := s.store.Now()
	org := domain.Organization{ID: ids.New("org"), Name: req.Name, Status: domain.OrgActive, CreatedAt: now, UpdatedAt: now}
	secret := ids.NewSecret("owner")
	key := domain.OwnerKey{ID: ids.New("okey"), OrgID: org.ID, KeyHash: ids.HashSecret(secret), Label: "initial", CreatedAt: now}
	if err := s.store.CreateOrganization(r.Context(), org, key, "operator"); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createOrgResponse{Organization: org, OwnerAPIKey: secret})
}

func (s *Server) handleGetOrganization(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	writeJSON(w, http.StatusOK, c.Org)
}

func (s *Server) handlePauseOrganization(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	s.setOrgStatus(w, r, c, domain.OrgPaused)
}

func (s *Server) handleResumeOrganization(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	s.setOrgStatus(w, r, c, domain.OrgActive)
}

func (s *Server) setOrgStatus(w http.ResponseWriter, r *http.Request, c ownerCtx, status domain.OrgStatus) {
	if err := s.store.SetOrganizationStatus(r.Context(), c.Org.ID, status, c.actor()); err != nil {
		writeStoreError(w, err)
		return
	}
	org, err := s.store.GetOrganization(r.Context(), c.Org.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, org)
}

// --- agents -----------------------------------------------------------------

type createAgentRequest struct {
	Name string `json:"name"`
}

type createAgentResponse struct {
	Agent       domain.AgentPrincipal `json:"agent"`
	AgentAPIKey string                `json:"agent_api_key"`
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	var req createAgentRequest
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required (max 200 chars)")
		return
	}
	secret := ids.NewSecret("agent")
	agent := domain.AgentPrincipal{ID: ids.New("agt"), OrgID: c.Org.ID, Name: req.Name, Status: domain.AgentActive,
		KeyHash: ids.HashSecret(secret), CreatedAt: s.store.Now()}
	if err := s.store.CreateAgent(r.Context(), agent, c.actor()); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createAgentResponse{Agent: agent, AgentAPIKey: secret})
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	agents, err := s.store.ListAgents(r.Context(), c.Org.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": orEmpty(agents)})
}

func (s *Server) handleGetAgent(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	agent, err := s.store.GetAgent(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) handleRevokeAgent(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	if err := s.store.RevokeAgent(r.Context(), c.Org.ID, r.PathValue("id"), c.actor()); err != nil {
		writeStoreError(w, err)
		return
	}
	agent, err := s.store.GetAgent(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

// --- policy -----------------------------------------------------------------

// PolicyRequest is the owner-submitted budget policy. All amounts are
// base-unit decimal strings.
type PolicyRequest struct {
	Asset           domain.AssetSpec      `json:"asset"`
	PerRequestMax   money.Amount          `json:"per_request_max"`
	PerTaskMax      money.Amount          `json:"per_task_max"`
	AgentDailyMax   money.Amount          `json:"agent_daily_max"`
	OrgDailyMax     money.Amount          `json:"org_daily_max"`
	FeeAllowance    money.Amount          `json:"fee_allowance"`
	Endpoints       []domain.EndpointRule `json:"endpoints"`
	MaxTaskSeconds  int64                 `json:"max_task_seconds"`
	GrantTTLSeconds int64                 `json:"grant_ttl_seconds"`
}

func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	var req PolicyRequest
	if !decode(w, r, &req) {
		return
	}
	agent, err := s.store.GetAgent(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if req.MaxTaskSeconds == 0 {
		req.MaxTaskSeconds = 24 * 3600
	}
	if req.GrantTTLSeconds == 0 {
		req.GrantTTLSeconds = 60
	}
	p := domain.BudgetPolicyVersion{
		ID: ids.New("pol"), OrgID: c.Org.ID, AgentID: agent.ID, Asset: req.Asset,
		PerRequestMax: req.PerRequestMax, PerTaskMax: req.PerTaskMax, AgentDailyMax: req.AgentDailyMax, OrgDailyMax: req.OrgDailyMax,
		FeeAllowance: req.FeeAllowance, Endpoints: normalizeEndpoints(req.Endpoints), MaxTaskSeconds: req.MaxTaskSeconds,
		GrantTTLSeconds: req.GrantTTLSeconds, CreatedAt: s.store.Now(),
	}
	p.Asset.Scheme = strings.ToLower(strings.TrimSpace(p.Asset.Scheme))
	p.Asset.Network = strings.ToLower(strings.TrimSpace(p.Asset.Network))
	p.Asset.Asset = strings.TrimSpace(p.Asset.Asset)
	if err := policy.ValidatePolicy(p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_policy", err.Error())
		return
	}
	created, err := s.store.CreatePolicyVersion(r.Context(), p, c.actor())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func normalizeEndpoints(in []domain.EndpointRule) []domain.EndpointRule {
	out := make([]domain.EndpointRule, 0, len(in))
	for _, e := range in {
		e.URLPrefix = strings.TrimSpace(e.URLPrefix)
		for i := range e.Methods {
			e.Methods[i] = strings.ToUpper(strings.TrimSpace(e.Methods[i]))
		}
		for i := range e.Recipients {
			e.Recipients[i] = strings.TrimSpace(e.Recipients[i])
		}
		out = append(out, e)
	}
	return out
}

func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	agent, err := s.store.GetAgent(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if agent.PolicyVersionID == "" {
		writeError(w, http.StatusNotFound, "no_policy", "agent has no policy yet")
		return
	}
	p, err := s.store.GetPolicyVersion(r.Context(), c.Org.ID, agent.PolicyVersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleListPolicyVersions(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	ps, err := s.store.ListPolicyVersions(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": orEmpty(ps)})
}

// --- funding ----------------------------------------------------------------

type fundingRequest struct {
	Asset     string       `json:"asset"`
	Network   string       `json:"network"`
	Amount    money.Amount `json:"amount"`
	Reference string       `json:"reference"`
}

func (s *Server) handleDeposit(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	var req fundingRequest
	if !decode(w, r, &req) || !validFunding(w, &req) {
		return
	}
	b, err := s.store.Deposit(r.Context(), c.Org.ID, req.Asset, req.Network, req.Amount, req.Reference, c.actor())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleWithdraw(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	var req fundingRequest
	if !decode(w, r, &req) || !validFunding(w, &req) {
		return
	}
	b, err := s.store.Withdraw(r.Context(), c.Org.ID, req.Asset, req.Network, req.Amount, req.Reference, c.actor())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func validFunding(w http.ResponseWriter, req *fundingRequest) bool {
	req.Asset = strings.TrimSpace(req.Asset)
	req.Network = strings.ToLower(strings.TrimSpace(req.Network))
	if req.Asset == "" || req.Network == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "asset and network are required")
		return false
	}
	if req.Amount.IsZero() {
		writeError(w, http.StatusBadRequest, "invalid_request", "amount must be positive")
		return false
	}
	if len(req.Reference) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "reference too long")
		return false
	}
	return true
}

func (s *Server) handleBalances(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	bs, err := s.store.ListBalances(r.Context(), c.Org.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"balances": orEmpty(bs)})
}

func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	es, err := s.store.ListLedger(r.Context(), c.Org.ID, queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": orEmpty(es)})
}

// --- views ------------------------------------------------------------------

func (s *Server) handleOwnerListTasks(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	ts, err := s.store.ListTasks(r.Context(), c.Org.ID, r.URL.Query().Get("agent_id"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": orEmpty(ts)})
}

func (s *Server) handleOwnerCloseTask(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	if err := s.store.CloseTask(r.Context(), c.Org.ID, r.PathValue("id"), c.actor()); err != nil {
		writeStoreError(w, err)
		return
	}
	t, err := s.store.GetTask(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleOwnerListAttempts(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	as, err := s.store.ListPaymentAttempts(r.Context(), c.Org.ID, r.URL.Query().Get("task_id"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payment_attempts": orEmpty(as)})
}

func (s *Server) handleOwnerListReceipts(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	rs, err := s.store.ListReceipts(r.Context(), c.Org.ID, r.URL.Query().Get("task_id"), queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipts": orEmpty(rs)})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request, c ownerCtx) {
	evs, err := s.store.ListAudit(r.Context(), c.Org.ID, queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": orEmpty(evs)})
}

func queryLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// orEmpty ensures JSON arrays are [] rather than null.
func orEmpty[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}
