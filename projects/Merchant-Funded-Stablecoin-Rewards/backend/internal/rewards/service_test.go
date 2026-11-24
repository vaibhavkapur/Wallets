package rewards

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"unowallet/rewards/internal/money"
)

func newTestService(t *testing.T) (*Service, *SandboxProvider) {
	t.Helper()
	store, err := NewFileStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(store.Close)
	prov := NewSandboxProvider()
	svc := NewService(store, prov, "test-webhook-secret")
	baseTime := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return baseTime })
	return svc, prov
}

func setupFundedCampaign(t *testing.T, svc *Service, tenant string, budget, custCap, fixed money.Amount, feeBps, lossBps int64) (*Merchant, *CampaignView) {
	t.Helper()
	ctx := context.Background()
	m, err := svc.OnboardMerchant(ctx, tenant, OnboardMerchantInput{
		ID: "mer_1", Name: "Test Merchant", WebhookSecret: "whsec_test", TermsAccepted: true,
	})
	if err != nil {
		t.Fatalf("onboard merchant: %v", err)
	}
	cv, err := svc.CreateCampaign(ctx, tenant, CreateCampaignInput{
		CampaignID:          "cmp_1",
		MerchantID:          m.ID,
		Name:                "Test Campaign",
		RewardType:          RewardTypeFixed,
		FixedRewardMinor:    fixed,
		BudgetCapMinor:      budget,
		CustomerCapMinor:    custCap,
		ReturnWindowSeconds: 86400, // 1 day
		ReturnTreatment:     ReturnProRata,
		PlatformFeeBps:      feeBps,
		LossReserveBps:      lossBps,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := svc.FundCampaign(ctx, tenant, FundCampaignInput{
		CampaignID:  cv.CampaignID,
		ProviderRef: "ach_001",
		AmountMinor: budget,
		Reconciled:  true,
	}); err != nil {
		t.Fatalf("fund campaign: %v", err)
	}
	return m, cv
}

func assertLedgerHealthy(t *testing.T, svc *Service, tenant string) *LedgerSummary {
	t.Helper()
	sum, err := svc.GetLedgerSummary(context.Background(), tenant)
	if err != nil {
		t.Fatalf("ledger summary: %v", err)
	}
	if !sum.Balanced {
		t.Fatalf("ledger is NOT balanced: %+v", sum.TrialBalance)
	}
	if !sum.FundingCoversObligations {
		t.Fatalf("funding does NOT cover obligations: custody=%s obligations=%s", sum.ProviderCustodyUSD, sum.TotalObligationsUSD)
	}
	return sum
}

func TestEndToEndLifecyclePartialReturnsAndPayout(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tenant := "t1"

	// Percentage campaign: 10% cashback, 5% platform fee, 2% loss reserve, $100 budget, $50 customer cap.
	m, err := svc.OnboardMerchant(ctx, tenant, OnboardMerchantInput{
		ID: "mer_1", Name: "Roast Co", WebhookSecret: "whsec_1", TermsAccepted: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := svc.CreateCampaign(ctx, tenant, CreateCampaignInput{
		CampaignID:          "cmp_pct",
		MerchantID:          m.ID,
		Name:                "10% Back",
		RewardType:          RewardTypePercentage,
		PercentageBps:       1000,
		BudgetCapMinor:      10000,
		CustomerCapMinor:    5000,
		ReturnWindowSeconds: 86400,
		ReturnTreatment:     ReturnProRata,
		PlatformFeeBps:      500,
		LossReserveBps:      200,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Unreconciled (pending) funding must NOT allow rewarding orders yet!
	if _, err := svc.FundCampaign(ctx, tenant, FundCampaignInput{
		CampaignID: cv.CampaignID, ProviderRef: "ach_pending", AmountMinor: 10000, Reconciled: false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e_unfunded", EventType: EventOrderPaid, MerchantID: m.ID, CampaignID: cv.CampaignID,
		OrderID: "ORD-0", CustomerID: "cust_1", OrderSubtotalMinor: 10000,
	}); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("expected ErrInsufficientFunds before funding reconciliation, got %v", err)
	}

	// Now reconcile the funding event idempotently using the same provider_ref.
	if _, err := svc.FundCampaign(ctx, tenant, FundCampaignInput{
		CampaignID: cv.CampaignID, ProviderRef: "ach_pending", AmountMinor: 10000, Reconciled: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Order $200.00 -> $20.00 reward (2000 cents), $1.00 fee (100), $0.40 loss reserve (40).
	res, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e_paid_1", EventType: EventOrderPaid, MerchantID: m.ID, CampaignID: cv.CampaignID,
		OrderID: "ORD-1", CustomerID: "cust_1", OrderSubtotalMinor: 20000, Fulfilled: false,
	})
	if err != nil {
		t.Fatalf("order paid: %v", err)
	}
	if res.Reward.NetAmountMinor != 2000 || res.Reward.State != RewardPending {
		t.Fatalf("unexpected reward: %+v", res.Reward)
	}

	// Duplicate order event must deduplicate without creating duplicate obligations.
	dup, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e_paid_1", EventType: EventOrderPaid, MerchantID: m.ID, CampaignID: cv.CampaignID,
		OrderID: "ORD-1", CustomerID: "cust_1", OrderSubtotalMinor: 20000,
	})
	if err != nil || !dup.Deduplicated {
		t.Fatalf("expected deduplicated event, got dup=%+v err=%v", dup, err)
	}
	// Duplicate order_id with different event_id must also deduplicate!
	dup2, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e_paid_1_retry", EventType: EventOrderPaid, MerchantID: m.ID, CampaignID: cv.CampaignID,
		OrderID: "ORD-1", CustomerID: "cust_1", OrderSubtotalMinor: 20000,
	})
	if err != nil || !dup2.Deduplicated {
		t.Fatalf("expected deduplicated order_id, got %+v err=%v", dup2, err)
	}

	// Partial return of $50.00 (25% of $200) -> reward drops from $20.00 to $15.00 (reversed 500 cents).
	refRes, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e_ref_1", EventType: EventOrderRefunded, MerchantID: m.ID,
		OrderID: "ORD-1", RefundAmountMinor: 5000,
	})
	if err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if refRes.Reward.NetAmountMinor != 1500 || refRes.Reward.ReversedAmountMinor != 500 {
		t.Fatalf("expected net=1500 reversed=500, got %+v", refRes.Reward)
	}

	// Advancing clock before fulfillment MUST NOT make reward eligible (payment settlement != fulfillment).
	svc.AdvanceClock(48 * time.Hour)
	rep, err := svc.RunWorkerTick(ctx, tenant)
	if err != nil || rep.RewardsMadeEligible != 0 {
		t.Fatalf("unfulfilled order must not become eligible; rep=%+v err=%v", rep, err)
	}

	// Fulfill the order, then advance past 24h return window -> reward becomes eligible!
	if _, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e_fulfill_1", EventType: EventOrderFulfilled, MerchantID: m.ID, OrderID: "ORD-1",
	}); err != nil {
		t.Fatal(err)
	}
	svc.AdvanceClock(25 * time.Hour)
	rep, err = svc.RunWorkerTick(ctx, tenant)
	if err != nil || rep.RewardsMadeEligible != 1 {
		t.Fatalf("expected 1 reward made eligible, got %+v err=%v", rep, err)
	}

	// Enroll & verify customer beneficiary.
	bv, err := svc.RegisterBeneficiary(ctx, tenant, RegisterBeneficiaryInput{
		CustomerID: "cust_1", Asset: "USDC", Network: "base",
		Address: "0x71C7656EC7ab88b098defB751B7401B5f6d8976F", RecoveryDisclosed: true, AutoVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Obtain payout quote ($15.00 USD -> 15.000000 USDC gross, 0.25 USDC fee -> 14.750000 USDC net).
	q, err := svc.CreatePayoutQuote(ctx, tenant, CreatePayoutQuoteInput{
		CustomerID: "cust_1", BeneficiaryVersionID: bv.ID,
	})
	if err != nil {
		t.Fatalf("create quote: %v", err)
	}
	if q.SourceAmountBase != 1500 || q.GrossPayoutBase != 15_000_000 || q.NetPayoutBase != 14_750_000 {
		t.Fatalf("unexpected quote math: %+v", q)
	}

	// Authorize payout idempotently.
	att, err := svc.AuthorizePayout(ctx, tenant, CreatePayoutInput{
		CustomerID: "cust_1", QuoteID: q.ID, BeneficiaryVersionID: bv.ID,
		IdempotencyKey: "idem_pay_1", TermsApproved: true,
	})
	if err != nil {
		t.Fatalf("authorize payout: %v", err)
	}
	if att.Status != PayoutSucceeded || att.TxHash == "" {
		t.Fatalf("expected succeeded payout, got %+v", att)
	}

	// Re-submitting same idempotency key returns identical payout without double-spending.
	attDup, err := svc.AuthorizePayout(ctx, tenant, CreatePayoutInput{
		CustomerID: "cust_1", QuoteID: q.ID, BeneficiaryVersionID: bv.ID,
		IdempotencyKey: "idem_pay_1", TermsApproved: true,
	})
	if err != nil || attDup.ID != att.ID {
		t.Fatalf("idempotency failed: got %+v err=%v", attDup, err)
	}

	// Post-payout chargeback must NOT claw back the paid reward; draws loss reserve & opens exception.
	cbRes, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e_cb_1", EventType: EventOrderChargeback, MerchantID: m.ID,
		OrderID: "ORD-1", RefundAmountMinor: 15000,
	})
	if err != nil {
		t.Fatalf("chargeback: %v", err)
	}
	if cbRes.Reward.State != RewardPaid || !cbRes.Reward.PostPayoutDisputed {
		t.Fatalf("paid reward must stay paid and flagged disputed, got %+v", cbRes.Reward)
	}
	if cbRes.Exception == nil || cbRes.Exception.Category != "post_payout_chargeback" {
		t.Fatalf("expected post_payout_chargeback exception, got %+v", cbRes.Exception)
	}

	assertLedgerHealthy(t, svc, tenant)
}

