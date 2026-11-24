package rewards

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"unowallet/rewards/internal/money"
)

type HTTPConfig struct {
	OperatorToken   string
	WebhookSecret   string
	AllowUnsignedUI bool // Allows sandbox UI helper endpoint to sign merchant events with server-held secret without exposing secret to browser
	WebDistDir      string
	AppOrigin       string
}

type Server struct {
	svc *Service
	cfg HTTPConfig
	mux *http.ServeMux
}

func NewHTTPHandler(svc *Service, cfg HTTPConfig) http.Handler {
	if cfg.OperatorToken == "" {
		cfg.OperatorToken = "local-operator"
	}
	if cfg.WebhookSecret == "" {
		cfg.WebhookSecret = "local-webhook-secret"
	}
	s := &Server{
		svc: svc,
		cfg: cfg,
		mux: http.NewServeMux(),
	}
	s.registerRoutes()
	return s.withMiddleware(s.mux)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /v1/openapi.json", s.handleOpenAPI)

	// Merchants & Campaigns
	s.mux.HandleFunc("GET /v1/merchants", s.handleListMerchants)
	s.mux.HandleFunc("POST /v1/merchants", s.handleOnboardMerchant)
	s.mux.HandleFunc("GET /v1/campaigns", s.handleListCampaigns)
	s.mux.HandleFunc("POST /v1/campaigns", s.handleCreateCampaign)
	s.mux.HandleFunc("POST /v1/campaigns/{id}/funding", s.handleFundCampaign)
	s.mux.HandleFunc("POST /v1/campaigns/{id}/status", s.handleSetCampaignStatus)

	// Merchant order/fulfillment/refund/chargeback event ingestion (HMAC-SHA256 signed)
	s.mux.HandleFunc("POST /v1/merchant-events", s.handleMerchantEvent)
	// Sandbox UI simulator endpoint (server signs event with merchant's secret without leaking secret to browser)
	s.mux.HandleFunc("POST /v1/sandbox/simulate-merchant-event", s.handleSandboxSimulateMerchantEvent)

	// Customer rewards, beneficiaries, payout quotes & payouts
	s.mux.HandleFunc("GET /v1/rewards", s.handleListRewards)
	s.mux.HandleFunc("GET /v1/beneficiaries", s.handleListBeneficiaries)
	s.mux.HandleFunc("POST /v1/beneficiaries", s.handleRegisterBeneficiary)
	s.mux.HandleFunc("POST /v1/beneficiaries/{id}/verify", s.handleVerifyBeneficiary)
	s.mux.HandleFunc("POST /v1/payout-quotes", s.handleCreatePayoutQuote)
	s.mux.HandleFunc("POST /v1/payouts", s.handleCreatePayout)
	s.mux.HandleFunc("GET /v1/payouts", s.handleListPayouts)
	s.mux.HandleFunc("GET /v1/payouts/{id}", s.handleGetPayout)
	s.mux.HandleFunc("POST /v1/payouts/{id}/retry", s.handleRetryPayout)

	// Signed payout provider webhook callbacks
	s.mux.HandleFunc("POST /v1/webhooks/payout-provider", s.handleProviderWebhook)

	// Operator-only endpoints: reconciliation exceptions, ledger audit, pause controls, worker ticks
	s.mux.HandleFunc("GET /v1/reconciliation-exceptions", s.handleListExceptions)
	s.mux.HandleFunc("POST /v1/reconciliation-exceptions/{id}/resolve", s.handleResolveException)
	s.mux.HandleFunc("POST /v1/reconciliation/run", s.handleRunWorkerTick)
	s.mux.HandleFunc("GET /v1/ledger/summary", s.handleLedgerSummary)
	s.mux.HandleFunc("GET /v1/ledger/entries", s.handleLedgerEntries)
	s.mux.HandleFunc("GET /v1/system/status", s.handleSystemStatus)
	s.mux.HandleFunc("POST /v1/system/pause", s.handleSetPause)
	s.mux.HandleFunc("POST /v1/sandbox/advance-clock", s.handleAdvanceClock)
	s.mux.HandleFunc("POST /v1/sandbox/custody-drift", s.handleSetCustodyDrift)

	// Static SPA frontend if WEB_DIST is configured and exists.
	if s.cfg.WebDistDir != "" {
		if info, err := os.Stat(s.cfg.WebDistDir); err == nil && info.IsDir() {
			s.mux.Handle("/", s.spaHandler(s.cfg.WebDistDir))
		}
	}
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if origin := r.Header.Get("Origin"); origin != "" && (s.cfg.AppOrigin == "" || origin == s.cfg.AppOrigin || strings.HasPrefix(origin, "http://127.0.0.1:") || strings.HasPrefix(origin, "http://localhost:")) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, X-Tenant-ID, X-Customer-ID, X-Merchant-ID, X-Operator-Token, X-Webhook-Timestamp, X-Webhook-Signature")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
			return
		}
		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean != "." && clean != "" {
			if _, err := os.Stat(filepath.Join(dir, clean)); err == nil {
				fs.ServeHTTP(w, r)
				return
			}
		}
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}

