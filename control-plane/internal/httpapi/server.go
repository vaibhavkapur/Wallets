// Package httpapi exposes the owner, agent, and internal (gateway) APIs.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/unowallet/agent-spending-accounts/control-plane/internal/domain"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/grant"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/ids"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/policy"
	"github.com/unowallet/agent-spending-accounts/control-plane/internal/store"
)

const maxBodyBytes = 256 * 1024

// Config configures the API server.
type Config struct {
	// InternalToken authenticates the Rust gateway. Required.
	InternalToken string
	// OperatorToken gates organization creation and product-level pause. If
	// empty, organization creation is open (development only).
	OperatorToken string
	// AllowedOrigins for CORS (dashboard). Empty disables CORS headers.
	AllowedOrigins []string
	Logger         *slog.Logger
}

// Server holds dependencies for handlers.
type Server struct {
	store  *store.Store
	signer *grant.Signer
	cfg    Config
	log    *slog.Logger
	mux    *http.ServeMux
}

// New builds the router.
func New(st *store.Store, signer *grant.Signer, cfg Config) (*Server, error) {
	if cfg.InternalToken == "" {
		return nil, errors.New("httpapi: InternalToken is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Server{store: st, signer: signer, cfg: cfg, log: cfg.Logger, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", s.handleHealth)

	// Operator / bootstrap
	m.Handle("POST /v1/organizations", s.operator(http.HandlerFunc(s.handleCreateOrganization)))

	// Owner
	own := func(h func(http.ResponseWriter, *http.Request, ownerCtx)) http.Handler { return s.owner(h) }
	m.Handle("GET /v1/organization", own(s.handleGetOrganization))
	m.Handle("POST /v1/organization/pause", own(s.handlePauseOrganization))
	m.Handle("POST /v1/organization/resume", own(s.handleResumeOrganization))
	m.Handle("POST /v1/agents", own(s.handleCreateAgent))
	m.Handle("GET /v1/agents", own(s.handleListAgents))
	m.Handle("GET /v1/agents/{id}", own(s.handleGetAgent))
	m.Handle("POST /v1/agents/{id}/revoke", own(s.handleRevokeAgent))
	m.Handle("PUT /v1/agents/{id}/policy", own(s.handlePutPolicy))
	m.Handle("GET /v1/agents/{id}/policy", own(s.handleGetPolicy))
	m.Handle("GET /v1/agents/{id}/policy/versions", own(s.handleListPolicyVersions))
	m.Handle("POST /v1/funding/deposits", own(s.handleDeposit))
	m.Handle("POST /v1/funding/withdrawals", own(s.handleWithdraw))
	m.Handle("GET /v1/balances", own(s.handleBalances))
	m.Handle("GET /v1/ledger", own(s.handleLedger))
	m.Handle("GET /v1/tasks", own(s.handleOwnerListTasks))
	m.Handle("POST /v1/tasks/{id}/close", own(s.handleOwnerCloseTask))
	m.Handle("GET /v1/payment-attempts", own(s.handleOwnerListAttempts))
	m.Handle("GET /v1/receipts", own(s.handleOwnerListReceipts))
	m.Handle("GET /v1/audit", own(s.handleAudit))

	// Agent
	ag := func(h func(http.ResponseWriter, *http.Request, agentCtx)) http.Handler { return s.agent(h) }
	m.Handle("POST /v1/tasks", ag(s.handleCreateTask))
	m.Handle("GET /v1/tasks/{id}", ag(s.handleGetTask))
	m.Handle("GET /v1/tasks/{id}/budget", ag(s.handleTaskBudget))
	m.Handle("GET /v1/tasks/{id}/receipts", ag(s.handleTaskReceipts))
	m.Handle("GET /v1/payment-attempts/{id}", ag(s.handleAgentGetAttempt))

	// Internal (gateway)
	in := func(h http.HandlerFunc) http.Handler { return s.internal(h) }
	m.Handle("GET /internal/v1/grant-public-key", in(s.handleGrantPublicKey))
	m.Handle("POST /internal/v1/paid-requests/begin", in(s.handleBegin))
	m.Handle("POST /internal/v1/paid-requests/{id}/authorize", in(s.handleAuthorize))
	m.Handle("POST /internal/v1/paid-requests/{id}/abandon", in(s.handleAbandon))
	m.Handle("POST /internal/v1/paid-requests/{id}/submitted", in(s.handleSubmitted))
	m.Handle("POST /internal/v1/paid-requests/{id}/outcome", in(s.handleOutcome))
}

// Handler returns the root handler with CORS and logging middleware.
func (s *Server) Handler() http.Handler {
	return s.cors(s.logging(s.mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- middleware -------------------------------------------------------------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		// Never log Authorization headers, bodies, or tokens.
		s.log.Info("http", "method", r.Method, "path", r.URL.Path, "status", rw.status, "ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			for _, o := range s.cfg.AllowedOrigins {
				if o == origin || o == "*" {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Vary", "Origin")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					break
				}
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func constantTimeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) operator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.OperatorToken != "" && !constantTimeEqual(bearer(r), s.cfg.OperatorToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "operator token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) internal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !constantTimeEqual(bearer(r), s.cfg.InternalToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "internal token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type ownerCtx struct {
	Org   domain.Organization
	KeyID string
}

func (c ownerCtx) actor() string { return "owner:" + c.KeyID }

func (s *Server) owner(h func(http.ResponseWriter, *http.Request, ownerCtx)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !strings.HasPrefix(tok, "owner_") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "owner API key required")
			return
		}
		key, err := s.store.GetOwnerKeyByHash(r.Context(), ids.HashSecret(tok))
		if err != nil || key.RevokedAt != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid owner API key")
			return
		}
		org, err := s.store.GetOrganization(r.Context(), key.OrgID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "organization not found")
			return
		}
		h(w, r, ownerCtx{Org: org, KeyID: key.ID})
	})
}

