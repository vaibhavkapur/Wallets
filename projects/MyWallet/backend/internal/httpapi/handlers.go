package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"mywallet/backend/internal/appleauth"
	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/provider"
)

// ConsentPurpose is the only purpose v1 asks the user to agree to.
const ConsentPurpose = "save_card_for_display"

// --- wire types -------------------------------------------------------------

type cardJSON struct {
	ID          string    `json:"id"`
	Brand       string    `json:"brand"`
	Last4       string    `json:"last4"`
	ExpMonth    int       `json:"exp_month"`
	ExpYear     int       `json:"exp_year"`
	Nickname    *string   `json:"nickname"`
	IsPreferred bool      `json:"is_preferred"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func toCardJSON(c domain.SavedCard) cardJSON {
	return cardJSON{ID: c.ID, Brand: c.Brand, Last4: c.Last4, ExpMonth: c.ExpMonth, ExpYear: c.ExpYear, Nickname: c.Nickname,
		IsPreferred: c.IsPreferred, State: string(c.State), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

type clientSessionJSON struct {
	Provider           string    `json:"provider"`
	SetupClientSecret  string    `json:"setup_client_secret"`
	CustomerRef        string    `json:"customer_ref"`
	EphemeralKeySecret string    `json:"ephemeral_key_secret"`
	EphemeralKeyExpiry time.Time `json:"ephemeral_key_expires_at"`
	PublishableKey     string    `json:"publishable_key"`
}

type setupJSON struct {
	ID            string             `json:"id"`
	Status        string             `json:"status"`
	FailureCode   *string            `json:"failure_code"`
	Card          *cardJSON          `json:"card"`
	ClientSession *clientSessionJSON `json:"client_session,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

func toSetupJSON(a domain.SetupAttempt, card *domain.SavedCard, sess *provider.ClientSession, providerName string) setupJSON {
	out := setupJSON{ID: a.ID, Status: string(a.Status), FailureCode: a.FailureCode, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	if card != nil {
		c := toCardJSON(*card)
		out.Card = &c
	}
	if sess != nil {
		out.ClientSession = &clientSessionJSON{Provider: providerName, SetupClientSecret: sess.Setup.ClientSecret, CustomerRef: sess.CustomerRef,
			EphemeralKeySecret: sess.EphemeralKeySecret, EphemeralKeyExpiry: sess.EphemeralKeyExpiry, PublishableKey: sess.PublishableKey}
	}
	return out
}

type userJSON struct {
	ID        string    `json:"id"`
	Email     *string   `json:"email"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func toUserJSON(u domain.User) userJSON {
	return userJSON{ID: u.ID, Email: u.Email, Status: string(u.Status), CreatedAt: u.CreatedAt}
}

// --- auth -------------------------------------------------------------------

func (s *Server) handleAuthApple(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IdentityToken string `json:"identity_token"`
		Nonce         string `json:"nonce"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.IdentityToken == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "identity_token is required")
		return
	}
	id, err := s.verifier.Verify(r.Context(), req.IdentityToken, req.Nonce)
	if err != nil {
		if errors.Is(err, appleauth.ErrInvalidToken) {
			writeError(w, http.StatusUnauthorized, "invalid_identity", "identity token could not be verified")
			return
		}
		s.log.Error("apple verification", "err", err)
		writeError(w, http.StatusBadGateway, "identity_unavailable", "identity provider could not be reached")
		return
	}
	var email *string
	if id.EmailVerified {
		email = id.Email
	}
	u, created, err := s.store.UpsertUserByAppleSubject(r.Context(), id.Subject, email)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if u.Status != domain.UserActive {
		writeError(w, http.StatusConflict, "account_deleting", "this account is being deleted; sign in again later")
		return
	}
	token, _, err := s.store.CreateSession(r.Context(), u.ID, s.cfg.SessionTTL)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"session_token": token,
		"expires_at":    s.store.Now().Add(s.cfg.SessionTTL),
		"user":          toUserJSON(u),
	})
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request, a authCtx) {
	if err := s.store.RevokeSession(r.Context(), a.Session.ID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, a authCtx) {
	writeJSON(w, http.StatusOK, toUserJSON(a.User))
}

// --- card setups ------------------------------------------------------------

func (s *Server) handleCreateSetup(w http.ResponseWriter, r *http.Request, a authCtx) {
	idem := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idem == "" || len(idem) > 128 {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required (1-128 chars)")
		return
	}
	var req struct {
		Consent struct {
			Accepted bool   `json:"accepted"`
			Version  string `json:"version"`
		} `json:"consent"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !req.Consent.Accepted || req.Consent.Version == "" {
		writeError(w, http.StatusBadRequest, "consent_required", "consent.accepted must be true and consent.version must be set")
		return
	}
	res, err := s.enroll.StartSetup(r.Context(), a.User, idem, ConsentPurpose, req.Consent.Version)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, toSetupJSON(res.Attempt, res.Card, res.Session, s.enroll.Provider().Name()))
}

func (s *Server) handleGetSetup(w http.ResponseWriter, r *http.Request, a authCtx) {
	attempt, card, err := s.enroll.GetSetup(r.Context(), a.User.ID, r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSetupJSON(attempt, card, nil, s.enroll.Provider().Name()))
}

func (s *Server) handleCloseSetup(w http.ResponseWriter, r *http.Request, a authCtx) {
	var req struct {
		Outcome string `json:"outcome"`
	}
	if !decode(w, r, &req) {
		return
	}
	var failed bool
	switch req.Outcome {
	case "", "canceled":
	case "failed":
		failed = true
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", `outcome must be "canceled" or "failed"`)
		return
	}
	attempt, card, err := s.enroll.CloseSetup(r.Context(), a.User.ID, r.PathValue("id"), failed)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSetupJSON(attempt, card, nil, s.enroll.Provider().Name()))
}

// --- cards ------------------------------------------------------------------

func (s *Server) handleListCards(w http.ResponseWriter, r *http.Request, a authCtx) {
	cards, err := s.store.ListCards(r.Context(), nil, a.User.ID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	out := make([]cardJSON, 0, len(cards))
	for _, c := range cards {
		out = append(out, toCardJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"cards": out})
}

func (s *Server) handleGetCard(w http.ResponseWriter, r *http.Request, a authCtx) {
	c, err := s.store.GetCard(r.Context(), nil, a.User.ID, r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCardJSON(c))
}

const maxNicknameRunes = 40

func (s *Server) handlePatchCard(w http.ResponseWriter, r *http.Request, a authCtx) {
	var req struct {
		Nickname    *string `json:"nickname"`
		IsPreferred *bool   `json:"is_preferred"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid")
		return
	}
	// Distinguish "nickname": null (clear) from an absent field.
	fields, ok := decodeFields(body, &req)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid")
		return
	}
	var nickname **string
	if _, present := fields["nickname"]; present {
		if req.Nickname != nil {
			trimmed := strings.TrimSpace(*req.Nickname)
			if trimmed == "" {
				req.Nickname = nil
			} else {
				if utf8.RuneCountInString(trimmed) > maxNicknameRunes {
					writeError(w, http.StatusBadRequest, "invalid_request", "nickname is too long")
					return
				}
				req.Nickname = &trimmed
			}
		}
		nickname = &req.Nickname
	}
	if nickname == nil && req.IsPreferred == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "provide nickname and/or is_preferred")
		return
	}
	c, err := s.store.UpdateCardDisplay(r.Context(), a.User.ID, r.PathValue("id"), nickname, req.IsPreferred)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toCardJSON(c))
}

