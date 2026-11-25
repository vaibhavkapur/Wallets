# Architecture

[Documentation home](index.md)

```text
Customer / Operator → React UI (TypeScript + BigInt money)
                            │
                            ▼
                  Go Application API (:8090)
                   ├─ Versioned RecurringRule CRUD (v1, v2, ...)
                   ├─ IANA Timezone / DST / Leap Scheduler
                   ├─ Deterministic Policy Engine (SKU, pack, price, budget, sub)
                   ├─ Immutable Consent & Atomic Budget Reservation
                   └─ Transactional Notification Outbox & Durable Job Queue
                            │
                            ▼
                Go Recovery Worker (embedded or standalone)
                   ├─ Due-rule scanner & multi-slot outage coalescing
                   ├─ Fresh quote fetcher (MerchantAdapter)
                   ├─ Checkout handoff (Sandbox or Agent-Checkout HTTP)
                   ├─ Timeout-unknown reconciliation (by idempotency key)
                   └─ Outbox notification dispatcher

PostgreSQL or FileStore ← rules, occurrences, quotes, proposals, consents,
                          reservations, checkouts, jobs, outbox, audit log
Merchant Webhooks       → HMAC-SHA256 verified fulfillment & partial/full refunds
```

## Application Boundary

Deterministic Go backend code authorizes every spending action. The UI proposes configuration changes and submits customer approvals; untrusted merchant responses or client payloads cannot alter rule constraints, bypass budget caps, or fabricate payment outcomes.

Raw card details (PAN/CVC), wallet private keys, and reusable signing credentials are never accepted, stored, or logged. Payment methods are referenced only by scoped credential identifiers (`pm_*`).

## Schedule Semantics and Occurrence Uniqueness

Calendar schedules (`weekly` or `monthly`) and minimum-interval schedules (`interval_days`) are strictly separated and validated so they can never be interchanged:

- **DST gaps** (e.g. `America/New_York` 02:30 on spring-forward Sunday) shift deterministically to the first valid instant after the gap (03:00 EDT).
- **DST repeated hours** (e.g. `America/New_York` 01:30 on fall-back Sunday) pick the first or second occurrence explicitly according to `dst_policy`.
- **Leap dates and shorter months** clamp to month-end (`clamp_month_end`) or carry Feb 29 to March 1 (`carry_march_first`).
- **Unique logical slots**: Each due slot produces a deterministic `slot_key` (`{kind}:{utc_rfc3339}`) and `idempotency_key` (`idem:{tenant}:{rule}:{slot}`). Both PostgreSQL (`UNIQUE (tenant_id, rule_id, slot_key)`) and `FileStore` prevent duplicate occurrences under worker restarts or concurrent scans.
- **Outage coalescing**: When clock progression spans multiple missed schedule slots, `ComputeDueWindow` creates at most one recovery occurrence (`outage_recovery: true`, `missed_slots_count: N`) awaiting customer approval and advances `next_run_at_utc` strictly past `now`.

## Execution-Time Policy Gate and Provider Boundary

No database transaction is ever held open across a merchant quote, checkout submission, reconciliation lookup, or order cancellation call:

1. **Quote Phase**: Commit `scheduled -> quoting`, fetch fresh `MerchantQuote` outside the DB lock, then commit quote + `PurchaseProposal` + policy evaluation (exact SKU, pack size, stock, substitution rules, delivered-price cap, monthly budget preview).
2. **Approval Binding**: `ApproveProposal` verifies rule status (`active`), current rule version (`proposal.rule_version == rule.current_version`), expiry, policy block flags, explicit substitution confirmation (when required), optional quote digest / amount pins, and absence of unresolved prior orders before writing an immutable `ConsentRecord`. Editing a rule creates a new version and immediately supersedes older pending proposals.
3. **Execution Phase 1 (Atomic DB commit)**: Re-verify product-level global pause, rule status, rule version, unrevoked/unexpired `ConsentRecord`, unresolved prior orders, minimum elapsed interval, and monthly budget capacity (`used + delivered_total <= monthly_budget`). Atomically persist `BudgetReservation` (`reserved`), `CheckoutReference` (`pending`), and transition `approved -> executing`.
4. **Execution Phase 2 (External provider call)**: Call `CheckoutAdapter.SubmitCheckout` outside any DB lock using the occurrence's `idempotency_key`.
5. **Execution Phase 3 (Outcome commit)**:
   - Settled → `completed` / `settled`, reservation `settled`, fulfillment `confirmed` or `delayed`.
   - Failed → `failed` / `failed`, reservation `released`.
   - Timeout / transport error → `needs_reconciliation` / `timeout_unknown`, reservation `uncertain_held` (never released and never retried with a new payment attempt until reconciled).

## Independent Payment, Fulfillment, and Pause Semantics

- **Duplicate-order prevention**: Any prior occurrence for a rule in `executing`, `needs_reconciliation`, `approved`, unresolved payment (`pending`, `authorized`, `timeout_unknown`), or non-terminal fulfillment (`unfulfilled`, `confirmed`, `delayed`, `shipped`) blocks a new purchase attempt until delivered or canceled.
- **Pause vs. Cancel**: Pausing a rule stops all future occurrences immediately. If a pause races with an in-flight provider submission already past Phase 1, the occurrence is flagged `pause_raced_submission: true`, future runs remain blocked, and the already-submitted order can be explicitly canceled via `POST /v1/occurrences/{id}/cancel-order`.
- **Money representation**: All monetary amounts use integer minor units (`int64`) with explicit `asset`, `network`, and `decimals`, serialized as JSON decimal strings (`"2499"`). TypeScript formats and computes exclusively with `BigInt`.
