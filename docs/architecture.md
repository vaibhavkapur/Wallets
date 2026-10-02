# Architecture

[Documentation home](index.md)

```text
Customer → React UI → Go application API
                          ├─ Structured intent and sandbox merchant quotes
                          ├─ Card eligibility and versioned reward ranking
                          ├─ Exact-checkout approval and budget reservation
                          └─ Transactional outbox
                                      ↓
                             Go recovery worker
                              ├─ Sandbox payment provider
                              ├─ Payment status lookup
                              └─ Refund confirmation

PostgreSQL or file store ← approvals, attempts, orders, ledger, audit, outbox
Operator UI             → pause, reconciliation, ledger, fulfillment simulation
```

## Application Boundary

The Go service authorizes purchases deterministically. The UI supplies product selection and approval actions; it cannot supply a successful payment result or increase the account budget. There is no model-driven execution component.

Merchant quotes contain delivered totals, stock, accepted card networks, delivery estimates, return terms, currency, and a 15-minute expiry. Card ranking excludes unsupported or unenrolled cards and sorts eligible cards by estimated cashback.

## Approval and Spending

An approval stores the checkout version, a SHA-256 fingerprint of its purchase-critical fields, a five-minute expiry, and revocation state. The fingerprint covers the intent, quote, selected card, reward rules, and sandbox scenario. Changing merchant, card, or scenario increments the version and invalidates earlier approval.

Execution revalidates the merchant quote, approval, card eligibility, per-purchase limit, account budget, and pause state. It atomically persists one payment attempt, reserves its amount, records the idempotency key, and enqueues payment work. The sandbox provider checks approval validity again before the first submission.

Amounts are integer USD cents serialized as decimal strings. TypeScript uses `BigInt` for monetary arithmetic. The account budget is a spending policy, not a held deposit.

## Payment and Order State

- A quoted checkout awaits approval, becomes approved, and enters execution. Successful payment completes it; cancellation or a confirmed failure closes it.
- A payment independently tracks pending, customer action required, outcome unknown, succeeded, or failed.
- An order independently tracks confirmed, shipped, delivered, failed, cancellation requested, or canceled fulfillment.
- Refunds are queued separately. Pending requests count against the refundable amount before confirmation.

Successful payment with a failed merchant order remains visible as a charge requiring support or refund. A timeout retains the reservation. Recovery queries the existing provider record instead of creating another attempt.

The operator can simulate shipping and delivery updates on eligible receipts. The return window ends 30 days after recorded delivery. Cancellation is restricted to unshipped orders or merchant-order failures.

## Processes and Integrations

The API runs an embedded worker by default. PostgreSQL also supports `backend/cmd/worker` as a separate process. Outbox leases and idempotent provider identifiers let work resume after a crash.

`backend/internal/partners` contains independent ACP and Stripe test-token clients, not selected by the sandbox runtime. See [Protocol Versions](protocol-versions.md) for verification boundaries and [Database and Recovery](database.md) for persistence.
