// Package httpapi exposes the application API consumed by the iOS app and
// the webhook endpoint called by the payment provider.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mywallet/backend/internal/appleauth"
	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/enrollment"
	"mywallet/backend/internal/store"
)

const maxBodyBytes = 64 * 1024

// Config configures the API server.
type Config struct {
	// SessionTTL bounds how long a sign-in lasts before the app must
	// re-authenticate with Apple.
	SessionTTL time.Duration
	// DevHooks enables the fixture-only /dev endpoints. Never in production.
	DevHooks DevHooks
	Logger   *slog.Logger
}

// DevHooks is implemented by the fake provider to simulate the cardholder
// finishing the native sheet. It is nil for real providers.
type DevHooks interface {
	CompleteSetup(setupID string, brand, last4 string, expMonth, expYear int) (string, error)
	SetSetupOutcome(setupID string, outcome string, code string) error
}

// Server holds dependencies for handlers.
type Server struct {
	store    *store.Store
	enroll   *enrollment.Service
	verifier appleauth.Verifier
	cfg      Config
	log      *slog.Logger
	mux      *http.ServeMux
}

// New builds the router.
func New(st *store.Store, enroll *enrollment.Service, verifier appleauth.Verifier, cfg Config) (*Server, error) {
	if verifier == nil {
		return nil, errors.New("httpapi: verifier is required")
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 30 * 24 * time.Hour
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	s := &Server{store: st, enroll: enroll, verifier: verifier, cfg: cfg, log: cfg.Logger, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /healthz", s.handleHealth)

	m.HandleFunc("POST /v1/auth/apple", s.handleAuthApple)
	m.HandleFunc("POST /v1/webhooks/provider", s.handleWebhook)

	auth := func(h func(http.ResponseWriter, *http.Request, authCtx)) http.Handler { return s.authed(h) }
	m.Handle("POST /v1/auth/sign-out", auth(s.handleSignOut))
	m.Handle("GET /v1/me", auth(s.handleMe))
	m.Handle("POST /v1/card-setups", auth(s.handleCreateSetup))
	m.Handle("GET /v1/card-setups/{id}", auth(s.handleGetSetup))
	m.Handle("POST /v1/card-setups/{id}/close", auth(s.handleCloseSetup))
	m.Handle("GET /v1/cards", auth(s.handleListCards))
	m.Handle("GET /v1/cards/{id}", auth(s.handleGetCard))
	m.Handle("PATCH /v1/cards/{id}", auth(s.handlePatchCard))
	m.Handle("DELETE /v1/cards/{id}", auth(s.handleDeleteCard))
	m.Handle("DELETE /v1/account", auth(s.handleDeleteAccount))

	if s.cfg.DevHooks != nil {
		m.Handle("POST /dev/card-setups/{id}/complete", auth(s.handleDevCompleteSetup))
		m.Handle("POST /dev/card-setups/{id}/outcome", auth(s.handleDevSetupOutcome))
	}
}

// Handler returns the root handler with logging middleware.
func (s *Server) Handler() http.Handler { return s.logging(s.mux) }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "provider": s.enroll.Provider().Name()})
}

// --- middleware -------------------------------------------------------------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		// Only method, route-ish path, status, and latency. Never headers,
		// bodies, query strings, or identifiers from the URL.
		s.log.Info("http", "method", r.Method, "path", routePattern(r), "status", rw.status, "ms", time.Since(start).Milliseconds())
	})
}

func routePattern(r *http.Request) string {
	if p := r.Pattern; p != "" {
		if i := strings.IndexByte(p, ' '); i >= 0 {
			return p[i+1:]
		}
		return p
	}
	return "unmatched"
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

type authCtx struct {
	User    domain.User
	Session domain.Session
}

func (s *Server) authed(h func(http.ResponseWriter, *http.Request, authCtx)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !strings.HasPrefix(tok, "ses_") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "session token required")
			return
		}
		u, sess, err := s.store.UserBySessionToken(r.Context(), tok)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid or expired session")
			return
		}
		if u.Status != domain.UserActive {
			// Account deletion is allowed to be polled by the same client,
			// but nothing else.
			if !(r.Method == http.MethodDelete && r.URL.Path == "/v1/account") {
				writeError(w, http.StatusUnauthorized, "unauthorized", "account is not active")
				return
			}
		}
		h(w, r, authCtx{User: u, Session: sess})
	})
}

// --- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var e apiError
	e.Error.Code, e.Error.Message = code, msg
	writeJSON(w, status, e)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid")
		return false
	}
	return true
}

// decodeFields decodes body into v and also reports which top-level fields
// were present, so handlers can tell an explicit null from an omission.
func decodeFields(body []byte, v any) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if len(strings.TrimSpace(string(body))) == 0 {
		return map[string]json.RawMessage{}, true
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, false
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return nil, false
	}
	return fields, true
}

func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, enrollment.ErrNotFound), errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, enrollment.ErrAccountInactive):
		writeError(w, http.StatusConflict, "account_inactive", "account is not active")
	case errors.Is(err, enrollment.ErrProviderFailed):
		s.log.Warn("provider failure", "err", err)
		writeError(w, http.StatusBadGateway, "provider_unavailable", "the card provider could not be reached; try again")
	default:
		s.log.Error("internal error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}
