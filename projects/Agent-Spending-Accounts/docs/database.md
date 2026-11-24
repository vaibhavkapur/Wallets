# Database and Ledger

[Documentation home](index.md)

## Persistence

PostgreSQL is the only durable store; caches never decide an available balance. The schema lives in `control-plane/migrations/0001_init.sql` and is applied by `store.Migrate` on startup, tracked in `schema_migrations`. Amounts are `NUMERIC(39,0)` base units with non-negative checks; the application additionally bounds them to 2^128−1 so Go, Rust, and TypeScript agree on representable values.

Tables: `organizations`, `owner_keys`, `agents`, `policy_versions`, `tasks`, `account_balances`, `payment_attempts`, `reservations`, `execution_grants`, `receipts`, `ledger_entries`, `audit_events`, `outbox`. Credentials are stored only as SHA-256 hashes. Policy versions are immutable; agents point at their current version and tasks remember the version they were created under.

## Reservation protection

`store.Reserve` locks `payment_attempts` → `organizations` → `agents` → `tasks` → `account_balances` with `SELECT … FOR UPDATE` in that order, recomputes usage from `reservations` under the lock, and inserts the hold, the grant row, the ledger journal, the audit event, and the outbox message in the same transaction. `UNIQUE (task_id, idempotency_key)` on `payment_attempts` makes duplicate requests return the original attempt; `UNIQUE (payment_attempt_id)` on `reservations` and `receipts` prevents double holds and double receipts.

Status transitions are enforced in `store.ResolveAttempt`: `settled` requires a prior `submitted` and amounts within the reservation; `unknown` requires a prior `submitted`; `failed` and `expired` release the hold; terminal states cannot change. `execution_grants.consumed_at` is set exactly once by `MarkSubmitted`.

## Ledger

Every balance change posts a balanced journal (`postJournal` rejects unbalanced legs) across these accounts per organization, asset, and network:

- `funding:external` — counterparty for owner deposits and withdrawals.
- `org:<id>:available` — spendable customer funds.
- `org:<id>:reserved` — funds held by open reservations.
- `settlement:<network>` — funds paid to resource beneficiaries.

Reserve moves `amount + fee_allowance` from available to reserved; settle moves the actual cost to settlement and returns the unused remainder; release returns the full hold. `store.LedgerInvariant` recomputes available and reserved from `ledger_entries` and compares them with `account_balances`; the Go API tests assert it after every balance read.

## Audit, outbox, and retention

`audit_events` records every owner, agent, gateway, and worker action with the actor and a JSON detail blob that never contains credentials or payment headers. `outbox` rows are written transactionally with state changes and published in order by the worker (`LogPublisher` by default). Both tables are append-only. Back up the database together with the grant signing seed; without the seed, in-flight grants cannot be verified after a restart.

The development stack and tests use an embedded PostgreSQL whose data directory is deleted on shutdown. Compose persists data in the `pgdata` volume.
