# Recurring Purchase Agent

A scheduled essentials reorder workflow that enforces exact product and pack-size matching, timezone- and daylight-saving-aware schedules, delivered-price and monthly budget limits, immutable customer approval binding, and recovery from uncertain payment outcomes without creating duplicate orders.

> **[Read the full documentation](docs/index.md)**

Built with Go, React, TypeScript, and PostgreSQL, with a durable file store for local development. Products, merchants, payment methods, checkout outcomes, and delivery updates are sandbox fixtures. No real money moves.

## Getting Started

```bash
# Requires Go 1.27.1, Node.js 20.20.2, npm, and Make
git clone https://github.com/vaibhavkapur/Wallets.git
cd Wallets/projects/Recurring-Purchase-Agent
make setup
make test
make dev
```

- UI: http://127.0.0.1:5190
- API: http://127.0.0.1:8090
- Local operator token: `local-operator`

The file store at `.local/recurring-state.json` is the default. Set `DATABASE_URL` to use PostgreSQL, or run `docker compose up --build` for PostgreSQL and the built application.

See [Getting Started](docs/getting-started.md) for prerequisites, configuration, verification, and the first recurring reorder.

## Quick Example

1. Open **Reorder Rules** and create a rule for **Ethiopia Yirgacheffe Whole Bean Coffee (12 oz)**, quantity **1**, preferred merchant **Roaster Direct**, a **$30.00** max delivered price, **$75.00** monthly budget, and **C2 — Assisted Reorder** mode.
2. Select **+1 Day & Scan** in the top bar to advance the sandbox clock and trigger a worker recovery scan. A fresh quote (**$25.02** delivered including shipping and tax) and immutable purchase proposal appear under **Approvals & Reminders**.
3. Review the delivered-price breakdown, pack-size verification, and expiry timestamp, then select **Approve Exact Order** to reserve monthly budget and submit checkout.
4. Open **Orders & Occurrences** to inspect independent scheduling, payment, and fulfillment states, simulate delivery or a partial shipping refund, or request merchant cancellation for an already-submitted order.

To explore recovery and guardrails, open **Operations & Sandbox Lab** and inject **Timeout (Actually Settled → Reconcile)**, **Delayed Fulfillment**, **+45% Price Spike**, **Pack Size Shrunk to 10**, or **Simulate 35-Day Outage**. A timeout keeps the monthly budget reserved (`uncertain_held`) while reconciliation looks up the original idempotency key; multi-slot outages coalesce into one recovery proposal instead of generating a catch-up backlog. See the [Demo Script](docs/demo-script.md) for full walkthroughs.

Live merchant/provider onboarding, real stored-credential mandates, and pilot milestones remain open. Optional HTTP adapters for sibling `Agent-Checkout` (`AGENT_CHECKOUT_URL`) and `Card-Stablecoin-Payment-Optimizer` (`OPTIMIZER_URL`) are decoupled and disabled by default in the sandbox runtime. See [Protocol Versions](docs/protocol-versions.md) and [Deployment](docs/deployment.md) for the integration boundary.