func TestConcurrentOrdersEnforceCampaignCapAtomically(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tenant := "t_conc"

	// Budget = $100.00 (10,000 cents). Fixed reward = $10.00 (1,000 cents), 0 fee, 0 loss reserve -> max 10 orders!
	// Customer cap = $20.00 -> each customer max 2 orders.
	setupFundedCampaign(t, svc, tenant, 10000, 2000, 1000, 0, 0)

	const totalRequests = 40
	var succeeded int64
	var wg sync.WaitGroup
	wg.Add(totalRequests)

	for i := 0; i < totalRequests; i++ {
		i := i
		go func() {
			defer wg.Done()
			cust := fmt.Sprintf("cust_%d", i%15)
			_, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
				EventID:            fmt.Sprintf("evt_%d", i),
				EventType:          EventOrderPaid,
				MerchantID:         "mer_1",
				CampaignID:         "cmp_1",
				OrderID:            fmt.Sprintf("ORD-%d", i),
				CustomerID:         cust,
				OrderSubtotalMinor: 5000,
				Fulfilled:          true,
			})
			if err == nil {
				atomic.AddInt64(&succeeded, 1)
			}
		}()
	}
	wg.Wait()

	if succeeded != 10 {
		t.Fatalf("expected exactly 10 orders within $100 cap, got %d", succeeded)
	}

	sum := assertLedgerHealthy(t, svc, tenant)
	if sum.CampaignAvailableUSD != 0 {
		t.Fatalf("expected available campaign budget 0, got %s", sum.CampaignAvailableUSD)
	}
	if sum.RewardPendingUSD != 10000 {
		t.Fatalf("expected pending rewards 10000, got %s", sum.RewardPendingUSD)
	}
}

