// Package enrollment implements card saving, removal, and account deletion
// on top of the store and the payment provider. The HTTP API, the webhook
// handler, and the retry worker all call into this package so that every
// path converges on the same, provider-verified state.
package enrollment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"mywallet/backend/internal/domain"
	"mywallet/backend/internal/provider"
	"mywallet/backend/internal/store"
)

// Errors surfaced to the API layer.
var (
	ErrNotFound        = errors.New("enrollment: not found")
	ErrAccountInactive = errors.New("enrollment: account is not active")
	ErrProviderFailed  = errors.New("enrollment: provider request failed")
)

// Failure codes recorded on setup attempts. Provider decline codes are
// recorded verbatim when present.
const (
	FailureProviderUnavailable = "provider_unavailable"
	FailureCustomerMismatch    = "customer_mismatch"
	FailureSetupMissing        = "setup_missing"
	FailureExpired             = "expired"
	FailureClientReported      = "client_reported_failure"
)

// Service coordinates the store and the provider.
type Service struct {
	st  *store.Store
	pv  provider.Provider
	log *slog.Logger
	Now func() time.Time
}

// New builds a service.
func New(st *store.Store, pv provider.Provider, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, pv: pv, log: log, Now: func() time.Time { return time.Now().UTC() }}
}

// Provider exposes the configured provider (for health and dev tooling).
func (s *Service) Provider() provider.Provider { return s.pv }

// --- customers --------------------------------------------------------------

// EnsureCustomer returns the provider customer for a user, creating it on
// first use. The mapping is derived from the authenticated user only.
func (s *Service) EnsureCustomer(ctx context.Context, userID string) (domain.ProviderCustomer, error) {
	pc, err := s.st.GetProviderCustomer(ctx, nil, userID)
	if err == nil {
		return pc, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return domain.ProviderCustomer{}, err
	}
	ref, err := s.pv.CreateCustomer(ctx, userID)
	if err != nil {
		return domain.ProviderCustomer{}, fmt.Errorf("%w: create customer: %v", ErrProviderFailed, err)
	}
	pc, err = s.st.PutProviderCustomer(ctx, domain.ProviderCustomer{UserID: userID, Provider: s.pv.Name(), CustomerRef: ref})
	if errors.Is(err, store.ErrConflict) {
		// Lost a race; the stored mapping wins.
		return pc, nil
	}
	return pc, err
}

// --- setup ------------------------------------------------------------------

// StartResult is what the API returns for a setup attempt.
type StartResult struct {
	Attempt  domain.SetupAttempt
	Card     *domain.SavedCard
	Session  *provider.ClientSession
	Replayed bool
}

