# Agent Spending Accounts

Narrowly scoped spending authority for software agents, with atomic multi-budget reservations, single-use execution grants, and per-task cost visibility for the account owner.

[Get Started](getting-started.md) · [API Reference](api-reference.md) · [Repository README](../README.md)

## Key Features

- Organization and agent identities with owner-managed funding, immutable budget policy versions, and an allowlist of endpoints and payment recipients that a model cannot extend.
- Per-request, per-task, per-agent-day, and per-organization-day limits reserved atomically in PostgreSQL, including fee allowances and pending payments.
- A Rust x402 gateway that validates each payment challenge, acts only on a short-lived single-use grant bound to the exact payload, coordinates an isolated signer, and refuses redirects, private destinations, and oversized responses.
- Receipts that link task, resource, quote, policy version, payment, and service outcome; unknown payment results are held until reconciled.
- Owner dashboard, typed TypeScript SDK, and an optional MCP adapter that exposes spending tools without any budget-mutation capability.

## Tech Stack and Scope

Go 1.22+ with pgx and PostgreSQL 16 (embedded for development and tests); Rust stable with axum, reqwest, and jsonwebtoken; TypeScript with Vite/React, Vitest, and the MCP SDK. The signing provider, x402 resource server, and facilitator settlement lookup are local fixtures. No funds move on any network.

## Documentation

- [Getting Started](getting-started.md)
- [Architecture](architecture.md)
- [API Reference](api-reference.md)
- [Configuration](configuration.md)
- [Database and Ledger](database.md)
- [Testing](testing.md)
- [Deployment](deployment.md)
- [Grant Contract](grant-contract.md)
- [Threat Model](threat-model.md)
- [Demo Script](demo-script.md)
- [Recorded Test Report](test-report.md)

## Project Structure

- `control-plane/`: Go API, store, policy engine, grants, workers, migrations, and the `mockresource` and `mocksigner` fixtures.
- `gateway/`: Rust paid-request gateway (URL guard, x402, grant verification, bounded client, flow).
- `sdk/`, `mcp/`, `dashboard/`, `examples/research-agent/`: TypeScript workspace packages.
- `scripts/`: local stack (`dev-up.sh`, `dev-down.sh`), end-to-end scenario (`e2e.sh`), and test-report generation.

The implementation guides describe the current code. [Development plan](../plan.md) records design intent, milestones, and open decisions; planned items such as a live wallet provider are not automatically implemented.

## Related projects

These are independent companion repositories, not runtime dependencies or claims of an implemented integration:

- [Agent Services Marketplace](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Agent-Services-Marketplace): research agent that discovers paid services over MCP and settles with x402 or MPP.
- [Agent Authorization Wallet + Merchant Trust Gateway](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Agent-Authorization-Wallet-Merchant-Trust-Gateway): delegated purchase authority and transaction-integrity verification.
- [Autonomous Price-Watch Buyer](https://github.com/vaibhavkapur/Agentic-Commerce/tree/main/projects/Autonomous-Price-Watch-Buyer): single-purchase agent with recovery from concurrent and uncertain outcomes.
- [Smart Wallet Policy Engine](https://github.com/vaibhavkapur/Stablecoins/tree/main/projects/Smart-Wallet-Policy-Engine): transaction risk evaluation and wallet authorization.
- [Stablecoin Payments API](https://github.com/vaibhavkapur/Stablecoins/tree/main/projects/Stablecoin-Payments-API): wallets, transfers, checkout, and double-entry ledger reconciliation.
