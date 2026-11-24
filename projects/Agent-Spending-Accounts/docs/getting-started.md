# Getting Started

[Documentation home](index.md)

## Prerequisites and installation

Use Go 1.22+, a Rust stable toolchain (`rustup`), Node.js 20+ with npm, `jq`, and `curl`. PostgreSQL is not required: development and tests start an embedded PostgreSQL 16 (downloaded once into the OS temp directory). Docker is optional for the Compose topology.

```bash
git clone https://github.com/vaibhavkapur/Agent-Spending-Accounts.git
cd Agent-Spending-Accounts
npm install
make test
make up
```

`make up` builds the Go binaries and the gateway, then starts four processes in the background and records their PIDs in `.dev/pids`:

- control plane with embedded PostgreSQL and in-process workers: `http://127.0.0.1:8080`
- Rust gateway: `http://127.0.0.1:8402`
- mock x402 resource server and facilitator lookup: `http://127.0.0.1:9402`
- mock signing provider: `http://127.0.0.1:9403`

Logs are written to `.dev/*.log`. `make down` stops everything. `make e2e` runs the scripted scenario in [Testing](testing.md); `make example` runs the example agent; `make dashboard` serves the owner console at `http://localhost:5173`.

## First paid request

1. Create an organization with `POST /v1/organizations`. The response contains `owner_api_key`, shown once.
2. As the owner, create an agent (`POST /v1/agents`, returns `agent_api_key` once), set its policy (`PUT /v1/agents/{id}/policy`) with the mock resource's `url_prefix` (`http://127.0.0.1:9402/v1`) and recipient (`0x209693Bc6afc0C5328bA36FaF03C514EF312287C`), and record a deposit (`POST /v1/funding/deposits`).
3. As the agent, create a task (`POST /v1/tasks`). The budget and deadline are capped by policy.
4. Send `POST /v1/tasks/{task_id}/paid-requests` to the gateway with an `idempotency_key` and the resource URL. The gateway probes the resource, validates the 402 challenge, obtains a grant, pays, and returns the response with `payment`, `service`, and `receipt`.
5. Read `GET /v1/tasks/{task_id}/budget` and `GET /v1/tasks/{task_id}/receipts`, or open the dashboard with the owner key.

The README's Quick Example contains the exact `curl` commands. The [API Reference](api-reference.md) lists headers, bodies, and error codes; [Demo Script](demo-script.md) walks through the adversarial modes of the mock resource.

## Using the SDK and MCP adapter

```ts
import { AgentClient, amount } from "@asa/sdk";
const agent = new AgentClient({ controlPlaneUrl: "http://127.0.0.1:8080", gatewayUrl: "http://127.0.0.1:8402", apiKey: process.env.ASA_AGENT_API_KEY! });
const task = await agent.createTask({ description: "weather", budget: amount("1000000") });
const { data, response } = await agent.paidJson(task.id, { idempotencyKey: `${task.id}:lisbon`, url: "http://127.0.0.1:9402/v1/weather?city=Lisbon" });
```

The MCP server runs with `ASA_AGENT_API_KEY=agent_… node mcp/dist/index.js` over stdio and exposes `asa_create_task`, `asa_get_task_budget`, `asa_paid_request`, `asa_list_receipts`, and `asa_get_payment_attempt`. It refuses owner keys.

## Alternative and verification

`docker compose up --build` starts the same services on PostgreSQL 16 in containers; see [Deployment](deployment.md). The development stack relaxes the gateway's URL policy (`ALLOW_HTTP`, `ALLOW_PRIVATE_TARGETS`) so it can reach the local mock resource; production must not. Expected test results are listed in [Testing](testing.md) and the dated [Test Report](test-report.md).
