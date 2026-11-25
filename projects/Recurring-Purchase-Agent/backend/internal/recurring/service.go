package recurring

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// Service implements all deterministic workflow, policy, budget, and reconciliation logic.
type Service struct {
	Store     Store
	Clock     Clock
	Merchant  MerchantAdapter
	Checkout  CheckoutAdapter
	Optimizer *PaymentOptimizerClient

	// BeforeProviderSubmitHook is an optional hook executed strictly outside any DB lock
	// right before Checkout.SubmitCheckout. Used in tests to deterministically exercise
	// pause-vs-submission races.
	BeforeProviderSubmitHook func(occurrenceID string)
}

func NewService(store Store, clock Clock, merchant MerchantAdapter, checkout CheckoutAdapter, optimizer *PaymentOptimizerClient) *Service {
	if clock == nil {
		clock = RealClock{}
	}
	if merchant == nil {
		merchant = NewSandboxMerchantAdapter()
	}
	if checkout == nil {
		checkout = NewSandboxCheckoutAdapter()
	}
	return &Service{
		Store:     store,
		Clock:     clock,
		Merchant:  merchant,
		Checkout:  checkout,
		Optimizer: optimizer,
	}
}

// CreateRuleRequest defines input for POST /v1/recurring-rules.
type CreateRuleRequest struct {
	Name         string            `json:"name"`
	ApprovalMode ApprovalMode      `json:"approval_mode"`
	Schedule     ScheduleSpec      `json:"schedule"`
	Product      ProductConstraint `json:"product"`
}

// UpdateRuleRequest defines input for PATCH /v1/recurring-rules/{id}.
type UpdateRuleRequest struct {
	Name         *string            `json:"name,omitempty"`
	ApprovalMode *ApprovalMode      `json:"approval_mode,omitempty"`
	Schedule     *ScheduleSpec      `json:"schedule,omitempty"`
	Product      *ProductConstraint `json:"product,omitempty"`
}

// ApproveProposalRequest binds customer approval to exact proposal terms.
type ApproveProposalRequest struct {
	ExpectedQuoteDigest string      `json:"expected_quote_digest,omitempty"`
	ExpectedTotalMinor  *MinorUnits `json:"expected_total_minor,omitempty"`
	AcceptSubstitution  bool        `json:"accept_substitution,omitempty"`
	ExecuteImmediately  *bool       `json:"execute_immediately,omitempty"`
}

// GrantMandateRequest creates a scoped C3 delegated payment mandate.
type GrantMandateRequest struct {
	MaxAmount          Money  `json:"max_amount"`
	PaymentMethodID    string `json:"payment_method_id"`
	ProviderMandateRef string `json:"provider_mandate_ref"`
	TTLHours           int    `json:"ttl_hours"`
}

// MerchantWebhookEvent represents a verified merchant fulfillment/refund webhook.
type MerchantWebhookEvent struct {
	TenantID          string            `json:"tenant_id"`
	MerchantOrderID   string            `json:"merchant_order_id"`
	FulfillmentStatus FulfillmentStatus `json:"fulfillment_status,omitempty"`
	PartialRefund     *Money            `json:"partial_refund,omitempty"`
	FullRefund        bool              `json:"full_refund,omitempty"`
	Note              string            `json:"note,omitempty"`
}

func validateProductConstraint(pc *ProductConstraint) error {
	if pc == nil {
		return problem(400, "invalid_product", "Product constraint is required.")
	}
	item, ok := FindCatalogItem(pc.SKU)
	if !ok {
		return problem(400, "unknown_sku", fmt.Sprintf("Product SKU %q is not in the supported catalog.", pc.SKU))
	}
	if pc.Category == "" {
		pc.Category = item.Category
	}
	if pc.Title == "" {
		pc.Title = item.Title
	}
	if pc.PackSizeValue <= 0 {
		pc.PackSizeValue = item.PackSizeValue
	}
	if pc.PackSizeUnit == "" {
		pc.PackSizeUnit = item.PackSizeUnit
	}
	if pc.Quantity < 1 || pc.Quantity > 20 {
		return problem(400, "invalid_quantity", "Quantity must be between 1 and 20.")
	}
	if pc.PreferredMerchantID == "" {
		pc.PreferredMerchantID = item.Merchants[0].MerchantID
	} else {
		foundMerchant := false
		for _, m := range item.Merchants {
			if m.MerchantID == pc.PreferredMerchantID {
				foundMerchant = true
				break
			}
		}
		if !foundMerchant {
			return problem(400, "invalid_merchant", fmt.Sprintf("Merchant %q does not carry SKU %q.", pc.PreferredMerchantID, pc.SKU))
		}
	}
	if err := pc.MaxDeliveredPrice.Validate(false); err != nil {
		return problem(400, "invalid_max_price", "Max delivered price: "+err.Error())
	}
	if err := pc.MonthlyBudget.Validate(false); err != nil {
		return problem(400, "invalid_monthly_budget", "Monthly budget: "+err.Error())
	}
	if err := pc.MaxDeliveredPrice.SameUnit(pc.MonthlyBudget); err != nil {
		return problem(400, "currency_mismatch", err.Error())
	}
	if pc.MonthlyBudget.AmountMinor < pc.MaxDeliveredPrice.AmountMinor {
		return problem(400, "invalid_monthly_budget", "Monthly budget must be at least max delivered price.")
	}
	if pc.SubstitutionPolicy == "" {
		pc.SubstitutionPolicy = SubstitutionPolicyApprovalRequired
	}
	switch pc.SubstitutionPolicy {
	case SubstitutionPolicyNone, SubstitutionPolicyApprovalRequired, SubstitutionPolicyPreauthorizedExact:
	default:
		return problem(400, "invalid_substitution_policy", "Substitution policy must be none, approval_required, or preauthorized_exact.")
	}
	if pc.SubstitutionPolicy == SubstitutionPolicyPreauthorizedExact && len(pc.PreauthorizedSubstituteSKU) == 0 {
		return problem(400, "missing_preauthorized_skus", "preauthorized_exact requires at least one preauthorized substitute SKU.")
	}
	for _, sub := range pc.PreauthorizedSubstituteSKU {
		if _, ok := FindCatalogItem(sub); !ok {
			return problem(400, "unknown_substitute_sku", fmt.Sprintf("Preauthorized substitute SKU %q does not exist.", sub))
		}
	}
	if pc.PaymentMethodID == "" {
		pc.PaymentMethodID = DefaultPaymentMethods()[0].ID
	}
	if _, ok := FindPaymentMethod(pc.PaymentMethodID); !ok {
		return problem(400, "invalid_payment_method", fmt.Sprintf("Unknown payment method %q.", pc.PaymentMethodID))
	}
	if strings.TrimSpace(pc.ShippingPostalCode) == "" {
		pc.ShippingPostalCode = "94103"
	}
	return nil
}

func validateApprovalMode(mode ApprovalMode, paymentMethodID string, c3Enabled bool) error {
	switch mode {
	case ApprovalModeReminderOnly, ApprovalModeAssistedReorder:
		return nil
	case ApprovalModeLimitedDelegation:
		if !c3Enabled {
			return problem(403, "delegation_not_enabled", "C3 limited delegation is gated behind pilot rollout approval and provider mandate verification.")
		}
		pm, ok := FindPaymentMethod(paymentMethodID)
		if !ok || !pm.SupportsDelegation {
			return problem(400, "payment_method_no_delegation", "Selected payment method does not support provider-verified delegated execution.")
		}
		return nil
	default:
		return problem(400, "invalid_approval_mode", "Approval mode must be C1_REMINDER_ONLY, C2_ASSISTED_REORDER, or C3_LIMITED_DELEGATION.")
	}
}

func appendAudit(st *TenantState, now time.Time, actor, action, resType, resID string, detail map[string]string) {
	st.Audits = append(st.Audits, &AuditEvent{
		AuditID:      newID("aud"),
		TenantID:     st.TenantID,
		Actor:        actor,
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		Detail:       detail,
		CreatedAt:    now.UTC(),
	})
}

func enqueueNotification(st *TenantState, now time.Time, ruleID, occID, kind, message string) *NotificationEvent {
	ev := &NotificationEvent{
		EventID:      newID("ntf"),
		TenantID:     st.TenantID,
		RuleID:       ruleID,
		OccurrenceID: occID,
		Kind:         kind,
		Channel:      "in_app_and_email",
		Status:       "pending",
		Message:      message,
		CreatedAt:    now.UTC(),
	}
	st.Notifications = append(st.Notifications, ev)
	enqueueJobLocked(st, now, "dispatch_notification", ev.EventID, ev.EventID, 3)
	return ev
}

func enqueueJobLocked(st *TenantState, now time.Time, kind, dedupeKey, resourceID string, maxAttempts int) *WorkerJob {
	k := kind + "|" + dedupeKey
	if existingID, ok := st.JobDedupe[k]; ok {
		if job, exists := st.Jobs[existingID]; exists {
			return job
		}
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	job := &WorkerJob{
		JobID:         newID("job"),
		TenantID:      st.TenantID,
		Kind:          kind,
		DedupeKey:     dedupeKey,
		ResourceID:    resourceID,
		Status:        "pending",
		MaxAttempts:   maxAttempts,
		NextAttemptAt: now.UTC(),
		CreatedAt:     now.UTC(),
		UpdatedAt:     now.UTC(),
	}
	st.Jobs[job.JobID] = job
	st.JobDedupe[k] = job.JobID
	return job
}

// CreateRule creates version 1 of a recurring reorder rule.
func (s *Service) CreateRule(ctx context.Context, tenantID, actor string, req CreateRuleRequest) (*RecurringRule, error) {
	if req.ApprovalMode == "" {
		req.ApprovalMode = ApprovalModeAssistedReorder
	}
	if err := ValidateScheduleSpec(&req.Schedule); err != nil {
		return nil, err
	}
	if err := validateProductConstraint(&req.Product); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = fmt.Sprintf("%s (%d %s)", req.Product.Title, req.Product.PackSizeValue, req.Product.PackSizeUnit)
	}

	now := s.Clock.Now().UTC()
	initial, err := ComputeInitialRun(req.Schedule, now)
	if err != nil {
		return nil, problem(400, "schedule_compute_failed", err.Error())
	}

	var created *RecurringRule
	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		if err := validateApprovalMode(req.ApprovalMode, req.Product.PaymentMethodID, st.Faults.EnableC3Delegation); err != nil {
			return err
		}
		ruleID := newID("rule")
		v1 := RecurringRuleVersion{
			RuleID:       ruleID,
			TenantID:     tenantID,
			Version:      1,
			Name:         strings.TrimSpace(req.Name),
			Status:       RuleStatusActive,
			ApprovalMode: req.ApprovalMode,
			Schedule:     req.Schedule,
			Product:      req.Product,
			NextRunAtUTC: initial.UTC,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		rule := &RecurringRule{
			RuleID:         ruleID,
			TenantID:       tenantID,
			CurrentVersion: 1,
			Current:        v1,
			Versions:       []RecurringRuleVersion{v1},
		}
		st.Rules[ruleID] = rule
		appendAudit(st, now, actor, "rule.created", "rule", ruleID, map[string]string{
			"version":       "1",
			"approval_mode": string(req.ApprovalMode),
			"schedule_kind": string(req.Schedule.Kind),
			"timezone":      req.Schedule.Timezone,
			"next_run_utc":  initial.UTC.Format(time.RFC3339),
		})
		created = rule
		return nil
	})
	return created, err
}

