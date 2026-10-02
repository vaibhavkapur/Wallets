package checkout

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type HTTPConfig struct {
	SessionSecret string
	OperatorToken string
	WebhookSecret string
	AllowedOrigin string
	Demo          bool
	SecureCookies bool
}
type Server struct {
	Service *Service
	Config  HTTPConfig
}
type principal struct {
	Tenant  string `json:"tenant"`
	Role    string `json:"role"`
	Expires int64  `json:"expires"`
}

func (s *Server) sign(payload string) string {
	h := hmac.New(sha256.New, []byte(s.Config.SessionSecret))
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (s *Server) cookie(p principal) string {
	b, _ := json.Marshal(p)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.sign(payload)
}
func (s *Server) auth(r *http.Request) (principal, error) {
	c, e := r.Cookie("uno_session")
	if e != nil {
		return principal{}, problem(401, "unauthenticated", "Open a sandbox session first.")
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 || !hmac.Equal([]byte(parts[1]), []byte(s.sign(parts[0]))) {
		return principal{}, problem(401, "unauthenticated", "Invalid session.")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	var p principal
	if e != nil || json.Unmarshal(b, &p) != nil || p.Tenant == "" || p.Expires < time.Now().Unix() {
		return p, problem(401, "unauthenticated", "Your session expired. Reload to continue.")
	}
	return p, nil
}
func decode(r *http.Request, out any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 16385))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return problem(400, "invalid_json", "Invalid request: "+e.Error())
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return problem(400, "invalid_json", "Only one JSON object is allowed.")
	}
	return nil
}
func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error) {
	var p *Problem
	if errors.As(e, &p) {
		reply(w, p.Status, map[string]string{"error": p.Code, "message": p.Message})
		return
	}
	reply(w, 500, map[string]string{"error": "internal_error", "message": "Unable to complete this operation. Your payment status is preserved; try refreshing."})
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]string{"status": "ok", "mode": "sandbox"})
	})
	mux.HandleFunc("POST /v1/session", s.session)
	mux.HandleFunc("POST /v1/webhooks/sandbox", s.webhook)
	mux.HandleFunc("/v1/", s.api)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" && origin != s.Config.AllowedOrigin {
				fail(w, problem(403, "origin_rejected", "This origin cannot change your checkout."))
				return
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				fail(w, problem(403, "origin_rejected", "Cross-site requests are not allowed."))
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				fail(w, problem(415, "content_type", "Send application/json."))
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16384)
		mux.ServeHTTP(w, r)
	})
}
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if !s.Config.Demo {
		fail(w, problem(403, "sandbox_disabled", "Demo sessions are disabled."))
		return
	}
	var body struct {
		Role  string `json:"role"`
		Token string `json:"token"`
	}
	if e := decode(r, &body); e != nil {
		fail(w, e)
		return
	}
	role := "customer"
	if body.Role == "operator" {
		if s.Config.OperatorToken == "" || !hmac.Equal([]byte(body.Token), []byte(s.Config.OperatorToken)) {
			fail(w, problem(403, "operator_required", "An operator access token is required."))
			return
		}
		role = "operator"
	}
	p := principal{"sandbox-customer", role, time.Now().Add(8 * time.Hour).Unix()}
	http.SetCookie(w, &http.Cookie{Name: "uno_session", Value: s.cookie(p), HttpOnly: true, Secure: s.Config.SecureCookies, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 28800})
	reply(w, 200, map[string]string{"mode": "sandbox", "role": role, "name": "Alex Morgan"})
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	p, e := s.auth(r)
	if e != nil {
		fail(w, e)
		return
	}
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(path) < 2 {
		fail(w, missing())
		return
	}
	var out any
	status := 200
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/dashboard":
		var d Dashboard
		d, e = s.Service.Dashboard(r.Context(), p.Tenant)
		if p.Role != "operator" {
			d.Jobs = []Job{}
			d.Ledger = []LedgerEntry{}
		}
		out = d
	case r.Method == "POST" && r.URL.Path == "/v1/purchase-intents":
		var in IntentInput
		if e = decode(r, &in); e == nil {
			out, e = s.Service.CreateIntent(r.Context(), p.Tenant, in)
			status = 201
		}
	case r.Method == "POST" && len(path) == 4 && path[1] == "purchase-intents" && path[3] == "quotes":
		out, e = s.Service.Quote(r.Context(), p.Tenant, path[2])
		status = 201
	case len(path) >= 3 && path[1] == "checkouts":
		cid := path[2]
		if len(path) == 3 && r.Method == "GET" {
			var st *State
			st, e = s.Service.Store.View(r.Context(), p.Tenant)
			if e == nil {
				c, ok := st.Checkouts[cid]
				if !ok {
					e = missing()
				} else {
					out = c
				}
			}
			break
		}
		if len(path) != 4 || r.Method != "POST" {
			e = missing()
			break
		}
		switch path[3] {
		case "selection":
			var in SelectionInput
			if e = decode(r, &in); e == nil {
				out, e = s.Service.Select(r.Context(), p.Tenant, cid, in)
			}
		case "approval":
			var in struct {
				Version int `json:"version"`
			}
			if e = decode(r, &in); e == nil {
				out, e = s.Service.Approve(r.Context(), p.Tenant, cid, in.Version)
				status = 201
			}
		case "execute":
			var in ExecuteInput
			if e = decode(r, &in); e == nil {
				out, e = s.Service.Execute(r.Context(), p.Tenant, cid, in.ApprovalID, r.Header.Get("Idempotency-Key"))
				status = 202
			}
		case "cancel":
			e = s.Service.Revoke(r.Context(), p.Tenant, cid)
			out = map[string]bool{"ok": e == nil}
		case "challenge":
			var in struct {
				Success bool `json:"success"`
			}
			if e = decode(r, &in); e == nil {
				e = s.Service.Confirm(r.Context(), p.Tenant, cid, in.Success)
				out = map[string]bool{"ok": e == nil}
			}
		case "reconcile":
			e = s.Service.Reconcile(r.Context(), p.Tenant, cid)
			status = 202
			out = map[string]bool{"queued": e == nil}
		default:
			e = missing()
		}
	case len(path) >= 3 && path[1] == "orders":
		oid := path[2]
		if len(path) == 3 && r.Method == "GET" {
			var st *State
			st, e = s.Service.Store.View(r.Context(), p.Tenant)
			if e == nil {
				o, ok := st.Orders[oid]
				if !ok {
					e = missing()
				} else {
					out = o
				}
			}
			break
		}
		if len(path) == 4 && r.Method == "POST" && (path[3] == "refund-requests" || path[3] == "cancel") {
			var in struct {
				Amount Money  `json:"amount"`
				Reason string `json:"reason"`
			}
			if e = decode(r, &in); e == nil {
				out, e = s.Service.Refund(r.Context(), p.Tenant, oid, r.Header.Get("Idempotency-Key"), in.Amount, in.Reason, path[3] == "cancel")
				status = 202
			}
		} else {
			e = missing()
		}
	case r.Method == "POST" && len(path) == 4 && path[1] == "cards" && path[3] == "enroll":
		e = s.Service.Enroll(r.Context(), p.Tenant, path[2])
		out = map[string]bool{"enrolled": e == nil}
	case r.Method == "POST" && len(path) == 5 && path[1] == "operator" && path[2] == "orders" && path[4] == "advance":
		if p.Role != "operator" {
			e = problem(403, "operator_required", "Operator access is required.")
			break
		}
		e = s.Service.AdvanceFulfillment(r.Context(), p.Tenant, path[3])
		out = map[string]bool{"ok": e == nil}
	case r.Method == "POST" && r.URL.Path == "/v1/operator/pause":
		if p.Role != "operator" {
			e = problem(403, "operator_required", "Operator access is required.")
			break
		}
		var in struct {
			Paused bool `json:"paused"`
		}
		if e = decode(r, &in); e == nil {
			e = s.Service.Pause(r.Context(), p.Tenant, in.Paused)
			out = map[string]bool{"paused": in.Paused}
		}
	default:
		e = missing()
	}
	if e != nil {
		fail(w, e)
		return
	}
	reply(w, status, out)
}
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if s.Config.WebhookSecret == "" {
		fail(w, problem(503, "webhook_disabled", "Webhook signing is not configured."))
		return
	}
	stamp, e := strconv.ParseInt(r.Header.Get("X-Webhook-Timestamp"), 10, 64)
	if e != nil || stamp < time.Now().Add(-5*time.Minute).Unix() || stamp > time.Now().Add(time.Minute).Unix() {
		fail(w, problem(401, "invalid_signature", "Webhook timestamp is invalid."))
		return
	}
	body, e := io.ReadAll(r.Body)
	if e != nil {
		fail(w, e)
		return
	}
	h := hmac.New(sha256.New, []byte(s.Config.WebhookSecret))
	fmt.Fprintf(h, "%d.", stamp)
	h.Write(body)
	signature, e := hex.DecodeString(r.Header.Get("X-Webhook-Signature"))
	if e != nil || !hmac.Equal(signature, h.Sum(nil)) {
		fail(w, problem(401, "invalid_signature", "Webhook signature is invalid."))
		return
	}
	var event struct {
		ID         string `json:"id"`
		Tenant     string `json:"tenant"`
		CheckoutID string `json:"checkout_id"`
	}
	if json.Unmarshal(body, &event) != nil || event.ID == "" || event.Tenant == "" || event.CheckoutID == "" {
		fail(w, problem(400, "invalid_event", "Invalid webhook event."))
		return
	}
	e = s.Service.Store.Update(r.Context(), event.Tenant, func(st *State) error {
		if st.Webhooks[event.ID] {
			return nil
		}
		c, ok := st.Checkouts[event.CheckoutID]
		if !ok || c.AttemptID == "" {
			return missing()
		}
		st.Webhooks[event.ID] = true
		st.enqueue("reconcile", c.ID)
		st.event(c.ID, "webhook.received", "Verified event queued for authoritative provider lookup")
		return nil
	})
	if e != nil {
		fail(w, e)
		return
	}
	reply(w, 202, map[string]bool{"accepted": true})
}
