# Agent Checkout

A purchase workflow that compares delivered prices and card rewards, requires approval of an exact checkout, and keeps payment, fulfillment, refund, and recovery state independently accountable.

[Get Started](getting-started.md) · [API Reference](api-reference.md) · [Repository README](../README.md)

## Key Features

- Structured product selection, merchant quotes, card eligibility, versioned reward estimates, and exact-purchase approval.
- Atomic budget reservations, durable payment attempts and outbox jobs, idempotent execution, and recovery after a lost payment response.
- Issuer-challenge simulation, separate payment and fulfillment status, receipts, partial/full refunds, cancellation, audit evidence, and operator controls.

## Tech Stack and Scope

Go / React 19 / TypeScript / Vite; PostgreSQL or a single-process durable JSON file store; Go race tests and Playwright browser tests. Products, prices, identities, merchants, payment-provider outcomes, and shipping events are fixtures. The UI is branded Uno.

The app accepts explicit catalog selections. It does not currently parse natural-language requests, shop arbitrary URLs, collect real card details, or execute live purchases. Optional MCP, UCP, AP2, and Rust components remain outside the implementation.

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

- `backend/cmd/server/`: HTTP API, static frontend serving, and embedded worker.
- `backend/cmd/worker/`: separate PostgreSQL outbox worker.
- `backend/cmd/schema/`: OpenAPI and JSON Schema generation from public Go types.
- `backend/internal/checkout/`: purchase policy, stores, sandbox adapters, worker, HTTP handlers, and tests.
- `backend/internal/partners/`: isolated ACP and Stripe test-token HTTP clients.
- `backend/migrations/`: PostgreSQL tenant-aggregate schema.
- `contracts/`: generated versioned application API contract.
- `web/`: React application, generated TypeScript models, and browser tests.
- `scripts/`: development, isolated browser-test server, and contract-drift checks.

These guides describe the current implementation. The original [development plan](../plan.md) records design intent and future milestones; planned features are not automatically implemented.

## Related Projects

These are independent companion repositories, not runtime dependencies or claims of implemented integrations:

- [Cross-Merchant Procurement Agent](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Cross-Merchant-Procurement-Agent): supplier offers, company constraints, and protocol-specific checkout.
- [Autonomous Price-Watch Buyer](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Autonomous-Price-Watch-Buyer): bounded purchase authority and recovery from concurrent triggers.
- [Agent Authorization Wallet + Merchant Trust Gateway](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Agent-Authorization-Wallet-Merchant-Trust-Gateway): delegated authorization and merchant verification.
- [Agentic Commerce Protocol Test Lab](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Agentic-Commerce-Protocol-Test-Lab): local protocol fixtures and failure testing.
- [Agentic Commerce + Stablecoin Checkout](https://github.com/vaibhavkapur/Stablecoins/tree/main/projects/Agentic-Commerce-Stablecoin-Checkout): conversational commerce and simulated stablecoin payment routing.
