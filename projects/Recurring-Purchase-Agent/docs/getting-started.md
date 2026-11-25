# Getting Started

[Documentation home](index.md)

## Prerequisites

Use macOS or Linux with Bash, Git, Make, Go **1.27.1**, Node.js **20.20.2**, and npm. The Go toolchain is recorded in `backend/go.mod`; Node is recorded in `.nvmrc`. Docker Compose is optional. The local file store uses Unix `flock` locking and is not a native Windows implementation.

## Clone, Install, and Start

```bash
git clone https://github.com/vaibhavkapur/Wallets.git
cd Wallets/projects/Recurring-Purchase-Agent
make setup
make test
make dev
```

`make setup` downloads Go modules and installs locked frontend dependencies. `make dev` starts the Go API and embedded worker on `127.0.0.1:8090` and starts Vite on `127.0.0.1:5190`. Vite proxies `/v1` and `/healthz` to the API.

Open [the UI](http://127.0.0.1:5190), or verify the API health check:

```bash
curl http://127.0.0.1:8090/healthz
```

The response reports `status: "ok"`, `mode: "sandbox"`, and the current `now_utc` sandbox clock. The browser automatically opens a local demo session for tenant `household-demo`. No registration or real payment credentials are required.

## First Recurring Reorder

1. Open **Reorder Rules** and review the prefilled **Ethiopia Yirgacheffe Whole Bean Coffee (12 oz)** form, or choose another coffee or household essential from the catalog.
2. Select **Create Recurring Rule** to create version `v1` in `C2_ASSISTED_REORDER` mode.
3. Click **+1 Day & Scan** in the top bar (or open **Operations & Sandbox Lab** and click **+3 Hours** / **+1 Day**). The worker detects the due slot, fetches a fresh sandbox quote, verifies policy constraints, and creates a pending `PurchaseProposal`.
4. Switch to **Approvals & Reminders**, inspect the delivered-price breakdown (item subtotal, shipping, tax, stock status, and expiry), and select **Approve Exact Order**.
5. Open **Orders & Occurrences** to inspect independent Schedule, Payment, and Fulfillment states, mark the order delivered, apply a simulated partial shipping refund, or request merchant cancellation.

To access operator controls, open **Operations & Sandbox Lab** and elevate your session with the local fixture token `local-operator`.

## Persistence and Restart

By default, tenant state, occurrences, reservations, notifications, and audit events persist in `.local/recurring-state.json`. Stop `make dev` with `Ctrl+C` before starting a second instance against the same file; the store rejects concurrent writers via `flock`.

Use PostgreSQL when running multiple processes or a separate worker binary (`backend/cmd/worker`). See [Database and Recovery](database.md) and [Deployment](deployment.md). Exported environment variables are read directly by Go binaries; `.env` is not auto-loaded.

## Next Steps

Read [Architecture](architecture.md), [Configuration](configuration.md), [API Reference](api-reference.md), and [Testing](testing.md). See [Demo Script](demo-script.md) for guided edge-case walkthroughs.
