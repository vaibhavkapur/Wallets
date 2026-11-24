# Testing

[Documentation home](index.md)

## Automated Checks

```bash
make setup
make test
make build
make check-contracts
```

- `make test` runs:
  - Go unit, double-entry ledger, 40-goroutine concurrency, lifecycle, recovery, HTTP API, and shared-fixture tests with `-race`.
  - `go vet ./...`.
  - Rust batch reconciler `cargo test` (shared fixture parity with Go) and `cargo clippy --all-targets -- -D warnings`.
  - TypeScript strict type-checking (`tsc -b`) and `BigInt` decimal formatting/parsing tests (`npm test`).
- `make build` compiles the Vite production frontend, Go `rewards-server` and `rewards-worker` binaries, and the Rust release reconciler binary.
- `make check-contracts` regenerates OpenAPI from `backend/cmd/schema` and verifies zero drift against `contracts/openapi.json`.

If API types or endpoints change intentionally, run `make contracts` and commit the updated `contracts/openapi.json`.

## Covered Invariants

- **Exact Money & Rounding (`backend/internal/money/money_test.go`, `web/tests/money.test.mjs`)**: JSON decimal-string roundtrips, explicit float rejection, `int64` overflow checks, floor vs. ceil bps rounding, USD-to-USDC conversion, Base EVM address validation (rejecting unsupported assets/networks and zero addresses), and TypeScript `BigInt` precision beyond `Number.MAX_SAFE_INTEGER`.
- **Accounting & Concurrency (`backend/internal/rewards/service_test.go`)**:
  - Unreconciled pending funding cannot back reward promises.
  - Duplicate event IDs and duplicate order IDs never create duplicate obligations.
  - 40 concurrent goroutines competing for a $100 budget cap reserve exactly 10 × $10 rewards with zero over-allocation and zero data races.
  - Partial returns reverse the exact pro-rata reward, fee, and loss reserve; unfulfilled orders never become eligible even after the return-window duration elapses.
  - Post-payout chargebacks never claw back paid stablecoin rewards; they draw from campaign `loss_reserve` and open a `post_payout_chargeback` operator exception.
- **Payout Safety & Recovery (`service_test.go`)**:
  - Rotating a customer's beneficiary revokes the prior version and rejects quotes bound to the old version.
  - Expired quotes and balances below the disclosed minimum payout threshold are rejected.
  - Provider timeouts (`timeout_succeeded` and `timeout_failed`) persist `outcome_unknown` and reject blind retries (`ErrOutcomeUnknown`) until the worker reconciles the actual provider status.
  - Duplicate provider callbacks deduplicate cleanly; late confirmations on failed attempts and provider custody drift open operator reconciliation exceptions.
  - Global pause controls block new reward issuance and payouts.
- **HTTP Security & Isolation (`http_test.go`)**: operator RBAC (`403` without valid token), HMAC-SHA256 webhook verification (`401` when unsigned/tampered), tenant isolation across `X-Tenant-ID`, and idempotent payout submission.
- **Go + Rust Parity (`statement_reconciler_test.go`, `reconciler/src/lib.rs`)**: both implementations independently process `fixtures/statement_batch_input.json` and match `fixtures/statement_batch_expected.json`.

## PostgreSQL Integration

```bash
docker compose up -d db
TEST_DATABASE_URL='postgres://rewards:rewards_local@127.0.0.1:54329/rewards?sslmode=disable' make test
```

When neither `TEST_DATABASE_URL` nor `DATABASE_URL` is set, `TestPostgresStoreIntegration` skips cleanly. GitHub Actions provisions PostgreSQL 17 to execute this check automatically.

## Evidence Scope

The [Recorded Test Report](test-report.md) is a dated verification snapshot. Live command output and commit-specific GitHub Actions runs supersede it. Fixture tests do not establish live merchant, custody, or stablecoin payout provider certification.