func tenantIDFromReq(r *http.Request) string {
	t := strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
	if t == "" {
		return "default"
	}
	return t
}

func (s *Server) requireOperator(r *http.Request) bool {
	tok := strings.TrimSpace(r.Header.Get("X-Operator-Token"))
	if tok == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			tok = strings.TrimSpace(auth[7:])
		}
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.OperatorToken)) == 1
}

func decodeJSON(r *http.Request, dst any) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return body, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return body, err
	}
	return body, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrValidation), errors.Is(err, ErrBelowMinimumPayout), errors.Is(err, money.ErrInvalidAmount), errors.Is(err, money.ErrInvalidAsset), errors.Is(err, money.ErrInvalidAddress):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrUnauthorized), errors.Is(err, ErrInvalidSignature):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrPaused):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrInsufficientFunds), errors.Is(err, ErrCustomerCapExceeded):
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrConflict), errors.Is(err, ErrQuoteExpired), errors.Is(err, ErrQuoteConsumed), errors.Is(err, ErrBeneficiaryMismatch), errors.Is(err, ErrOutcomeUnknown):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   s.svc.Now(),
	})
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, BuildOpenAPISpec())
}

func (s *Server) handleListMerchants(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListMerchants(r.Context(), tenantIDFromReq(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"merchants": list})
}

func (s *Server) handleOnboardMerchant(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	var in OnboardMerchantInput
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	m, err := s.svc.OnboardMerchant(r.Context(), tenantIDFromReq(r), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleListCampaigns(w http.ResponseWriter, r *http.Request) {
	merchantID := strings.TrimSpace(r.URL.Query().Get("merchant_id"))
	list, err := s.svc.ListCampaigns(r.Context(), tenantIDFromReq(r), merchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": list})
}

func (s *Server) handleCreateCampaign(w http.ResponseWriter, r *http.Request) {
	var in CreateCampaignInput
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// If X-Merchant-ID is provided (merchant auth), enforce it matches request payload.
	if mHdr := strings.TrimSpace(r.Header.Get("X-Merchant-ID")); mHdr != "" && in.MerchantID != mHdr {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "merchant_id header mismatch"})
		return
	}
	cv, err := s.svc.CreateCampaign(r.Context(), tenantIDFromReq(r), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cv)
}

func (s *Server) handleFundCampaign(w http.ResponseWriter, r *http.Request) {
	var in FundCampaignInput
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	in.CampaignID = r.PathValue("id")
	fe, err := s.svc.FundCampaign(r.Context(), tenantIDFromReq(r), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, fe)
}

func (s *Server) handleSetCampaignStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status CampaignStatus `json:"status"`
	}
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	actor := "merchant"
	if s.requireOperator(r) {
		actor = "operator"
	}
	cv, err := s.svc.SetCampaignStatus(r.Context(), tenantIDFromReq(r), r.PathValue("id"), in.Status, actor)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cv)
}