// StartSetup creates (or replays) a setup attempt for the user. The
// idempotency key scopes to the user; repeated calls return the same attempt
// and fresh short-lived client credentials while it is still open.
func (s *Service) StartSetup(ctx context.Context, user domain.User, idemKey, consentPurpose, consentVersion string) (StartResult, error) {
	if user.Status != domain.UserActive {
		return StartResult{}, ErrAccountInactive
	}
	var attempt domain.SetupAttempt
	replayed := false
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		consent, err := s.st.RecordConsent(ctx, tx, user.ID, consentPurpose, consentVersion)
		if err != nil {
			return err
		}
		attempt, err = s.st.CreateSetupAttempt(ctx, tx, user.ID, idemKey, consent.ID, s.pv.Name())
		if errors.Is(err, store.ErrConflict) {
			replayed = true
			// Roll back the duplicate consent record; the original stands.
			return errReplay
		}
		return err
	})
	if err != nil && !errors.Is(err, errReplay) {
		return StartResult{}, err
	}
	if replayed {
		attempt, err = s.st.SetupAttemptByKey(ctx, user.ID, idemKey)
		if err != nil {
			return StartResult{}, err
		}
		if attempt.Status.Terminal() {
			res := StartResult{Attempt: attempt, Replayed: true}
			if attempt.SavedCardID != nil {
				if c, err := s.st.GetCard(ctx, nil, user.ID, *attempt.SavedCardID); err == nil {
					res.Card = &c
				}
			}
			return res, nil
		}
	}

	pc, err := s.EnsureCustomer(ctx, user.ID)
	if err != nil {
		s.markFailed(ctx, attempt.ID, FailureProviderUnavailable)
		return StartResult{}, err
	}
	setupRef := ""
	if attempt.SetupRef != nil {
		setupRef = *attempt.SetupRef
	} else {
		// The provider idempotency key is the attempt ID, so a crash between
		// the insert and this call cannot create a second provider object.
		setup, err := s.pv.CreateSetup(ctx, pc.CustomerRef, "mywallet-setup-"+attempt.ID)
		if err != nil {
			s.markFailed(ctx, attempt.ID, FailureProviderUnavailable)
			return StartResult{}, fmt.Errorf("%w: create setup: %v", ErrProviderFailed, err)
		}
		if err := s.st.SetSetupRef(ctx, nil, attempt.ID, setup.ID); err != nil {
			return StartResult{}, err
		}
		setupRef = setup.ID
		attempt.SetupRef = &setupRef
	}
	session, err := s.pv.ClientSession(ctx, pc.CustomerRef, setupRef)
	if err != nil {
		return StartResult{}, fmt.Errorf("%w: client session: %v", ErrProviderFailed, err)
	}
	attempt, err = s.st.GetSetupAttempt(ctx, nil, user.ID, attempt.ID)
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{Attempt: attempt, Session: &session, Replayed: replayed}, nil
}

var errReplay = errors.New("replay")

func (s *Service) markFailed(ctx context.Context, attemptID, code string) {
	c := code
	if err := s.st.SetSetupStatus(ctx, nil, attemptID, domain.SetupFailed, &c, nil); err != nil {
		s.log.Error("mark setup failed", "attempt", attemptID, "err", err)
	}
}

// GetSetup returns the server-verified state of an attempt the user owns,
// refreshing from the provider while the attempt is open.
func (s *Service) GetSetup(ctx context.Context, userID, attemptID string) (domain.SetupAttempt, *domain.SavedCard, error) {
	attempt, err := s.st.GetSetupAttempt(ctx, nil, userID, attemptID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.SetupAttempt{}, nil, ErrNotFound
	}
	if err != nil {
		return domain.SetupAttempt{}, nil, err
	}
	if !attempt.Status.Terminal() {
		if refreshed, err := s.Reconcile(ctx, attempt.ID); err == nil {
			attempt = refreshed
		} else {
			s.log.Warn("reconcile on read", "attempt", attempt.ID, "err", err)
		}
	}
	var card *domain.SavedCard
	if attempt.SavedCardID != nil {
		if c, err := s.st.GetCard(ctx, nil, userID, *attempt.SavedCardID); err == nil {
			card = &c
		}
	}
	return attempt, card, nil
}

