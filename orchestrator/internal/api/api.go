// Package api is the HTTP surface. Recommendation access and execution access
// are separate scopes, and every handler runs as one tenant.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/service"
	"github.com/unowallet/card-stablecoin-optimizer/orchestrator/internal/store"
)

// Scopes. A key holds only the scopes it needs.
const (
	ScopeEvaluationsRead  = "evaluations:read"
	ScopeEvaluationsWrite = "evaluations:write"
	ScopePreferencesRead  = "preferences:read"
	ScopePreferencesWrite = "preferences:write"
	ScopeHandoffsRead     = "handoffs:read"
	ScopeHandoffsWrite    = "handoffs:write"
	ScopeOutcomesWrite    = "outcomes:write"
	ScopeReportsRead      = "reports:read"
)

// AllScopes lists every scope, for tooling.
var AllScopes = []string{
	ScopeEvaluationsRead, ScopeEvaluationsWrite, ScopePreferencesRead, ScopePreferencesWrite,
	ScopeHandoffsRead, ScopeHandoffsWrite, ScopeOutcomesWrite, ScopeReportsRead,
}

const maxBodyBytes = 1 << 20

// Authenticator resolves a presented secret to a principal.
type Authenticator interface {
	Authenticate(ctx context.Context, secret string) (*store.Principal, error)
}

// Server serves the application API.
type Server struct {
	svc  *service.Service
	auth Authenticator
	log  *slog.Logger
}

// New returns a Server.
func New(svc *service.Service, auth Authenticator, log *slog.Logger) *Server {
	return &Server{svc: svc, auth: auth, log: log}
}

type handler func(w http.ResponseWriter, r *http.Request, p *store.Principal) error

// Handler returns the routed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	route := func(pattern, scope string, h handler) {
		mux.Handle(pattern, s.wrap(pattern, scope, h))
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	route("GET /v1/meta", "", s.meta)
	route("GET /v1/catalog/baskets", ScopeEvaluationsRead, s.catalog)
	route("POST /v1/evaluations", ScopeEvaluationsWrite, s.createEvaluation)
	route("GET /v1/evaluations/{id}", ScopeEvaluationsRead, s.getEvaluation)
	route("POST /v1/evaluations/{id}/refresh", ScopeEvaluationsWrite, s.refreshEvaluation)
	route("POST /v1/evaluations/{id}/execution-handoffs", ScopeHandoffsWrite, s.createHandoff)
	route("GET /v1/execution-handoffs/{id}", ScopeHandoffsRead, s.getHandoff)
	route("POST /v1/outcomes", ScopeOutcomesWrite, s.recordOutcome)
	route("GET /v1/customers/{id}/preferences", ScopePreferencesRead, s.getPreferences)
	route("PUT /v1/customers/{id}/preferences", ScopePreferencesWrite, s.putPreferences)
	route("GET /v1/reports/summary", ScopeReportsRead, s.summary)
	return mux
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	var body errorBody
	body.Error.Code, body.Error.Message = code, message
	writeJSON(w, status, body)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// wrap authenticates, authorizes, limits the body, and maps errors. Request
