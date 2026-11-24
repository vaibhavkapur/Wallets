# Getting Started

[Documentation home](index.md)

## Prerequisites

Use macOS or Linux with Bash, Git, Make, Go **1.27.1**, Node.js **20.20.2**, and npm. The Go toolchain is recorded in `backend/go.mod`; Node is recorded in `.nvmrc`. Docker Compose is optional. The file store uses Unix file locking and is not a native Windows implementation.

## Clone, Install, and Start

```bash
git clone https://github.com/vaibhavkapur/Agent-Checkout.git
cd Agent-Checkout
make setup
make test
make dev
```

`make setup` downloads Go modules and installs locked frontend dependencies. `make dev` builds the Go executable, starts its API and embedded worker on `127.0.0.1:8088`, and starts Vite on `127.0.0.1:5188`. Vite proxies `/v1` and `/healthz` to the API.

Open [the UI](http://127.0.0.1:5188), or check the API:

```bash
curl http://127.0.0.1:8088/healthz
```

The response identifies the app as a sandbox. The browser creates a local demo session automatically. No registration or payment credentials are required.

## First Purchase

Choose Studio Pro, Midnight, quantity one, a $200 delivered-price limit, and ZIP 94103. Compare offers, choose an eligible card, review the exact purchase, and check the consent box before approving.

The default fixture quotes $182.94 delivered and estimates $5.48 cashback on Everyday Cash. These are illustrative values, not real merchant offers or verified rewards. The receipt appears after the worker confirms the sandbox payment and order.

Open **My orders** to inspect receipts, request a partial refund, or cancel an eligible order. **Activity** shows the audit trail. **Operator console** accepts `local-operator` when launched through `make dev`; this is a local fixture token.

## Persistence and Restart

Data persists in `.local/state.json`, including pending jobs and simulated provider outcomes. Stop the development processes with Ctrl+C before starting another instance against the same file. The store rejects a second writer. Session cookies become invalid after restart unless `SESSION_SECRET` is configured; reload the app to create another demo session.

Use PostgreSQL for multiple processes. See [Database and Recovery](database.md) and [Deployment](deployment.md). The development scripts read exported environment variables; they do not automatically load `.env`.

## Next Steps

Read [Configuration](configuration.md), [Architecture](architecture.md), and [Testing](testing.md). The [Demo Script](demo-script.md) covers challenges, timeouts, changed quotes, cancellations, and refunds.
