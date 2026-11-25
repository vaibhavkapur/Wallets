package recurring

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testHarness struct {
	store    *FileStore
	clock    *FakeClock
	merchant *SandboxMerchantAdapter
	checkout *SandboxCheckoutAdapter
	svc      *Service
	worker   *Worker
}

func newHarness(t *testing.T, startUTC time.Time) *testHarness {
	t.Helper()
	store, err := NewFileStore("")
	if err != nil {
		t.Fatalf("filestore: %v", err)
	}
	t.Cleanup(store.Close)
	clk := NewFakeClock(startUTC)
	merch := NewSandboxMerchantAdapter()
	chk := NewSandboxCheckoutAdapter()
	svc := NewService(store, clk, merch, chk, nil)
	wrk := NewWorker(svc, "test-worker-1")
	return &testHarness{
		store:    store,
		clock:    clk,
		merchant: merch,
		checkout: chk,
		svc:      svc,
		worker:   wrk,
	}
}

func defaultCoffeeRuleReq(mode ApprovalMode) CreateRuleRequest {
	return CreateRuleRequest{
		Name:         "Morning Ethiopia Coffee",
		ApprovalMode: mode,
		Schedule: ScheduleSpec{
			Kind:               ScheduleKindInterval,
			Timezone:           "America/New_York",
			DSTPolicy:          DSTPolicyShiftForwardFirst,
			LeapDayPolicy:      LeapDayPolicyClampMonthEnd,
			LocalHour:          9,
			LocalMinute:        0,
			IntervalDays:       14,
			MinIntervalDays:    10,
			ProposalTTLMinutes: 120,
		},
		Product: ProductConstraint{
			SKU:                "COFFEE-ETHIOPIA-12OZ",
			PackSizeValue:      12,
			PackSizeUnit:       "oz",
			Quantity:           1,
			PreferredMerchantID: "roaster_direct",
			MaxDeliveredPrice:  USD(3000),
			MonthlyBudget:      USD(6000),
			SubstitutionPolicy: SubstitutionPolicyApprovalRequired,
			PaymentMethodID:    "pm_card_everyday",
			ShippingPostalCode: "10001",
		},
	}
}

func TestC1ReminderOnlyNeverCharges(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	rule, err := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeReminderOnly))
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	if _, err := h.worker.RunOnce(ctx); err != nil {
		t.Fatalf("worker run: %v", err)
	}

	occs, err := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if err != nil || len(occs) != 1 {
		t.Fatalf("expected 1 occurrence, got %d (err=%v)", len(occs), err)
	}
	prop := occs[0].Proposal
	if prop == nil || !prop.ReminderOnly {
		t.Fatalf("expected reminder-only proposal, got %+v", prop)
	}

	// Approving a C1 reminder proposal must be rejected; reminder != approval/payment.
	if _, err := h.svc.ApproveProposal(ctx, "t1", "user", prop.ProposalID, ApproveProposalRequest{}); err == nil {
		t.Fatalf("expected reminder_only_mode error")
	}
	if h.checkout.UniqueOrderCount() != 0 {
		t.Fatalf("expected 0 orders in C1 mode, got %d", h.checkout.UniqueOrderCount())
	}
}

func TestTimezoneEditAndStaleApprovalRejected(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	rule, err := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	origNext := rule.Current.NextRunAtUTC

	h.clock.Set(origNext.Add(time.Minute))
	if _, err := h.worker.RunOnce(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	propID := occs[0].Proposal.ProposalID

	// Edit rule timezone from America/New_York to America/Los_Angeles (creates v2).
	newSched := rule.Current.Schedule
	newSched.Timezone = "America/Los_Angeles"
	updated, err := h.svc.UpdateRule(ctx, "t1", "user", rule.RuleID, UpdateRuleRequest{Schedule: &newSched})
	if err != nil {
		t.Fatalf("update rule: %v", err)
	}
	if updated.CurrentVersion != 2 {
		t.Fatalf("expected version 2, got %d", updated.CurrentVersion)
	}
	if updated.Current.Schedule.Timezone != "America/Los_Angeles" {
		t.Fatalf("expected updated timezone")
	}

	// Approving the v1 proposal must fail because v2 superseded it.
	if _, err := h.svc.ApproveProposal(ctx, "t1", "user", propID, ApproveProposalRequest{}); err == nil {
		t.Fatalf("expected stale approval rejection after rule edit")
	}
	if h.checkout.UniqueOrderCount() != 0 {
		t.Fatalf("stale approval must never create an order")
	}
}

func TestProposalExpiryWithoutPurchase(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)

	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	prop := occs[0].Proposal

	// Advance past TTL (120 min) without approving.
	h.clock.Advance(130 * time.Minute)
	_, _ = h.worker.RunOnce(ctx)

	detail, _ := h.svc.GetOccurrenceDetail(ctx, "t1", occs[0].Occurrence.OccurrenceID)
	if detail.Occurrence.Status != OccurrenceExpired || detail.Proposal.Status != ProposalExpired {
		t.Fatalf("expected expired occurrence & proposal, got occ=%s prop=%s", detail.Occurrence.Status, detail.Proposal.Status)
	}
	if _, err := h.svc.ApproveProposal(ctx, "t1", "user", prop.ProposalID, ApproveProposalRequest{}); err == nil {
		t.Fatalf("expected expired proposal approval to fail")
	}
	if h.checkout.UniqueOrderCount() != 0 {
		t.Fatalf("expired proposal must never charge")
	}
}

