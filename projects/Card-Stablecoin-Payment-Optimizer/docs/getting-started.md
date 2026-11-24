# Getting Started

[Documentation home](index.md)

## Prerequisites and installation

Use Go 1.27+, a Rust toolchain via `rustup` (the workspace pins 1.99.0 with `clippy` and `rustfmt`), Node.js 20+ with npm, Python 3.9+, and `curl`. PostgreSQL is not required: development and tests start an embedded PostgreSQL 16 (downloaded once into the OS temp directory). Docker is not used.

```bash
git clone https://github.com/vaibhavkapur/Card-Stablecoin-Payment-Optimizer.git
cd Card-Stablecoin-Payment-Optimizer
make install      # npm workspaces
make test         # Rust, reference check, Go, TypeScript
make up           # local stack in assisted mode
```

`make up` (`scripts/dev-up.sh assisted`) builds the engine and the Go binaries into `.dev/bin/`, then starts `devstack` in the background and records its PID in `.dev/pids`. `devstack` runs:

- an embedded PostgreSQL on port 54329 with data under `.dev/postgres/` (kept between runs)
- the Rust engine as a child process on a free loopback port, with a development token
- the API at `http://127.0.0.1:8080`, seeded with tenant `ten_dev` and an API key holding every scope, written to `.dev/api-key`

Logs are in `.dev/devstack.log`. `make down` stops everything. `make up-shadow` starts the same stack in shadow mode, where evaluations are recorded but nothing can be handed off for execution. `make e2e` runs the scripted scenario in [Testing](testing.md); `make offline-eval` runs milestone O1; `make web` serves the comparison interface at `http://localhost:5173`, whose dev proxy injects the key from `.dev/api-key` so it never reaches the browser.

## First comparison

1. Store the customer's preferences with `PUT /v1/customers/{id}/preferences`: the default method (the baseline for savings), the minimum saving before switching, the rails they consent to pay with, hard constraints and point valuations. Without stored preferences only card payments are consented, so a stablecoin option is excluded as `not_consented`.
2. `GET /v1/catalog/baskets` lists the recorded synthetic baskets; pick a `merchant_id` and `basket_ref`.
3. `POST /v1/evaluations` with `customer_id`, `merchant_id` and `basket_ref`. The response carries the engine result verbatim: outcome, rationale, preferred option, savings versus the baseline split into immediate and estimated parts, each option's breakdown or exclusions, assumptions, sensitivities and the expiry.
4. `POST /v1/evaluations/{id}/refresh` obtains fresh quotes and a new evaluation linked to the first.
5. In assisted mode, `POST /v1/evaluations/{id}/execution-handoffs` with the customer's approval of one eligible option at exactly its `amount_due` creates a constrained reference for the execution service. `POST /v1/outcomes` records what the provider or executor observed, and `GET /v1/reports/summary` reports coverage, selection, quote-to-execution differences and savings.

The README's Quick Example contains the exact `curl` commands. The [API Reference](api-reference.md) lists bodies, scopes and error codes; [Demo Script](demo-script.md) walks through the interface.

## Using the SDK

```ts
import { OptimizerClient, formatMicroUsd, explain } from "@unowallet/optimizer-sdk";

const client = new OptimizerClient({ baseUrl: "http://127.0.0.1:8080", apiKey: process.env.OPTIMIZER_API_KEY! });
const ev = await client.evaluate({ customer_id: "cus_1", merchant_id: "mer_demo_outfitter", basket_ref: "trail-running-shoes" });
console.log(ev.result.outcome, ev.result.preferred_option_id);
for (const o of ev.result.options) {
  console.log(o.label, o.status, o.breakdown && formatMicroUsd(o.breakdown.estimated_net_cost_micro_usd));
}
for (const s of ev.result.sensitivities) console.log(explain(s));
```

The SDK's types come from `contracts/engine-v1.schema.json` and `contracts/openapi.yaml` (`npm run generate`). It formats decimal strings exactly and never converts an amount to a floating-point number; it contains no financial formula.

## Operator tasks

`orchestrator/cmd/optimizerctl` works directly against `DATABASE_URL`: `create-tenant`, `create-key` (prints the secret once; scopes are listed in the [API Reference](api-reference.md)), `revoke-key`, `pause` and `resume` (globally or per tenant). The devstack database is reachable at `postgres://optimizer:optimizer@127.0.0.1:54329/optimizer?sslmode=disable` while `make up` is running.
