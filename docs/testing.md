# Testing

[Documentation home](index.md)

## Unit and integration suites

```bash
make test-go      # go vet + go test ./... -race (embedded PostgreSQL)
make test-rust    # cargo clippy --all-targets -D warnings + cargo test (unit and property tests)
make test-ts      # build the SDK, typecheck every workspace, run Vitest suites
make test         # all of the above
```

The Go suite (`control-plane/internal/httpapi`) drives the full HTTP API against a real PostgreSQL started by `internal/devpg`. Set `TEST_DATABASE_URL` to use an existing server instead. Cases: happy path with receipt and grant claims, partial settlement returning unused holds, 40 concurrent requests against one task budget, 20 concurrent requests against an agent-daily limit across tasks, idempotent replay, cross-tenant isolation, challenge validation (beneficiary, asset, network, scheme, amount, resource, hash mismatch, SSRF-style URLs), grant replay and expiry with worker recovery, unknown outcomes held until reconciled, revocation and pause during execution, insufficient balance including fees, policy supersession and agent-side cap enforcement, internal-token and string-amount enforcement.

The Rust suite covers canonical amount parsing and checked arithmetic (with `proptest` over all `u128`), URL normalization, segment-aware prefix matching, forbidden IP ranges, pre-connect rejection of loopback and link-local hosts, x402 challenge parsing and requirement selection (changed beneficiary listed first, wrong network/asset, overpriced, malformed), payment header encoding, and grant verification including every binding mutation and payload mutation after issuance.

The TypeScript suites cover exact amount formatting and parsing, SDK request shapes and error mapping with a fake `fetch`, the MCP tools' default idempotency key and structured denials, and dashboard formatting plus the owner-key-only sign-in.

## End-to-end scenario

```bash
make up
make e2e
make live-sdk     # SDK contract test against the running stack
make example      # example agent walkthrough
make down
```

`scripts/e2e.sh` provisions an organization and runs 36 checks through the gateway against the mock resource: settlement with receipt and balance movement, idempotent replay, redirect refusal, changed beneficiary, wrong network, wrong asset, overpriced and malformed challenges, endpoint outside the allowlist, metadata-endpoint SSRF attempt, caller `max_amount`, settlement without a usable result, oversized response truncation, a lost response that is held as `unknown` and then reconciled to `settled` via the facilitator lookup, task budget exhaustion, and owner pause and revoke. It exits non-zero on any failure.

## Recorded report

`scripts/test-report.sh` runs every suite and the end-to-end scenario and rewrites [Test Report](test-report.md) with the dated results. Treat that file as a record of one run, not proof that tests ran on every later commit; current command output is authoritative.

## Expected invariants

Concurrent requests never exceed a task, agent-daily, organization-daily, or balance limit; the ledger always reconciles with the balance rows; a rejected or expired attempt holds nothing; a replayed or mutated grant is refused by both the gateway and the control plane; an unknown payment outcome keeps its reservation until the verifier answers; a settled payment with an undelivered result is recorded as such and never repurchased automatically. No live network, wallet provider, or facilitator is covered by the fixture suites.