func TestPolicyCasesPriceIncreasePackSizePartialStockSubstitution(t *testing.T) {
	ctx := context.Background()

	t.Run("price_increase_blocked", func(t *testing.T) {
		h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{PriceMultiplierBps: 15000}) // +50%
		rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
		h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
		_, _ = h.worker.RunOnce(ctx)
		occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
		if !occs[0].Proposal.PolicyBlocked {
			t.Fatalf("expected price increase to block proposal")
		}
		if _, err := h.svc.ApproveProposal(ctx, "t1", "user", occs[0].Proposal.ProposalID, ApproveProposalRequest{}); err == nil {
			t.Fatalf("expected approval blocked on price cap breach")
		}
	})

	t.Run("pack_size_change_requires_approval", func(t *testing.T) {
		h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{PriceMultiplierBps: 10000, ForcePackSizeValue: 10})
		rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
		h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
		_, _ = h.worker.RunOnce(ctx)
		occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
		prop := occs[0].Proposal
		if !prop.RequiresSubstitutionApproval {
			t.Fatalf("expected pack-size shrink to require substitution approval")
		}
		// Reject without AcceptSubstitution=true
		if _, err := h.svc.ApproveProposal(ctx, "t1", "user", prop.ProposalID, ApproveProposalRequest{}); err == nil {
			t.Fatalf("expected error without AcceptSubstitution")
		}
		// Succeed with explicit AcceptSubstitution=true
		det, err := h.svc.ApproveProposal(ctx, "t1", "user", prop.ProposalID, ApproveProposalRequest{AcceptSubstitution: true})
		if err != nil || det.Occurrence.Status != OccurrenceCompleted {
			t.Fatalf("expected explicit substitution approval to succeed, got %v (err=%v)", det, err)
		}
	})

	t.Run("partial_stock_blocked", func(t *testing.T) {
		h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{
			PriceMultiplierBps: 10000,
			ForceStockStatus:   string(StockPartialStock),
			ForceAvailableQty:  1,
		})
		req := defaultCoffeeRuleReq(ApprovalModeAssistedReorder)
		req.Product.Quantity = 2
		req.Product.MaxDeliveredPrice = USD(6000)
		req.Product.MonthlyBudget = USD(12000)
		rule, err := h.svc.CreateRule(ctx, "t1", "user", req)
		if err != nil {
			t.Fatalf("create rule: %v", err)
		}
		h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
		_, _ = h.worker.RunOnce(ctx)
		occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
		if !occs[0].Proposal.PolicyBlocked || occs[0].Quote.StockStatus != StockPartialStock {
			t.Fatalf("expected partial stock block, got %+v", occs[0].Proposal)
		}
	})

	t.Run("preauthorized_exact_substitution_vs_unapproved", func(t *testing.T) {
		h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
		_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{
			PriceMultiplierBps: 10000,
			ForceSubstituteSKU: "COFFEE-COLOMBIA-12OZ",
		})
		req := defaultCoffeeRuleReq(ApprovalModeAssistedReorder)
		req.Product.SubstitutionPolicy = SubstitutionPolicyPreauthorizedExact
		req.Product.PreauthorizedSubstituteSKU = []string{"COFFEE-COLOMBIA-12OZ"}
		rule, err := h.svc.CreateRule(ctx, "t1", "user", req)
		if err != nil {
			t.Fatalf("create rule: %v", err)
		}
		h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
		_, _ = h.worker.RunOnce(ctx)
		occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
		if occs[0].Proposal.RequiresSubstitutionApproval || occs[0].Proposal.PolicyBlocked {
			t.Fatalf("preauthorized exact SKU should not require extra substitution flag: %+v", occs[0].Proposal)
		}
	})
}

