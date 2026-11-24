# Database and Recovery

[Documentation home](index.md)

## File Store

The default `.local/state.json` holds tenant-scoped checkout state and sandbox provider records. It uses an exclusive process lock, copy-on-write updates, atomic rename, and file/directory synchronization. A failed callback does not publish changes.

Only one process may open a file store; the API runs its worker in-process. State, locks, build outputs, and caches are excluded from Git.

## PostgreSQL

The initial schema is [001_tenant_aggregate.sql](../backend/migrations/001_tenant_aggregate.sql). Startup executes an equivalent idempotent `CREATE TABLE IF NOT EXISTS`; it is not a versioned migration runner.

Each `checkout_tenants` row contains a JSONB aggregate, revision, and update timestamp. Mutations lock the tenant row using `SELECT ... FOR UPDATE`. Records include intents, checkouts, approvals, attempts, orders, refunds, card references, idempotency, provider fixtures, webhook deduplication, jobs, audit, and ledger entries.

Approval validation, budget calculation, reservation, idempotency, and outbox writes commit together. Provider work happens after commit. Tenants do not share records; same-tenant mutations serialize on the row.

This is MVP aggregate persistence. Reporting indexes, normalized high-volume records, versioned migrations, and retention policies remain future work.

## Outbox Recovery

1. Execution persists one payment attempt and reserves budget before submission.
2. A worker claims a due job with a 30-second lease and commits the claim.
3. The provider operation uses the saved payment or refund ID for idempotency.
4. Success converts the reservation to net spending; confirmed failure releases it.
5. An uncertain result preserves the reservation and queues reconciliation. The timeout fixture is normally looked up after 12–15 seconds.
6. A crashed worker's lease expires, allowing another worker to resume the logical operation.

The sandbox provider's charge records persist separately from application outcome updates in the same store. The timeout fixture records success, loses its first response, then returns that same charge on lookup. It demonstrates recovery without an external provider.

## Ledger and Refunds

The append-only policy ledger uses `reserve`, `capture`, `release`, and `refund` events. It is not a double-entry funds ledger. Available budget equals the limit minus net spending and outstanding reservations.

Pending and completed refunds count toward the refund cap. Budget is credited only after confirmation. Cancellation can refund the remainder after a partial refund. Replaying an identical idempotency key returns the existing request; a different payload with that key is rejected.

See [Testing](testing.md) for PostgreSQL verification and [Deployment](deployment.md) for a separate worker.
