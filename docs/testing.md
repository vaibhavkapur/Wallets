# Testing

[Documentation home](index.md)

## Automated Checks

```bash
make setup
make test
make build
make check-contracts
```

`make test` runs Go tests with the race detector, `go vet`, and TypeScript checking. Partner contract tests start local TLS servers. `make build` checks and bundles the frontend and compiles `.local/uno-server`. `make check-contracts` regenerates OpenAPI and TypeScript models in a temporary location and checks for drift.

If API types change intentionally, run `make contracts` and commit both generated files.

## Covered Invariants

Go tests cover concurrent duplicate execution, budget contention, altered carts, changed cards, expired/revoked approval, queued approval expiry, unsupported cards, changed price/stock, pause, tenant isolation, and money serialization/rounding.

Recovery tests cover issuer challenges, declines, timeout after a successful charge, restart recovery, signed/delayed/duplicate webhooks, merchant failure after payment, partial/full refunds, cancellation, fulfillment progression, and the delivery-based return window. Store tests check rollback and writer exclusion. Adapter tests verify authentication, version headers, token scope, and ambiguous completion.

## PostgreSQL Integration

```bash
docker compose up -d db
TEST_DATABASE_URL='postgres://uno:uno_local@127.0.0.1:54328/uno?sslmode=disable' make test
```

Without `TEST_DATABASE_URL`, the PostgreSQL test explicitly skips. It creates a unique tenant, completes a purchase, opens another connection to verify persistence, and removes its own tenant record. CI provisions PostgreSQL to enable this check.

## Browser Suite

```bash
cd web
npx playwright install chromium
npm run test:e2e
```

The suite builds the frontend and starts an isolated API on `127.0.0.1:8099` with temporary data and a fixture operator token. Keep that port free. Tests run serially and cover purchase/refund/cancellation, issuer authentication, timeout recovery, changed prices, declines, operator pause/resume, and mobile overflow/approval.

Failure screenshots and traces stay in ignored output directories. Use a clean exported environment: do not pass a real `DATABASE_URL` or `WORKER_MODE=external` into the fixture runner.

## Evidence Scope

The [Recorded Test Report](test-report.md) is a dated snapshot. Current command output and commit-specific GitHub Actions results are authoritative. Fixture tests do not establish real merchant interoperability, live settlement, production identity, or protocol conformance beyond the tested contracts.