func (s *Server) handleMerchantEvent(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantIDFromReq(r)
	var in MerchantEventInput
	body, err := decodeJSON(r, &in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	secret, err := s.svc.GetMerchantWebhookSecret(r.Context(), tenantID, in.MerchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	ts := r.Header.Get("X-Webhook-Timestamp")
	sig := r.Header.Get("X-Webhook-Signature")
	if err := s.svc.VerifyHMAC(secret, ts, sig, body, 10*time.Minute); err != nil {
		writeServiceError(w, err)
		return
	}
	res, err := s.svc.IngestMerchantEvent(r.Context(), tenantID, in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	status := http.StatusCreated
	if res.Deduplicated {
		status = http.StatusOK
	}
	writeJSON(w, status, res)
}

// handleSandboxSimulateMerchantEvent lets the local sandbox merchant dashboard trigger
// signed order/fulfillment/refund/chargeback webhooks without exposing raw webhook secrets to browser JS.
func (s *Server) handleSandboxSimulateMerchantEvent(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AllowUnsignedUI {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "sandbox simulator disabled"})
		return
	}
	tenantID := tenantIDFromReq(r)
	var in MerchantEventInput
	body, err := decodeJSON(r, &in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	secret, err := s.svc.GetMerchantWebhookSecret(r.Context(), tenantID, in.MerchantID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Sign and verify through the exact same HMAC verification path.
	ts := strconv.FormatInt(s.svc.Now().Unix(), 10)
	sig := ComputeHMAC(secret, ts, body)
	if err := s.svc.VerifyHMAC(secret, ts, sig, body, 10*time.Minute); err != nil {
		writeServiceError(w, err)
		return
	}
	res, err := s.svc.IngestMerchantEvent(r.Context(), tenantID, in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func customerIDFromReq(r *http.Request) string {
	if c := strings.TrimSpace(r.Header.Get("X-Customer-ID")); c != "" {
		return c
	}
	return strings.TrimSpace(r.URL.Query().Get("customer_id"))
}

func (s *Server) handleListRewards(w http.ResponseWriter, r *http.Request) {
	custID := customerIDFromReq(r)
	if custID == "" {
		custID = "cust_alex"
	}
	summary, err := s.svc.ListCustomerRewards(r.Context(), tenantIDFromReq(r), custID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleListBeneficiaries(w http.ResponseWriter, r *http.Request) {
	custID := customerIDFromReq(r)
	if custID == "" {
		custID = "cust_alex"
	}
	list, err := s.svc.ListBeneficiaries(r.Context(), tenantIDFromReq(r), custID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"beneficiaries": list})
}

func (s *Server) handleRegisterBeneficiary(w http.ResponseWriter, r *http.Request) {
	var in RegisterBeneficiaryInput
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if hdr := strings.TrimSpace(r.Header.Get("X-Customer-ID")); hdr != "" {
		if in.CustomerID != "" && in.CustomerID != hdr {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "customer_id header mismatch"})
			return
		}
		in.CustomerID = hdr
	}
	bv, err := s.svc.RegisterBeneficiary(r.Context(), tenantIDFromReq(r), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, bv)
}

func (s *Server) handleVerifyBeneficiary(w http.ResponseWriter, r *http.Request) {
	custID := customerIDFromReq(r)
	if custID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-Customer-ID header or customer_id query required"})
		return
	}
	bv, err := s.svc.VerifyBeneficiary(r.Context(), tenantIDFromReq(r), custID, r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bv)
}

func (s *Server) handleCreatePayoutQuote(w http.ResponseWriter, r *http.Request) {
	var in CreatePayoutQuoteInput
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if hdr := strings.TrimSpace(r.Header.Get("X-Customer-ID")); hdr != "" {
		if in.CustomerID != "" && in.CustomerID != hdr {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "customer_id header mismatch"})
			return
		}
		in.CustomerID = hdr
	}
	q, err := s.svc.CreatePayoutQuote(r.Context(), tenantIDFromReq(r), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, q)
}

func (s *Server) handleCreatePayout(w http.ResponseWriter, r *http.Request) {
	var in CreatePayoutInput
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if hdr := strings.TrimSpace(r.Header.Get("X-Customer-ID")); hdr != "" {
		if in.CustomerID != "" && in.CustomerID != hdr {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "customer_id header mismatch"})
			return
		}
		in.CustomerID = hdr
	}
	if idem := strings.TrimSpace(r.Header.Get("Idempotency-Key")); idem != "" {
		in.IdempotencyKey = idem
	}
	att, err := s.svc.AuthorizePayout(r.Context(), tenantIDFromReq(r), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

func (s *Server) handleListPayouts(w http.ResponseWriter, r *http.Request) {
	custID := customerIDFromReq(r)
	if custID == "" && !s.requireOperator(r) {
		custID = "cust_alex"
	}
	list, err := s.svc.ListPayouts(r.Context(), tenantIDFromReq(r), custID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"payouts": list})
}

