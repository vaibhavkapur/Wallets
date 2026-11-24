# Merchant-Funded Stablecoin Rewards

A merchant-funded cashback and stablecoin payout workflow where merchants prefund versioned reward campaigns, qualifying orders atomically reserve campaign capacity and platform/loss reserves in a double-entry ledger, and customers claim eligible balances to verified Base `USDC` destinations with explicit fee, FX, and recovery disclosures. Payment settlement, order fulfillment, return-window expiry, and payout attempts are tracked independently, and uncertain payout outcomes are reconciled rather than blindly retried.

> **[Read the full documentation](docs/index.md)**

Built with Go and PostgreSQL (financial ledger, campaign reservations, webhooks, workers), React and TypeScript (customer, merchant, and operator interfaces), and an optional read-only Rust batch settlement reconciler (`reconciler/`) parity-tested against the Go reference path. Pilot merchants, prefunding deposits, and payout settlements use local sandbox fixtures; no real funds move.

## Getting Started

```bash
# Requires Go 1.27.1, Node.js 20.20.2, npm, Rust/Cargo (for optional batch reconciler), and Make
git clone https://github.com/vaibhavkapur/Wallets.git
cd Wallets/projects/Merchant-Funded-Stablecoin-Rewards
make setup
make test
make dev
```

- UI: http://127.0.0.1:5189
- API: http://127.0.0.1:8089
- Local operator token: `local-operator`

The durable file store at `.local/rewards-state.json` is the default for single-process development. Set `DATABASE_URL` to use PostgreSQL, or run `docker compose up --build` for PostgreSQL and the built application.

See [Getting Started](docs/getting-started.md) for prerequisites, configuration, verification, and the first payout.

## Quick Example

1. Open the **Customer Rewards** tab for `cust_alex`. Two seeded pilot orders appear: `ORD-9001` (**$10.00 eligible** from Northstar Trail Supply) and `ORD-9002` (**$15.00 pending** awaiting fulfillment from Soundhouse Audio).
2. Confirm the enrolled verified Base `USDC` destination (`ben_cust_alex_v1`), click **Get Payout Quote** to inspect the USD-to-USDC conversion, `$0.25 USDC` fee, expiry, and disclosure, then check approval and click **Authorize Payout**.
3. Open **Merchant Campaigns** to inspect prefunded budget utilization, create a versioned campaign, or send HMAC-SHA256 signed `order.paid`, `order.fulfilled`, `order.refunded`, and `order.chargeback` events.
4. Open **Operator Queue** (token `local-operator`) to verify the double-entry trial balance and funding coverage, advance the sandbox clock past return windows, simulate provider custody drift, or toggle the global pause control.

To explore recovery, choose **Provider timeout after broadcast (`outcome_unknown`)** under **Sandbox Settlement Scenario** before authorizing a payout. Funds stay locked in `liability:payout_clearing` and blind retries are rejected (`409 Conflict`) until **Run Worker & Reconciliation Tick** queries the provider and settles the existing payout. See the [Demo Script](docs/demo-script.md) for partial returns, post-payout chargebacks, beneficiary rotation, and custody mismatches.

Live merchant agreements, custody, KYC/screening, tax reporting, and R1–R4 pilot milestones remain open. Sandbox fixtures do not establish production payout permission. See [Deployment](docs/deployment.md) and [Threat Model](docs/threat-model.md) for the live-use boundary.
