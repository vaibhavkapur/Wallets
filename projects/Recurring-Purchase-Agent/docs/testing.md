# Testing

[Documentation home](index.md)

## Running the Test Suite

From `projects/Recurring-Purchase-Agent`:

```bash
make test
make build
make check-contracts
```

`make test` runs:

1. `go test -race -v ./...` — state-machine, fake-clock, concurrency/race, and HTTP API tests.
2. `go vet ./...` — Go static analysis.
3. `npm run typecheck` — strict TypeScript checking (`tsc --noEmit`).
4. `npm test` — Vitest unit tests for BigInt-safe money parsing/formatting and schedule descriptions.

## Covered Validation Cases

All validation scenarios specified in [plan.md](../plan.md) are automated in `backend/internal/recurring/`:

- **Scheduler & Money (`schedule_money_test.go`)**:
  - Decimal-string JSON serialization for `MinorUnits`, rejection of bare numeric JSON values, large `> 2^53` integer preservation, overflow detection, and half-up basis-point rounding.
  - Spring-forward DST gaps (`America/New_York` 2026-03-08 02:30 → 03:00 EDT / 07:00 UTC).
  - Fall-back DST repeated hours (`America/New_York` 2026-11-01 01:30 under `shift_forward_first` vs. `shift_forward_second`).
  - Leap dates (Feb 29 in leap year 2028 vs. non-leap year 2027 under `clamp_month_end` vs. `carry_march_first`) and shorter-month clamping (31st → Apr 30).
  - Strict separation rejecting mixed calendar/interval fields.
- **Workflow, Policy, Concurrency & Recovery (`service_test.go`)**:
  - C1 reminder-only mode (reminders sent without payment; approval rejected).
  - Timezone edits (`v1 -> v2`) and rejection of stale approvals bound to superseded versions.
  - Proposal expiry without purchasing.
  - Price increase exceeding `max_delivered_price`, pack-size shrink requiring explicit substitution consent, partial stock blocking, and preauthorized exact substitute matching.
  - Payment timeout reconciliation for both settled (`UniqueOrderCount == 1`, zero duplicate charges) and failed (`BudgetReservation` released) outcomes.
  - Delayed fulfillment blocking subsequent duplicate reorders until delivered.
  - Monthly budget cap enforcement and automatic monthly-period rollover.
  - Multi-slot outage (35 days on weekly rule) coalescing into one recovery proposal with zero backlog orders.
  - Pause racing with in-flight provider submission (`BeforeProviderSubmitHook`), flagging `pause_raced_submission`, blocking future runs, and canceling the already-submitted order explicitly.
  - 8-goroutine concurrent worker scans and approvals under `-race`, plus expired-lease reclamation after simulated worker crash.
  - C3 limited delegation pilot gating, mandate binding, and immediate revocation blocking.
  - Skip-next occurrence advancement and operator global pause blocking.
- **HTTP, RBAC, Tenant Isolation & Webhooks (`http_test.go`)**:
  - Unauthenticated rejection (`401`), cross-tenant isolation (`404`), operator-only pause RBAC (`403` for customer, `200` for operator), and HMAC-SHA256 webhook verification with partial refund accounting.
- **Frontend Unit Tests (`web/src/money.test.ts`)**:
  - BigInt formatting of large minor units (`> Number.MAX_SAFE_INTEGER`), exact USD string parsing, BigInt budget percentages, and calendar-vs-interval schedule labels.
