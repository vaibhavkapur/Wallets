package rewards

import (
	"context"
	"errors"
	"fmt"
	"time"

	"unowallet/rewards/internal/money"
)

type WorkerTickReport struct {
	TenantID              string    `json:"tenant_id"`
	RunAt                 time.Time `json:"run_at"`
	RewardsMadeEligible   int       `json:"rewards_made_eligible"`
	PayoutsReconciled     int       `json:"payouts_reconciled"`
	CustodyDriftUSDMinor  money.Amount `json:"custody_drift_usd_minor"`
	LedgerBalanced        bool      `json:"ledger_balanced"`
	FundingCoversLiabs    bool      `json:"funding_covers_obligations"`
	NewExceptionsOpened   int       `json:"new_exceptions_opened"`
}

// RunWorkerTick executes one full deterministic worker cycle for a tenant:
// 1. Return-window & fulfillment checks -> transition eligible pending rewards to payable.
// 2. Reconcile any pending / submitted / outcome_unknown payout attempts against provider (outside DB tx!).
// 3. Reconcile database ledger custody balance against provider-held custody snapshot & verify trial balance.
func (svc *Service) RunWorkerTick(ctx context.Context, tenantID string) (*WorkerTickReport, error) {
	now := svc.Now()
	report := &WorkerTickReport{
		TenantID: tenantID,
		RunAt:    now,
	}

	// Step 1: Transition pending rewards whose payment is settled, order is fulfilled, and return window expired.
	var toPoll []PayoutAttempt
	var ledgerCustodyUSD money.Amount
	var exceptionsBefore int

	err := svc.store.Update(ctx, tenantID, func(s *State) error {
		exceptionsBefore = len(s.Exceptions)
		for _, r := range s.Rewards {
			if r.State != RewardPending || r.NetAmountMinor <= 0 {
				continue
			}
			if !r.PaymentSettled || !r.Fulfilled || r.EligibleAt == nil || now.Before(*r.EligibleAt) {
				continue
			}

			postings := []JournalPosting{
				{AccountCode: AcctRewardPending, ScopeID: r.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: r.NetAmountMinor},
				{AccountCode: AcctRewardPayable, ScopeID: r.CustomerID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: r.NetAmountMinor},
			}
			if r.PlatformFeeMinor > 0 {
				postings = append(postings,
					JournalPosting{AccountCode: AcctCampaignReserved, ScopeID: r.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: r.PlatformFeeMinor},
					JournalPosting{AccountCode: AcctPlatformRevenue, ScopeID: "global", Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: r.PlatformFeeMinor},
				)
			}
			if r.LossReserveMinor > 0 {
				postings = append(postings,
					JournalPosting{AccountCode: AcctCampaignReserved, ScopeID: r.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Debit, Amount: r.LossReserveMinor},
					JournalPosting{AccountCode: AcctLossReserve, ScopeID: r.CampaignID, Asset: money.AssetUSD, Network: money.NetworkFiat, Decimals: money.DecimalsUSD, Direction: Credit, Amount: r.LossReserveMinor},
				)
			}
			if _, err := PostJournal(s, "eligible:"+r.ID, "reward_eligible", r.ID,
				fmt.Sprintf("Return window expired for order %s; reward %s now payable", r.OrderID, r.ID),
				postings, now); err != nil {
				return err
			}

			r.State = RewardEligible
			r.UpdatedAt = now
			if res, ok := s.Reservations[r.ReservationID]; ok {
				res.Status = ReservationConverted
				res.UpdatedAt = now
			}
			addOutbox(s, "reward.eligible", r.ID, map[string]any{"reward_id": r.ID, "customer_id": r.CustomerID}, now)
			report.RewardsMadeEligible++
		}

		// Snapshot payout attempts needing provider status reconciliation.
		for _, att := range s.PayoutAttempts {
			if att.Status == PayoutSubmitted || att.Status == PayoutPending || att.Status == PayoutOutcomeUnknown {
				toPoll = append(toPoll, *att)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Step 2: Poll provider outside DB transaction for pending / outcome_unknown attempts.
	type pollOutcome struct {
		attemptID string
		resp      ProviderPayoutResponse
		notFound  bool
		err       error
	}
	outcomes := make([]pollOutcome, 0, len(toPoll))
	for _, att := range toPoll {
		resp, err := svc.provider.GetPayoutStatus(ctx, att.IdempotencyKey, att.ProviderRef)
		if errors.Is(err, ErrProviderNotFound) {
			outcomes = append(outcomes, pollOutcome{attemptID: att.ID, notFound: true})
			continue
		}
		outcomes = append(outcomes, pollOutcome{attemptID: att.ID, resp: resp, err: err})
	}

	// Apply polled payout outcomes transactionally.
	if len(outcomes) > 0 {
		if err := svc.store.Update(ctx, tenantID, func(s *State) error {
			for _, o := range outcomes {
				att := s.PayoutAttempts[o.attemptID]
				if att == nil {
					continue
				}
				if o.notFound {
					// Provider has no record of this idempotency key -> safe to treat as confirmed never broadcast.
					if err := applyProviderPayoutOutcome(s, att, ProviderPayoutResponse{
						Status:        PayoutFailed,
						FailureReason: "reconciled as not found at provider; safe to retry",
					}, now); err != nil {
						return err
					}
					report.PayoutsReconciled++
					continue
				}
				if o.err != nil {
					continue
				}
				prevStatus := att.Status
				if err := applyProviderPayoutOutcome(s, att, o.resp, now); err != nil {
					return err
				}
				if att.Status != prevStatus {
					report.PayoutsReconciled++
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	// Step 3: Read ledger custody balance, fetch provider custody snapshot outside DB tx, then reconcile.
	stView, err := svc.store.View(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	ledgerCustodyUSD = SumAccountsByCode(stView, AcctProviderCustody, money.AssetUSD, money.NetworkFiat)
	snapshot, snapErr := svc.provider.GetCustodySnapshot(ctx, tenantID, ledgerCustodyUSD)

	err = svc.store.Update(ctx, tenantID, func(s *State) error {
		sum := BuildLedgerSummary(s)
		report.LedgerBalanced = sum.Balanced
		report.FundingCoversLiabs = sum.FundingCoversObligations

		if !sum.Balanced {
			openException(s, "ledger_imbalance", SeverityCritical, "ledger", tenantID, money.AssetUSD, money.DecimalsUSD,
				0, 1, "Double-entry ledger trial balance failed asset balancing invariant", now)
		}
		if !sum.FundingCoversObligations {
			openException(s, "funding_coverage_deficit", SeverityCritical, "ledger", tenantID, money.AssetUSD, money.DecimalsUSD,
				sum.TotalObligationsUSD, sum.ProviderCustodyUSD,
				fmt.Sprintf("Ledger custody (%s) does not cover obligations (%s)", sum.ProviderCustodyUSD, sum.TotalObligationsUSD), now)
		}

		if snapErr == nil {
			drift := snapshot.HeldBalanceBase - sum.ProviderCustodyUSD
			report.CustodyDriftUSDMinor = drift
			if drift != 0 {
				sev := SeverityWarning
				if drift < 0 {
					sev = SeverityCritical
				}
				openException(s, "provider_balance_mismatch", sev, "custody", snapshot.Source, snapshot.Asset, snapshot.Decimals,
					sum.ProviderCustodyUSD, snapshot.HeldBalanceBase,
					fmt.Sprintf("Provider custody balance (%s %s) differs from ledger custody (%s %s) by %s",
						money.FormatDecimal(snapshot.HeldBalanceBase, snapshot.Decimals), snapshot.Asset,
						money.FormatDecimal(sum.ProviderCustodyUSD, snapshot.Decimals), snapshot.Asset,
						money.FormatDecimal(drift, snapshot.Decimals)),
					now)
			}
		}

		t := now
		s.LastReconciledAt = &t
		if len(s.Exceptions) > exceptionsBefore {
			report.NewExceptionsOpened = len(s.Exceptions) - exceptionsBefore
		}
		return nil
	})
	return report, err
}

// StartBackgroundWorker periodically ticks all tenants until ctx is canceled.
func (svc *Service) StartBackgroundWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tenants, err := svc.store.Tenants(ctx)
				if err != nil {
					continue
				}
				for _, tid := range tenants {
					_, _ = svc.RunWorkerTick(ctx, tid)
				}
			}
		}
	}()
}
