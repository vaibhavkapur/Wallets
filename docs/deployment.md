# Deployment

[Documentation home](index.md)

## Local stack

`make up` (`scripts/dev-up.sh assisted`) builds the engine and the Go binaries and runs `devstack`, which starts an embedded PostgreSQL with data in `.dev/postgres/`, the engine as a child process, and the API; `make down` stops it. `make up-shadow` runs the same stack in shadow mode. There is no Docker Compose file in this repository.

## Separate processes

For a production-style layout run three things against one PostgreSQL:

1. `optimizer-engine` (`engine/`, `cargo build --release -p optimizer-service`) with `ENGINE_AUTH_TOKEN` and `ENGINE_LISTEN_ADDR` on a private network. It is stateless and holds no provider credentials; run as many as needed.
2. `optimizerd` (`orchestrator/cmd/optimizerd`) with `DATABASE_URL`, `ENGINE_URL`, `ENGINE_AUTH_TOKEN`, `OPTIMIZER_MODE`, `OPTIMIZER_POLICY_FILE` and, to deliver handoffs, `EXECUTOR_WEBHOOK_URL` + `EXECUTOR_WEBHOOK_SECRET`. The outbox relay and the expired-handoff sweep run in-process; several instances may run concurrently because outbox claims use `FOR UPDATE SKIP LOCKED` and the sweep is idempotent.
3. `optimizerctl` for tenants, keys and the pause control, run by operators with database access only.

Serve the comparison interface as static files (`npm run build -w compare`) behind a reverse proxy that injects the API key for `/api`, as the Vite dev proxy does; the key must never be shipped to the browser.

## Modes and rollout

Start in `shadow` mode: evaluations are recorded, exclusion and warning counts appear in `GET /v1/reports/summary`, and customer-reported selections can be posted with `source: customer_report`. Review stale-data and eligibility exclusions there before switching to `assisted`. Evaluations produced in shadow mode are never executable, so the switch does not retroactively enable any handoff.

## Pause and recovery

`optimizerctl pause -reason …` (globally or per tenant) stops new evaluations and new handoffs and makes every open handoff read as not executable; outcome ingestion keeps running so in-flight payments are still reconciled. Handoffs that expire without an outcome are swept to `unknown` and block further attempts for that customer and basket until the executor reports; an operator can resolve them by posting an `executor` outcome with the observed status. Back up PostgreSQL; the engine and the policy file are reproducible from the repository, and every evaluation stores the request that produced it.

## Scope

This repository ships a recorded quote provider and no execution service. Before live use, implement `quotes.Provider` and `quotes.Balances` against the chosen merchant checkout path (ACP or UCP where needed) and the stablecoin acceptance and conversion provider, pass `quotestest.RunProviderContract`, replace the policy file with the asset, network and funding sources actually supported, and connect the outbox to the execution service that holds card credentials and signing access. Confirm geography, contractual responsibilities, custody boundaries, screening and disclosure obligations, customer recovery and support ownership for the real money flow, as `plan.md` requires.