// UpdateRule creates a new immutable RecurringRuleVersion and supersedes any pending proposals from prior versions.
func (s *Service) UpdateRule(ctx context.Context, tenantID, actor, ruleID string, req UpdateRuleRequest) (*RecurringRule, error) {
	now := s.Clock.Now().UTC()
	var updated *RecurringRule
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		rule, ok := st.Rules[ruleID]
		if !ok {
			return problem(404, "rule_not_found", "Recurring rule not found.")
		}
		if rule.Current.Status == RuleStatusCanceled {
			return problem(409, "rule_canceled", "Canceled rules cannot be edited; create a new rule instead.")
		}

		next := rule.Current
		next.Version = rule.CurrentVersion + 1
		next.UpdatedAt = now
		if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
			next.Name = strings.TrimSpace(*req.Name)
		}
		if req.Schedule != nil {
			sched := *req.Schedule
			if err := ValidateScheduleSpec(&sched); err != nil {
				return err
			}
			next.Schedule = sched
		}
		if req.Product != nil {
			prod := *req.Product
			if err := validateProductConstraint(&prod); err != nil {
				return err
			}
			next.Product = prod
		}
		if req.ApprovalMode != nil {
			next.ApprovalMode = *req.ApprovalMode
		}
		if err := validateApprovalMode(next.ApprovalMode, next.Product.PaymentMethodID, st.Faults.EnableC3Delegation); err != nil {
			return err
		}

		// Recompute next run UTC whenever schedule or timezone changes.
		if req.Schedule != nil {
			recomputed, err := ComputeInitialRun(next.Schedule, now)
			if err != nil {
				return problem(400, "schedule_compute_failed", err.Error())
			}
			next.NextRunAtUTC = recomputed.UTC
		}

		rule.CurrentVersion = next.Version
		rule.Current = next
		rule.Versions = append(rule.Versions, next)

		// Supersede pending proposals bound to older rule versions so stale approvals cannot execute.
		superseded := 0
		for _, prop := range st.Proposals {
			if prop.RuleID == ruleID && prop.Status == ProposalPending && prop.RuleVersion < next.Version {
				prop.Status = ProposalSuperseded
				dec := now
				prop.DecidedAt = &dec
				if occ, ok := st.Occurrences[prop.OccurrenceID]; ok && occ.Status == OccurrenceAwaitingApproval {
					occ.Status = OccurrenceExpired
					occ.StatusReason = fmt.Sprintf("Superseded by rule version v%d.", next.Version)
					occ.UpdatedAt = now
				}
				superseded++
			}
		}

		appendAudit(st, now, actor, "rule.updated", "rule", ruleID, map[string]string{
			"version":              fmt.Sprintf("%d", next.Version),
			"timezone":             next.Schedule.Timezone,
			"next_run_utc":         next.NextRunAtUTC.Format(time.RFC3339),
			"superseded_proposals": fmt.Sprintf("%d", superseded),
		})
		updated = rule
		return nil
	})
	return updated, err
}

// PauseRule prevents new execution under the rule while clearly distinguishing
// any already-submitted in-flight or completed order.
func (s *Service) PauseRule(ctx context.Context, tenantID, actor, ruleID, reason string) (*RecurringRule, []string, error) {
	now := s.Clock.Now().UTC()
	var updated *RecurringRule
	var inFlightOrders []string

	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		rule, ok := st.Rules[ruleID]
		if !ok {
			return problem(404, "rule_not_found", "Recurring rule not found.")
		}
		if rule.Current.Status == RuleStatusCanceled {
			return problem(409, "rule_canceled", "Canceled rule cannot be paused.")
		}
		rule.Current.Status = RuleStatusPaused
		rule.Current.PauseReason = strings.TrimSpace(reason)
		rule.Current.UpdatedAt = now
		rule.Versions[len(rule.Versions)-1] = rule.Current

		// Identify any already-executing or unresolved orders so the user knows pause
		// stops future orders and does NOT silently cancel an already-submitted merchant order.
		for _, occ := range st.Occurrences {
			if occ.RuleID != ruleID {
				continue
			}
			if occ.Status == OccurrenceExecuting || occ.Status == OccurrenceNeedsReconciliation {
				occ.PauseRacedSubmission = true
				occ.UpdatedAt = now
				inFlightOrders = append(inFlightOrders, occ.OccurrenceID)
			} else if occ.Status == OccurrenceCompleted &&
				occ.FulfillmentStatus != FulfillmentDelivered &&
				occ.FulfillmentStatus != FulfillmentCanceled {
				inFlightOrders = append(inFlightOrders, occ.OccurrenceID)
			}
		}

		msg := fmt.Sprintf("Paused rule %q. Future scheduled purchases are blocked.", rule.Current.Name)
		if len(inFlightOrders) > 0 {
			msg += fmt.Sprintf(" Note: %d already-submitted order(s) remain active and must be canceled via the merchant if still eligible.", len(inFlightOrders))
		}
		enqueueNotification(st, now, ruleID, "", "rule_paused", msg)
		appendAudit(st, now, actor, "rule.paused", "rule", ruleID, map[string]string{
			"reason":           rule.Current.PauseReason,
			"in_flight_orders": fmt.Sprintf("%d", len(inFlightOrders)),
		})
		updated = rule
		return nil
	})
	return updated, inFlightOrders, err
}

// ResumeRule reactivates a paused rule and advances NextRunAtUTC strictly into the future
// so resuming never creates a backlog of catch-up orders.
func (s *Service) ResumeRule(ctx context.Context, tenantID, actor, ruleID string) (*RecurringRule, error) {
	now := s.Clock.Now().UTC()
	var updated *RecurringRule
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		rule, ok := st.Rules[ruleID]
		if !ok {
			return problem(404, "rule_not_found", "Recurring rule not found.")
		}
		if rule.Current.Status != RuleStatusPaused {
			return problem(409, "rule_not_paused", "Rule is not currently paused.")
		}
		nextRun, err := ComputeInitialRun(rule.Current.Schedule, now)
		if err != nil {
			return err
		}
		rule.Current.Status = RuleStatusActive
		rule.Current.PauseReason = ""
		rule.Current.NextRunAtUTC = nextRun.UTC
		rule.Current.UpdatedAt = now
		rule.Versions[len(rule.Versions)-1] = rule.Current

		appendAudit(st, now, actor, "rule.resumed", "rule", ruleID, map[string]string{
			"next_run_utc": nextRun.UTC.Format(time.RFC3339),
		})
		updated = rule
		return nil
	})
	return updated, err
}

// CancelRule permanently stops a recurring rule without canceling already-submitted orders.
func (s *Service) CancelRule(ctx context.Context, tenantID, actor, ruleID string) (*RecurringRule, error) {
	now := s.Clock.Now().UTC()
	var updated *RecurringRule
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		rule, ok := st.Rules[ruleID]
		if !ok {
			return problem(404, "rule_not_found", "Recurring rule not found.")
		}
		rule.Current.Status = RuleStatusCanceled
		rule.Current.UpdatedAt = now
		rule.Versions[len(rule.Versions)-1] = rule.Current
		for _, prop := range st.Proposals {
			if prop.RuleID == ruleID && prop.Status == ProposalPending {
				prop.Status = ProposalExpired
				dec := now
				prop.DecidedAt = &dec
				if occ, ok := st.Occurrences[prop.OccurrenceID]; ok && occ.Status == OccurrenceAwaitingApproval {
					occ.Status = OccurrenceCanceled
					occ.StatusReason = "Rule canceled before approval."
					occ.UpdatedAt = now
				}
			}
		}
		appendAudit(st, now, actor, "rule.canceled", "rule", ruleID, nil)
		updated = rule
		return nil
	})
	return updated, err
}