// CloseSetup is called when the native sheet is dismissed. The provider is
// consulted first: a setup that actually succeeded is saved, not canceled.
func (s *Service) CloseSetup(ctx context.Context, userID, attemptID string, failed bool) (domain.SetupAttempt, *domain.SavedCard, error) {
	attempt, err := s.st.GetSetupAttempt(ctx, nil, userID, attemptID)
	if errors.Is(err, store.ErrNotFound) {
		return domain.SetupAttempt{}, nil, ErrNotFound
	}
	if err != nil {
		return domain.SetupAttempt{}, nil, err
	}
	if attempt.Status.Terminal() {
		return s.GetSetup(ctx, userID, attemptID)
	}
	// Consult the provider before touching anything: a late success is
	// saved, and a decline code is captured before cancellation clears it.
	before, err := s.Reconcile(ctx, attempt.ID)
	if err != nil {
		return domain.SetupAttempt{}, nil, err
	}
	if before.Status.Terminal() {
		return s.GetSetup(ctx, userID, attemptID)
	}
	if attempt.SetupRef != nil {
		if err := s.pv.CancelSetup(ctx, *attempt.SetupRef); err != nil && !errors.Is(err, provider.ErrNotFound) {
			s.log.Warn("cancel setup at provider", "attempt", attempt.ID, "err", err)
		}
	}
	after, err := s.Reconcile(ctx, attempt.ID)
	if err != nil {
		return domain.SetupAttempt{}, nil, err
	}
	if after.Status == domain.SetupSaved || after.Status == domain.SetupFailed {
		return s.GetSetup(ctx, userID, attemptID)
	}
	status := domain.SetupCanceled
	var code *string
	if failed {
		status = domain.SetupFailed
		c := FailureClientReported
		if before.FailureCode != nil {
			c = *before.FailureCode
		}
		code = &c
	}
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		cur, err := s.st.GetSetupAttemptForUpdate(ctx, tx, attempt.ID)
		if err != nil {
			return err
		}
		if cur.Status == domain.SetupSaved || cur.Status == domain.SetupFailed {
			return nil
		}
		if err := s.st.SetSetupStatus(ctx, tx, attempt.ID, status, code, nil); err != nil {
			return err
		}
		return s.st.CompleteOperationBySubject(ctx, tx, domain.OpReconcileSetup, attempt.ID)
	})
	if err != nil {
		return domain.SetupAttempt{}, nil, err
	}
	return s.GetSetup(ctx, userID, attemptID)
}

// Reconcile fetches the provider state for an attempt and applies it. It is
// safe to call concurrently from polls, webhooks, and the worker; the row
// lock serializes the transition and the card insert is idempotent.
func (s *Service) Reconcile(ctx context.Context, attemptID string) (domain.SetupAttempt, error) {
	attempt, err := s.st.GetSetupAttemptByID(ctx, attemptID)
	if err != nil {
		return domain.SetupAttempt{}, err
	}
	if attempt.Status.Terminal() || attempt.SetupRef == nil {
		return attempt, nil
	}
	pc, err := s.st.GetProviderCustomer(ctx, nil, attempt.UserID)
	if err != nil {
		return attempt, err
	}
	setup, err := s.pv.GetSetup(ctx, *attempt.SetupRef)
	if errors.Is(err, provider.ErrNotFound) {
		return s.transition(ctx, attempt.ID, domain.SetupFailed, strPtr(FailureSetupMissing), nil)
	}
	if err != nil {
		return attempt, err
	}
	if setup.CustomerRef != pc.CustomerRef {
		s.log.Warn("setup customer mismatch", "attempt", attempt.ID)
		return s.transition(ctx, attempt.ID, domain.SetupFailed, strPtr(FailureCustomerMismatch), nil)
	}

	switch setup.State {
	case provider.SetupSucceeded:
		return s.saveFromSetup(ctx, attempt, pc, setup)
	case provider.SetupRequiresAction:
		return s.transition(ctx, attempt.ID, domain.SetupAuthenticationRequired, nil, nil)
	case provider.SetupProcessing:
		a, err := s.transition(ctx, attempt.ID, domain.SetupProcessing, nil, nil)
		if err == nil {
			_ = s.st.EnqueueOperation(ctx, nil, domain.OpReconcileSetup, attempt.ID)
		}
		return a, err
	case provider.SetupCanceled:
		return s.transition(ctx, attempt.ID, domain.SetupCanceled, nil, nil)
	default:
		// requires_payment_method / requires_confirmation: still collecting.
		// A decline is surfaced as the failure code while the sheet stays open.
		var code *string
		if setup.LastErrorCode != "" {
			code = strPtr(setup.LastErrorCode)
		}
		return s.transition(ctx, attempt.ID, domain.SetupCollecting, code, nil)
	}
}

