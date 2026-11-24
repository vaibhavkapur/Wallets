# Architecture and Ledger

[Documentation home](index.md)

## System Topology

1. **Go Financial Backend (`backend/`)**: owns merchant onboarding, immutable `CampaignVersion` rules, reconciled `FundingEvent` deposits, HMAC-verified `PurchaseAttribution` ingestion, atomic `CampaignReservation` locks, `RewardAccrual` lifecycle state, double-entry `LedgerAccount`/`JournalEntry` accounting, versioned `BeneficiaryVersion` records, `PayoutQuote` and `PayoutAttempt` orchestration, `OutboxEvent` persistence, `AuditLog`, and `ReconciliationException` tracking.
2. **Go Background Workers (`internal/rewards/worker.go`, `cmd/worker`)**: run return-window and fulfillment eligibility transitions, poll provider status for `pending`/`submitted`/`outcome_unknown` payouts outside database transactions, and reconcile database custody balances against provider custody snapshots.
3. **TypeScript + React Interfaces (`web/`)**: three role-focused views (Customer Rewards, Merchant Campaigns, Operator Queue) communicating over `/v1/*` with pure `BigInt` decimal string parsing/formatting (`web/src/money.ts`).
4. **Optional Rust Batch Reconciler (`reconciler/`)**: bounded, read-only CLI and library for normalizing large settlement statement files and producing deterministic discrepancy reports matching Go's `ReconcileStatementBatch`. It never mutates balances or initiates payouts.

## Accounting Principles

- **Exact Integer Units**: USD campaign budgets and reward liabilities use 2-decimal minor units (`USD/fiat`). Stablecoin payouts use 6-decimal base units (`USDC/base`). JSON payloads serialize all monetary amounts as base-10 decimal strings; TypeScript never coerces amounts to IEEE-754 floats.
- **Explicit Rounding**: reward accrual uses floor rounding (`MulBpsFloor`, `ProRataFloor`) so promises never exceed rules; platform fee and loss reserve allocations use ceiling rounding (`MulBpsCeil`) so funding never under-covers obligations.
- **Per-Asset Balancing Invariant**: every `JournalEntry` enforces `SUM(debits) == SUM(credits)` independently for every `(asset, network)` pair in its postings.
- **Prefunding Coverage**: `asset:provider_custody` must cover `campaign_budget_available + campaign_reserved + reward_pending + reward_payable + payout_clearing + loss_reserve`. Unreconciled (`pending`) deposits cannot back reward promises.

## Double-Entry Journal Matrix

| Event | Asset / Network | Debit Accounts | Credit Accounts | Notes |
|---|---|---|---|---|
| **Prefunding Reconciled** | `USD / fiat` | `asset:provider_custody[global]` | `liability:campaign_budget_available[campaign_id]` | Funding received and reconciled |
| **Order Paid & Reserved** | `USD / fiat` | `liability:campaign_budget_available[campaign_id]` (`R+F+L`) | `liability:reward_pending[customer_id]` (`R`), `liability:campaign_reserved[campaign_id]` (`F+L`) | Atomic budget & customer cap enforcement |
| **Pre-Eligibility Refund** | `USD / fiat` | `liability:reward_pending[customer_id]` (`dR`), `liability:campaign_reserved[campaign_id]` (`dF+dL`) | `liability:campaign_budget_available[campaign_id]` (`dR+dF+dL`) | `pro_rata` or `full_reversal` |
| **Reward Eligible** | `USD / fiat` | `liability:reward_pending[customer_id]` (`R`), `liability:campaign_reserved[campaign_id]` (`F+L`) | `liability:reward_payable[customer_id]` (`R`), `revenue:platform_fees[global]` (`F`), `liability:loss_reserve[campaign_id]` (`L`) | Requires `payment_settled && fulfilled && now >= eligible_at` |
| **Pre-Payout Refund (after eligible)** | `USD / fiat` | `liability:reward_payable[customer_id]` (`dR`), `revenue:platform_fees[global]` (`dF`), `liability:loss_reserve[campaign_id]` (`dL`) | `liability:campaign_budget_available[campaign_id]` (`dR+dF+dL`) | Reverses before irreversible payout |
| **Payout Prepared** (Tx 1) | `USD / fiat` | `liability:reward_payable[customer_id]` | `liability:payout_clearing[logical_payout_id]` | Committed before calling payout provider |
| **Payout Failed** (confirmed) | `USD / fiat` | `liability:payout_clearing[logical_payout_id]` | `liability:reward_payable[customer_id]` | Never posted while `outcome_unknown` |
| **Payout Settled** | `USD / fiat` + `USDC / base` | USD: `liability:payout_clearing` → CR `asset:provider_custody`; USDC: `clearing:fx_usdc` (`gross`) → CR `clearing:external_payout_settled` (`net`) + CR `liability:provider_fees` (`fee`) | Balanced independently per asset | Reduces liability and records on-chain net + fee |
| **Post-Payout Chargeback / Late Refund** | `USD / fiat` | `liability:loss_reserve[campaign_id]` (`min(loss, reserve)`) | `liability:campaign_budget_available[campaign_id]` | Irreversible payout stays `paid`; opens `post_payout_chargeback` exception |

## Reward and Payout Lifecycles

- **Reward states**: `pending` → `eligible` → `payout_requested` → `paid`, or `reversed` prior to payout.
- **Payout attempt states** (tracked independently): `prepared`, `submitted`, `pending`, `succeeded`, `failed`, or `outcome_unknown`.

See [Database and Recovery](database.md) for two-phase payout persistence and [Threat Model](threat-model.md) for security invariants.
