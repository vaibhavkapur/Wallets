# Database

[Documentation home](index.md)

## Persistence

PostgreSQL is the only durable store; caches never decide an available balance, and balances are not stored at all except inside the quote snapshot of an evaluation. The schema lives in `orchestrator/migrations/0001_init.sql` and is applied by `store.Open` on startup under an advisory lock, tracked in `schema_migrations`. Every query is scoped by `tenant_id`.

Tables: `tenants`, `api_keys`, `product_controls`, `preference_policies`, `basket_snapshots`, `reward_rule_versions`, `evaluations`, `rail_quotes`, `execution_handoffs`, `observed_outcomes`, `outbox`. API keys are stored as SHA-256 hashes with their scopes; no raw card data, wallet keys or reusable credentials exist anywhere in the schema.

## Append-only snapshots

A `BEFORE UPDATE OR DELETE` trigger (`forbid_mutation`) rejects any change to `preference_policies`, `basket_snapshots`, `reward_rule_versions`, `evaluations`, `rail_quotes` and `observed_outcomes`. An evaluation row holds the exact request text sent to the engine, its SHA-256 as confirmed by the engine, the result as JSONB, every version that determined it, the outcome, preferred and baseline option ids, warnings, attribution and expiry. Its rail quotes (one per option, the full `PaymentOption` as used) and its basket snapshot are written in the same transaction.

`reward_rule_versions` is keyed by `(tenant_id, rule_id, rule_version)` and stores a content hash of the rule definition (earn rule, cap limit, description; the consumed amount of a cap is excluded because it changes over time). A later evaluation presenting the same version with different contents is rejected with `invalid_quote_data` instead of silently recording a changed rule.

## Idempotency and concurrency

- `evaluations (tenant_id, idempotency_key)` and `execution_handoffs (tenant_id, idempotency_key)` are unique; a replay with the same request fingerprint returns the original, a different fingerprint is `409 idempotency_key_reused`.
- `execution_handoffs_one_unresolved` is a partial unique index on `(tenant_id, customer_id, basket_hash) WHERE status IN ('created', 'unknown')`. Concurrent handoff requests race on it inside the insert transaction; the loser receives `prior_attempt_unresolved`.
- `observed_outcomes (tenant_id, source, event_id)` is unique. Recording an outcome locks the handoff row `FOR UPDATE` (or takes a transaction-scoped advisory lock derived from the event when there is no handoff), reads prior outcomes, derives the new state and updates the handoff status in one transaction. Handoff `status` is the only mutable application field besides `updated_at`.
- `preference_policies (tenant_id, customer_id, version)` is unique; versions are allocated with `MAX(version) + 1` and retried on conflict.

## Outbox and workers

`outbox` rows are inserted in the same transaction as the handoff they announce. The relay claims rows with `FOR UPDATE SKIP LOCKED`, sets `claimed_until` and increments `attempts` in one short transaction, delivers with no transaction open, then marks `delivered_at` or schedules `available_at` for a retry. The sweep moves `created` handoffs past `expires_at` plus a grace period to `unknown`.

The development stack keeps its embedded PostgreSQL data in `.dev/postgres/`; tests use a fresh embedded instance per package run.