func TestPaymentTimeoutReconciliationNoDuplicateCharge(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{
		PriceMultiplierBps:   10000,
		ForcePaymentBehavior: "timeout_settled",
	})

	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)

	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	occID := occs[0].Occurrence.OccurrenceID
	propID := occs[0].Proposal.ProposalID

	det, err := h.svc.ApproveProposal(ctx, "t1", "user", propID, ApproveProposalRequest{})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if det.Occurrence.Status != OccurrenceNeedsReconciliation {
		t.Fatalf("expected needs_reconciliation after timeout, got %s", det.Occurrence.Status)
	}
	if det.Occurrence.PaymentStatus != PaymentTimeoutUnknown {
		t.Fatalf("expected timeout_unknown payment status, got %s", det.Occurrence.PaymentStatus)
	}
	if det.Reservation.Status != ReservationUncertainHeld {
		t.Fatalf("budget must stay uncertain_held during timeout, got %s", det.Reservation.Status)
	}

	// Re-executing must NOT submit a second charge.
	_, _ = h.svc.ExecuteOccurrence(ctx, "t1", "user", occID)
	if h.checkout.SubmitCount != 1 {
		t.Fatalf("expected exactly 1 provider submit, got %d", h.checkout.SubmitCount)
	}

	// Worker recovery scan reconciles the unknown outcome cleanly.
	_, _ = h.worker.RunOnce(ctx)
	reconciled, _ := h.svc.GetOccurrenceDetail(ctx, "t1", occID)
	if reconciled.Occurrence.Status != OccurrenceCompleted || reconciled.Occurrence.PaymentStatus != PaymentSettled {
		t.Fatalf("expected reconciled completed+settled, got %s / %s", reconciled.Occurrence.Status, reconciled.Occurrence.PaymentStatus)
	}
	if reconciled.Reservation.Status != ReservationSettled {
		t.Fatalf("expected reservation settled, got %s", reconciled.Reservation.Status)
	}
	if h.checkout.UniqueOrderCount() != 1 || h.checkout.SubmitCount != 1 {
		t.Fatalf("expected 1 unique order and 1 submit, got orders=%d submits=%d", h.checkout.UniqueOrderCount(), h.checkout.SubmitCount)
	}
}

func TestPaymentTimeoutFailedReleasesBudget(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{
		PriceMultiplierBps:   10000,
		ForcePaymentBehavior: "timeout_failed",
	})

	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)

	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	det, _ := h.svc.ApproveProposal(ctx, "t1", "user", occs[0].Proposal.ProposalID, ApproveProposalRequest{})
	if det.Reservation.Status != ReservationUncertainHeld {
		t.Fatalf("expected uncertain_held")
	}

	_, _ = h.worker.RunOnce(ctx)
	after, _ := h.svc.GetOccurrenceDetail(ctx, "t1", occs[0].Occurrence.OccurrenceID)
	if after.Occurrence.Status != OccurrenceFailed || after.Reservation.Status != ReservationReleased {
		t.Fatalf("expected failed + released reservation, got %s / %s", after.Occurrence.Status, after.Reservation.Status)
	}
}

func TestDelayedFulfillmentBlocksDuplicateOrderUntilDelivered(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{
		PriceMultiplierBps:   10000,
		ForcePaymentBehavior: "delayed_fulfillment",
	})

	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)
	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	det, err := h.svc.ApproveProposal(ctx, "t1", "user", occs[0].Proposal.ProposalID, ApproveProposalRequest{})
	if err != nil || det.Occurrence.FulfillmentStatus != FulfillmentDelayed {
		t.Fatalf("expected delayed fulfillment, got %+v err=%v", det, err)
	}

	// Advance 15 days past the next scheduled run; duplicate order must be BLOCKED!
	h.clock.Advance(15 * 24 * time.Hour)
	_, _ = h.worker.RunOnce(ctx)
	occs2, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs2) != 1 {
		t.Fatalf("expected duplicate occurrence to be blocked while prior order is delayed, got %d", len(occs2))
	}

	// Once webhook delivers the delayed order, the next scan proceeds cleanly.
	_, err = h.svc.HandleMerchantWebhook(ctx, MerchantWebhookEvent{
		TenantID:          "t1",
		MerchantOrderID:   det.Checkout.MerchantOrderID,
		FulfillmentStatus: FulfillmentDelivered,
	})
	if err != nil {
		t.Fatalf("webhook deliver: %v", err)
	}
	_, _ = h.worker.RunOnce(ctx)
	occs3, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs3) != 2 {
		t.Fatalf("expected new occurrence after prior order delivered, got %d", len(occs3))
	}
}