func TestBeneficiaryVersionBindingAndQuoteExpiry(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tenant := "t_ben"
	setupFundedCampaign(t, svc, tenant, 10000, 10000, 1000, 0, 0)

	// Issue eligible $10 reward.
	if _, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "e1", EventType: EventOrderPaid, MerchantID: "mer_1", CampaignID: "cmp_1",
		OrderID: "ORD-1", CustomerID: "cust_1", OrderSubtotalMinor: 5000, Fulfilled: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc.AdvanceClock(25 * time.Hour)
	if _, err := svc.RunWorkerTick(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	bv1, err := svc.RegisterBeneficiary(ctx, tenant, RegisterBeneficiaryInput{
		CustomerID: "cust_1", Asset: "USDC", Network: "base",
		Address: "0x1111111111111111111111111111111111111111", RecoveryDisclosed: true, AutoVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Quote bound to bv1 with 60s TTL.
	q1, err := svc.CreatePayoutQuote(ctx, tenant, CreatePayoutQuoteInput{
		CustomerID: "cust_1", BeneficiaryVersionID: bv1.ID, TTLSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Customer updates beneficiary to v2 -> old quote bound to v1 MUST fail (cannot silently redirect to v2!).
	bv2, err := svc.RegisterBeneficiary(ctx, tenant, RegisterBeneficiaryInput{
		CustomerID: "cust_1", Asset: "USDC", Network: "base",
		Address: "0x2222222222222222222222222222222222222222", RecoveryDisclosed: true, AutoVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AuthorizePayout(ctx, tenant, CreatePayoutInput{
		CustomerID: "cust_1", QuoteID: q1.ID, BeneficiaryVersionID: bv1.ID, IdempotencyKey: "pay_old_ben", TermsApproved: true,
	}); !errors.Is(err, ErrBeneficiaryMismatch) {
		t.Fatalf("expected ErrBeneficiaryMismatch when beneficiary rotated, got %v", err)
	}

	// New quote on bv2, then let it expire -> must fail with ErrQuoteExpired.
	q2, err := svc.CreatePayoutQuote(ctx, tenant, CreatePayoutQuoteInput{
		CustomerID: "cust_1", BeneficiaryVersionID: bv2.ID, TTLSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.AdvanceClock(60 * time.Second)
	if _, err := svc.AuthorizePayout(ctx, tenant, CreatePayoutInput{
		CustomerID: "cust_1", QuoteID: q2.ID, BeneficiaryVersionID: bv2.ID, IdempotencyKey: "pay_expired", TermsApproved: true,
	}); !errors.Is(err, ErrQuoteExpired) {
		t.Fatalf("expected ErrQuoteExpired, got %v", err)
	}
}

func TestUnknownOutcomeReconciliationBeforeRetryAndLateConfirmation(t *testing.T) {
	svc, prov := newTestService(t)
	ctx := context.Background()
	tenant := "t_unk"
	setupFundedCampaign(t, svc, tenant, 50000, 50000, 1000, 0, 0)

	// Create two $10 rewards for two scenarios.
	for i := 1; i <= 2; i++ {
		if _, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
			EventID: fmt.Sprintf("e%d", i), EventType: EventOrderPaid, MerchantID: "mer_1", CampaignID: "cmp_1",
			OrderID: fmt.Sprintf("ORD-%d", i), CustomerID: fmt.Sprintf("cust_%d", i), OrderSubtotalMinor: 5000, Fulfilled: true,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.RegisterBeneficiary(ctx, tenant, RegisterBeneficiaryInput{
			CustomerID: fmt.Sprintf("cust_%d", i), Asset: "USDC", Network: "base",
			Address: fmt.Sprintf("0x%040d", i), RecoveryDisclosed: true, AutoVerify: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	svc.AdvanceClock(25 * time.Hour)
	if _, err := svc.RunWorkerTick(ctx, tenant); err != nil {
		t.Fatal(err)
	}

	// Scenario 1: Provider times out AFTER actually broadcasting (timeout_succeeded).
	q1, err := svc.CreatePayoutQuote(ctx, tenant, CreatePayoutQuoteInput{CustomerID: "cust_1"})
	if err != nil {
		t.Fatal(err)
	}
	att1, err := svc.AuthorizePayout(ctx, tenant, CreatePayoutInput{
		CustomerID: "cust_1", QuoteID: q1.ID, IdempotencyKey: "idem_t1", TermsApproved: true, SandboxScenario: "timeout_succeeded",
	})
	if err != nil {
		t.Fatal(err)
	}
	if att1.Status != PayoutOutcomeUnknown {
		t.Fatalf("expected outcome_unknown, got %s", att1.Status)
	}
	// Blind retry MUST be rejected with ErrOutcomeUnknown!
	if _, err := svc.RetryPayout(ctx, tenant, "cust_1", RetryPayoutInput{
		LogicalPayoutID: att1.LogicalPayoutID, IdempotencyKey: "idem_t1_retry",
	}); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected ErrOutcomeUnknown blocking blind retry, got %v", err)
	}
	// Worker reconciles with provider -> discovers it actually succeeded, settles ledger, resolves exception!
	rep, err := svc.RunWorkerTick(ctx, tenant)
	if err != nil || rep.PayoutsReconciled != 1 {
		t.Fatalf("expected 1 reconciled payout, got %+v err=%v", rep, err)
	}
	det1, err := svc.GetPayout(ctx, tenant, att1.LogicalPayoutID, "cust_1")
	if err != nil || det1.LatestAttempt.Status != PayoutSucceeded {
		t.Fatalf("expected succeeded after reconciliation, got %+v err=%v", det1, err)
	}

	// Scenario 2: Provider times out BEFORE broadcasting (timeout_failed) -> reconciles to failed -> safe retry succeeds reusing logical_payout_id!
	q2, err := svc.CreatePayoutQuote(ctx, tenant, CreatePayoutQuoteInput{CustomerID: "cust_2"})
	if err != nil {
		t.Fatal(err)
	}
	att2, err := svc.AuthorizePayout(ctx, tenant, CreatePayoutInput{
		CustomerID: "cust_2", QuoteID: q2.ID, IdempotencyKey: "idem_t2", TermsApproved: true, SandboxScenario: "timeout_failed",
	})
	if err != nil || att2.Status != PayoutOutcomeUnknown {
		t.Fatalf("expected outcome_unknown, got %+v err=%v", att2, err)
	}
	if _, err := svc.RunWorkerTick(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	det2, _ := svc.GetPayout(ctx, tenant, att2.LogicalPayoutID, "cust_2")
	if det2.LatestAttempt.Status != PayoutFailed {
		t.Fatalf("expected reconciled failed, got %s", det2.LatestAttempt.Status)
	}

	// Now retry safely with same logical_payout_id!
	retryAtt, err := svc.RetryPayout(ctx, tenant, "cust_2", RetryPayoutInput{
		LogicalPayoutID: att2.LogicalPayoutID, IdempotencyKey: "idem_t2_retry2",
	})
	if err != nil || retryAtt.Status != PayoutSucceeded || retryAtt.AttemptNumber != 2 {
		t.Fatalf("expected retry #2 to succeed, got %+v err=%v", retryAtt, err)
	}

	// Late confirmation arriving for the previously-failed attempt #1 must open an operator exception!
	if _, _, err := svc.ProcessProviderWebhook(ctx, tenant, ProviderWebhookInput{
		CallbackID: "cb_late_1", AttemptID: att2.ID, ProviderRef: att2.ProviderRef,
		Status: PayoutSucceeded, TxHash: "0xdeadbeef", Confirmations: 12,
	}); err != nil {
		t.Fatal(err)
	}
	// Duplicate callback must deduplicate cleanly.
	_, dedup, err := svc.ProcessProviderWebhook(ctx, tenant, ProviderWebhookInput{
		CallbackID: "cb_late_1", AttemptID: att2.ID, ProviderRef: att2.ProviderRef,
		Status: PayoutSucceeded, TxHash: "0xdeadbeef", Confirmations: 12,
	})
	if err != nil || !dedup {
		t.Fatalf("expected duplicate callback deduplicated, got dedup=%v err=%v", dedup, err)
	}

	// Inject provider custody balance drift -> worker must open provider_balance_mismatch exception!
	prov.SetCustodyDrift(tenant, -500)
	if _, err := svc.RunWorkerTick(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	excs, err := svc.ListExceptions(ctx, tenant, true)
	if err != nil {
		t.Fatal(err)
	}
	cats := map[string]bool{}
	for _, ex := range excs {
		cats[ex.Category] = true
	}
	if !cats["late_confirmation_after_failure"] || !cats["provider_balance_mismatch"] {
		t.Fatalf("expected late_confirmation_after_failure and provider_balance_mismatch exceptions, got %+v", cats)
	}

	assertLedgerHealthy(t, svc, tenant)
}

func TestPauseControlAndMinimumThreshold(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tenant := "t_pause"
	setupFundedCampaign(t, svc, tenant, 10000, 10000, 200, 0, 0) // $2 reward < $5 min payout

	// Global issuance pause blocks orders.
	if _, err := svc.SetPauseControl(ctx, tenant, true, false, "maintenance", "op_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "ep1", EventType: EventOrderPaid, MerchantID: "mer_1", CampaignID: "cmp_1",
		OrderID: "ORD-P1", CustomerID: "cust_1", OrderSubtotalMinor: 5000, Fulfilled: true,
	}); !errors.Is(err, ErrPaused) {
		t.Fatalf("expected ErrPaused, got %v", err)
	}

	// Unpause issuance, issue $2 reward, make eligible -> quote must fail ErrBelowMinimumPayout ($2 < $5).
	if _, err := svc.SetPauseControl(ctx, tenant, false, false, "resumed", "op_1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IngestMerchantEvent(ctx, tenant, MerchantEventInput{
		EventID: "ep2", EventType: EventOrderPaid, MerchantID: "mer_1", CampaignID: "cmp_1",
		OrderID: "ORD-P2", CustomerID: "cust_1", OrderSubtotalMinor: 5000, Fulfilled: true,
	}); err != nil {
		t.Fatal(err)
	}
	svc.AdvanceClock(25 * time.Hour)
	_, _ = svc.RunWorkerTick(ctx, tenant)
	_, _ = svc.RegisterBeneficiary(ctx, tenant, RegisterBeneficiaryInput{
		CustomerID: "cust_1", Asset: "USDC", Network: "base",
		Address: "0x71C7656EC7ab88b098defB751B7401B5f6d8976F", RecoveryDisclosed: true, AutoVerify: true,
	})
	if _, err := svc.CreatePayoutQuote(ctx, tenant, CreatePayoutQuoteInput{CustomerID: "cust_1"}); !errors.Is(err, ErrBelowMinimumPayout) {
		t.Fatalf("expected ErrBelowMinimumPayout, got %v", err)
	}
}