func (s *Service) saveFromSetup(ctx context.Context, attempt domain.SetupAttempt, pc domain.ProviderCustomer, setup provider.Setup) (domain.SetupAttempt, error) {
	if setup.PaymentMethodRef == "" {
		return s.transition(ctx, attempt.ID, domain.SetupFailed, strPtr(FailureSetupMissing), nil)
	}
	pm, err := s.pv.GetPaymentMethod(ctx, setup.PaymentMethodRef)
	if err != nil {
		return attempt, err
	}
	if pm.CustomerRef != pc.CustomerRef {
		s.log.Warn("payment method customer mismatch", "attempt", attempt.ID)
		return s.transition(ctx, attempt.ID, domain.SetupFailed, strPtr(FailureCustomerMismatch), nil)
	}
	var out domain.SetupAttempt
	err = s.st.WithTx(ctx, func(tx store.Tx) error {
		cur, err := s.st.GetSetupAttemptForUpdate(ctx, tx, attempt.ID)
		if err != nil {
			return err
		}
		if cur.Status.Terminal() {
			out = cur
			return nil
		}
		hasPreferred, err := s.st.HasPreferredCard(ctx, tx, cur.UserID)
		if err != nil {
			return err
		}
		card, err := s.st.InsertCard(ctx, tx, domain.SavedCard{UserID: cur.UserID, Provider: s.pv.Name(), PaymentMethodRef: pm.ID,
			Brand: pm.Brand, Last4: pm.Last4, ExpMonth: pm.ExpMonth, ExpYear: pm.ExpYear, IsPreferred: !hasPreferred})
		if err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
		if card.UserID != cur.UserID {
			// The same provider reference is already owned by someone else.
			return s.st.SetSetupStatus(ctx, tx, cur.ID, domain.SetupFailed, strPtr(FailureCustomerMismatch), nil)
		}
		if err := s.st.SetSetupStatus(ctx, tx, cur.ID, domain.SetupSaved, nil, &card.ID); err != nil {
			return err
		}
		_ = s.st.CompleteOperationBySubject(ctx, tx, domain.OpReconcileSetup, cur.ID)
		out, err = s.st.GetSetupAttemptForUpdate(ctx, tx, cur.ID)
		return err
	})
	return out, err
}

func (s *Service) transition(ctx context.Context, attemptID string, status domain.SetupStatus, code, cardID *string) (domain.SetupAttempt, error) {
	var out domain.SetupAttempt
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		cur, err := s.st.GetSetupAttemptForUpdate(ctx, tx, attemptID)
		if err != nil {
			return err
		}
		if cur.Status.Terminal() {
			out = cur
			return nil
		}
		if cur.Status != status || !equalStr(cur.FailureCode, code) {
			if err := s.st.SetSetupStatus(ctx, tx, attemptID, status, code, cardID); err != nil {
				return err
			}
		}
		if status.Terminal() {
			_ = s.st.CompleteOperationBySubject(ctx, tx, domain.OpReconcileSetup, attemptID)
		}
		out, err = s.st.GetSetupAttemptForUpdate(ctx, tx, attemptID)
		return err
	})
	return out, err
}

// ExpireStaleSetups cancels open attempts untouched for longer than maxAge
// after one last reconciliation, so abandoned sheets do not stay open forever.
func (s *Service) ExpireStaleSetups(ctx context.Context, reconcileAfter, maxAge time.Duration, limit int) error {
	now := s.Now()
	stale, err := s.st.StaleOpenSetupAttempts(ctx, now.Add(-reconcileAfter), limit)
	if err != nil {
		return err
	}
	for _, a := range stale {
		refreshed, err := s.Reconcile(ctx, a.ID)
		if err != nil {
			s.log.Warn("reconcile stale setup", "attempt", a.ID, "err", err)
			continue
		}
		if !refreshed.Status.Terminal() && now.Sub(a.UpdatedAt) > maxAge {
			if a.SetupRef != nil {
				_ = s.pv.CancelSetup(ctx, *a.SetupRef)
			}
			if _, err := s.transition(ctx, a.ID, domain.SetupCanceled, strPtr(FailureExpired), nil); err != nil {
				s.log.Warn("expire stale setup", "attempt", a.ID, "err", err)
			}
		}
	}
	return nil
}

