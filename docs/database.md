# Database

PostgreSQL 16. Migrations are embedded in the binary (`backend/migrations/*.sql`) and applied once each at startup, recorded in `schema_migrations`.

| Table | Purpose | Key invariants |
|---|---|---|
| `users` | Account established by Sign in with Apple | `apple_subject` unique; `status ∈ active, deleting, deleted`; deletion scrubs email and rewrites the subject to `deleted:<id>` so the Apple ID can create a fresh account |
| `sessions` | Opaque bearer tokens | only `token_hash` (SHA-256) is stored; `expires_at`, `revoked_at` |
| `provider_customers` | User → provider customer mapping | one per user (PK `user_id`); `(provider, customer_ref)` unique; derived from the authenticated account, never from the caller |
| `consent_records` | Agreement to save a card for a purpose | `purpose`, `consent_version`, timestamp; referenced by each attempt |
| `setup_attempts` | One enrollment attempt | `(user_id, idempotency_key)` unique; `(provider, setup_ref)` unique; `status` check constraint; partial index on open attempts for the stale sweep |
| `saved_cards` | Provider payment-method reference + masked display fields | `(provider, payment_method_ref)` unique (replay safety); `last4` exactly 4 chars; `exp_month` 1–12; partial unique index: at most one preferred, non-removed card per user; `state ∈ saved, removal_pending, removed` |
| `provider_events` | Processed webhook IDs | PK `(provider, event_id)`; insert-or-skip gives deduplication |
| `pending_operations` | Durable retryable work | `kind ∈ detach_card, delete_account, reconcile_setup`; partial unique `(kind, subject_id)` while active; `attempts`, `next_attempt_at`, `last_error`, `completed_at` |

Never stored: full card numbers, CVCs, setup client secrets, ephemeral keys, raw session tokens, raw webhook payloads.

## Locking

- State transitions on attempts and cards run inside `READ COMMITTED` transactions that take `SELECT … FOR UPDATE` on the row.
- Inserts that may collide (`setup_attempts`, `saved_cards`, `provider_customers`) use `ON CONFLICT DO NOTHING` and then read the winner, so a duplicate does not abort the enclosing transaction.
- The worker claims operations with `FOR UPDATE SKIP LOCKED` and pushes `next_attempt_at` forward as a lease, so several workers (or a crashed one) cannot double-run or starve the queue.

## Retention

Account deletion removes the provider customer and mapping, marks cards `removed`, and scrubs PII from `users`. Rows in `setup_attempts`, `saved_cards` (masked fields only), `consent_records`, and `provider_events` are kept for audit; the retention period is a product decision still open in the plan.