// SkipNextOccurrence skips the single upcoming scheduled slot and advances NextRunAtUTC.
func (s *Service) SkipNextOccurrence(ctx context.Context, tenantID, actor, ruleID string) (*ScheduleOccurrence, *RecurringRule, error) {
	now := s.Clock.Now().UTC()
	var skipped *ScheduleOccurrence
	var updatedRule *RecurringRule

	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		rule, ok := st.Rules[ruleID]
		if !ok {
			return problem(404, "rule_not_found", "Recurring rule not found.")
		}
		if rule.Current.Status == RuleStatusCanceled {
			return problem(409, "rule_canceled", "Canceled rule has no upcoming occurrence.")
		}
		slotUTC := rule.Current.NextRunAtUTC.UTC()
		slotKey := SlotKey(rule.Current.Schedule, slotUTC)
		idxKey := ruleID + "|" + slotKey
		if existingID, exists := st.SlotIndex[idxKey]; exists {
			return problem(409, "occurrence_already_exists", fmt.Sprintf("Occurrence %s already exists for this slot.", existingID))
		}

		loc, err := time.LoadLocation(rule.Current.Schedule.Timezone)
		if err != nil {
			return err
		}
		occID := newID("occ")
		occ := &ScheduleOccurrence{
			OccurrenceID:          occID,
			TenantID:              tenantID,
			RuleID:                ruleID,
			RuleVersion:           rule.CurrentVersion,
			SlotKey:               slotKey,
			IdempotencyKey:        fmt.Sprintf("idem:%s:%s:%s", tenantID, ruleID, slotKey),
			ScheduledForUTC:       slotUTC,
			ScheduledLocalDisplay: slotUTC.In(loc).Format("2006-01-02 15:04 MST"),
			Status:                OccurrenceSkipped,
			PaymentStatus:         PaymentUninitiated,
			FulfillmentStatus:     FulfillmentUnfulfilled,
			ApprovalMode:          rule.Current.ApprovalMode,
			StatusReason:          "Skipped by customer before execution.",
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		st.Occurrences[occID] = occ
		st.SlotIndex[idxKey] = occID

		nextRes, err := NextRunAfter(rule.Current.Schedule, slotUTC)
		if err != nil {
			return err
		}
		rule.Current.NextRunAtUTC = nextRes.UTC
		rule.Current.SkippedSlotKeys = append(rule.Current.SkippedSlotKeys, slotKey)
		rule.Current.UpdatedAt = now
		rule.Versions[len(rule.Versions)-1] = rule.Current

		appendAudit(st, now, actor, "occurrence.skipped", "occurrence", occID, map[string]string{
			"rule_id":      ruleID,
			"slot_key":     slotKey,
			"next_run_utc": nextRes.UTC.Format(time.RFC3339),
		})
		skipped = occ
		updatedRule = rule
		return nil
	})
	return skipped, updatedRule, err
}

// GrantDelegatedMandate records a provider-backed C3 mandate bound to exact rule version, SKU, price, and expiry.
func (s *Service) GrantDelegatedMandate(ctx context.Context, tenantID, actor, ruleID string, req GrantMandateRequest) (*ConsentRecord, error) {
	now := s.Clock.Now().UTC()
	if err := req.MaxAmount.Validate(false); err != nil {
		return nil, problem(400, "invalid_max_amount", err.Error())
	}
	if req.TTLHours <= 0 {
		req.TTLHours = 24 * 30
	}
	if req.TTLHours > 24*90 {
		return nil, problem(400, "invalid_ttl", "Delegated mandate TTL cannot exceed 90 days.")
	}
	pm, ok := FindPaymentMethod(req.PaymentMethodID)
	if !ok || !pm.SupportsDelegation {
		return nil, problem(400, "payment_method_no_delegation", "Payment method does not support delegated mandates.")
	}
	if strings.TrimSpace(req.ProviderMandateRef) == "" {
		return nil, problem(400, "missing_mandate_ref", "Provider mandate reference is required.")
	}

	var created *ConsentRecord
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		if !st.Faults.EnableC3Delegation {
			return problem(403, "delegation_not_enabled", "C3 delegated mode is not enabled for this tenant.")
		}
		rule, ok := st.Rules[ruleID]
		if !ok {
			return problem(404, "rule_not_found", "Recurring rule not found.")
		}
		expiresAt := now.Add(time.Duration(req.TTLHours) * time.Hour)
		rec := &ConsentRecord{
			ConsentID:          newID("cns"),
			TenantID:           tenantID,
			Kind:               ConsentKindDelegatedMandate,
			RuleID:             ruleID,
			RuleVersion:        rule.CurrentVersion,
			MerchantID:         rule.Current.Product.PreferredMerchantID,
			SKU:                rule.Current.Product.SKU,
			MaxAmount:          req.MaxAmount,
			PaymentMethodID:    req.PaymentMethodID,
			ProviderMandateRef: strings.TrimSpace(req.ProviderMandateRef),
			GrantedBy:          actor,
			GrantedAt:          now,
			ExpiresAt:          expiresAt,
		}
		rec.SignatureDigest = computeConsentDigest(rec)
		st.Consents[rec.ConsentID] = rec
		appendAudit(st, now, actor, "consent.mandate_granted", "consent", rec.ConsentID, map[string]string{
			"rule_id":      ruleID,
			"rule_version": fmt.Sprintf("%d", rule.CurrentVersion),
			"mandate_ref":  rec.ProviderMandateRef,
		})
		created = rec
		return nil
	})
	return created, err
}

// RevokeConsent immediately revokes an approval or delegated mandate.
func (s *Service) RevokeConsent(ctx context.Context, tenantID, actor, consentID string) (*ConsentRecord, error) {
	now := s.Clock.Now().UTC()
	var updated *ConsentRecord
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		rec, ok := st.Consents[consentID]
		if !ok {
			return problem(404, "consent_not_found", "Consent record not found.")
		}
		if rec.RevokedAt == nil {
			t := now
			rec.RevokedAt = &t
		}
		appendAudit(st, now, actor, "consent.revoked", "consent", consentID, map[string]string{
			"rule_id": rec.RuleID,
			"kind":    string(rec.Kind),
		})
		updated = rec
		return nil
	})
	return updated, err
}

func computeConsentDigest(c *ConsentRecord) string {
	raw := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%s|%s|%d|%s|%s",
		c.TenantID, c.Kind, c.RuleID, c.RuleVersion, c.ProposalID,
		c.QuoteDigest, c.MerchantID, c.SKU, c.MaxAmount.AmountMinor,
		c.PaymentMethodID, c.ExpiresAt.UTC().Format(time.RFC3339))
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

// hasUnresolvedPriorOrder enforces duplicate-order prevention across pending, uncertain, or delayed orders.
func hasUnresolvedPriorOrder(st *TenantState, ruleID, excludeOccurrenceID string) (string, string) {
	for _, occ := range st.Occurrences {
		if occ.RuleID != ruleID || occ.OccurrenceID == excludeOccurrenceID {
			continue
		}
		switch occ.Status {
		case OccurrenceExecuting, OccurrenceNeedsReconciliation, OccurrenceApproved:
			return occ.OccurrenceID, fmt.Sprintf("Prior occurrence %s is still %s.", occ.OccurrenceID, occ.Status)
		}
		switch occ.PaymentStatus {
		case PaymentPending, PaymentAuthorized, PaymentTimeoutUnknown:
			return occ.OccurrenceID, fmt.Sprintf("Prior occurrence %s has unresolved payment status %s.", occ.OccurrenceID, occ.PaymentStatus)
		}
		if occ.Status == OccurrenceCompleted &&
			occ.FulfillmentStatus != FulfillmentDelivered &&
			occ.FulfillmentStatus != FulfillmentCanceled {
			return occ.OccurrenceID, fmt.Sprintf("Prior order %s is still %s; blocking duplicate reorder until delivered or canceled.", occ.OccurrenceID, occ.FulfillmentStatus)
		}
	}
	return "", ""
}

// budgetUsedMinor computes total reserved, uncertain-held, and net settled spend for a rule and YYYY-MM period.
func budgetUsedMinor(st *TenantState, ruleID, period string) int64 {
	var used int64
	for _, res := range st.Reservations {
		if res.RuleID != ruleID || res.BudgetPeriod != period {
			continue
		}
		switch res.Status {
		case ReservationReserved, ReservationUncertainHeld:
			used += int64(res.Reserved.AmountMinor)
		case ReservationSettled:
			used += int64(res.Settled.AmountMinor)
		case ReservationPartiallyRefunded:
			net := int64(res.Settled.AmountMinor) - int64(res.Refunded.AmountMinor)
			if net > 0 {
				used += net
			}
		case ReservationReleased:
			// Released reservations do not consume budget.
		}
	}
	return used
}

