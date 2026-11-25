package recurring

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
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
	Worker  *Worker
	Config  HTTPConfig
}

type principal struct {
	Tenant  string `json:"tenant"`
	Role    string `json:"role"` // customer | operator
	Actor   string `json:"actor"`
	Expires int64  `json:"expires"`
}

func NewServer(svc *Service, worker *Worker, cfg HTTPConfig) *Server {
	if cfg.SessionSecret == "" {
		cfg.SessionSecret = "local-dev-recurring-secret"
	}
	if cfg.OperatorToken == "" {
		cfg.OperatorToken = "local-operator"
	}
	if cfg.WebhookSecret == "" {
		cfg.WebhookSecret = "local-webhook-secret"
	}
	return &Server{Service: svc, Worker: worker, Config: cfg}
}

func (s *Server) sign(payload string) string {
	h := hmac.New(sha256.New, []byte(s.Config.SessionSecret))
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (s *Server) encodeCookie(p principal) string {
	b, _ := json.Marshal(p)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.sign(payload)
}

func (s *Server) authenticate(r *http.Request) (principal, error) {
	c, err := r.Cookie("uno_recurring_session")
	if err != nil {
		return principal{}, problem(401, "unauthenticated", "Open a session first via POST /v1/session.")
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 2 || !hmac.Equal([]byte(parts[1]), []byte(s.sign(parts[0]))) {
		return principal{}, problem(401, "unauthenticated", "Invalid session signature.")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	var p principal
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Tenant == "" || p.Expires < time.Now().Unix() {
		return principal{}, problem(401, "unauthenticated", "Session expired or invalid.")
	}
	return p, nil
}

func decodeJSON(r *http.Request, out any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 32768))
	dec.DisallowUnknownFields()
	err := dec.Decode(out)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return problem(400, "invalid_json", "Invalid JSON payload: "+err.Error())
	}
	var tail any
	if dec.Decode(&tail) != io.EOF {
		return problem(400, "invalid_json", "Only a single JSON object is permitted.")
	}
	return nil
}

func replyJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func failJSON(w http.ResponseWriter, err error) {
	var p *Problem
	if errors.As(err, &p) {
		replyJSON(w, p.Status, map[string]string{
			"error":   p.Code,
			"message": p.Message,
		})
		return
	}
	replyJSON(w, http.StatusInternalServerError, map[string]string{
		"error":   "internal_error",
		"message": "Unable to complete operation safely. Existing orders and reservations remain protected.",
	})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		mode := "production"
		if s.Config.Demo {
			mode = "sandbox"
		}
		replyJSON(w, http.StatusOK, map[string]any{
			"status":  "ok",
			"mode":    mode,
			"now_utc": s.Service.Clock.Now().UTC().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("POST /v1/session", s.handleSession)
	mux.HandleFunc("GET /v1/catalog", s.handleCatalog)
	mux.HandleFunc("POST /v1/webhooks/merchant", s.handleWebhook)

	// Authenticated tenant-scoped endpoints
	mux.HandleFunc("GET /v1/workspace", s.withAuth(s.handleWorkspace))

	mux.HandleFunc("POST /v1/recurring-rules", s.withAuth(s.handleCreateRule))
	mux.HandleFunc("PATCH /v1/recurring-rules/{id}", s.withAuth(s.handleUpdateRule))
	mux.HandleFunc("POST /v1/recurring-rules/{id}/pause", s.withAuth(s.handlePauseRule))
	mux.HandleFunc("POST /v1/recurring-rules/{id}/resume", s.withAuth(s.handleResumeRule))
	mux.HandleFunc("POST /v1/recurring-rules/{id}/cancel", s.withAuth(s.handleCancelRule))
	mux.HandleFunc("POST /v1/recurring-rules/{id}/skip-next", s.withAuth(s.handleSkipNext))
	mux.HandleFunc("GET /v1/recurring-rules/{id}/occurrences", s.withAuth(s.handleListRuleOccurrences))
	mux.HandleFunc("POST /v1/recurring-rules/{id}/mandates", s.withAuth(s.handleGrantMandate))

	mux.HandleFunc("POST /v1/consents/{id}/revoke", s.withAuth(s.handleRevokeConsent))

	mux.HandleFunc("POST /v1/proposals/{id}/approve", s.withAuth(s.handleApproveProposal))
	mux.HandleFunc("POST /v1/proposals/{id}/reject", s.withAuth(s.handleRejectProposal))

	mux.HandleFunc("GET /v1/occurrences/{id}", s.withAuth(s.handleGetOccurrence))
	mux.HandleFunc("POST /v1/occurrences/{id}/reconcile", s.withAuth(s.handleReconcileOccurrence))
	mux.HandleFunc("POST /v1/occurrences/{id}/cancel-order", s.withAuth(s.handleCancelOrder))

	// Operator-only control
	mux.HandleFunc("POST /v1/operator/pause", s.withAuth(s.handleOperatorPause))

	// Demo / sandbox simulation controls
	mux.HandleFunc("POST /v1/sandbox/tick", s.withAuth(s.handleSandboxTick))
	mux.HandleFunc("PUT /v1/sandbox/faults", s.withAuth(s.handleSandboxFaults))
	mux.HandleFunc("POST /v1/sandbox/fulfill/{id}", s.withAuth(s.handleSandboxFulfill))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		if s.Config.AllowedOrigin != "" {
			origin := r.Header.Get("Origin")
			if origin == s.Config.AllowedOrigin {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,OPTIONS")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead &&
				origin != "" && origin != s.Config.AllowedOrigin && !strings.HasPrefix(r.URL.Path, "/v1/webhooks/") {
				failJSON(w, problem(403, "invalid_origin", "Origin not allowed."))
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.authenticate(r)
		if err != nil {
			failJSON(w, err)
			return
		}
		next(w, r, p)
	}
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tenant        string `json:"tenant"`
		Role          string `json:"role"`
		OperatorToken string `json:"operator_token"`
	}
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	tenant := strings.TrimSpace(req.Tenant)
	if tenant == "" {
		tenant = "household-demo"
	}
	role := "customer"
	actor := "customer:" + tenant
	if req.Role == "operator" {
		if !hmac.Equal([]byte(req.OperatorToken), []byte(s.Config.OperatorToken)) {
			failJSON(w, problem(403, "invalid_operator_token", "Valid operator token is required for operator role."))
			return
		}
		role = "operator"
		actor = "operator:" + tenant
	}
	p := principal{
		Tenant:  tenant,
		Role:    role,
		Actor:   actor,
		Expires: time.Now().Add(12 * time.Hour).Unix(),
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "uno_recurring_session",
		Value:    s.encodeCookie(p),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Config.SecureCookies,
		MaxAge:   43200,
	})
	replyJSON(w, http.StatusOK, map[string]any{
		"tenant": p.Tenant,
		"role":   p.Role,
		"actor":  p.Actor,
	})
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request) {
	replyJSON(w, http.StatusOK, map[string]any{
		"catalog":         DefaultCatalog(),
		"payment_methods": DefaultPaymentMethods(),
	})
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request, p principal) {
	st, err := s.Service.Store.View(r.Context(), p.Tenant)
	if err != nil {
		failJSON(w, err)
		return
	}
	globalPause, err := s.Service.Store.GetGlobalPause(r.Context())
	if err != nil {
		failJSON(w, err)
		return
	}

	rules := make([]*RecurringRule, 0, len(st.Rules))
	for _, rl := range st.Rules {
		rules = append(rules, rl)
	}
	sort.Slice(rules, func(i, j int) bool {
		return rules[i].Current.CreatedAt.After(rules[j].Current.CreatedAt)
	})

	occurrences := make([]*OccurrenceDetail, 0, len(st.Occurrences))
	for _, occ := range st.Occurrences {
		d := &OccurrenceDetail{Occurrence: occ}
		if occ.QuoteID != "" {
			d.Quote = st.Quotes[occ.QuoteID]
		}
		if occ.ProposalID != "" {
			d.Proposal = st.Proposals[occ.ProposalID]
		}
		if occ.ConsentID != "" {
			d.Consent = st.Consents[occ.ConsentID]
		}
		if occ.ReservationID != "" {
			d.Reservation = st.Reservations[occ.ReservationID]
		}
		if occ.CheckoutID != "" {
			d.Checkout = st.Checkouts[occ.CheckoutID]
		}
		occurrences = append(occurrences, d)
	}
	sort.Slice(occurrences, func(i, j int) bool {
		return occurrences[i].Occurrence.ScheduledForUTC.After(occurrences[j].Occurrence.ScheduledForUTC)
	})

	consents := make([]*ConsentRecord, 0, len(st.Consents))
	for _, c := range st.Consents {
		consents = append(consents, c)
	}
	sort.Slice(consents, func(i, j int) bool {
		return consents[i].GrantedAt.After(consents[j].GrantedAt)
	})

	// Compute budget summaries per rule for current period.
	now := s.Service.Clock.Now().UTC()
	budgetUsage := map[string]map[string]any{}
	for _, rl := range rules {
		period := BudgetPeriodKey(rl.Current.Schedule.Timezone, now)
		used := budgetUsedMinor(st, rl.RuleID, period)
		remaining := int64(rl.Current.Product.MonthlyBudget.AmountMinor) - used
		if remaining < 0 {
			remaining = 0
		}
		budgetUsage[rl.RuleID] = map[string]any{
			"period":    period,
			"used":      USD(used),
			"remaining": USD(remaining),
			"cap":       rl.Current.Product.MonthlyBudget,
		}
	}

	replyJSON(w, http.StatusOK, map[string]any{
		"tenant":        p.Tenant,
		"role":          p.Role,
		"now_utc":       now.Format(time.RFC3339),
		"demo_mode":     s.Config.Demo,
		"global_pause":  globalPause,
		"faults":        st.Faults,
		"rules":         rules,
		"occurrences":   occurrences,
		"consents":      consents,
		"budget_usage":  budgetUsage,
		"notifications": st.Notifications,
		"audits":        st.Audits,
	})
}

func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request, p principal) {
	var req CreateRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	rule, err := s.Service.CreateRule(r.Context(), p.Tenant, p.Actor, req)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusCreated, rule)
}

