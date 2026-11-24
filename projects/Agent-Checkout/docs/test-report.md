# Test Report

[Documentation home](index.md)

Recorded: **3 October 2026**. A local verification snapshot, not a claim about later commits or external provider certification.

## Verified During Implementation

- Go unit, HTTP, recovery, and partner contract tests passed with the race detector.
- `go vet` passed.
- TypeScript checking and the Vite production build passed.
- The Go server binary compiled.
- OpenAPI and generated TypeScript contract-drift checks passed.
- All seven automated Playwright Chromium tests passed before publication: purchase/refund/cancellation, issuer challenge, timeout recovery, changed quote, decline, operator pause/resume, and mobile approval.
- Interactive browser checks verified purchase approval, issuer verification, timeout recovery, partial refund, cancellation, operator access, pause/resume, and a 390-pixel mobile approval flow without horizontal overflow.

## Not Yet Established

- PostgreSQL integration skipped locally because no running database was available. The optional test and CI service are configured.
- Docker/Compose startup and real merchant/Stripe account interoperability were not verified.
- No live charge, external refund, AP2 conformance, pilot cohort, or production identity flow was tested.

Use [Testing](testing.md) to reproduce checks. Current output and commit-specific GitHub Actions results supersede this snapshot.
