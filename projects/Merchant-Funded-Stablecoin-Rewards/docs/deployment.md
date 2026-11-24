# Deployment

[Documentation home](index.md)

## Local Containers

```bash
docker compose up --build
```

Compose starts PostgreSQL 17.6 (`127.0.0.1:54329`) and a multi-stage-built non-root container (`127.0.0.1:8089`) serving both the Go API and the compiled React frontend from a single origin. A named volume (`rewards-db`) preserves state across restarts.

To run PostgreSQL with Vite hot reload:

```bash
docker compose up -d db
DATABASE_URL='postgres://rewards:rewards_local@127.0.0.1:54329/rewards?sslmode=disable' make dev
```

## Standalone Build

```bash
make build
APP_ORIGIN=http://127.0.0.1:8089 \
STATE_FILE="$PWD/.local/rewards-state.json" \
WEB_DIST="$PWD/web/dist" \
OPERATOR_TOKEN=local-operator \
  .local/rewards-server
```

Ensure no other process holds the lock on `.local/rewards-state.json` before starting.

## Standalone PostgreSQL Worker

For multi-process deployments, point both `rewards-server` and `rewards-worker` at the same `DATABASE_URL`:

```bash
DATABASE_URL='postgres://rewards:rewards_local@127.0.0.1:54329/rewards?sslmode=disable' .local/rewards-worker
```

The standalone worker polls all tenants every 5 seconds to process return-window eligibility, reconcile pending/unknown payouts, and check custody balances.

## Optional Rust Batch Reconciler

```bash
make reconciler
# or using the release binary:
./reconciler/target/release/rewards-reconciler fixtures/statement_batch_input.json
```

The reconciler runs out-of-band as a read-only CLI utility producing deterministic JSON discrepancy reports; it has no database write credentials or payout signing access.

## Live-Use Boundary

This repository is a standalone sandbox MVP, not a production money-transmission or custody deployment. Before a live funded pilot (milestones R1–R4 in [plan.md](../plan.md)):

- Confirm legal entity, fund custody ownership, customer reward obligor, geography, screening/KYC, and applicable tax reporting with counsel and the chosen payout provider.
- Replace `SandboxProvider` with a commercials-approved payout/custody integration supporting the explicit reward-payout use case and recipient jurisdictions.
- Replace fixture header auth (`X-Customer-ID`, `X-Merchant-ID`, static `OPERATOR_TOKEN`) with authenticated customer/merchant sessions, RBAC, MFA for beneficiary edits, and KMS-managed webhook secrets.
- Disable `SANDBOX_UI_SIMULATOR` and `SEED_PILOT_DATA`, enable TLS, structured audit log retention, versioned SQL migrations, and production monitoring/alerting on open `ReconciliationException` rows.
