# Test Report

[Documentation home](index.md)

Recorded: **24 November 2026**. A local verification snapshot, not a claim about later commits or external provider certification.

## Verified During Implementation

- `go test -race -v ./...` passed all 16 test suites (including 4 policy subtests) in `backend/internal/recurring/`:
  - Decimal-string `Money` serialization, large `> 2^53` integer preservation, checked arithmetic, and half-up basis-point rounding.
  - DST spring-forward gaps, fall-back repeated hours (`shift_forward_first` and `shift_forward_second`), leap dates (2027 vs. 2028), month-end clamping, and strict calendar-vs-interval validation.
  - C1 reminder-only enforcement, timezone edits (`v1 -> v2`) with stale approval rejection, and proposal expiry without purchasing.
  - Price spikes, pack-size shrinkflation, partial stock, and preauthorized exact substitutions.
  - Payment timeout reconciliation (`timeout_settled` with zero duplicate orders and `timeout_failed` budget release), delayed-fulfillment duplicate blocking, monthly budget rollover, and 35-day outage coalescing into a single recovery proposal.
  - Concurrent pause racing with in-flight provider submission, 8-goroutine worker/approval race checks under `-race`, expired lease reclamation, C3 mandate gating and revocation, skip-next, tenant isolation, operator RBAC, and HMAC-verified webhooks with partial refunds.
- `go vet ./...` passed with zero diagnostics.
- `go build ./cmd/server` and `go build ./cmd/worker` succeeded cleanly.
- `npm run typecheck` (`tsc --noEmit`), `npm test` (4/4 Vitest unit tests in `web/src/money.test.ts`), and `npm run build` (`vite build`) passed cleanly.
- OpenAPI contract generation (`make contracts`) and contract-drift verification (`make check-contracts`) passed.

## Not Yet Established

- Live PostgreSQL integration was not executed locally because no PostgreSQL daemon was running in the local environment; `PgStore` compiles cleanly and PostgreSQL CI is configured in `.github/workflows/recurring-purchase-agent.yml`.
- Docker/Compose container runtime and live browser E2E runs were not executed in the local sandbox environment.
- No live merchant ACP/UCP feeds, real card/stablecoin charges, or external AP2 mandate verifications were tested.

Use [Testing](testing.md) to reproduce checks locally. Current output and commit-specific GitHub Actions results supersede this snapshot.