func (s *Server) handleDeleteCard(w http.ResponseWriter, r *http.Request, a authCtx) {
	c, err := s.enroll.RemoveCard(r.Context(), a.User.ID, r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	status := http.StatusOK
	if c.State == domain.CardRemovalPending {
		status = http.StatusAccepted
	}
	writeJSON(w, status, toCardJSON(c))
}

// --- account ----------------------------------------------------------------

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request, a authCtx) {
	status, err := s.enroll.DeleteAccount(r.Context(), a.User.ID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	code := http.StatusOK
	if status == domain.UserDeleting {
		code = http.StatusAccepted
	}
	writeJSON(w, code, map[string]string{"status": string(status)})
}

// --- webhooks ---------------------------------------------------------------

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_payload", "could not read payload")
		return
	}
	pv := s.enroll.Provider()
	ev, err := pv.ParseWebhook(payload, r.Header.Get("Stripe-Signature"), s.store.Now())
	if err != nil {
		s.log.Warn("webhook rejected", "err", err)
		writeError(w, http.StatusBadRequest, "invalid_signature", "signature verification failed")
		return
	}
	fresh, err := s.store.MarkEventProcessed(r.Context(), pv.Name(), ev.ID, ev.Type)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	if !fresh {
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	}
	if err := s.enroll.HandleEvent(r.Context(), ev); err != nil {
		// Let the provider retry; forget the event so the retry is processed.
		s.log.Warn("webhook handling failed", "type", ev.Type, "err", err)
		_ = s.store.ForgetEvent(r.Context(), pv.Name(), ev.ID)
		writeError(w, http.StatusInternalServerError, "retry", "temporary failure")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "processed"})
}

// --- dev fixtures -----------------------------------------------------------

func (s *Server) handleDevCompleteSetup(w http.ResponseWriter, r *http.Request, a authCtx) {
	var req struct {
		Brand    string `json:"brand"`
		Last4    string `json:"last4"`
		ExpMonth int    `json:"exp_month"`
		ExpYear  int    `json:"exp_year"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Brand == "" {
		req.Brand, req.Last4, req.ExpMonth, req.ExpYear = "visa", "4242", 12, 2030
	}
	attempt, _, err := s.enroll.GetSetup(r.Context(), a.User.ID, r.PathValue("id"))
	if err != nil || attempt.SetupRef == nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if _, err := s.cfg.DevHooks.CompleteSetup(*attempt.SetupRef, req.Brand, req.Last4, req.ExpMonth, req.ExpYear); err != nil {
		writeError(w, http.StatusConflict, "fixture_error", err.Error())
		return
	}
	attempt, card, err := s.enroll.GetSetup(r.Context(), a.User.ID, attempt.ID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSetupJSON(attempt, card, nil, s.enroll.Provider().Name()))
}

func (s *Server) handleDevSetupOutcome(w http.ResponseWriter, r *http.Request, a authCtx) {
	var req struct {
		Outcome string `json:"outcome"` // requires_action | processing | declined | canceled
		Code    string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	attempt, _, err := s.enroll.GetSetup(r.Context(), a.User.ID, r.PathValue("id"))
	if err != nil || attempt.SetupRef == nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := s.cfg.DevHooks.SetSetupOutcome(*attempt.SetupRef, req.Outcome, req.Code); err != nil {
		writeError(w, http.StatusBadRequest, "fixture_error", err.Error())
		return
	}
	attempt, card, err := s.enroll.GetSetup(r.Context(), a.User.ID, attempt.ID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSetupJSON(attempt, card, nil, s.enroll.Provider().Name()))
}