func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request, p principal) {
	ruleID := r.PathValue("id")
	var req UpdateRuleRequest
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	rule, err := s.Service.UpdateRule(r.Context(), p.Tenant, p.Actor, ruleID, req)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, rule)
}

func (s *Server) handlePauseRule(w http.ResponseWriter, r *http.Request, p principal) {
	ruleID := r.PathValue("id")
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	rule, inFlight, err := s.Service.PauseRule(r.Context(), p.Tenant, p.Actor, ruleID, req.Reason)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"rule":                      rule,
		"already_submitted_orders":  inFlight,
		"cancels_submitted_orders":  false,
		"stops_future_occurrences":  true,
	})
}

func (s *Server) handleResumeRule(w http.ResponseWriter, r *http.Request, p principal) {
	ruleID := r.PathValue("id")
	rule, err := s.Service.ResumeRule(r.Context(), p.Tenant, p.Actor, ruleID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, rule)
}

func (s *Server) handleCancelRule(w http.ResponseWriter, r *http.Request, p principal) {
	ruleID := r.PathValue("id")
	rule, err := s.Service.CancelRule(r.Context(), p.Tenant, p.Actor, ruleID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, rule)
}

func (s *Server) handleSkipNext(w http.ResponseWriter, r *http.Request, p principal) {
	ruleID := r.PathValue("id")
	occ, rule, err := s.Service.SkipNextOccurrence(r.Context(), p.Tenant, p.Actor, ruleID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"skipped_occurrence": occ,
		"rule":               rule,
	})
}

func (s *Server) handleListRuleOccurrences(w http.ResponseWriter, r *http.Request, p principal) {
	ruleID := r.PathValue("id")
	items, err := s.Service.ListRuleOccurrences(r.Context(), p.Tenant, ruleID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"rule_id":     ruleID,
		"occurrences": items,
	})
}

func (s *Server) handleGrantMandate(w http.ResponseWriter, r *http.Request, p principal) {
	ruleID := r.PathValue("id")
	var req GrantMandateRequest
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	consent, err := s.Service.GrantDelegatedMandate(r.Context(), p.Tenant, p.Actor, ruleID, req)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusCreated, consent)
}

func (s *Server) handleRevokeConsent(w http.ResponseWriter, r *http.Request, p principal) {
	consentID := r.PathValue("id")
	consent, err := s.Service.RevokeConsent(r.Context(), p.Tenant, p.Actor, consentID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, consent)
}

func (s *Server) handleApproveProposal(w http.ResponseWriter, r *http.Request, p principal) {
	proposalID := r.PathValue("id")
	var req ApproveProposalRequest
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	detail, err := s.Service.ApproveProposal(r.Context(), p.Tenant, p.Actor, proposalID, req)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, detail)
}

func (s *Server) handleRejectProposal(w http.ResponseWriter, r *http.Request, p principal) {
	proposalID := r.PathValue("id")
	var req struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	prop, err := s.Service.RejectProposal(r.Context(), p.Tenant, p.Actor, proposalID, req.Reason)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, prop)
}

func (s *Server) handleGetOccurrence(w http.ResponseWriter, r *http.Request, p principal) {
	occID := r.PathValue("id")
	detail, err := s.Service.GetOccurrenceDetail(r.Context(), p.Tenant, occID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, detail)
}

func (s *Server) handleReconcileOccurrence(w http.ResponseWriter, r *http.Request, p principal) {
	occID := r.PathValue("id")
	detail, err := s.Service.ReconcileOccurrence(r.Context(), p.Tenant, p.Actor, occID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, detail)
}