// --- removal ----------------------------------------------------------------

// RemoveCard marks the card for removal durably, then attempts provider
// detachment inline. If the provider is unavailable the card stays in
// removal_pending and the worker retries; the local row is never hidden
// before the provider confirms.
func (s *Service) RemoveCard(ctx context.Context, userID, cardID string) (domain.SavedCard, error) {
	var card domain.SavedCard
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		c, err := s.st.GetCard(ctx, tx, userID, cardID)
		if err != nil {
			return err
		}
		c, err = s.st.GetCardForUpdate(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		card = c
		if c.State == domain.CardRemoved {
			return nil
		}
		if c.State != domain.CardRemovalPending {
			if err := s.st.SetCardState(ctx, tx, c.ID, domain.CardRemovalPending); err != nil {
				return err
			}
		}
		return s.st.EnqueueOperation(ctx, tx, domain.OpDetachCard, c.ID)
	})
	if errors.Is(err, store.ErrNotFound) {
		return domain.SavedCard{}, ErrNotFound
	}
	if err != nil {
		return domain.SavedCard{}, err
	}
	if card.State == domain.CardRemoved {
		return card, nil
	}
	if err := s.RunDetach(ctx, card.ID); err != nil {
		s.log.Warn("detach deferred to worker", "card", card.ID, "err", err)
	}
	return s.st.GetCard(ctx, nil, userID, cardID)
}

// RunDetach performs provider detachment for a card and finishes local
// removal once the provider confirms. Safe to repeat.
func (s *Service) RunDetach(ctx context.Context, cardID string) error {
	card, err := s.st.GetCardByID(ctx, cardID)
	if err != nil {
		return err
	}
	if card.State == domain.CardRemoved {
		return s.st.CompleteOperationBySubject(ctx, nil, domain.OpDetachCard, cardID)
	}
	if err := s.pv.DetachPaymentMethod(ctx, card.PaymentMethodRef); err != nil && !errors.Is(err, provider.ErrAlreadyDetached) {
		return err
	}
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		if err := s.st.SetCardState(ctx, tx, cardID, domain.CardRemoved); err != nil {
			return err
		}
		return s.st.CompleteOperationBySubject(ctx, tx, domain.OpDetachCard, cardID)
	})
}

// --- account deletion -------------------------------------------------------

// DeleteAccount revokes sessions, schedules provider cleanup durably, and
// attempts it inline. Returns the resulting user status.
func (s *Service) DeleteAccount(ctx context.Context, userID string) (domain.UserStatus, error) {
	var status domain.UserStatus
	err := s.st.WithTx(ctx, func(tx store.Tx) error {
		u, err := s.st.GetUserForUpdate(ctx, tx, userID)
		if err != nil {
			return err
		}
		status = u.Status
		if u.Status == domain.UserDeleted {
			return nil
		}
		if err := s.st.RevokeAllSessions(ctx, tx, userID); err != nil {
			return err
		}
		if u.Status == domain.UserActive {
			if err := s.st.SetUserStatus(ctx, tx, userID, domain.UserDeleting); err != nil {
				return err
			}
			status = domain.UserDeleting
		}
		cards, err := s.st.ListCards(ctx, tx, userID)
		if err != nil {
			return err
		}
		for _, c := range cards {
			if c.State == domain.CardSaved {
				if err := s.st.SetCardState(ctx, tx, c.ID, domain.CardRemovalPending); err != nil {
					return err
				}
			}
		}
		return s.st.EnqueueOperation(ctx, tx, domain.OpDeleteAccount, userID)
	})
	if err != nil {
		return "", err
	}
	if status == domain.UserDeleted {
		return status, nil
	}
	if err := s.RunDeleteAccount(ctx, userID); err != nil {
		s.log.Warn("account deletion deferred to worker", "user", userID, "err", err)
		return domain.UserDeleting, nil
	}
	return domain.UserDeleted, nil
}

