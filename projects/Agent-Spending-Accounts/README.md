# Agent Spending Accounts

Budgeted spending accounts for software agents that buy paid APIs, data, and compute. An owner funds an account, approves specific endpoints and payment recipients, and sets per-request, per-task, per-agent, and per-organization limits; agents then purchase x402-protected resources through a gateway that cannot exceed what the control plane has atomically reserved. Payment settlement and service delivery are recorded separately, and uncertain outcomes are reconciled rather than guessed.

> **[Read the full documentation](docs/index.md)**

Built with Go and PostgreSQL (control plane), Rust (payment gateway), and TypeScript (SDK, MCP adapter, React owner dashboard). The signing provider, resource server, and facilitator lookup are local fixtures; no live network is used.

## Getting Started

```bash
# Requires Go 1.22+, Rust stable, Node.js 20+, jq, curl
npm install
make up          # control plane with embedded PostgreSQL, mock signer, mock x402 resource, gateway
make e2e         # end-to-end scenario including adversarial cases
make example     # provisions an organization and runs a budgeted research agent
make dashboard   # owner console at http://localhost:5173
make down
```

Or `docker compose up --build` for the same topology on a real PostgreSQL.

See [Getting Started](docs/getting-started.md) for prerequisites, cloning, configuration, and verification.

## Quick Example

```bash
CP=http://127.0.0.1:8080; GW=http://127.0.0.1:8402; RES=http://127.0.0.1:9402

# Owner: create an organization (prints the owner key once), an agent, a policy, and funding
curl -X POST $CP/v1/organizations -H 'content-type: application/json' -d '{"name":"Acme Research"}'
curl -X POST $CP/v1/agents -H "authorization: Bearer $OWNER_KEY" \
  -H 'content-type: application/json' -d '{"name":"research-agent"}'
curl -X PUT $CP/v1/agents/$AGENT_ID/policy -H "authorization: Bearer $OWNER_KEY" \
  -H 'content-type: application/json' -d '{
    "asset": {"asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","network":"base-sepolia","decimals":6,"scheme":"exact"},
    "per_request_max":"500000","per_task_max":"1000000","agent_daily_max":"5000000","org_daily_max":"20000000","fee_allowance":"0",
    "endpoints":[{"url_prefix":"'$RES'/v1","methods":["GET","POST"],"recipients":["0x209693Bc6afc0C5328bA36FaF03C514EF312287C"]}],
    "max_task_seconds":3600,"grant_ttl_seconds":60}'
curl -X POST $CP/v1/funding/deposits -H "authorization: Bearer $OWNER_KEY" \
  -H 'content-type: application/json' \
  -d '{"asset":"0x036CbD53842c5426634e7929541eC2318f3dCF7e","network":"base-sepolia","amount":"3000000","reference":"demo"}'

# Agent: open a task within policy, then buy a paid resource through the gateway
curl -X POST $CP/v1/tasks -H "authorization: Bearer $AGENT_KEY" \
  -H 'content-type: application/json' -d '{"description":"weather research","budget":"1000000"}'
curl -X POST $GW/v1/tasks/$TASK_ID/paid-requests -H "authorization: Bearer $AGENT_KEY" \
  -H 'content-type: application/json' \
  -d '{"idempotency_key":"weather-lisbon","method":"GET","url":"'$RES'/v1/weather?city=Lisbon"}'

# Inspect remaining capacity and receipts
curl $CP/v1/tasks/$TASK_ID/budget -H "authorization: Bearer $AGENT_KEY"
curl $CP/v1/tasks/$TASK_ID/receipts -H "authorization: Bearer $AGENT_KEY"
```

Replace `$OWNER_KEY`, `$AGENT_ID`, `$AGENT_KEY`, and `$TASK_ID` with values from the preceding responses. Amounts are base-unit decimal strings (`"1000000"` is 1 USDC). Repeating a paid request with the same `idempotency_key` returns the original outcome and never pays twice. See the [API Reference](docs/api-reference.md) and [Demo Script](docs/demo-script.md).
