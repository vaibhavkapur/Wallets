# Database and Recovery

[Documentation home](index.md)

## File Store (Local Single-Process Fallback)

When `DATABASE_URL` is unset, `FileStore` persists tenant-scoped state in `.local/rewards-state.json`. It acquires an exclusive `flock` on `.local/rewards-state.json.lock`, executes mutations on an isolated deep clone, writes to a temporary file, calls `fsync`, and atomically renames over the state file. Failed mutations roll back completely without modifying in-memory or on-disk state.

Only one process may open a `FileStore` at a time. Use PostgreSQL for multi-process or standalone worker deployments.

## PostgreSQL (`PostgresStore`)

The schema is embedded from [001_init.sql](../backend/migrations/001_init.sql) and applied idempotently at startup (`CREATE TABLE IF NOT EXISTS`). It defines:

- `rewards_tenant_state`: tenant-scoped transactional aggregate locked with `SELECT ... FOR UPDATE` inside a `ReadCommitted` transaction so concurrent order reservations, ledger postings, and outbox writes serialize atomically per tenant.
- Normalized relational tables (`merchants`, `campaign_versions`, `funding_events`, `purchase_attributions`, `campaign_reservations`, `reward_accruals`, `ledger_accounts`, `journal_entries`, `beneficiary_versions`, `payout_quotes`, `payout_attempts`, `reconciliation_exceptions`, `outbox_events`) using `NUMERIC(39,0)` integer base units and SQL `CHECK`/`UNIQUE` constraints for production scale migration and reporting queries.

## Two-Phase Payout Persistence & Timeout Recovery

Database transactions are NEVER held open across external payout provider requests:

1. **Tx 1 (`AuthorizePayout` Phase 1):** validate quote expiry, single-use consumption, active verified `BeneficiaryVersion`, eligible reward sum, and ledger payable balance. Transition rewards `eligible → payout_requested`, post balanced `payout_prepared` journal (`reward_payable → payout_clearing`), write `PayoutAttempt` (`prepared`) and `OutboxEvent`, and commit.
2. **External Call (Phase 2):** submit `ProviderPayoutRequest` with the durable `IdempotencyKey` outside any transaction.
3. **Tx 2 (Phase 3):** persist provider response:
   - `succeeded`: post balanced USD + USDC settlement journal, reduce custody and clearing, transition rewards to `paid`.
   - `pending` / `submitted`: keep clearing locked; worker polls `GetPayoutStatus` or processes signed `/v1/webhooks/payout-provider` callbacks.
   - `failed` (confirmed rejected): reverse clearing back to `reward_payable` and return rewards to `eligible`, enabling safe `RetryPayout` reusing `logical_payout_id`.
   - Transport timeout / error: mark `outcome_unknown`, keep funds locked in `payout_clearing`, and open a critical `unknown_payout_outcome` exception. Blind retries are rejected (`ErrOutcomeUnknown` / `409 Conflict`) until the reconciliation worker queries `GetPayoutStatus` and confirms whether the provider broadcast the transfer.

## Daily & On-Demand Reconciliation

`RunWorkerTick` runs three phases:

1. Promotes `pending` rewards to `eligible` only when `payment_settled == true`, `fulfilled == true`, and `now >= eligible_at`, recognizing platform fee revenue and funding the campaign loss reserve.
2. Polls provider status (outside DB tx) for `submitted`, `pending`, and `outcome_unknown` attempts and applies idempotent terminal transitions.
3. Fetches the provider custody snapshot (outside DB tx), verifies every journal balances by asset, checks `custody >= total_liabilities`, and opens `provider_balance_mismatch`, `ledger_imbalance`, or `funding_coverage_deficit` exceptions on any discrepancy.

See [Testing](testing.md) for PostgreSQL verification commands.
