# Test Report

[Documentation home](index.md)

Recorded: **24 November 2026**. A local verification snapshot, not a claim about later commits or external provider certification.

## Verified During Implementation

- `go test -race ./...` passed across `internal/money` and `internal/rewards`, including:
  - Exact integer decimal-string JSON serialization, float rejection, overflow protection, floor/ceil bps rounding, and Base `USDC` address validation.
  - Full lifecycle (unreconciled vs. reconciled prefunding, duplicate event/order deduplication, partial return pro-rata reversal, separate payment vs. fulfillment eligibility, quote generation, idempotent payout settlement, post-payout chargeback loss-reserve draw and exception).
  - 40-goroutine concurrent campaign cap enforcement with `-race` enabled.
  - Immutable `BeneficiaryVersion` rotation rejecting stale quotes and quote expiry enforcement.
  - Provider timeout (`outcome_unknown`) blocking blind retries until worker reconciliation settles or fails, duplicate callback deduplication, late confirmation exception, and provider custody drift detection.
  - Global pause controls, minimum payout threshold enforcement, HMAC-SHA256 webhook verification, operator RBAC, and tenant isolation.
  - Shared statement batch fixture parity (`fixtures/statement_batch_input.json` → `fixtures/statement_batch_expected.json`).
- `go vet ./...` passed with zero issues.
- Rust `reconciler/` passed `cargo test` (byte-for-byte parity with Go reference on shared fixtures), `cargo clippy --all-targets -- -D warnings`, and `cargo build --release`.
- Frontend `web/` passed `npm run typecheck` (`tsc -b`), `npm test` (4/4 `BigInt` precision tests including integers exceeding `Number.MAX_SAFE_INTEGER`), and `npm run build` (`vite build`).
- Go `rewards-server` and `rewards-worker` binaries compiled cleanly.
- OpenAPI contract drift check (`make check-contracts`) passed against `contracts/openapi.json`.

## Not Yet Established

- `TestPostgresStoreIntegration` skipped locally because no local PostgreSQL server was running; GitHub Actions provisions PostgreSQL 17.6 to run this test on CI.
- Docker Compose container startup, live merchant integrations, live custody/payout provider APIs, KYC/screening, and commercial R1–R4 pilot milestones were not executed.

Use [Testing](testing.md) to reproduce checks. Current command output and commit-specific GitHub Actions results supersede this snapshot.