// ScanDueRules scans all active rules for a tenant, creating at most ONE unique
// ScheduleOccurrence per slot and coalescing multi-slot outages into a single recovery proposal.
func (s *Service) ScanDueRules(ctx context.Context, tenantID string) ([]*ScheduleOccurrence, error) {
	globalPause, err := s.Store.GetGlobalPause(ctx)
	if err != nil {
		return nil, err
	}
	if globalPause.Paused {
		return nil, nil
	}
	now := s.Clock.Now().UTC()
	var created []*ScheduleOccurrence

	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		ruleIDs := make([]string, 0, len(st.Rules))
		for id := range st.Rules {
			ruleIDs = append(ruleIDs, id)
		}
		sort.Strings(ruleIDs)

		for _, ruleID := range ruleIDs {
			rule := st.Rules[ruleID]
			if rule.Current.Status != RuleStatusActive {
				continue
			}
			window, err := ComputeDueWindow(rule.Current.Schedule, rule.Current.NextRunAtUTC, now)
			if err != nil || !window.HasDue {
				continue
			}

			slotKey := SlotKey(rule.Current.Schedule, window.LatestDueUTC)
			idxKey := ruleID + "|" + slotKey
			if _, exists := st.SlotIndex[idxKey]; exists {
				// Unique slot invariant already satisfied; ensure next run moves forward.
				if !rule.Current.NextRunAtUTC.After(now) {
					rule.Current.NextRunAtUTC = window.NextFutureUTC
					rule.Versions[len(rule.Versions)-1] = rule.Current
				}
				continue
			}

			// Duplicate-order prevention: do not start a new reorder while a prior order is unresolved or delayed.
			if blockingOccID, reason := hasUnresolvedPriorOrder(st, ruleID, ""); blockingOccID != "" {
				// Check if we already emitted a block audit for this exact slot so we don't spam.
				blockKey := "blocked|" + ruleID + "|" + slotKey
				if _, alreadyNotified := st.JobDedupe[blockKey]; !alreadyNotified {
					st.JobDedupe[blockKey] = blockingOccID
					enqueueNotification(st, now, ruleID, blockingOccID, "duplicate_blocked", reason)
					appendAudit(st, now, "worker", "occurrence.blocked_unresolved_order", "rule", ruleID, map[string]string{
						"slot_key":     slotKey,
						"blocking_occ": blockingOccID,
						"reason":       reason,
					})
				}
				continue
			}

			// Minimum elapsed interval guard (applies even to calendar rules if configured).
			if rule.Current.LastCompletedUTC != nil && rule.Current.Schedule.MinIntervalDays > 0 {
				minDur := time.Duration(rule.Current.Schedule.MinIntervalDays) * 24 * time.Hour
				if now.Sub(*rule.Current.LastCompletedUTC) < minDur {
					continue
				}
			}

			occID := newID("occ")
			isOutage := window.MissedSlotsCount > 1
			occ := &ScheduleOccurrence{
				OccurrenceID:          occID,
				TenantID:              tenantID,
				RuleID:                ruleID,
				RuleVersion:           rule.CurrentVersion,
				SlotKey:               slotKey,
				IdempotencyKey:        fmt.Sprintf("idem:%s:%s:%s", tenantID, ruleID, slotKey),
				ScheduledForUTC:       window.LatestDueUTC,
				ScheduledLocalDisplay: window.LatestLocalDisp,
				Status:                OccurrenceScheduled,
				PaymentStatus:         PaymentUninitiated,
				FulfillmentStatus:     FulfillmentUnfulfilled,
				ApprovalMode:          rule.Current.ApprovalMode,
				OutageRecovery:        isOutage,
				MissedSlotsCount:      window.MissedSlotsCount,
				CreatedAt:             now,
				UpdatedAt:             now,
			}
			if isOutage {
				occ.StatusReason = fmt.Sprintf("Outage recovery: %d missed slots coalesced into one proposed action.", window.MissedSlotsCount)
			}
			st.Occurrences[occID] = occ
			st.SlotIndex[idxKey] = occID

			// Advance NextRunAtUTC strictly past now so outages never create catch-up backlogs.
			rule.Current.NextRunAtUTC = window.NextFutureUTC
			rule.Current.UpdatedAt = now
			rule.Versions[len(rule.Versions)-1] = rule.Current

			enqueueJobLocked(st, now, "quote_occurrence", occID, occID, 5)
			appendAudit(st, now, "worker", "occurrence.created", "occurrence", occID, map[string]string{
				"rule_id":         ruleID,
				"rule_version":    fmt.Sprintf("%d", rule.CurrentVersion),
				"slot_key":        slotKey,
				"outage_recovery": fmt.Sprintf("%t", isOutage),
				"missed_slots":    fmt.Sprintf("%d", window.MissedSlotsCount),
			})
			created = append(created, occ)
		}
		return nil
	})
	return created, err
}

// QuoteOccurrence fetches a fresh merchant quote outside any DB lock, evaluates deterministic
// product/pack-size/stock/price/substitution policies, and creates an immutable PurchaseProposal.
func (s *Service) QuoteOccurrence(ctx context.Context, tenantID, occurrenceID string) (*PurchaseProposal, error) {
	now := s.Clock.Now().UTC()

	// Phase 1: short DB transaction to validate and transition scheduled -> quoting.
	var quoteReq QuoteRequest
	var faults SandboxFaultConfig
	var ruleSnap RecurringRuleVersion
	var occSnap ScheduleOccurrence
	var skip bool

	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		occ, ok := st.Occurrences[occurrenceID]
		if !ok {
			return problem(404, "occurrence_not_found", "Occurrence not found.")
		}
		if occ.Status != OccurrenceScheduled && occ.Status != OccurrenceQuoting {
			skip = true
			return nil
		}
		rule, ok := st.Rules[occ.RuleID]
		if !ok || rule.Current.Status != RuleStatusActive {
			occ.Status = OccurrenceCanceled
			occ.StatusReason = "Rule is no longer active before quoting."
			occ.UpdatedAt = now
			skip = true
			return nil
		}
		occ.Status = OccurrenceQuoting
		occ.RuleVersion = rule.CurrentVersion
		occ.UpdatedAt = now
		ruleSnap = rule.Current
		occSnap = *occ
		faults = st.Faults
		quoteReq = QuoteRequest{
			OccurrenceID:        occurrenceID,
			SKU:                 rule.Current.Product.SKU,
			PackSizeValue:       rule.Current.Product.PackSizeValue,
			PackSizeUnit:        rule.Current.Product.PackSizeUnit,
			Quantity:            rule.Current.Product.Quantity,
			PreferredMerchantID: rule.Current.Product.PreferredMerchantID,
			ShippingPostalCode:  rule.Current.Product.ShippingPostalCode,
			Now:                 now,
			TTL:                 time.Duration(rule.Current.Schedule.ProposalTTLMinutes) * time.Minute,
		}
		return nil
	})
	if err != nil || skip {
		return nil, err
	}

	// Phase 2: external merchant quote strictly OUTSIDE any DB transaction!
	quote, err := s.Merchant.Quote(ctx, quoteReq, faults)
	if err != nil {
		return nil, err
	}

	// Phase 3: short DB transaction to persist immutable quote + proposal and evaluate policy.
	var createdProposal *PurchaseProposal
	var autoExecute bool

	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		occ, ok := st.Occurrences[occurrenceID]
		if !ok {
			return problem(404, "occurrence_not_found", "Occurrence not found.")
		}
		if occ.Status != OccurrenceQuoting {
			return nil
		}
		rule, ok := st.Rules[occ.RuleID]
		if !ok || rule.Current.Status != RuleStatusActive {
			occ.Status = OccurrenceCanceled
			occ.StatusReason = "Rule paused or canceled while quoting."
			occ.UpdatedAt = now
			return nil
		}
		// Ensure rule was not edited mid-quote.
		if rule.CurrentVersion != ruleSnap.Version {
			occ.Status = OccurrenceExpired
			occ.StatusReason = fmt.Sprintf("Rule changed from v%d to v%d during quoting.", ruleSnap.Version, rule.CurrentVersion)
			occ.UpdatedAt = now
			return nil
		}

		st.Quotes[quote.QuoteID] = quote
		occ.QuoteID = quote.QuoteID

		// Deterministic policy evaluation.
		var warnings []string
		policyBlocked := false
		blockReason := ""
		reqSubApproval := false
		subSummary := ""

		// 1. Stock & partial stock check.
		if quote.StockStatus == StockOutOfStock {
			policyBlocked = true
			blockReason = "Requested item is out of stock with no authorized substitute."
			warnings = append(warnings, blockReason)
		} else if quote.StockStatus == StockPartialStock || quote.AvailableQuantity < rule.Current.Product.Quantity {
			policyBlocked = true
			blockReason = fmt.Sprintf("Partial stock: requested %d, only %d available.", rule.Current.Product.Quantity, quote.AvailableQuantity)
			warnings = append(warnings, blockReason)
		}

		// 2. Pack-size change & substitution check.
		if quote.PackSizeChanged {
			subSummary = quote.SubstitutionReason
			if rule.Current.Product.SubstitutionPolicy == SubstitutionPolicyNone {
				policyBlocked = true
				blockReason = fmt.Sprintf("Pack size changed (%d %s -> %d %s) and substitutions are disabled.",
					rule.Current.Product.PackSizeValue, rule.Current.Product.PackSizeUnit, quote.PackSizeValue, quote.PackSizeUnit)
				warnings = append(warnings, blockReason)
			} else {
				reqSubApproval = true
				warnings = append(warnings, fmt.Sprintf("Pack-size change requires explicit approval (%d %s -> %d %s).",
					rule.Current.Product.PackSizeValue, rule.Current.Product.PackSizeUnit, quote.PackSizeValue, quote.PackSizeUnit))
			}
		}

		if quote.IsSubstitution {
			subSummary = quote.SubstitutionReason
			switch rule.Current.Product.SubstitutionPolicy {
			case SubstitutionPolicyNone:
				policyBlocked = true
				blockReason = fmt.Sprintf("Merchant offered substitute %s, but substitutions are not permitted.", quote.QuotedSKU)
				warnings = append(warnings, blockReason)
			case SubstitutionPolicyApprovalRequired:
				reqSubApproval = true
				warnings = append(warnings, fmt.Sprintf("Substitute %s requires explicit customer approval.", quote.QuotedSKU))
			case SubstitutionPolicyPreauthorizedExact:
				allowed := false
				for _, sku := range rule.Current.Product.PreauthorizedSubstituteSKU {
					if sku == quote.QuotedSKU {
						allowed = true
						break
					}
				}
				if !allowed || quote.PackSizeChanged {
					reqSubApproval = true
					warnings = append(warnings, fmt.Sprintf("Substitute %s is not in preauthorized exact list; approval required.", quote.QuotedSKU))
				}
			}
		}

		// 3. Delivered price cap check.
		if quote.DeliveredTotal.AmountMinor > rule.Current.Product.MaxDeliveredPrice.AmountMinor {
			policyBlocked = true
			msg := fmt.Sprintf("Delivered price %s exceeds max limit %s.",
				quote.DeliveredTotal.FormatDisplay(), rule.Current.Product.MaxDeliveredPrice.FormatDisplay())
			if blockReason == "" {
				blockReason = msg
			}
			warnings = append(warnings, msg)
		}

		// 4. Monthly budget preview check.
		period := BudgetPeriodKey(rule.Current.Schedule.Timezone, now)
		used := budgetUsedMinor(st, rule.RuleID, period)
		if used+int64(quote.DeliveredTotal.AmountMinor) > int64(rule.Current.Product.MonthlyBudget.AmountMinor) {
			policyBlocked = true
			msg := fmt.Sprintf("Monthly budget (%s for %s) would be exceeded by %s.",
				rule.Current.Product.MonthlyBudget.FormatDisplay(), period, quote.DeliveredTotal.FormatDisplay())
			if blockReason == "" {
				blockReason = msg
			}
			warnings = append(warnings, msg)
		}

		pm, _ := FindPaymentMethod(rule.Current.Product.PaymentMethodID)
		reminderOnly := rule.Current.ApprovalMode == ApprovalModeReminderOnly

		prop := &PurchaseProposal{
			ProposalID:                   newID("prop"),
			TenantID:                     tenantID,
			RuleID:                       rule.RuleID,
			RuleVersion:                  rule.CurrentVersion,
			OccurrenceID:                 occ.OccurrenceID,
			QuoteID:                      quote.QuoteID,
			QuoteDigest:                  quote.Digest,
			MerchantID:                   quote.MerchantID,
			MerchantName:                 quote.MerchantName,
			SKU:                          quote.QuotedSKU,
			Title:                        quote.Title,
			PackSizeLabel:                fmt.Sprintf("%d %s", quote.PackSizeValue, quote.PackSizeUnit),
			Quantity:                     quote.Quantity,
			DeliveredTotal:               quote.DeliveredTotal,
			PaymentMethodID:              rule.Current.Product.PaymentMethodID,
			PaymentMethodLabel:           pm.Label,
			RequiresSubstitutionApproval: reqSubApproval,
			SubstitutionSummary:          subSummary,
			PolicyWarnings:               warnings,
			PolicyBlocked:                policyBlocked,
			BlockReason:                  blockReason,
			ReminderOnly:                 reminderOnly,
			OutageRecovery:               occSnap.OutageRecovery,
			MissedSlotsCount:             occSnap.MissedSlotsCount,
			Status:                       ProposalPending,
			CreatedAt:                    now,
			ExpiresAt:                    quote.ExpiresAt,
		}
		st.Proposals[prop.ProposalID] = prop
		occ.ProposalID = prop.ProposalID
		occ.Status = OccurrenceAwaitingApproval
		occ.UpdatedAt = now
		if policyBlocked {
			occ.StatusReason = blockReason
		} else if reminderOnly {
			occ.StatusReason = "C1 reminder generated (no automatic payment enabled)."
		} else if occ.OutageRecovery {
			occ.StatusReason = fmt.Sprintf("Outage recovery proposal (%d missed slots coalesced) awaiting customer approval.", occ.MissedSlotsCount)
		} else {
			occ.StatusReason = "Fresh quote verified; awaiting customer approval."
		}

		// Emit appropriate notification to transactional outbox.
		switch {
		case quote.StockStatus == StockOutOfStock || quote.StockStatus == StockPartialStock:
			enqueueNotification(st, now, rule.RuleID, occ.OccurrenceID, "stockout",
				fmt.Sprintf("%s: %s", rule.Current.Name, blockReason))
		case quote.DeliveredTotal.AmountMinor > rule.Current.Product.MaxDeliveredPrice.AmountMinor:
			enqueueNotification(st, now, rule.RuleID, occ.OccurrenceID, "price_cap_exceeded",
				fmt.Sprintf("%s: %s", rule.Current.Name, blockReason))
		case occ.OutageRecovery:
			enqueueNotification(st, now, rule.RuleID, occ.OccurrenceID, "outage_recovery_proposed",
				fmt.Sprintf("Recovery proposal for %s (%d missed schedules coalesced): %s delivered.",
					rule.Current.Name, occ.MissedSlotsCount, quote.DeliveredTotal.FormatDisplay()))
		case reminderOnly:
			enqueueNotification(st, now, rule.RuleID, occ.OccurrenceID, "reminder",
				fmt.Sprintf("Reminder for %s: fresh quote %s delivered at %s (no automatic payment).",
					rule.Current.Name, quote.DeliveredTotal.FormatDisplay(), quote.MerchantName))
		default:
			enqueueNotification(st, now, rule.RuleID, occ.OccurrenceID, "proposal_ready",
				fmt.Sprintf("Approve reorder for %s (%d × %s): %s delivered via %s.",
					prop.Title, prop.Quantity, prop.PackSizeLabel, prop.DeliveredTotal.FormatDisplay(), prop.MerchantName))
		}

		// Check C3 limited delegation eligibility (only when NOT outage recovery, NOT policy blocked,
		// NOT requiring substitution approval, and an active unrevoked matching mandate exists).
		if rule.Current.ApprovalMode == ApprovalModeLimitedDelegation &&
			st.Faults.EnableC3Delegation &&
			!occ.OutageRecovery &&
			!policyBlocked &&
			!reqSubApproval {
			if mandate := findValidDelegatedMandate(st, rule.RuleID, rule.CurrentVersion, prop, now); mandate != nil {
				prop.Status = ProposalApproved
				dec := now
				prop.DecidedAt = &dec
				occ.ConsentID = mandate.ConsentID
				occ.Status = OccurrenceApproved
				occ.StatusReason = fmt.Sprintf("Authorized under delegated mandate %s.", mandate.ProviderMandateRef)
				enqueueJobLocked(st, now, "execute_occurrence", occ.OccurrenceID, occ.OccurrenceID, 3)
				appendAudit(st, now, "policy_engine", "proposal.delegated_authorized", "proposal", prop.ProposalID, map[string]string{
					"consent_id":  mandate.ConsentID,
					"mandate_ref": mandate.ProviderMandateRef,
				})
				autoExecute = true
			} else {
				occ.StatusReason = "C3 delegation requested, but no valid unrevoked mandate matched; awaiting manual approval."
			}
		}

		appendAudit(st, now, "worker", "proposal.created", "proposal", prop.ProposalID, map[string]string{
			"occurrence_id":  occ.OccurrenceID,
			"delivered":      prop.DeliveredTotal.FormatDisplay(),
			"policy_blocked": fmt.Sprintf("%t", policyBlocked),
			"reminder_only":  fmt.Sprintf("%t", reminderOnly),
		})
		createdProposal = prop
		return nil
	})
	if err != nil {
		return nil, err
	}
	if autoExecute {
		_, _ = s.ExecuteOccurrence(ctx, tenantID, "policy_engine", occurrenceID)
	}
	return createdProposal, nil
}