func (s *Server) handleGetPayout(w http.ResponseWriter, r *http.Request) {
	custID := ""
	if !s.requireOperator(r) {
		custID = customerIDFromReq(r)
	}
	detail, err := s.svc.GetPayout(r.Context(), tenantIDFromReq(r), r.PathValue("id"), custID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleRetryPayout(w http.ResponseWriter, r *http.Request) {
	var in RetryPayoutInput
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	in.LogicalPayoutID = r.PathValue("id")
	if idem := strings.TrimSpace(r.Header.Get("Idempotency-Key")); idem != "" {
		in.IdempotencyKey = idem
	}
	custID := ""
	if !s.requireOperator(r) {
		custID = customerIDFromReq(r)
	}
	att, err := s.svc.RetryPayout(r.Context(), tenantIDFromReq(r), custID, in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

func (s *Server) handleProviderWebhook(w http.ResponseWriter, r *http.Request) {
	var in ProviderWebhookInput
	body, err := decodeJSON(r, &in)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ts := r.Header.Get("X-Webhook-Timestamp")
	sig := r.Header.Get("X-Webhook-Signature")
	if err := s.svc.VerifyHMAC(s.cfg.WebhookSecret, ts, sig, body, 10*time.Minute); err != nil {
		writeServiceError(w, err)
		return
	}
	att, dedup, err := s.svc.ProcessProviderWebhook(r.Context(), tenantIDFromReq(r), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deduplicated": dedup, "payout_attempt": att})
}

func (s *Server) handleListExceptions(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	onlyOpen := r.URL.Query().Get("status") == "open"
	list, err := s.svc.ListExceptions(r.Context(), tenantIDFromReq(r), onlyOpen)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"exceptions": list})
}

func (s *Server) handleResolveException(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	var in struct {
		ResolutionNote string `json:"resolution_note"`
		ResolvedBy     string `json:"resolved_by"`
	}
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	actor := strings.TrimSpace(in.ResolvedBy)
	if actor == "" {
		actor = "operator"
	}
	ex, err := s.svc.ResolveException(r.Context(), tenantIDFromReq(r), r.PathValue("id"), actor, in.ResolutionNote)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ex)
}

func (s *Server) handleRunWorkerTick(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	rep, err := s.svc.RunWorkerTick(r.Context(), tenantIDFromReq(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleLedgerSummary(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	sum, err := s.svc.GetLedgerSummary(r.Context(), tenantIDFromReq(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) handleLedgerEntries(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	entries, err := s.svc.ListJournalEntries(r.Context(), tenantIDFromReq(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.svc.GetSystemStatus(r.Context(), tenantIDFromReq(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSetPause(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	var in struct {
		IssuancePaused bool   `json:"issuance_paused"`
		PayoutsPaused  bool   `json:"payouts_paused"`
		Reason         string `json:"reason"`
	}
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	p, err := s.svc.SetPauseControl(r.Context(), tenantIDFromReq(r), in.IssuancePaused, in.PayoutsPaused, in.Reason, "operator")
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleAdvanceClock(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	var in struct {
		Seconds int64 `json:"seconds"`
	}
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if in.Seconds <= 0 {
		in.Seconds = 86400 * 15 // Default +15 days to clear return windows in sandbox demos
	}
	now := s.svc.AdvanceClock(time.Duration(in.Seconds) * time.Second)
	rep, err := s.svc.RunWorkerTick(r.Context(), tenantIDFromReq(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server_time": now, "worker_report": rep})
}

func (s *Server) handleSetCustodyDrift(w http.ResponseWriter, r *http.Request) {
	if !s.requireOperator(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "operator token required"})
		return
	}
	var in struct {
		DriftUSDMinor money.Amount `json:"drift_usd_minor"`
	}
	if _, err := decodeJSON(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sp, ok := s.svc.Provider().(*SandboxProvider)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "custody drift injection only available in sandbox mode"})
		return
	}
	sp.SetCustodyDrift(tenantIDFromReq(r), in.DriftUSDMinor)
	rep, err := s.svc.RunWorkerTick(r.Context(), tenantIDFromReq(r))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// BuildOpenAPISpec generates the canonical OpenAPI 3.1 contract for the service.
func BuildOpenAPISpec() map[string]any {
	strAmount := map[string]any{"type": "string", "pattern": "^-?[0-9]+$", "description": "Exact integer minor/base units encoded as a decimal string"}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Merchant-funded Stablecoin Rewards API",
			"version":     "1.0.0",
			"description": "Double-entry ledger, versioned merchant cashback campaigns, signed order/refund ingestion, versioned beneficiaries, and idempotent stablecoin payouts.",
		},
		"paths": map[string]any{
			"/healthz": map[string]any{
				"get": map[string]any{"summary": "Health check", "responses": map[string]any{"200": map[string]any{"description": "OK"}}},
			},
			"/v1/campaigns": map[string]any{
				"get":  map[string]any{"summary": "List active versioned campaigns with funding coverage", "responses": map[string]any{"200": map[string]any{"description": "Campaign list"}}},
				"post": map[string]any{"summary": "Create a versioned campaign with caps and return rules", "responses": map[string]any{"201": map[string]any{"description": "CampaignVersion created"}}},
			},
			"/v1/campaigns/{id}/funding": map[string]any{
				"post": map[string]any{"summary": "Record and reconcile merchant campaign prefunding", "responses": map[string]any{"201": map[string]any{"description": "FundingEvent recorded"}}},
			},
			"/v1/merchant-events": map[string]any{
				"post": map[string]any{"summary": "Ingest HMAC-SHA256 authenticated order, fulfillment, refund, or chargeback events", "responses": map[string]any{"201": map[string]any{"description": "Event processed"}, "200": map[string]any{"description": "Deduplicated"}}},
			},
			"/v1/rewards": map[string]any{
				"get": map[string]any{"summary": "List a customer's reward accruals, statements, and states", "responses": map[string]any{"200": map[string]any{"description": "Customer rewards summary"}}},
			},
			"/v1/beneficiaries": map[string]any{
				"get":  map[string]any{"summary": "List customer beneficiary versions", "responses": map[string]any{"200": map[string]any{"description": "Beneficiary versions"}}},
				"post": map[string]any{"summary": "Enroll or rotate an immutable verified beneficiary destination", "responses": map[string]any{"201": map[string]any{"description": "BeneficiaryVersion created"}}},
			},
			"/v1/payout-quotes": map[string]any{
				"post": map[string]any{"summary": "Obtain exact payout amount, asset, network, destination, fees, and expiry", "responses": map[string]any{"201": map[string]any{"description": "PayoutQuote created"}}},
			},
			"/v1/payouts": map[string]any{
				"get":  map[string]any{"summary": "List payout attempts", "responses": map[string]any{"200": map[string]any{"description": "Payout attempts"}}},
				"post": map[string]any{"summary": "Authorize one idempotent payout bound to an approved quote and beneficiary version", "responses": map[string]any{"201": map[string]any{"description": "PayoutAttempt result"}}},
			},
			"/v1/payouts/{id}": map[string]any{
				"get": map[string]any{"summary": "Report provider and settlement status for a logical payout", "responses": map[string]any{"200": map[string]any{"description": "PayoutDetail"}}},
			},
			"/v1/payouts/{id}/retry": map[string]any{
				"post": map[string]any{"summary": "Safely retry a confirmed-failed logical payout after reconciliation", "responses": map[string]any{"201": map[string]any{"description": "Retry PayoutAttempt"}}},
			},
			"/v1/webhooks/payout-provider": map[string]any{
				"post": map[string]any{"summary": "Ingest HMAC-SHA256 signed payout provider settlement callbacks", "responses": map[string]any{"200": map[string]any{"description": "Callback processed"}}},
			},
			"/v1/reconciliation-exceptions": map[string]any{
				"get": map[string]any{"summary": "Operator-only discrepancy queue", "responses": map[string]any{"200": map[string]any{"description": "Reconciliation exceptions"}}},
			},
			"/v1/reconciliation-exceptions/{id}/resolve": map[string]any{
				"post": map[string]any{"summary": "Resolve an operator reconciliation exception with audit note", "responses": map[string]any{"200": map[string]any{"description": "Resolved exception"}}},
			},
			"/v1/ledger/summary": map[string]any{
				"get": map[string]any{"summary": "Operator double-entry trial balance and funding coverage audit", "responses": map[string]any{"200": map[string]any{"description": "LedgerSummary"}}},
			},
			"/v1/system/pause": map[string]any{
				"post": map[string]any{"summary": "Operator product-level pause control for reward issuance and payouts", "responses": map[string]any{"200": map[string]any{"description": "PauseControl"}}},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"AmountString": strAmount,
			},
		},
	}
}
