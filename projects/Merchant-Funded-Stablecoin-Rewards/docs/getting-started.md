# Getting Started

[Documentation home](index.md)

## Prerequisites

Use macOS or Linux with Bash, Git, Make, Go **1.27.1**, Node.js **20.20.2**, npm, and Rust/Cargo (for the optional batch reconciler). Go toolchain requirements live in `backend/go.mod`; Node is recorded in `.nvmrc`. Docker Compose is optional. The local file store uses Unix file locking (`flock`) and is not intended for native Windows.

## Clone, Install, and Start

```bash
git clone https://github.com/vaibhavkapur/Wallets.git
cd Wallets/projects/Merchant-Funded-Stablecoin-Rewards
make setup
make test
make dev
```

`make setup` downloads Go modules and installs locked frontend dependencies. `make dev` starts the Go API and embedded worker on `127.0.0.1:8089` and starts Vite on `127.0.0.1:5189`. Vite proxies `/v1` and `/healthz` to the API.

Open [the UI](http://127.0.0.1:5189), or check the API directly:

```bash
curl http://127.0.0.1:8089/healthz
```

On first start with an empty store and `SEED_PILOT_DATA=true`, the service seeds three prefunded pilot merchants (`Roast & Co. Coffee`, `Soundhouse Audio`, and `Northstar Trail Supply`), a verified Base `USDC` beneficiary for `cust_alex`, one eligible order (`ORD-9001`, `$10.00`), and one pending unfulfilled order (`ORD-9002`, `$15.00`).

## First Payout Walkthrough

1. In **Customer Rewards**, verify `cust_alex` shows **$10.00 Eligible to Claim** and **$15.00 Pending Return Window**.
2. Inspect the enrolled destination (`ben_cust_alex_v1`, `USDC` on `base`).
3. Click **Get Payout Quote** to lock exact USD-to-USDC conversion (`10.000000 USDC` gross, `-0.250000 USDC` fee, `9.750000 USDC` net) and quote expiry.
4. Check the approval confirmation and click **Authorize Payout**. The payout settles with a deterministic sandbox transaction hash and transitions `ORD-9001` from `eligible` to `paid`.
5. Open **Operator Queue** (default fixture token `local-operator`) and verify **All Journals Balanced** and **Funding Covers Obligations** remain green.

## Persistence and Restart

By default, state persists to `.local/rewards-state.json`. Stop `make dev` with Ctrl+C before starting another instance against the same file; `FileStore` rejects a second concurrent writer. To run multiple processes or a separate worker, configure `DATABASE_URL` with PostgreSQL. See [Database and Recovery](database.md) and [Deployment](deployment.md).

## Next Steps

Read [Architecture and Ledger](architecture.md), [API Reference](api-reference.md), [Configuration](configuration.md), and [Testing](testing.md). Follow [Demo Script](demo-script.md) to exercise partial returns, `outcome_unknown` recovery, beneficiary rotation, custody drift, and post-payout chargebacks.
