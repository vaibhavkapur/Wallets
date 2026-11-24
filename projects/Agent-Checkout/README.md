# Agent Checkout

A customer-approved checkout workflow that compares delivered merchant prices and eligible card rewards, binds approval to an exact purchase, and recovers from uncertain payment outcomes without submitting another charge.

> **[Read the full documentation](docs/index.md)**

Built with Go, React, TypeScript, and PostgreSQL, with a durable file store for local development. Products, merchants, cards, payments, and delivery updates are sandbox fixtures. No real money moves.

## Getting Started

```bash
# Requires Go 1.27.1, Node.js 20.20.2, npm, and Make
git clone https://github.com/vaibhavkapur/Wallets.git
cd Wallets/projects/Agent-Checkout
make setup
make test
make dev
```

- UI: http://127.0.0.1:5188
- API: http://127.0.0.1:8088
- Local operator token: `local-operator`

The file store at `.local/state.json` is the default. Set `DATABASE_URL` to use PostgreSQL, or run `docker compose up --build` for PostgreSQL and the built application.

See [Getting Started](docs/getting-started.md) for prerequisites, configuration, verification, and the first purchase.

## Quick Example

1. Choose **Studio Pro**, **Midnight**, quantity **1**, a **$200** delivered-price limit, and ZIP **94103**.
2. Select **Find my checkout** and compare three sandbox merchants. Soundhouse quotes **$182.94** delivered; Everyday Cash estimates **$5.48** cashback.
3. Choose **Review & approve**, verify the merchant, item, card, delivery, and total, then approve the one-time sandbox purchase.
4. Open the receipt in **My orders** to request a partial refund or cancel and refund the remaining amount.

To explore recovery, select **Timeout after payment** under **Sandbox scenario** before approving. The budget stays reserved while the worker looks up the existing payment; it does not submit a new charge. See the [Demo Script](docs/demo-script.md) for challenges, declines, changed prices, and merchant failures.

Live merchant/provider onboarding and pilot milestones remain open. The independent ACP and Stripe test-token clients are contract-tested but are not connected to the demo runtime. See [Protocol Versions](docs/protocol-versions.md) and [Deployment](docs/deployment.md) for the integration boundary.