func TestMonthlyCapAndRollover(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	req := defaultCoffeeRuleReq(ApprovalModeAssistedReorder)
	req.Schedule.IntervalDays = 10
	req.Schedule.MinIntervalDays = 1
	// One coffee order is ~$25.02; cap of $30 allows 1 order in October, blocks 2nd in October, allows 1 in November!
	req.Product.MaxDeliveredPrice = USD(3000)
	req.Product.MonthlyBudget = USD(3000)

	rule, err := h.svc.CreateRule(ctx, "t1", "user", req)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Order #1 in October -> succeeds and delivered.
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)
	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	det1, err := h.svc.ApproveProposal(ctx, "t1", "user", occs[0].Proposal.ProposalID, ApproveProposalRequest{})
	if err != nil || det1.Occurrence.Status != OccurrenceCompleted {
		t.Fatalf("order 1 failed: %v", err)
	}
	_, _ = h.svc.HandleMerchantWebhook(ctx, MerchantWebhookEvent{
		TenantID:          "t1",
		MerchantOrderID:   det1.Checkout.MerchantOrderID,
		FulfillmentStatus: FulfillmentDelivered,
	})

	// Order #2 still in October (11 days later, Oct 12) -> blocked by $30 monthly cap!
	h.clock.Advance(11 * 24 * time.Hour)
	_, _ = h.worker.RunOnce(ctx)
	occs2, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs2) != 2 || !occs2[0].Proposal.PolicyBlocked {
		t.Fatalf("expected 2nd October order blocked by monthly cap, got %+v", occs2[0].Proposal)
	}
	// Reject blocked October proposal so it's out of the way.
	_, _ = h.svc.RejectProposal(ctx, "t1", "user", occs2[0].Proposal.ProposalID, "over cap")

	// Advance into November (Nov 2) -> monthly cap rolls over and succeeds!
	h.clock.Set(time.Date(2026, 11, 2, 15, 0, 0, 0, time.UTC))
	_, _ = h.worker.RunOnce(ctx)
	occs3, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if occs3[0].Proposal.PolicyBlocked {
		t.Fatalf("expected November rollover budget to allow order, got block: %s", occs3[0].Proposal.BlockReason)
	}
	det3, err := h.svc.ApproveProposal(ctx, "t1", "user", occs3[0].Proposal.ProposalID, ApproveProposalRequest{})
	if err != nil || det3.Occurrence.Status != OccurrenceCompleted {
		t.Fatalf("expected November order to succeed, got %v err=%v", det3, err)
	}
}

func TestOutageSpanningMultipleSchedulesCreatesSingleRecoveryAction(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	dow := int(time.Monday)
	req := defaultCoffeeRuleReq(ApprovalModeAssistedReorder)
	req.Schedule = ScheduleSpec{
		Kind:               ScheduleKindCalendar,
		Timezone:           "America/New_York",
		DSTPolicy:          DSTPolicyShiftForwardFirst,
		LeapDayPolicy:      LeapDayPolicyClampMonthEnd,
		CalendarCadence:    CalendarCadenceWeekly,
		DayOfWeek:          &dow,
		LocalHour:          9,
		LocalMinute:        0,
		MinIntervalDays:    5,
		ProposalTTLMinutes: 1440,
	}
	rule, err := h.svc.CreateRule(ctx, "t1", "user", req)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Simulate a 35-day outage (5 missed Mondays!).
	h.clock.Advance(35 * 24 * time.Hour)
	_, _ = h.worker.RunOnce(ctx)

	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs) != 1 {
		t.Fatalf("outage must coalesce into 1 recovery occurrence, not a backlog; got %d", len(occs))
	}
	if !occs[0].Occurrence.OutageRecovery || occs[0].Occurrence.MissedSlotsCount < 4 {
		t.Fatalf("expected OutageRecovery=true with MissedSlotsCount>=4, got %+v", occs[0].Occurrence)
	}
	if h.checkout.UniqueOrderCount() != 0 {
		t.Fatalf("outage recovery must await approval and never auto-purchase")
	}
}