func (s *Server) handleCancelOrder(w http.ResponseWriter, r *http.Request, p principal) {
	occID := r.PathValue("id")
	detail, err := s.Service.CancelSubmittedOrder(r.Context(), p.Tenant, p.Actor, occID)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, detail)
}

func (s *Server) handleOperatorPause(w http.ResponseWriter, r *http.Request, p principal) {
	if p.Role != "operator" {
		failJSON(w, problem(403, "operator_required", "Only authenticated operators may toggle product-level pause."))
		return
	}
	var req struct {
		Paused bool   `json:"paused"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	st, err := s.Service.SetGlobalPause(r.Context(), p.Actor, req.Paused, req.Reason)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, st)
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32768))
	if err != nil {
		failJSON(w, problem(400, "invalid_body", "Could not read webhook body."))
		return
	}
	sig := r.Header.Get("X-Uno-Signature")
	mac := hmac.New(sha256.New, []byte(s.Config.WebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		failJSON(w, problem(401, "invalid_signature", "Webhook HMAC signature verification failed."))
		return
	}
	var ev MerchantWebhookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		failJSON(w, problem(400, "invalid_json", "Invalid webhook payload."))
		return
	}
	detail, err := s.Service.HandleMerchantWebhook(r.Context(), ev)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, detail)
}

func (s *Server) handleSandboxTick(w http.ResponseWriter, r *http.Request, p principal) {
	if !s.Config.Demo {
		failJSON(w, problem(403, "sandbox_disabled", "Sandbox clock controls are disabled outside demo mode."))
		return
	}
	var req struct {
		AdvanceMinutes int    `json:"advance_minutes"`
		SetTimeUTC     string `json:"set_time_utc"`
	}
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	if fc, ok := s.Service.Clock.(*FakeClock); ok {
		if req.SetTimeUTC != "" {
			parsed, err := time.Parse(time.RFC3339, req.SetTimeUTC)
			if err != nil {
				failJSON(w, problem(400, "invalid_time", "set_time_utc must be RFC3339."))
				return
			}
			fc.Set(parsed)
		} else if req.AdvanceMinutes != 0 {
			fc.Advance(time.Duration(req.AdvanceMinutes) * time.Minute)
		}
	}
	processed := 0
	if s.Worker != nil {
		n, err := s.Worker.RunOnce(r.Context())
		if err != nil {
			failJSON(w, err)
			return
		}
		processed = n
	}
	replyJSON(w, http.StatusOK, map[string]any{
		"now_utc":        s.Service.Clock.Now().UTC().Format(time.RFC3339),
		"jobs_processed": processed,
	})
}

func (s *Server) handleSandboxFaults(w http.ResponseWriter, r *http.Request, p principal) {
	if !s.Config.Demo {
		failJSON(w, problem(403, "sandbox_disabled", "Sandbox fault injection is disabled outside demo mode."))
		return
	}
	var cfg SandboxFaultConfig
	if err := decodeJSON(r, &cfg); err != nil {
		failJSON(w, err)
		return
	}
	updated, err := s.Service.SetSandboxFaults(r.Context(), p.Tenant, p.Actor, cfg)
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, updated)
}

func (s *Server) handleSandboxFulfill(w http.ResponseWriter, r *http.Request, p principal) {
	if !s.Config.Demo {
		failJSON(w, problem(403, "sandbox_disabled", "Sandbox fulfillment simulator is disabled outside demo mode."))
		return
	}
	occID := r.PathValue("id")
	var req struct {
		FulfillmentStatus FulfillmentStatus `json:"fulfillment_status"`
		PartialRefund     *Money            `json:"partial_refund,omitempty"`
		FullRefund        bool              `json:"full_refund,omitempty"`
		Note              string            `json:"note,omitempty"`
	}
	if err := decodeJSON(r, &req); err != nil {
		failJSON(w, err)
		return
	}
	detail, err := s.Service.GetOccurrenceDetail(r.Context(), p.Tenant, occID)
	if err != nil {
		failJSON(w, err)
		return
	}
	if detail.Checkout == nil || detail.Checkout.MerchantOrderID == "" {
		failJSON(w, problem(409, "no_merchant_order", "Occurrence does not have a submitted merchant order yet."))
		return
	}
	updated, err := s.Service.HandleMerchantWebhook(r.Context(), MerchantWebhookEvent{
		TenantID:          p.Tenant,
		MerchantOrderID:   detail.Checkout.MerchantOrderID,
		FulfillmentStatus: req.FulfillmentStatus,
		PartialRefund:     req.PartialRefund,
		FullRefund:        req.FullRefund,
		Note:              req.Note,
	})
	if err != nil {
		failJSON(w, err)
		return
	}
	replyJSON(w, http.StatusOK, updated)
}