type agentCtx struct {
	Org   domain.Organization
	Agent domain.AgentPrincipal
}

func (c agentCtx) actor() string { return "agent:" + c.Agent.ID }

// authenticateAgent resolves an agent API key. Revoked agents are rejected.
func (s *Server) authenticateAgent(ctx context.Context, tok string) (agentCtx, error) {
	if !strings.HasPrefix(tok, "agent_") {
		return agentCtx{}, errors.New("agent API key required")
	}
	agent, err := s.store.GetAgentByKeyHash(ctx, ids.HashSecret(tok))
	if err != nil {
		return agentCtx{}, errors.New("invalid agent API key")
	}
	if agent.Status != domain.AgentActive {
		return agentCtx{}, errors.New("agent revoked")
	}
	org, err := s.store.GetOrganization(ctx, agent.OrgID)
	if err != nil {
		return agentCtx{}, errors.New("organization not found")
	}
	return agentCtx{Org: org, Agent: agent}, nil
}

func (s *Server) agent(h func(http.ResponseWriter, *http.Request, agentCtx)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := s.authenticateAgent(r.Context(), bearer(r))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}
		h(w, r, c)
	})
}

// --- JSON helpers -----------------------------------------------------------

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Extra   any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

func writeErrorDetails(w http.ResponseWriter, status int, code, msg string, details any) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg, Extra: details}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body required")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return false
	}
	return true
}

// writeStoreError maps store/policy errors to HTTP responses.
func writeStoreError(w http.ResponseWriter, err error) {
	if d, ok := policy.AsDenial(err); ok {
		writeErrorDetails(w, http.StatusForbidden, string(d.Code), d.Detail, nil)
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", fmt.Sprintf("internal error: %v", redact(err)))
	}
}

// redact strips anything that could resemble a credential from error text.
func redact(err error) string {
	msg := err.Error()
	for _, p := range []string{"owner_", "agent_", "Bearer "} {
		if i := strings.Index(msg, p); i >= 0 {
			return msg[:i] + "[redacted]"
		}
	}
	return msg
}