func findValidDelegatedMandate(st *TenantState, ruleID string, ruleVersion int, prop *PurchaseProposal, now time.Time) *ConsentRecord {
	for _, c := range st.Consents {
		if c.Kind != ConsentKindDelegatedMandate {
			continue
		}
		if c.RuleID != ruleID || c.RuleVersion != ruleVersion {
			continue
		}
		if c.RevokedAt != nil || now.After(c.ExpiresAt) {
			continue
		}
		if c.MerchantID != prop.MerchantID || c.SKU != prop.SKU || c.PaymentMethodID != prop.PaymentMethodID {
			continue
		}
		if err := c.MaxAmount.SameUnit(prop.DeliveredTotal); err != nil {
			continue
		}
		if prop.DeliveredTotal.AmountMinor > c.MaxAmount.AmountMinor {
			continue
		}
		return c
	}
	return nil
}

// ApproveProposal validates customer approval against immutable proposal, rule version, expiry,
// and substitution constraints, records ConsentRecord, and optionally executes checkout.
func (s *Service) ApproveProposal(ctx context.Context, tenantID, actor, proposalID string, req ApproveProposalRequest) (*OccurrenceDetail, error) {
	globalPause, err := s.Store.GetGlobalPause(ctx)
	if err != nil {
		return nil, err
	}
	if globalPause.Paused {
		return nil, problem(503, "product_paused", "Product-level emergency pause is active: "+globalPause.Reason)
	}

	now := s.Clock.Now().UTC()
	var occurrenceID string
	execNow := true
	if req.ExecuteImmediately != nil {
		execNow = *req.ExecuteImmediately
	}

	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		prop, ok := st.Proposals[proposalID]
		if !ok {
			return problem(404, "proposal_not_found", "Purchase proposal not found.")
		}
		if prop.ReminderOnly {
			return problem(409, "reminder_only_mode", "This rule is in C1 Reminder-Only mode. Switch the rule to C2 Assisted Reorders to authorize payment.")
		}
		if prop.Status != ProposalPending {
			return problem(409, "proposal_not_pending", fmt.Sprintf("Proposal is already %s.", prop.Status))
		}
		if now.After(prop.ExpiresAt) {
			prop.Status = ProposalExpired
			dec := now
			prop.DecidedAt = &dec
			if occ, ok := st.Occurrences[prop.OccurrenceID]; ok && occ.Status == OccurrenceAwaitingApproval {
				occ.Status = OccurrenceExpired
				occ.StatusReason = "Proposal expired before approval."
				occ.UpdatedAt = now
			}
			return problem(409, "proposal_expired", "Proposal expired without purchasing.")
		}
		if prop.PolicyBlocked {
			return problem(409, "policy_blocked", "Proposal cannot be approved because policy check failed: "+prop.BlockReason)
		}
		if prop.RequiresSubstitutionApproval && !req.AcceptSubstitution {
			return problem(409, "substitution_approval_required", "This proposal includes a substitution or pack-size change and requires explicit accept_substitution confirmation.")
		}
		if req.ExpectedQuoteDigest != "" && req.ExpectedQuoteDigest != prop.QuoteDigest {
			return problem(409, "quote_mismatch", "Proposal quote digest does not match reviewed quote.")
		}
		if req.ExpectedTotalMinor != nil && *req.ExpectedTotalMinor != prop.DeliveredTotal.AmountMinor {
			return problem(409, "amount_mismatch", "Proposal delivered total does not match reviewed amount.")
		}

		rule, ok := st.Rules[prop.RuleID]
		if !ok {
			return problem(404, "rule_not_found", "Associated recurring rule not found.")
		}
		if rule.Current.Status != RuleStatusActive {
			return problem(409, "rule_not_active", fmt.Sprintf("Rule is %s; cannot approve purchase.", rule.Current.Status))
		}
		if rule.CurrentVersion != prop.RuleVersion {
			prop.Status = ProposalSuperseded
			dec := now
			prop.DecidedAt = &dec
			return problem(409, "stale_approval", fmt.Sprintf("Proposal was generated for rule v%d, but current rule is v%d.", prop.RuleVersion, rule.CurrentVersion))
		}

		occ, ok := st.Occurrences[prop.OccurrenceID]
		if !ok {
			return problem(404, "occurrence_not_found", "Associated occurrence not found.")
		}
		if occ.Status != OccurrenceAwaitingApproval {
			return problem(409, "invalid_occurrence_state", fmt.Sprintf("Occurrence is in state %s.", occ.Status))
		}

		if blockingOccID, reason := hasUnresolvedPriorOrder(st, rule.RuleID, occ.OccurrenceID); blockingOccID != "" {
			return problem(409, "unresolved_prior_order", reason)
		}

		// Record immutable customer consent bound to this exact proposal & quote digest.
		consent := &ConsentRecord{
			ConsentID:       newID("cns"),
			TenantID:        tenantID,
			Kind:            ConsentKindProposalApproval,
			RuleID:          rule.RuleID,
			RuleVersion:     rule.CurrentVersion,
			ProposalID:      prop.ProposalID,
			QuoteDigest:     prop.QuoteDigest,
			MerchantID:      prop.MerchantID,
			SKU:             prop.SKU,
			MaxAmount:       prop.DeliveredTotal,
			PaymentMethodID: prop.PaymentMethodID,
			GrantedBy:       actor,
			GrantedAt:       now,
			ExpiresAt:       prop.ExpiresAt,
		}
		consent.SignatureDigest = computeConsentDigest(consent)
		st.Consents[consent.ConsentID] = consent

		dec := now
		prop.Status = ProposalApproved
		prop.DecidedAt = &dec
		occ.ConsentID = consent.ConsentID
		occ.Status = OccurrenceApproved
		occ.StatusReason = "Approved by customer; queued for execution."
		occ.UpdatedAt = now
		occurrenceID = occ.OccurrenceID

		enqueueJobLocked(st, now, "execute_occurrence", occ.OccurrenceID, occ.OccurrenceID, 3)
		appendAudit(st, now, actor, "proposal.approved", "proposal", prop.ProposalID, map[string]string{
			"occurrence_id": occ.OccurrenceID,
			"consent_id":    consent.ConsentID,
			"total":         prop.DeliveredTotal.FormatDisplay(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	if execNow {
		if _, err := s.ExecuteOccurrence(ctx, tenantID, actor, occurrenceID); err != nil {
			// Return policy/budget errors directly if Phase 1 rejected; provider timeouts
			// are persisted as needs_reconciliation and returned cleanly via detail.
			var prob *Problem
			if errors.As(err, &prob) {
				return nil, prob
			}
		}
	}
	return s.GetOccurrenceDetail(ctx, tenantID, occurrenceID)
}

// RejectProposal declines a pending proposal without purchasing.
func (s *Service) RejectProposal(ctx context.Context, tenantID, actor, proposalID, reason string) (*PurchaseProposal, error) {
	now := s.Clock.Now().UTC()
	var updated *PurchaseProposal
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		prop, ok := st.Proposals[proposalID]
		if !ok {
			return problem(404, "proposal_not_found", "Purchase proposal not found.")
		}
		if prop.Status != ProposalPending {
			return problem(409, "proposal_not_pending", fmt.Sprintf("Proposal is already %s.", prop.Status))
		}
		dec := now
		prop.Status = ProposalRejected
		prop.DecidedAt = &dec
		if occ, ok := st.Occurrences[prop.OccurrenceID]; ok && occ.Status == OccurrenceAwaitingApproval {
			occ.Status = OccurrenceCanceled
			if strings.TrimSpace(reason) != "" {
				occ.StatusReason = "Declined by customer: " + strings.TrimSpace(reason)
			} else {
				occ.StatusReason = "Declined by customer."
			}
			occ.UpdatedAt = now
		}
		appendAudit(st, now, actor, "proposal.rejected", "proposal", proposalID, map[string]string{
			"reason": strings.TrimSpace(reason),
		})
		updated = prop
		return nil
	})
	return updated, err
}

// ExecuteOccurrence performs execution-time policy rechecks, atomically reserves monthly budget,
// commits executing state, calls the checkout adapter OUTSIDE any DB transaction, and records
// the outcome (treating timeouts as unknown outcomes requiring reconciliation).
func (s *Service) ExecuteOccurrence(ctx context.Context, tenantID, actor, occurrenceID string) (*OccurrenceDetail, error) {
	globalPause, err := s.Store.GetGlobalPause(ctx)
	if err != nil {
		return nil, err
	}
	if globalPause.Paused {
		return nil, problem(503, "product_paused", "Execution blocked by product-level emergency pause.")
	}

	now := s.Clock.Now().UTC()
	var submitReq CheckoutSubmitRequest
	var faults SandboxFaultConfig
	var reservationID string
	var checkoutID string
	var shouldCallProvider bool

	// Phase 1: Atomic recheck + budget reservation + executing transition.
	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		occ, ok := st.Occurrences[occurrenceID]
		if !ok {
			return problem(404, "occurrence_not_found", "Occurrence not found.")
		}
		// Idempotent guard: never submit a second checkout for an occurrence already executing/completed/reconciling.
		if occ.Status == OccurrenceExecuting || occ.Status == OccurrenceCompleted || occ.Status == OccurrenceNeedsReconciliation {
			return nil
		}
		if occ.Status != OccurrenceApproved {
			return problem(409, "occurrence_not_approved", fmt.Sprintf("Occurrence is %s; cannot execute.", occ.Status))
		}
		rule, ok := st.Rules[occ.RuleID]
		if !ok {
			return problem(404, "rule_not_found", "Recurring rule not found.")
		}
		if rule.Current.Status != RuleStatusActive {
			occ.Status = OccurrenceCanceled
			occ.StatusReason = fmt.Sprintf("Execution blocked because rule is %s.", rule.Current.Status)
			occ.UpdatedAt = now
			appendAudit(st, now, actor, "execution.blocked_rule_paused", "occurrence", occurrenceID, map[string]string{
				"rule_status": string(rule.Current.Status),
			})
			return problem(409, "rule_not_active", occ.StatusReason)
		}
		if rule.CurrentVersion != occ.RuleVersion {
			occ.Status = OccurrenceExpired
			occ.StatusReason = fmt.Sprintf("Execution blocked: rule changed to v%d (proposal was v%d).", rule.CurrentVersion, occ.RuleVersion)
			occ.UpdatedAt = now
			return problem(409, "stale_rule_version", occ.StatusReason)
		}

		prop, ok := st.Proposals[occ.ProposalID]
		if !ok {
			return problem(404, "proposal_not_found", "Proposal not found.")
		}
		if prop.ReminderOnly {
			return problem(409, "reminder_only_mode", "C1 reminder proposals cannot execute payment.")
		}
		if now.After(prop.ExpiresAt) {
			prop.Status = ProposalExpired
			occ.Status = OccurrenceExpired
			occ.StatusReason = "Proposal expired before execution."
			occ.UpdatedAt = now
			return problem(409, "proposal_expired", occ.StatusReason)
		}

		// Verify ConsentRecord (unrevoked, unexpired, exact match on merchant, SKU, amount, rule version).
		consent, ok := st.Consents[occ.ConsentID]
		if !ok || consent.RevokedAt != nil || now.After(consent.ExpiresAt) {
			occ.Status = OccurrenceCanceled
			occ.StatusReason = "Consent missing, expired, or revoked at execution time."
			occ.UpdatedAt = now
			appendAudit(st, now, actor, "execution.blocked_consent_revoked", "occurrence", occurrenceID, nil)
			return problem(403, "consent_revoked_or_expired", occ.StatusReason)
		}
		if consent.RuleID != rule.RuleID || consent.RuleVersion != rule.CurrentVersion ||
			consent.MerchantID != prop.MerchantID || consent.SKU != prop.SKU ||
			consent.PaymentMethodID != prop.PaymentMethodID ||
			prop.DeliveredTotal.AmountMinor > consent.MaxAmount.AmountMinor {
			occ.Status = OccurrenceCanceled
			occ.StatusReason = "Consent scope does not match current execution terms."
			occ.UpdatedAt = now
			return problem(403, "consent_mismatch", occ.StatusReason)
		}
		if consent.Kind == ConsentKindProposalApproval && (consent.ProposalID != prop.ProposalID || consent.QuoteDigest != prop.QuoteDigest) {
			return problem(403, "consent_mismatch", "Approval consent is not bound to this exact proposal.")
		}

		// Recheck unresolved prior orders immediately before submission.
		if blockingOccID, reason := hasUnresolvedPriorOrder(st, rule.RuleID, occ.OccurrenceID); blockingOccID != "" {
			occ.Status = OccurrenceFailed
			occ.StatusReason = reason
			occ.UpdatedAt = now
			return problem(409, "unresolved_prior_order", reason)
		}

		// Recheck minimum elapsed interval.
		if rule.Current.LastCompletedUTC != nil && rule.Current.Schedule.MinIntervalDays > 0 {
			minDur := time.Duration(rule.Current.Schedule.MinIntervalDays) * 24 * time.Hour
			if now.Sub(*rule.Current.LastCompletedUTC) < minDur {
				occ.Status = OccurrenceFailed
				occ.StatusReason = "Minimum interval between purchases has not elapsed."
				occ.UpdatedAt = now
				return problem(409, "min_interval_not_elapsed", occ.StatusReason)
			}
		}

		// Atomic monthly budget reservation including shipping & tax.
		period := BudgetPeriodKey(rule.Current.Schedule.Timezone, now)
		used := budgetUsedMinor(st, rule.RuleID, period)
		needed := int64(prop.DeliveredTotal.AmountMinor)
		if used+needed > int64(rule.Current.Product.MonthlyBudget.AmountMinor) {
			occ.Status = OccurrenceFailed
			occ.StatusReason = fmt.Sprintf("Monthly budget cap %s for %s exceeded (used %d + needed %d).",
				rule.Current.Product.MonthlyBudget.FormatDisplay(), period, used, needed)
			occ.UpdatedAt = now
			appendAudit(st, now, actor, "execution.blocked_budget", "occurrence", occurrenceID, map[string]string{
				"period": period,
				"used":   fmt.Sprintf("%d", used),
				"needed": fmt.Sprintf("%d", needed),
			})
			return problem(409, "monthly_budget_exceeded", occ.StatusReason)
		}

		res := &BudgetReservation{
			ReservationID: newID("res"),
			TenantID:      tenantID,
			RuleID:        rule.RuleID,
			OccurrenceID:  occ.OccurrenceID,
			BudgetPeriod:  period,
			Reserved:      prop.DeliveredTotal,
			Settled:       USD(0),
			Refunded:      USD(0),
			Status:        ReservationReserved,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		st.Reservations[res.ReservationID] = res
		occ.ReservationID = res.ReservationID
		reservationID = res.ReservationID

		chk := &CheckoutReference{
			CheckoutID:        newID("chk"),
			OccurrenceID:      occ.OccurrenceID,
			IdempotencyKey:    occ.IdempotencyKey,
			Provider:          "sandbox_direct",
			MerchantID:        prop.MerchantID,
			PaymentMethodID:   prop.PaymentMethodID,
			PaymentStatus:     PaymentPending,
			FulfillmentStatus: FulfillmentUnfulfilled,
			ChargedAmount:     prop.DeliveredTotal,
			RefundedAmount:    USD(0),
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		st.Checkouts[chk.CheckoutID] = chk
		occ.CheckoutID = chk.CheckoutID
		checkoutID = chk.CheckoutID

		occ.Status = OccurrenceExecuting
		occ.PaymentStatus = PaymentPending
		occ.StatusReason = "Budget reserved; submitting checkout to provider."
		occ.UpdatedAt = now

		submitReq = CheckoutSubmitRequest{
			IdempotencyKey:  occ.IdempotencyKey,
			OccurrenceID:    occ.OccurrenceID,
			ProposalID:      prop.ProposalID,
			MerchantID:      prop.MerchantID,
			SKU:             prop.SKU,
			Quantity:        prop.Quantity,
			DeliveredTotal:  prop.DeliveredTotal,
			PaymentMethodID: prop.PaymentMethodID,
			ConsentID:       consent.ConsentID,
		}
		faults = st.Faults
		shouldCallProvider = true
		appendAudit(st, now, actor, "execution.submitted", "occurrence", occurrenceID, map[string]string{
			"reservation_id":  res.ReservationID,
			"checkout_id":     chk.CheckoutID,
			"idempotency_key": occ.IdempotencyKey,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !shouldCallProvider {
		return s.GetOccurrenceDetail(ctx, tenantID, occurrenceID)
	}

	if s.BeforeProviderSubmitHook != nil {
		s.BeforeProviderSubmitHook(occurrenceID)
	}

	// Phase 2: External provider submission strictly OUTSIDE any DB transaction!
	outcome, callErr := s.Checkout.SubmitCheckout(ctx, submitReq, faults)

	// Phase 3: Commit provider outcome atomically.
	afterNow := s.Clock.Now().UTC()
	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		occ := st.Occurrences[occurrenceID]
		res := st.Reservations[reservationID]
		chk := st.Checkouts[checkoutID]
		rule := st.Rules[occ.RuleID]

		if callErr != nil {
			// Treat provider timeout or transport failure as UNKNOWN outcome.
			// Hold budget reservation and schedule reconciliation; NEVER retry payment blindly!
			chk.PaymentStatus = PaymentTimeoutUnknown
			chk.LastError = callErr.Error()
			chk.UpdatedAt = afterNow

			res.Status = ReservationUncertainHeld
			res.UpdatedAt = afterNow

			occ.Status = OccurrenceNeedsReconciliation
			occ.PaymentStatus = PaymentTimeoutUnknown
			occ.StatusReason = "Provider timed out with unknown outcome. Budget remains reserved pending reconciliation; no duplicate payment will be submitted."
			occ.UpdatedAt = afterNow

			enqueueJobLocked(st, afterNow, "reconcile_checkout", occ.OccurrenceID, occ.OccurrenceID, 10)
			enqueueNotification(st, afterNow, occ.RuleID, occ.OccurrenceID, "reconciliation_needed", occ.StatusReason)
			appendAudit(st, afterNow, "checkout_adapter", "execution.timeout_unknown", "occurrence", occurrenceID, map[string]string{
				"error": callErr.Error(),
			})
			return nil
		}

		chk.Provider = outcome.Provider
		chk.MerchantOrderID = outcome.MerchantOrderID
		chk.PaymentIntentID = outcome.PaymentIntentID
		chk.PaymentStatus = outcome.PaymentStatus
		chk.FulfillmentStatus = outcome.FulfillmentStatus
		chk.LastError = outcome.ErrorMessage
		chk.UpdatedAt = afterNow

		if outcome.PaymentStatus == PaymentSettled {
			res.Status = ReservationSettled
			res.Settled = chk.ChargedAmount
			res.UpdatedAt = afterNow

			occ.Status = OccurrenceCompleted
			occ.PaymentStatus = PaymentSettled
			occ.FulfillmentStatus = outcome.FulfillmentStatus
			occ.UpdatedAt = afterNow

			if rule != nil {
				completedAt := afterNow
				rule.Current.LastCompletedUTC = &completedAt
				rule.Current.UpdatedAt = afterNow
				rule.Versions[len(rule.Versions)-1] = rule.Current
				if rule.Current.Status == RuleStatusPaused {
					occ.PauseRacedSubmission = true
					occ.StatusReason = fmt.Sprintf("Order %s was already in flight when rule was paused. Future orders are stopped; use Cancel Order to request merchant cancellation.", outcome.MerchantOrderID)
				} else if outcome.FulfillmentStatus == FulfillmentDelayed {
					occ.StatusReason = fmt.Sprintf("Payment settled (order %s), but merchant fulfillment is delayed. Next scheduled reorder will wait until resolved.", outcome.MerchantOrderID)
				} else {
					occ.StatusReason = fmt.Sprintf("Payment settled and order %s confirmed by merchant.", outcome.MerchantOrderID)
				}
			}

			notifKind := "order_submitted"
			if outcome.FulfillmentStatus == FulfillmentDelayed {
				notifKind = "order_delayed"
			}
			enqueueNotification(st, afterNow, occ.RuleID, occ.OccurrenceID, notifKind, occ.StatusReason)
			appendAudit(st, afterNow, "checkout_adapter", "execution.completed", "occurrence", occurrenceID, map[string]string{
				"merchant_order_id":  outcome.MerchantOrderID,
				"payment_status":     string(outcome.PaymentStatus),
				"fulfillment_status": string(outcome.FulfillmentStatus),
				"pause_raced":        fmt.Sprintf("%t", occ.PauseRacedSubmission),
			})
			return nil
		}

		// Definitive payment failure -> release budget reservation.
		res.Status = ReservationReleased
		res.UpdatedAt = afterNow

		occ.Status = OccurrenceFailed
		occ.PaymentStatus = PaymentFailed
		occ.FulfillmentStatus = FulfillmentUnfulfilled
		occ.StatusReason = "Payment failed: " + outcome.ErrorMessage
		occ.UpdatedAt = afterNow

		enqueueNotification(st, afterNow, occ.RuleID, occ.OccurrenceID, "order_failed", occ.StatusReason)
		appendAudit(st, afterNow, "checkout_adapter", "execution.failed", "occurrence", occurrenceID, map[string]string{
			"error": outcome.ErrorMessage,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetOccurrenceDetail(ctx, tenantID, occurrenceID)
}

// ReconcileOccurrence resolves an uncertain timeout_unknown payment by querying the provider
// with the original idempotency key (never submitting a second charge).
func (s *Service) ReconcileOccurrence(ctx context.Context, tenantID, actor, occurrenceID string) (*OccurrenceDetail, error) {
	st, err := s.Store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	occ, ok := st.Occurrences[occurrenceID]
	if !ok {
		return nil, problem(404, "occurrence_not_found", "Occurrence not found.")
	}
	if occ.Status != OccurrenceNeedsReconciliation {
		return s.GetOccurrenceDetail(ctx, tenantID, occurrenceID)
	}
	chk, ok := st.Checkouts[occ.CheckoutID]
	if !ok {
		return nil, problem(404, "checkout_not_found", "Checkout reference not found.")
	}

	// Query provider strictly OUTSIDE DB lock.
	outcome, err := s.Checkout.ReconcileCheckout(ctx, chk.IdempotencyKey)
	if err != nil {
		return nil, err
	}

	now := s.Clock.Now().UTC()
	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		occ := st.Occurrences[occurrenceID]
		if occ.Status != OccurrenceNeedsReconciliation {
			return nil
		}
		chk := st.Checkouts[occ.CheckoutID]
		res := st.Reservations[occ.ReservationID]
		rule := st.Rules[occ.RuleID]

		t := now
		chk.LastReconciledAt = &t
		chk.MerchantOrderID = outcome.MerchantOrderID
		chk.PaymentIntentID = outcome.PaymentIntentID
		chk.PaymentStatus = outcome.PaymentStatus
		chk.FulfillmentStatus = outcome.FulfillmentStatus
		chk.LastError = outcome.ErrorMessage
		chk.UpdatedAt = now

		switch outcome.PaymentStatus {
		case PaymentSettled:
			res.Status = ReservationSettled
			res.Settled = chk.ChargedAmount
			res.UpdatedAt = now
			occ.Status = OccurrenceCompleted
			occ.PaymentStatus = PaymentSettled
			occ.FulfillmentStatus = outcome.FulfillmentStatus
			occ.StatusReason = fmt.Sprintf("Reconciled: original payment settled (order %s) without duplicate charge.", outcome.MerchantOrderID)
			occ.UpdatedAt = now
			if rule != nil {
				completedAt := now
				rule.Current.LastCompletedUTC = &completedAt
				rule.Versions[len(rule.Versions)-1] = rule.Current
			}
			enqueueNotification(st, now, occ.RuleID, occ.OccurrenceID, "order_reconciled", occ.StatusReason)
		case PaymentFailed:
			res.Status = ReservationReleased
			res.UpdatedAt = now
			occ.Status = OccurrenceFailed
			occ.PaymentStatus = PaymentFailed
			occ.FulfillmentStatus = FulfillmentUnfulfilled
			occ.StatusReason = "Reconciled: provider confirmed payment failed; budget reservation released."
			occ.UpdatedAt = now
			enqueueNotification(st, now, occ.RuleID, occ.OccurrenceID, "order_reconciled", occ.StatusReason)
		default:
			// Still unknown — keep reservation held!
			occ.UpdatedAt = now
		}

		appendAudit(st, now, actor, "checkout.reconciled", "occurrence", occurrenceID, map[string]string{
			"payment_status":     string(outcome.PaymentStatus),
			"fulfillment_status": string(outcome.FulfillmentStatus),
			"merchant_order_id":  outcome.MerchantOrderID,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetOccurrenceDetail(ctx, tenantID, occurrenceID)
}

// CancelSubmittedOrder attempts merchant cancellation for an already-submitted order
// (explicitly distinct from pausing a recurring rule).
func (s *Service) CancelSubmittedOrder(ctx context.Context, tenantID, actor, occurrenceID string) (*OccurrenceDetail, error) {
	st, err := s.Store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	occ, ok := st.Occurrences[occurrenceID]
	if !ok {
		return nil, problem(404, "occurrence_not_found", "Occurrence not found.")
	}
	chk, ok := st.Checkouts[occ.CheckoutID]
	if !ok || chk.MerchantOrderID == "" {
		return nil, problem(409, "no_submitted_order", "No submitted merchant order exists for this occurrence.")
	}
	if chk.FulfillmentStatus == FulfillmentCanceled {
		return s.GetOccurrenceDetail(ctx, tenantID, occurrenceID)
	}

	// External merchant cancel call outside DB lock.
	resCancel, err := s.Checkout.CancelOrder(ctx, chk.MerchantOrderID)
	if err != nil {
		return nil, err
	}

	now := s.Clock.Now().UTC()
	err = s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		occ := st.Occurrences[occurrenceID]
		chk := st.Checkouts[occ.CheckoutID]
		res := st.Reservations[occ.ReservationID]

		chk.CancelRequested = true
		chk.CancelOutcome = resCancel.Message
		chk.PaymentStatus = resCancel.PaymentStatus
		chk.FulfillmentStatus = resCancel.FulfillmentStatus
		chk.UpdatedAt = now

		if resCancel.Canceled {
			chk.RefundedAmount = chk.ChargedAmount
			if res != nil {
				res.Status = ReservationReleased
				res.Refunded = chk.ChargedAmount
				res.UpdatedAt = now
			}
			occ.Status = OccurrenceCanceled
			occ.PaymentStatus = resCancel.PaymentStatus
			occ.FulfillmentStatus = FulfillmentCanceled
			occ.StatusReason = resCancel.Message
			occ.UpdatedAt = now
		} else {
			occ.UpdatedAt = now
		}

		appendAudit(st, now, actor, "order.cancel_requested", "occurrence", occurrenceID, map[string]string{
			"merchant_order_id": chk.MerchantOrderID,
			"canceled":          fmt.Sprintf("%t", resCancel.Canceled),
			"message":           resCancel.Message,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !resCancel.Canceled {
		return nil, problem(409, "cancel_rejected", resCancel.Message)
	}
	return s.GetOccurrenceDetail(ctx, tenantID, occurrenceID)
}

// HandleMerchantWebhook updates independent fulfillment and partial/full refund state.
func (s *Service) HandleMerchantWebhook(ctx context.Context, ev MerchantWebhookEvent) (*OccurrenceDetail, error) {
	now := s.Clock.Now().UTC()
	var matchedOccID string

	err := s.Store.Update(ctx, ev.TenantID, func(st *TenantState) error {
		var chk *CheckoutReference
		for _, c := range st.Checkouts {
			if c.MerchantOrderID == ev.MerchantOrderID {
				chk = c
				break
			}
		}
		if chk == nil {
			return problem(404, "order_not_found", "Merchant order not found for tenant.")
		}
		occ := st.Occurrences[chk.OccurrenceID]
		res := st.Reservations[occ.ReservationID]
		matchedOccID = occ.OccurrenceID

		if ev.FulfillmentStatus != "" {
			chk.FulfillmentStatus = ev.FulfillmentStatus
			occ.FulfillmentStatus = ev.FulfillmentStatus
		}
		if ev.FullRefund {
			chk.RefundedAmount = chk.ChargedAmount
			chk.PaymentStatus = PaymentRefunded
			occ.PaymentStatus = PaymentRefunded
			if res != nil {
				res.Refunded = chk.ChargedAmount
				res.Status = ReservationReleased
				res.UpdatedAt = now
			}
		} else if ev.PartialRefund != nil {
			if err := ev.PartialRefund.Validate(false); err != nil {
				return problem(400, "invalid_refund", err.Error())
			}
			newRefunded, err := chk.RefundedAmount.Add(*ev.PartialRefund)
			if err != nil {
				return err
			}
			if newRefunded.AmountMinor > chk.ChargedAmount.AmountMinor {
				return problem(400, "refund_exceeds_charge", "Refund exceeds charged amount.")
			}
			chk.RefundedAmount = newRefunded
			if newRefunded.AmountMinor == chk.ChargedAmount.AmountMinor {
				chk.PaymentStatus = PaymentRefunded
				occ.PaymentStatus = PaymentRefunded
				if res != nil {
					res.Refunded = newRefunded
					res.Status = ReservationReleased
					res.UpdatedAt = now
				}
			} else {
				chk.PaymentStatus = PaymentPartiallyRefunded
				occ.PaymentStatus = PaymentPartiallyRefunded
				if res != nil {
					res.Refunded = newRefunded
					res.Status = ReservationPartiallyRefunded
					res.UpdatedAt = now
				}
			}
		}
		if ev.Note != "" {
			occ.StatusReason = ev.Note
		}
		chk.UpdatedAt = now
		occ.UpdatedAt = now
		appendAudit(st, now, "merchant_webhook", "order.webhook_applied", "occurrence", occ.OccurrenceID, map[string]string{
			"merchant_order_id":  ev.MerchantOrderID,
			"fulfillment_status": string(occ.FulfillmentStatus),
			"payment_status":     string(occ.PaymentStatus),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetOccurrenceDetail(ctx, ev.TenantID, matchedOccID)
}

// ExpireStaleProposals marks unapproved proposals past ExpiresAt as expired without purchasing.
func (s *Service) ExpireStaleProposals(ctx context.Context, tenantID string) (int, error) {
	now := s.Clock.Now().UTC()
	expiredCount := 0
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		for _, prop := range st.Proposals {
			if prop.Status != ProposalPending || !now.After(prop.ExpiresAt) {
				continue
			}
			dec := now
			prop.Status = ProposalExpired
			prop.DecidedAt = &dec
			if occ, ok := st.Occurrences[prop.OccurrenceID]; ok && occ.Status == OccurrenceAwaitingApproval {
				occ.Status = OccurrenceExpired
				occ.StatusReason = "Proposal expired without approval; no purchase was made."
				occ.UpdatedAt = now
				enqueueNotification(st, now, occ.RuleID, occ.OccurrenceID, "proposal_expired",
					fmt.Sprintf("Unapproved proposal for %s expired with no charge.", prop.Title))
			}
			appendAudit(st, now, "worker", "proposal.expired", "proposal", prop.ProposalID, map[string]string{
				"occurrence_id": prop.OccurrenceID,
			})
			expiredCount++
		}
		return nil
	})
	return expiredCount, err
}

// GetOccurrenceDetail returns scheduling, proposal, consent, budget, payment, and fulfillment details.
func (s *Service) GetOccurrenceDetail(ctx context.Context, tenantID, occurrenceID string) (*OccurrenceDetail, error) {
	st, err := s.Store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	occ, ok := st.Occurrences[occurrenceID]
	if !ok {
		return nil, problem(404, "occurrence_not_found", "Occurrence not found.")
	}
	detail := &OccurrenceDetail{Occurrence: occ}
	if occ.QuoteID != "" {
		detail.Quote = st.Quotes[occ.QuoteID]
	}
	if occ.ProposalID != "" {
		detail.Proposal = st.Proposals[occ.ProposalID]
	}
	if occ.ConsentID != "" {
		detail.Consent = st.Consents[occ.ConsentID]
	}
	if occ.ReservationID != "" {
		detail.Reservation = st.Reservations[occ.ReservationID]
	}
	if occ.CheckoutID != "" {
		detail.Checkout = st.Checkouts[occ.CheckoutID]
	}
	return detail, nil
}

// ListRuleOccurrences explains upcoming and past occurrences for a rule sorted newest-first.
func (s *Service) ListRuleOccurrences(ctx context.Context, tenantID, ruleID string) ([]*OccurrenceDetail, error) {
	st, err := s.Store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if _, ok := st.Rules[ruleID]; !ok {
		return nil, problem(404, "rule_not_found", "Recurring rule not found.")
	}
	var out []*OccurrenceDetail
	for _, occ := range st.Occurrences {
		if occ.RuleID != ruleID {
			continue
		}
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
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Occurrence.ScheduledForUTC.After(out[j].Occurrence.ScheduledForUTC)
	})
	return out, nil
}

// SetSandboxFaults updates tenant-scoped sandbox fault injection settings for demos and validation.
func (s *Service) SetSandboxFaults(ctx context.Context, tenantID, actor string, cfg SandboxFaultConfig) (SandboxFaultConfig, error) {
	if cfg.PriceMultiplierBps <= 0 {
		cfg.PriceMultiplierBps = 10000
	}
	now := s.Clock.Now().UTC()
	err := s.Store.Update(ctx, tenantID, func(st *TenantState) error {
		st.Faults = cfg
		appendAudit(st, now, actor, "sandbox.faults_updated", "tenant", tenantID, map[string]string{
			"price_bps":        fmt.Sprintf("%d", cfg.PriceMultiplierBps),
			"stock_status":     cfg.ForceStockStatus,
			"payment_behavior": cfg.ForcePaymentBehavior,
			"c3_enabled":       fmt.Sprintf("%t", cfg.EnableC3Delegation),
		})
		return nil
	})
	return cfg, err
}

// SetGlobalPause toggles the operator-only product-level emergency pause control.
func (s *Service) SetGlobalPause(ctx context.Context, actor string, paused bool, reason string) (GlobalPauseState, error) {
	state := GlobalPauseState{
		Paused:    paused,
		Reason:    strings.TrimSpace(reason),
		UpdatedBy: actor,
		UpdatedAt: s.Clock.Now().UTC(),
	}
	if err := s.Store.SetGlobalPause(ctx, state); err != nil {
		return GlobalPauseState{}, err
	}
	return state, nil
}
