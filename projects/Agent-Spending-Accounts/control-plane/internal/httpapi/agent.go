package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/ids"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/money"
)

type createTaskRequest struct {
	Description     string       `json:"description"`
	Budget          money.Amount `json:"budget"`
	DurationSeconds int64        `json:"duration_seconds,omitempty"`
}

// handleCreateTask creates a task under the agent's current policy. The
// budget is capped by the policy's per-task maximum and the deadline by the
// policy's maximum task duration; an agent cannot widen either.
func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request, c agentCtx) {
	var req createTaskRequest
	if !decode(w, r, &req) {
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	if len(req.Description) > 1000 {
		writeError(w, http.StatusBadRequest, "invalid_request", "description too long")
		return
	}
	if c.Org.Status != domain.OrgActive {
		writeError(w, http.StatusForbidden, "org_paused", "organization is paused")
		return
	}
	if c.Agent.PolicyVersionID == "" {
		writeError(w, http.StatusForbidden, "no_policy", "agent has no budget policy; ask the account owner")
		return
	}
	pol, err := s.store.GetPolicyVersion(r.Context(), c.Org.ID, c.Agent.PolicyVersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	budget := req.Budget
	if budget.IsZero() || budget.Cmp(pol.PerTaskMax) > 0 {
		budget = pol.PerTaskMax
	}
	dur := time.Duration(req.DurationSeconds) * time.Second
	if dur <= 0 || dur > pol.MaxTaskDuration {
		dur = pol.MaxTaskDuration
	}
	now := s.store.Now()
	task := domain.Task{ID: ids.New("tsk"), OrgID: c.Org.ID, AgentID: c.Agent.ID, PolicyVersionID: pol.ID, Description: req.Description,
		Status: domain.TaskActive, Budget: budget, Deadline: now.Add(dur), CreatedAt: now}
	if err := s.store.CreateTask(r.Context(), task, c.actor()); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

// loadOwnTask enforces that agents only see their own tasks.
func (s *Server) loadOwnTask(w http.ResponseWriter, r *http.Request, c agentCtx) (domain.Task, bool) {
	task, err := s.store.GetTask(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil || task.AgentID != c.Agent.ID {
		writeError(w, http.StatusNotFound, "not_found", "task not found")
		return task, false
	}
	return task, true
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request, c agentCtx) {
	task, ok := s.loadOwnTask(w, r, c)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, task)
}

// TaskBudgetResponse shows spendable capacity for one task only.
type TaskBudgetResponse struct {
	TaskID        string            `json:"task_id"`
	Status        domain.TaskStatus `json:"status"`
	Deadline      time.Time         `json:"deadline"`
	Asset         domain.AssetSpec  `json:"asset"`
	TaskBudget    money.Amount      `json:"task_budget"`
	Held          money.Amount      `json:"held"`
	Committed     money.Amount      `json:"committed"`
	Remaining     money.Amount      `json:"remaining"`
	PerRequestMax money.Amount      `json:"per_request_max"`
	FeeAllowance  money.Amount      `json:"fee_allowance"`
	Spendable     bool              `json:"spendable"`
}

func (s *Server) handleTaskBudget(w http.ResponseWriter, r *http.Request, c agentCtx) {
	task, ok := s.loadOwnTask(w, r, c)
	if !ok {
		return
	}
	pol, err := s.store.GetPolicyVersion(r.Context(), c.Org.ID, task.PolicyVersionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	usage, err := s.store.GetTaskUsage(r.Context(), c.Org.ID, task.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	used, _ := usage.Held.Add(usage.Committed)
	cap := money.Min(task.Budget, pol.PerTaskMax)
	remaining, err := cap.Sub(used)
	if err != nil {
		remaining = money.Zero()
	}
	now := s.store.Now()
	spendable := task.Status == domain.TaskActive && now.Before(task.Deadline) && c.Org.Status == domain.OrgActive &&
		c.Agent.PolicyVersionID == task.PolicyVersionID && !remaining.IsZero()
	writeJSON(w, http.StatusOK, TaskBudgetResponse{
		TaskID: task.ID, Status: task.Status, Deadline: task.Deadline, Asset: pol.Asset, TaskBudget: cap,
		Held: usage.Held, Committed: usage.Committed, Remaining: remaining, PerRequestMax: money.Min(pol.PerRequestMax, remaining),
		FeeAllowance: pol.FeeAllowance, Spendable: spendable,
	})
}

func (s *Server) handleTaskReceipts(w http.ResponseWriter, r *http.Request, c agentCtx) {
	task, ok := s.loadOwnTask(w, r, c)
	if !ok {
		return
	}
	rs, err := s.store.ListReceipts(r.Context(), c.Org.ID, task.ID, queryLimit(r))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipts": orEmpty(rs)})
}

func (s *Server) handleAgentGetAttempt(w http.ResponseWriter, r *http.Request, c agentCtx) {
	a, err := s.store.GetPaymentAttempt(r.Context(), c.Org.ID, r.PathValue("id"))
	if err != nil || a.AgentID != c.Agent.ID {
		writeError(w, http.StatusNotFound, "not_found", "payment attempt not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}
