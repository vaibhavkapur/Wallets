# Architecture

[Documentation home](index.md)

## Overview

The model proposes actions; deterministic backend code authorizes spending. The Go control plane owns identity, policy, and the authoritative budget state. The Rust gateway performs the paid request but can neither grant nor increase budgets: it acts only on a short-lived, single-use execution grant issued after an atomic reservation, and it re-verifies that grant against the exact payload before anything is signed.

```text
Agent (SDK / MCP) ── agent key ──▶ Rust gateway ── internal token ──▶ Go control plane ──▶ PostgreSQL
                                        │                                   │
                                        │ 1 begin: auth, task, endpoint allowlist, idempotency
                                        │ 2 SSRF check + DNS pin → unpaid probe → 402 challenge
                                        │ 3 validate challenge vs policy; authorize → reservation + grant
                                        │ 4 verify grant bindings (org, agent, task, url, payTo, asset, network, scheme, amount, hash)
                                        │ 5 isolated signer signs only that authorization ──▶ signing provider
                                        │ 6 write-ahead "submitted" → send X-PAYMENT → bounded response
                                        │ 7 outcome: settled | failed | unknown, plus service outcome
                                        ▼
                               workers: release expired holds, reconcile unknown via facilitator, publish outbox
```

## Components and technology

- `control-plane/internal/money`: exact base-unit amounts (`big.Int`, ≤ 2^128−1), decimal-string JSON, checked arithmetic.
- `control-plane/internal/policy`: pure authorization engine (principals, endpoint and recipient allowlist, asset pinning, budget checks, denial codes).
- `control-plane/internal/store`: PostgreSQL persistence; `Reserve`, `MarkSubmitted`, and `ResolveAttempt` are the only paths that move money between `available`, `reserved`, and `settlement` accounts.
- `control-plane/internal/grant`: Ed25519 JWT issuance and the canonical requirements hash.
- `control-plane/internal/httpapi`: owner, agent, and internal (gateway) endpoints.
- `control-plane/internal/workers`: expired-authorization recovery, reconciliation through a `SettlementVerifier`, outbox publishing.
- `control-plane/cmd/mockresource` and `cmd/mocksigner`: fixtures for the resource server/facilitator and the signing provider.
- `gateway/src/urlguard.rs`, `x402.rs`, `grant.rs`, `signer.rs`, `client.rs`, `flow.rs`: destination validation, challenge handling, grant verification, signer client, bounded fetching, and the orchestrated flow.
- `sdk/`, `mcp/`, `dashboard/`: agent and owner clients, MCP tools, React console.

## Execution guarantees and limits

**Atomic reservations.** `Reserve` runs in one transaction that locks the organization, agent, task, and balance rows in a fixed order, recomputes task, agent-daily, and organization-daily usage under the lock, and holds `amount + fee_allowance`. Two concurrent requests cannot both observe the same available capacity; policy denials mark the attempt rejected in the same transaction and hold nothing.

**Bound, single-use grants.** The grant binds every fact about the payment; the gateway refuses a grant whose bindings differ from its local context and tracks used grant ids. The control plane consumes the grant when the gateway records submission, so replay is rejected by both layers. Grant lifetime is at most 300 seconds and also bounds the signed authorization's validity.

**Write-ahead submission.** The gateway records `submitted` before transmitting. An `authorized` attempt that never reached `submitted` provably never paid and is released after grant expiry plus a margin; a `submitted` attempt without an outcome becomes `unknown` and is reconciled through the facilitator lookup. Nothing is released on a lost HTTP response alone.

**Settlement is not delivery.** The receipt records payment status and a separate service outcome (HTTP status, delivered flag, body digest, truncation). A settled payment with an unusable result is not automatically repurchased; identical requests reuse the idempotency key.

**Untrusted resources.** URLs are normalized, matched to the owner's endpoint prefix on both sides, resolved once, checked against forbidden address ranges, and pinned for the connection. Redirects are never followed; bodies are capped; the 402 challenge is data, never instructions.

Limits of the current implementation are listed in [Threat Model](threat-model.md); the exact Go/Rust contract is in [Grant Contract](grant-contract.md).