// RunDeleteAccount detaches every card, deletes the provider customer, and
// scrubs the user. Safe to repeat until it succeeds.
func (s *Service) RunDeleteAccount(ctx context.Context, userID string) error {
	cards, err := s.st.ListCards(ctx, nil, userID)
	if err != nil {
		return err
	}
	for _, c := range cards {
		if err := s.RunDetach(ctx, c.ID); err != nil {
			return fmt.Errorf("detach %s: %w", c.ID, err)
		}
	}
	pc, err := s.st.GetProviderCustomer(ctx, nil, userID)
	if err == nil {
		if err := s.pv.DeleteCustomer(ctx, pc.CustomerRef); err != nil {
			return fmt.Errorf("delete customer: %w", err)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return s.st.WithTx(ctx, func(tx store.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM provider_customers WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if err := s.st.ScrubUser(ctx, tx, userID); err != nil {
			return err
		}
		return s.st.CompleteOperationBySubject(ctx, tx, domain.OpDeleteAccount, userID)
	})
}

// --- webhooks ---------------------------------------------------------------

// HandleEvent applies a verified, deduplicated provider event. The payload
// is only used to locate our record; the resulting state is always fetched
// from the provider.
func (s *Service) HandleEvent(ctx context.Context, ev provider.Event) error {
	switch ev.Type {
	case "setup_intent.succeeded", "setup_intent.setup_failed", "setup_intent.canceled", "setup_intent.requires_action":
		attempt, err := s.st.SetupAttemptBySetupRef(ctx, s.pv.Name(), ev.ObjectID)
		if errors.Is(err, store.ErrNotFound) {
			s.log.Info("webhook for unknown setup", "type", ev.Type)
			return nil
		}
		if err != nil {
			return err
		}
		_, err = s.Reconcile(ctx, attempt.ID)
		return err
	case "payment_method.detached":
		card, err := s.st.CardByPaymentMethodRef(ctx, s.pv.Name(), ev.ObjectID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if card.State == domain.CardRemoved {
			return nil
		}
		return s.st.WithTx(ctx, func(tx store.Tx) error {
			if err := s.st.SetCardState(ctx, tx, card.ID, domain.CardRemoved); err != nil {
				return err
			}
			return s.st.CompleteOperationBySubject(ctx, tx, domain.OpDetachCard, card.ID)
		})
	case "payment_method.automatically_updated", "payment_method.updated":
		card, err := s.st.CardByPaymentMethodRef(ctx, s.pv.Name(), ev.ObjectID)
		if errors.Is(err, store.ErrNotFound) || card.State == domain.CardRemoved {
			return nil
		}
		if err != nil {
			return err
		}
		pm, err := s.pv.GetPaymentMethod(ctx, card.PaymentMethodRef)
		if err != nil {
			return err
		}
		return s.st.UpdateCardMetadata(ctx, card.ID, pm.Brand, pm.Last4, pm.ExpMonth, pm.ExpYear)
	default:
		return nil
	}
}

// --- worker entry points ----------------------------------------------------

// RunOperation executes one pending operation.
func (s *Service) RunOperation(ctx context.Context, op domain.PendingOperation) error {
	switch op.Kind {
	case domain.OpDetachCard:
		return s.RunDetach(ctx, op.SubjectID)
	case domain.OpDeleteAccount:
		return s.RunDeleteAccount(ctx, op.SubjectID)
	case domain.OpReconcileSetup:
		a, err := s.Reconcile(ctx, op.SubjectID)
		if err != nil {
			return err
		}
		if !a.Status.Terminal() {
			return errors.New("setup still processing")
		}
		return nil
	default:
		return fmt.Errorf("unknown operation kind %q", op.Kind)
	}
}

func strPtr(s string) *string { return &s }

func equalStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
