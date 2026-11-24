# Merchant-Funded Stablecoin Rewards

A merchant-funded cashback and stablecoin payout platform that keeps merchant prefunding, pending and payable reward liabilities, loss reserves, fees, and payout clearing strictly balanced in a double-entry ledger.

[Get Started](getting-started.md) · [API Reference](api-reference.md) · [Repository README](../README.md)

## Key Features

- Versioned merchant cashback campaigns (`fixed` or `percentage_bps`) with prefunding-before-promising, atomic budget and per-customer caps, eligible SKU filters, return windows, and explicit platform fee and loss-reserve bps.
- HMAC-SHA256 authenticated order, fulfillment, partial/full refund, and post-payout chargeback ingestion with strict idempotency and separate payment-settlement vs. fulfillment tracking.
- Balanced double-entry ledger verified per asset (`USD/fiat` and `USDC/base`) with exact integer minor/base units serialized as decimal strings in JSON and formatted with `BigInt` in TypeScript.
- Immutable `BeneficiaryVersion` enrollment and Base `USDC` address verification; quotes bind to a specific beneficiary version and expire cleanly.
- Two-phase payout authorization that never holds a database transaction open across a provider call, reconciles `outcome_unknown` timeouts before permitting retries, and routes post-payout chargebacks through loss reserves and operator exceptions rather than assuming irreversible transfers can be clawed back.
- Optional zero-dependency Rust read-only batch settlement reconciler verified for deterministic parity with the Go reference path on shared fixtures.

## Tech Stack and Scope

Go 1.27 / React 19 / TypeScript / Vite; PostgreSQL 17 or a single-process durable JSON file store; optional Rust batch reconciler (`reconciler/`); Go race tests, Rust `cargo test` + `clippy`, and TypeScript `BigInt` precision tests. Pilot merchants, prefunding deposits, and payout settlements use deterministic local sandbox fixtures.

This implementation does not issue a proprietary token, provide yield or lending, integrate live merchant stores or live payout/custody providers, or implement production identity/KYC screening.

## Documentation

- [Getting Started](getting-started.md)
- [Architecture and Ledger](architecture.md)
- [API Reference](api-reference.md)
- [Configuration](configuration.md)
- [Database and Recovery](database.md)
- [Testing](testing.md)
- [Deployment](deployment.md)
- [Demo Script](demo-script.md)
- [Threat Model](threat-model.md)
- [Recorded Test Report](test-report.md)

## Project Structure

- `backend/cmd/server/`: HTTP API, embedded reconciliation worker, and optional static frontend serving.
- `backend/cmd/worker/`: standalone PostgreSQL reconciliation and eligibility worker.
- `backend/cmd/schema/`: OpenAPI 3.1 schema generator.
- `backend/internal/money/`: checked integer arithmetic, bps rounding, JSON decimal-string serialization, and Base `USDC` address validation.
- `backend/internal/rewards/`: domain records, double-entry ledger, FileStore and PostgresStore, sandbox payout/custody provider, service logic, workers, HTTP handlers, statement batch reconciler, and tests.
- `backend/migrations/`: PostgreSQL schema (`001_init.sql`) with embedded SQL.
- `reconciler/`: bounded read-only Rust batch settlement reconciler (`cargo test` / `cargo clippy`).
- `fixtures/`: shared statement batch input and expected discrepancy report verified by both Go and Rust.
- `contracts/`: generated OpenAPI 3.1 contract (`openapi.json`).
- `web/`: React + TypeScript application (Customer Rewards, Merchant Campaigns, Operator Queue) and `BigInt` formatting tests.
- `scripts/`: local development runner and contract-drift verification script.

These guides describe the current implementation. The original [project plan](../plan.md) records design intent, commercial hypotheses, and pilot milestones; planned commercial integrations are not automatically implemented.

## Related Projects

These are companion projects in the [Wallets](https://github.com/vaibhavkapur/Wallets) collection, not runtime dependencies:

- [Agent Checkout](../../Agent-Checkout/): customer-approved retail purchases, card comparison, and timeout recovery.
- [Agent Spending Accounts](../../Agent-Spending-Accounts/): budgeted spending accounts and x402 payment gateway for software agents.
- [Card-versus-Stablecoin Payment Optimizer](../../Card-Stablecoin-Payment-Optimizer/): deterministic comparison of card versus stablecoin basket costs.
- [MyWallet](../../MyWallet/): SwiftUI iOS card-saving wallet with Go/PostgreSQL enrollment verification.
