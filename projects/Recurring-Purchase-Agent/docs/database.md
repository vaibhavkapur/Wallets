# Database and Recovery

[Documentation home](index.md)

## File Store (Local Default)

When `DATABASE_URL` is unset, `FileStore` persists tenant state and global pause controls to `.local/recurring-state.json`. It enforces single-process ownership with Unix `flock`, performs copy-on-write state cloning per `Update`, writes to a temporary file with `fsync`, atomically renames over the target path, and `fsync`s the parent directory. Failed update callbacks never mutate committed state.

Because only one writer may hold the file lock, run the server with `RUN_EMBEDDED_WORKER=true` in file-store mode. State files, locks, caches, and build outputs are excluded from Git.

## PostgreSQL

The schema is defined in [001_schema.sql](../backend/migrations/001_schema.sql) and initialized automatically by Docker Compose (`docker-entrypoint-initdb.d`). On startup, `PgStore` also ensures the `recurring_tenants` and `recurring_global_controls` tables exist idempotently.

- **Row-level locking**: Every tenant mutation opens a transaction and locks the tenant row with `SELECT ... FOR UPDATE`, serializing concurrent approvals, budget reservations, job leases, and rule updates for that tenant without locking other tenants.
- **Database uniqueness constraints**: `schedule_occurrences` defines `UNIQUE (tenant_id, rule_id, slot_key)` and `UNIQUE (tenant_id, idempotency_key)`; `budget_reservations` and `checkout_references` enforce `UNIQUE (tenant_id, occurrence_id)`; `worker_jobs` enforces `UNIQUE (tenant_id, kind, dedupe_key)`.
- **Provider isolation**: No database transaction is ever held open during merchant quoting, checkout submission, reconciliation, or order cancellation.

## Durable Worker Leases and Outbox Recovery

1. **Due-rule scan**: `ScanDueRules` creates at most one `ScheduleOccurrence` per `(rule_id, slot_key)` (coalescing multi-slot outages into one recovery proposal) and enqueues a deduplicated `quote_occurrence` job.
2. **Job leasing**: `Worker.acquireNextLease` claims one pending due job with a 30-second lease (`lease_owner`, `lease_until`) and increments `attempts`.
3. **Stale-lease reclamation**: On every tick, `reclaimExpiredLeases` returns crashed workers' expired leases to `pending` (or `dead` once `attempts >= max_attempts`), enabling another worker to resume cleanly.
4. **Timeout reconciliation**: When `CheckoutAdapter.SubmitCheckout` returns `ErrProviderTimeout`, the occurrence enters `needs_reconciliation` (`payment_status = timeout_unknown`), the `BudgetReservation` transitions to `uncertain_held`, and a `reconcile_checkout` job is enqueued. Reconciliation queries `ReconcileCheckout(idempotencyKey)` and either settles the existing order or releases the budget on confirmed failure—never submitting a second charge.
5. **Notification outbox**: Customer reminders, approval requests, price/stock alerts, and outage-recovery notices are persisted inside the same atomic transaction as the state change and dispatched by `dispatch_notification` jobs.