func TestPauseRacingWithSubmissionAndCancelDistinction(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)
	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	propID := occs[0].Proposal.ProposalID
	occID := occs[0].Occurrence.OccurrenceID

	// Inject hook: right after DB committed `executing` and BEFORE provider returns,
	// customer pauses the rule concurrently!
	h.svc.BeforeProviderSubmitHook = func(id string) {
		_, inFlight, err := h.svc.PauseRule(ctx, "t1", "user", rule.RuleID, "going on vacation")
		if err != nil {
			t.Errorf("concurrent pause failed: %v", err)
		}
		if len(inFlight) != 1 || inFlight[0] != id {
			t.Errorf("expected pause to report 1 in-flight order %s, got %v", id, inFlight)
		}
	}

	det, err := h.svc.ApproveProposal(ctx, "t1", "user", propID, ApproveProposalRequest{})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if !det.Occurrence.PauseRacedSubmission {
		t.Fatalf("expected PauseRacedSubmission=true when pause raced in-flight checkout")
	}
	if det.Occurrence.Status != OccurrenceCompleted || h.checkout.UniqueOrderCount() != 1 {
		t.Fatalf("in-flight order should complete cleanly with 1 order, got status=%s orders=%d", det.Occurrence.Status, h.checkout.UniqueOrderCount())
	}

	// Future occurrences must be blocked because the rule is paused.
	h.clock.Advance(20 * 24 * time.Hour)
	_, _ = h.worker.RunOnce(ctx)
	afterPauseOccs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(afterPauseOccs) != 1 {
		t.Fatalf("paused rule must not create future occurrences, got %d", len(afterPauseOccs))
	}

	// Customer explicitly cancels the already-submitted order via merchant.
	canceled, err := h.svc.CancelSubmittedOrder(ctx, "t1", "user", occID)
	if err != nil {
		t.Fatalf("cancel submitted order: %v", err)
	}
	if canceled.Occurrence.FulfillmentStatus != FulfillmentCanceled || canceled.Occurrence.PaymentStatus != PaymentRefunded {
		t.Fatalf("expected canceled + refunded, got %s / %s", canceled.Occurrence.FulfillmentStatus, canceled.Occurrence.PaymentStatus)
	}
}

