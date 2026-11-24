# Card-versus-Stablecoin Payment Optimizer

Deterministic comparison of a customer's eligible card and stablecoin payment options for the same basket, by estimated net cost after fees, discounts and conservatively valued rewards, with every assumption and exclusion explained.

[Get Started](getting-started.md) · [API Reference](api-reference.md) · [Repository README](../README.md)

## Key Features

- A pure Rust calculation engine: exact `i128` micro-USD arithmetic with directed rounding, eligibility and exclusion rules, reward caps and point valuations, a customer-configurable savings threshold, ties instead of arbitrary picks, and machine-readable explanations. Every result records contract, engine, formula, rounding, policy and preference versions and the SHA-256 of its input.
- Unknown required fees, unverified eligibility, stale or mismatched quotes and unreadable balances exclude an option and yield `insufficient_data`; nothing is ever treated as zero cost.
- A Go orchestrator that gathers quotes and balances (balances are never cached), applies versioned customer preferences and consent, persists append-only snapshots in PostgreSQL, and measures outcomes with realized savings kept separate from estimated rewards against an explicitly unobserved baseline.
- Execution handoffs that bind a customer approval to one evaluation, one eligible option and the exact amount due; one unresolved attempt per customer and basket; expired attempts swept to `unknown` until reconciled; a product-level pause.
- A typed TypeScript SDK generated from both contracts, exact string-based formatting with no floating point, and a React comparison interface that shows immediate costs separately from estimates and labels every trade-off.
- Shared golden vectors checked three ways: hand-worked expectations, an independent Python model in exact rationals, and the engine itself; a versioned recorded dataset for offline evaluation (milestone O1).

## Tech Stack and Scope

Rust 1.99 with serde, schemars and axum (engine); Go 1.27 with pgx and PostgreSQL 16 (embedded for development and tests); TypeScript with Vite/React, Vitest, openapi-typescript and json-schema-to-typescript; Python 3 for fixture generation and the independent check. All quotes are synthetic, replayed by a recorded provider; no merchant, card network, wallet or stablecoin network is contacted and no funds move.

## Documentation

- [Getting Started](getting-started.md)
- [Architecture](architecture.md)
- [Calculation Rules](calculation.md)
- [Evaluation Contract](evaluation-contract.md)
- [API Reference](api-reference.md)
- [Configuration](configuration.md)
- [Database](database.md)
- [Testing](testing.md)
- [Deployment](deployment.md)
- [Threat Model](threat-model.md)
- [Demo Script](demo-script.md)
- [Recorded Test Report](test-report.md)

## Project Structure

- `engine/crates/optimizer-core/`: the calculation crate (contract types, validation, engine, exact amounts) with golden and property tests.
- `engine/crates/optimizer-service/`: the authenticated internal HTTP service and the `schema` exporter.
- `orchestrator/`: Go API (`cmd/optimizerd`), operator tool (`cmd/optimizerctl`), offline evaluation (`cmd/offline-eval`), local stack (`cmd/devstack`), and `internal/` packages for the contract, engine client, quotes, store, service, API, outbox and offline runner, plus `migrations/`.
- `web/packages/sdk/`, `web/apps/compare/`: TypeScript workspace packages.
- `contracts/`: `engine-v1.schema.json` (generated from the Rust types) and `openapi.yaml`.
- `fixtures/`: golden vectors, the recorded dataset and the Python tools that build and check them.
- `config/policy.v1.json`: the versioned platform policy.
- `scripts/`: local stack (`dev-up.sh`, `dev-down.sh`), end-to-end scenario (`e2e.sh`) and test-report generation.

The implementation guides describe the current code. [Development plan](../plan.md) records design intent, milestones and open decisions; planned items such as real merchant quotes, checkout adapters and a conversion partner are not implemented.

## Related projects

These are independent companion repositories, not runtime dependencies or claims of an implemented integration:

- [Agent Spending Accounts](https://github.com/vaibhavkapur/Agent-Spending-Accounts): budgeted spending accounts for software agents with atomic reservations and single-use execution grants.
- [Autonomous Price-Watch Buyer](https://github.com/vaibhavkapur/Autonomous-Price-Watch-Buyer): single-purchase agent with recovery from concurrent and uncertain outcomes.
- [Agentic Commerce Stablecoin Checkout](https://github.com/vaibhavkapur/Agentic-Commerce-Stablecoin-Checkout): policy-controlled simulated USDC payments with merchant adapters and receipts.
- [Stablecoin Payment Orchestrator](https://github.com/vaibhavkapur/Stablecoin-Payment-Orchestrator): routing of USD-denominated merchant payments as USDC transfers by cost, latency and reliability.
- [Stablecoin Payments API](https://github.com/vaibhavkapur/Stablecoin-Payments-API): wallets, transfers, checkout and double-entry ledger reconciliation.
