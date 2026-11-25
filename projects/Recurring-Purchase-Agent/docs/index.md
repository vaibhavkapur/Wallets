# Recurring Purchase Agent

A scheduled reorder workflow for predictable essentials that binds every purchase to an immutable rule version and fresh merchant quote, tracks payment and fulfillment independently, and prevents duplicate orders across worker restarts, timeouts, outages, and delayed deliveries.

[Get Started](getting-started.md) · [API Reference](api-reference.md) · [Repository README](../README.md)

## Key Features

- Exact SKU and pack-size matching across a narrow coffee and household essentials catalog, with explicit substitution policies (`none`, `approval_required`, `preauthorized_exact`).
- Strictly separated calendar (`weekly` / `monthly`) and minimum-interval schedules with IANA timezones, daylight-saving gap/repeated-hour resolution, leap-day clamping or carry rules, and multi-slot outage coalescing.
- Milestone-gated approval modes: `C1_REMINDER_ONLY` (quotes and reminders without automatic payment), `C2_ASSISTED_REORDER` (customer approval per occurrence), and `C3_LIMITED_DELEGATION` (gated behind pilot flag, eligible payment instrument, and an active unrevoked `ConsentRecord` mandate).
- Atomic monthly budget reservations with period rollover, durable lease-based worker recovery, duplicate-order blocking across pending/delayed orders, `timeout_unknown` reconciliation, pause-vs-cancel distinction, HMAC-verified merchant webhooks, tenant isolation, and operator-only product-level pause.

## Tech Stack and Scope

Go 1.27 / React 19 / TypeScript / Vite; PostgreSQL (`pgx/v5`) or a single-process durable JSON file store; Go race tests (`go test -race`) and Vitest unit tests. Products, merchants, payment methods, checkout outcomes, and delivery events are sandbox fixtures.

The application uses customer-defined schedules and explicit catalog selections. It does not infer household inventory, scrape arbitrary merchant URLs, store raw card PAN/CVC or wallet private keys, or move real money. Optional sibling integrations (`Agent-Checkout`, `Card-Stablecoin-Payment-Optimizer`) and live ACP/UCP/AP2 production credentials remain outside the default sandbox runtime.

## Documentation

- [Getting Started](getting-started.md)
- [Architecture](architecture.md)
- [API Reference](api-reference.md)
- [Configuration](configuration.md)
- [Database and Recovery](database.md)
- [Testing](testing.md)
- [Deployment](deployment.md)
- [Protocol Versions](protocol-versions.md)
- [Demo Script](demo-script.md)
- [Recorded Test Report](test-report.md)

## Project Structure

- `backend/cmd/server/`: HTTP API and optional embedded worker.
- `backend/cmd/worker/`: standalone PostgreSQL / durable recovery worker.
- `backend/cmd/schema/`: OpenAPI 3.1 contract generator (`contracts/openapi.json`).
- `backend/internal/recurring/`: domain models, decimal-string money arithmetic, timezone/DST/leap scheduler, stores, policy engine, sandbox & optional HTTP adapters, durable worker, HTTP handlers, and Go race/state/HTTP tests.
- `backend/migrations/`: PostgreSQL schema (`001_schema.sql`) with uniqueness constraints.
- `contracts/`: generated versioned application OpenAPI contract.
- `web/`: React 19 + TypeScript customer and operations interface with BigInt-safe money utilities and Vitest tests.
- `scripts/`: local development runner and contract-drift check.

These guides describe the current implementation. The original [development plan](../plan.md) records design intent and future milestones; planned features are not automatically implemented.

## Related Projects

These are independent companion projects in the workspace portfolio, not mandatory runtime dependencies:

- [Agent Checkout](../Agent-Checkout/): customer-approved retail checkout, card-reward comparison, and payment recovery.
- [Agent Spending Accounts](../Agent-Spending-Accounts/): budgeted spending accounts and x402 gateway for software agents.
- [Card-versus-Stablecoin Payment Optimizer](../Card-Stablecoin-Payment-Optimizer/): net-cost comparison between eligible cards and stablecoins.
- [Merchant-Funded Stablecoin Rewards](../Merchant-Funded-Stablecoin-Rewards/): prefunded merchant cashback campaigns and idempotent Base USDC payouts.
- [MyWallet](../MyWallet/): SwiftUI iPhone wallet with Go enrollment verification.
- [Cross-Merchant Procurement Agent](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Cross-Merchant-Procurement-Agent): supplier offers, company constraints, and protocol-specific checkout.
- [Autonomous Price-Watch Buyer](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Autonomous-Price-Watch-Buyer): bounded purchase authority and recovery from concurrent triggers.