func TestWorkerRestartLeaseRecoveryAndConcurrentDuplicatedJobs(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))

	// Run 8 concurrent worker scans to simulate duplicated jobs & worker restarts under -race.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := NewWorker(h.svc, "worker-concurrent")
			_, _ = w.RunOnce(ctx)
		}(i)
	}
	wg.Wait()

	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs) != 1 {
		t.Fatalf("uniqueness invariant broken: expected 1 occurrence under concurrency, got %d", len(occs))
	}

	// Now simulate 8 concurrent approvals for the same proposal: must produce AT MOST 1 order!
	propID := occs[0].Proposal.ProposalID
	successes := 0
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := h.svc.ApproveProposal(ctx, "t1", "user", propID, ApproveProposalRequest{}); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 || h.checkout.UniqueOrderCount() != 1 {
		t.Fatalf("expected exactly 1 approval success and 1 order, got successes=%d orders=%d", successes, h.checkout.UniqueOrderCount())
	}

	// Lease expiration recovery test: force a stuck leased job and verify worker 2 reclaims it.
	err := h.store.Update(ctx, "t1", func(st *TenantState) error {
		past := h.clock.Now().Add(-5 * time.Minute)
		st.Jobs["job_stuck"] = &WorkerJob{
			JobID:         "job_stuck",
			TenantID:      "t1",
			Kind:          "dispatch_notification",
			DedupeKey:     "stuck-1",
			ResourceID:    "nonexistent",
			Status:        "leased",
			LeaseOwner:    "dead-worker",
			LeaseUntil:    &past,
			Attempts:      1,
			MaxAttempts:   3,
			NextAttemptAt: past,
			CreatedAt:     past,
			UpdatedAt:     past,
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed stuck job: %v", err)
	}
	w2 := NewWorker(h.svc, "recovered-worker-2")
	if _, err := w2.RunOnce(ctx); err != nil {
		t.Fatalf("worker 2 recovery: %v", err)
	}
	st, _ := h.store.View(ctx, "t1")
	if st.Jobs["job_stuck"].Status != "done" {
		t.Fatalf("expected reclaimed stuck job to finish as done, got %s", st.Jobs["job_stuck"].Status)
	}
}

func TestC3DelegationMandateAndRevocation(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	// Creating C3 rule fails while pilot gate is disabled.
	if _, err := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeLimitedDelegation)); err == nil {
		t.Fatalf("expected C3 rule creation blocked when EnableC3Delegation is false")
	}

	// Enable C3 pilot flag.
	_, _ = h.svc.SetSandboxFaults(ctx, "t1", "op", SandboxFaultConfig{
		PriceMultiplierBps: 10000,
		EnableC3Delegation: true,
	})
	rule, err := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeLimitedDelegation))
	if err != nil {
		t.Fatalf("create C3 rule: %v", err)
	}

	// Grant explicit delegated mandate bound to rule v1, exact SKU, max price, and payment method.
	mandate, err := h.svc.GrantDelegatedMandate(ctx, "t1", "user", rule.RuleID, GrantMandateRequest{
		MaxAmount:          USD(3000),
		PaymentMethodID:    "pm_card_everyday",
		ProviderMandateRef: "ap2_mandate_001",
		TTLHours:           720,
	})
	if err != nil {
		t.Fatalf("grant mandate: %v", err)
	}

	// Cycle 1: auto-executes under mandate.
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)
	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs) != 1 || occs[0].Occurrence.Status != OccurrenceCompleted {
		t.Fatalf("expected C3 auto-execution under active mandate, got %+v", occs[0].Occurrence)
	}

	// Deliver cycle 1, then REVOKE mandate before cycle 2.
	_, _ = h.svc.HandleMerchantWebhook(ctx, MerchantWebhookEvent{
		TenantID:          "t1",
		MerchantOrderID:   occs[0].Checkout.MerchantOrderID,
		FulfillmentStatus: FulfillmentDelivered,
	})
	if _, err := h.svc.RevokeConsent(ctx, "t1", "user", mandate.ConsentID); err != nil {
		t.Fatalf("revoke mandate: %v", err)
	}

	// Cycle 2: revoked mandate must NOT auto-execute; falls back to awaiting approval!
	h.clock.Advance(15 * 24 * time.Hour)
	_, _ = h.worker.RunOnce(ctx)
	occs2, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs2) != 2 || occs2[0].Occurrence.Status != OccurrenceAwaitingApproval {
		t.Fatalf("revoked mandate must block auto-execution and await approval, got %+v", occs2[0].Occurrence)
	}
	if h.checkout.UniqueOrderCount() != 1 {
		t.Fatalf("expected still only 1 order after mandate revocation, got %d", h.checkout.UniqueOrderCount())
	}
}

func TestSkipNextOccurrence(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))

	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	firstSlot := rule.Current.NextRunAtUTC

	skipped, updated, err := h.svc.SkipNextOccurrence(ctx, "t1", "user", rule.RuleID)
	if err != nil {
		t.Fatalf("skip next: %v", err)
	}
	if skipped.Status != OccurrenceSkipped || !updated.Current.NextRunAtUTC.After(firstSlot) {
		t.Fatalf("expected skipped occurrence and advanced next run")
	}

	// Advancing past the skipped slot must NOT create a second occurrence for that slot.
	h.clock.Set(firstSlot.Add(time.Hour))
	_, _ = h.worker.RunOnce(ctx)
	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)
	if len(occs) != 1 || occs[0].Occurrence.Status != OccurrenceSkipped {
		t.Fatalf("skipped slot must stay skipped, got %+v", occs)
	}
}

func TestGlobalProductPauseBlocksExecution(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
	rule, _ := h.svc.CreateRule(ctx, "t1", "user", defaultCoffeeRuleReq(ApprovalModeAssistedReorder))
	h.clock.Set(rule.Current.NextRunAtUTC.Add(time.Minute))
	_, _ = h.worker.RunOnce(ctx)
	occs, _ := h.svc.ListRuleOccurrences(ctx, "t1", rule.RuleID)

	_, err := h.svc.SetGlobalPause(ctx, "operator:ops", true, "merchant API incident")
	if err != nil {
		t.Fatalf("set global pause: %v", err)
	}
	_, err = h.svc.ApproveProposal(ctx, "t1", "user", occs[0].Proposal.ProposalID, ApproveProposalRequest{})
	var p *Problem
	if !errors.As(err, &p) || p.Code != "product_paused" {
		t.Fatalf("expected product_paused error, got %v", err)
	}
}
