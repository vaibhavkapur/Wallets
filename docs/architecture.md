# Architecture

[Documentation home](index.md)

## Overview

The engine computes; the orchestrator authorizes. The Rust engine is a pure function from a versioned request to a versioned result and holds no state, credentials or clock. The Go orchestrator owns quotes, balances, preferences, persistence, consent, handoffs and measurement, and it never computes a cost. The interface renders what it is given.

```text
Comparison UI / SDK / partner agent ── API key (scopes) ──▶ Go orchestrator ──▶ PostgreSQL (append-only snapshots)
                                                                │
                                                                │ 1 load preferences (latest immutable version)
                                                                │ 2 quotes (cacheable until expiry) + balances (never cached)
                                                                │ 3 apply consent, reserves, constraints → engine request
                                                                │ 4 POST /v1/evaluate ── bearer token ──▶ Rust engine ──▶ result + SHA-256(request)
                                                                │ 5 persist basket snapshot, rail quotes, reward rule versions, evaluation
                                                                │ 6 (assisted) approval → revalidate → handoff + outbox event ──▶ execution service
                                                                │ 7 outcomes from provider/executor → derived savings, handoff status
                                                                ▼
                                                 workers: outbox relay (HMAC webhook), expired-handoff sweep
```

## Components and technology

- `engine/crates/optimizer-core/src/amount.rs`: canonical decimal-string parsing, `i128` checked helpers, directed division.
- `engine/crates/optimizer-core/src/contract.rs`: request and response types (serde, `deny_unknown_fields`, JSON Schema derivation).
- `engine/crates/optimizer-core/src/validate.rs`: structural validation; a malformed request is rejected as a whole.
- `engine/crates/optimizer-core/src/engine.rs`: per-option exclusions and breakdowns, the decision rule, savings versus the baseline, assumptions and sensitivities. See [Calculation Rules](calculation.md).
- `engine/crates/optimizer-service/`: axum service with constant-time bearer comparison, a 1 MiB body limit, `POST /v1/evaluate`, `GET /v1/versions`, `GET /healthz`, and `optimizer-engine schema` for contract export.
- `orchestrator/internal/contract`: Go mirror of the request contract and the result fields the orchestrator reads; `Units` validates decimal strings exactly as the engine does.
- `orchestrator/internal/engine`: HTTP client that verifies the returned `input_sha256` against the bytes it sent and surfaces engine rejections with their field path.
- `orchestrator/internal/quotes`: `Provider` and `Balances` interfaces, the recorded provider (replays the dataset with re-stamped validity), a bounded quote cache that strips funds, and `quotestest.RunProviderContract` for any adapter.
- `orchestrator/internal/service`: evaluation assembly, preferences, handoffs, outcomes, reports and the sweep.
- `orchestrator/internal/store`: pgx persistence, migrations, idempotency, the one-unresolved-attempt index, outbox claims, report aggregation.
- `orchestrator/internal/api`: routing, API-key authentication, scope checks, strict JSON decoding, error mapping; bodies are never logged.
- `orchestrator/internal/outbox`: claim–deliver–mark relay with HMAC-SHA256 signatures and capped backoff.
- `web/packages/sdk`, `web/apps/compare`: generated types, client, formatting and explanation catalog; the React interface.

## Execution guarantees and limits

**Deterministic, bound results.** Equal requests produce equal responses; the service hashes the exact request bytes and the client refuses a result whose hash differs. Evaluations store the request text, the hash, the result and every version that determined it, so any stored evaluation can be reproduced.

**Nothing is assumed.** A fee of unknown amount, a required fee kind that was not quoted, a fee in an asset without a USD value, unverified merchant acceptance or jurisdiction, a stale or mismatched quote, an unreadable balance, or an overflow excludes the option as missing data and makes the outcome `insufficient_data`. Consent, availability, constraints, funding-source policy and funds are definite exclusions. Unverified rewards and discounts are valued at zero.

**Customer preferences are versioned and applied, never inferred.** Consent is the conjunction of the provider's record and the stored preference; a default method the provider does not offer produces a warning and no baseline rather than a guessed one; liquidity reserves reduce spendable balance per asset.

**Approval is bound.** A handoff requires assisted mode, an evaluation produced in assisted mode that has not expired, an eligible option, an approval by the evaluation's customer for exactly the engine's `amount_due` (asset and base units) given within the evaluation's window, and an `Idempotency-Key`. The handoff carries merchant, basket hash, rail, quote id, amount and expiry; the executor re-reads it, and `executable` turns false on expiry, pause or any status change.

**One attempt at a time; no silent rail switch.** A partial unique index allows one handoff in `created` or `unknown` per tenant, customer and basket hash. Concurrent requests race on the index, and exactly one wins. An expired handoff without an outcome is swept to `unknown` and keeps blocking until the executor reports `failed` or `succeeded`.

**Measurement is honest.** Realized immediate savings are recorded only for a settled, executed payment whose charge equals the estimate, against the baseline's immediate cost estimated at evaluation time and labelled `observed: false`. Estimated rewards are reported in a separate field. Shadow-mode observations and customer reports never realize savings. Refunds are recorded with their asset, cumulative total, conversion-cost status and timing, and may not exceed the charge.

**Boundaries.** No database transaction spans a call to the engine or a provider; balances are read on every evaluation; evaluations, quotes, basket snapshots, reward rule versions, preference versions and outcomes are append-only by trigger; a reward rule version seen with different contents rejects the evaluation; API keys are stored hashed with explicit scopes; a global or per-tenant pause stops evaluations and handoffs.

## Milestones

- **O1 Offline evaluation:** `cmd/offline-eval` and `TestOfflineEvaluation` run the recorded dataset through the engine and compare each result to its independently checked expectation.
- **O2 Shadow mode:** `OPTIMIZER_MODE=shadow` records evaluations with exclusion and warning counts in the report and accepts customer-reported selections; handoffs are refused.
- **O3 Assisted pilot:** `OPTIMIZER_MODE=assisted` adds approval handoffs and outcome ingestion.
- **O4 Integration:** the versioned API in `contracts/openapi.yaml` and the outbox payload (`execution_handoff.created`, version 1) are the contract for Agent Checkout, the Recurring Purchase Agent or an external agent.