// and response bodies are never logged.
func (s *Server) wrap(pattern, scope string, h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		tenant := ""
		defer func() {
			s.log.Info("request", "route", pattern, "status", sw.status, "tenant", tenant,
				"duration_ms", time.Since(start).Milliseconds())
		}()

		secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || secret == "" {
			sw.Header().Set("WWW-Authenticate", `Bearer realm="optimizer"`)
			writeError(sw, http.StatusUnauthorized, "unauthorized", "a bearer API key is required")
			return
		}
		principal, err := s.auth.Authenticate(r.Context(), secret)
		if errors.Is(err, store.ErrNotFound) {
			sw.Header().Set("WWW-Authenticate", `Bearer realm="optimizer"`)
			writeError(sw, http.StatusUnauthorized, "unauthorized", "the API key is not valid")
			return
		}
		if err != nil {
			s.log.Error("authentication failed", "error", err)
			writeError(sw, http.StatusInternalServerError, "internal_error", "an internal error occurred")
			return
		}
		tenant = principal.TenantID
		if scope != "" && !principal.HasScope(scope) {
			writeError(sw, http.StatusForbidden, "forbidden", "this API key lacks the "+scope+" scope")
			return
		}

		r.Body = http.MaxBytesReader(sw, r.Body, maxBodyBytes)
		if err := h(sw, r, principal); err != nil {
			var serviceErr *service.Error
			var tooLarge *http.MaxBytesError
			switch {
			case errors.As(err, &serviceErr):
				writeError(sw, serviceErr.Status, serviceErr.Code, serviceErr.Message)
			case errors.As(err, &tooLarge):
				writeError(sw, http.StatusRequestEntityTooLarge, "payload_too_large", "the request body is too large")
			default:
				s.log.Error("request failed", "route", pattern, "error", err)
				writeError(sw, http.StatusInternalServerError, "internal_error", "an internal error occurred")
			}
		}
	})
}

// decode reads one strict JSON value from the body.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return err
		}
		return &service.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "the request body is not valid: " + err.Error()}
	}
	if _, err := dec.Token(); err != io.EOF {
		return &service.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "unexpected data after the JSON body"}
	}
	return nil
}

func created(w http.ResponseWriter, replayed bool, v any) {
	if replayed {
		w.Header().Set("Idempotent-Replay", "true")
		writeJSON(w, http.StatusOK, v)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (s *Server) meta(w http.ResponseWriter, _ *http.Request, p *store.Principal) error {
	writeJSON(w, http.StatusOK, map[string]any{"mode": s.svc.Mode(), "tenant_id": p.TenantID, "scopes": p.Scopes})
	return nil
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request, _ *store.Principal) error {
	baskets, err := s.svc.Catalog(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"baskets": baskets})
	return nil
}

func (s *Server) createEvaluation(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	var in service.EvaluateInput
	if err := decode(r, &in); err != nil {
		return err
	}
	view, replayed, err := s.svc.Evaluate(r.Context(), p, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		return err
	}
	created(w, replayed, view)
	return nil
}

func (s *Server) getEvaluation(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	view, err := s.svc.GetEvaluation(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) refreshEvaluation(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	view, replayed, err := s.svc.Refresh(r.Context(), p, r.PathValue("id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		return err
	}
	created(w, replayed, view)
	return nil
}

func (s *Server) createHandoff(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	var in service.HandoffInput
	if err := decode(r, &in); err != nil {
		return err
	}
	view, replayed, err := s.svc.CreateHandoff(r.Context(), p, r.PathValue("id"), in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		return err
	}
	created(w, replayed, view)
	return nil
}

func (s *Server) getHandoff(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	view, err := s.svc.GetHandoff(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) recordOutcome(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	var in service.OutcomeInput
	if err := decode(r, &in); err != nil {
		return err
	}
	view, replayed, err := s.svc.RecordOutcome(r.Context(), p, in)
	if err != nil {
		return err
	}
	created(w, replayed, view)
	return nil
}

func (s *Server) getPreferences(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	view, err := s.svc.GetPreferences(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) putPreferences(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	var in service.CustomerPreferences
	if err := decode(r, &in); err != nil {
		return err
	}
	view, err := s.svc.PutPreferences(r.Context(), p, r.PathValue("id"), in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, view)
	return nil
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request, p *store.Principal) error {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)
	var err error
	if v := r.URL.Query().Get("from"); v != "" {
		if from, err = time.Parse(time.RFC3339, v); err != nil {
			return &service.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "from must be an RFC 3339 timestamp"}
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if to, err = time.Parse(time.RFC3339, v); err != nil {
			return &service.Error{Status: http.StatusBadRequest, Code: "invalid_request", Message: "to must be an RFC 3339 timestamp"}
		}
	}
	summary, err := s.svc.Summary(r.Context(), p, from, to)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, summary)
	return nil
}
