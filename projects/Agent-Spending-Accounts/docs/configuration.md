# Configuration

[Documentation home](index.md)

## Loading settings

All services read environment variables; nothing loads `.env` automatically. `.env.example` lists every variable with development values. `scripts/dev-up.sh` exports development defaults for the local stack and `docker-compose.yml` passes them to containers (override with a `.env` file next to the Compose file).

## Control plane (`control-plane/cmd/controlplane`)

- `DATABASE_URL`: PostgreSQL DSN. Migrations run on start. Required unless the binary is started with `--dev-embedded-postgres`, which launches an embedded PostgreSQL 16 for development only.
- `INTERNAL_TOKEN`: shared secret presented by the gateway on `/internal/v1/*`. Required.
- `OPERATOR_TOKEN`: gates `POST /v1/organizations`. Organization creation is open when empty (development).
- `GRANT_SIGNING_SEED`: 32-byte Ed25519 seed, base64 or hex. When unset an ephemeral key is generated and logged as a warning; grants then do not survive a restart.
- `LISTEN_ADDR`: default `:8080`.
- `ALLOWED_ORIGINS`: comma-separated CORS origins for the dashboard (`http://localhost:5173` by default in the dev stack).
- `RUN_WORKERS`: `false` disables the in-process workers; run `control-plane/cmd/worker` separately instead.
- `FACILITATOR_URL`: base URL for the settlement verifier (`GET {base}/settlements?payment_hash=…`). The mock resource server provides this endpoint.
- `SUBMITTED_GRACE`: how long a `submitted` attempt may go without an outcome before it is promoted to `unknown` (default `2m`).
- `UNKNOWN_RELEASE_AFTER`: how long an `unknown` attempt may remain `not_found` at the verifier before its hold is released (default `24h`).

## Gateway (`gateway/`)

- `CONTROL_PLANE_URL`, `INTERNAL_TOKEN`: required.
- `SIGNER_URL`, `SIGNER_TOKEN`: signing provider base URL and credential; the mock expects `POST /v1/sign`.
- `GRANT_PUBLIC_KEY_BASE64`: raw Ed25519 public key. Fetched from `GET /internal/v1/grant-public-key` when unset (the gateway retries while the control plane starts).
- `LISTEN_ADDR`: default `0.0.0.0:8402`.
- `MAX_RESPONSE_BYTES`: response body cap, default 1 MiB. Truncated responses are reported with `truncated: true` and `delivered: false`.
- `REQUEST_DEADLINE_SECONDS`: wall-clock limit per outbound request, default 30.
- `ALLOW_HTTP`, `ALLOW_PRIVATE_TARGETS`: development-only relaxations that allow plain `http://` resources and loopback/private destinations. Both log a warning at startup and must stay unset in production.
- `RUST_LOG`: tracing filter, default `info`.

## Fixtures

- `mockresource`: `LISTEN_ADDR` (`:9402`), `PAY_TO`, `ASSET`, `NETWORK`, `PUBLIC_URL`, `PRICE_WEATHER` (`100000`), `PRICE_SUMMARIZE` (`250000`), `SLOW_SECONDS`.
- `mocksigner`: `LISTEN_ADDR` (`:9403`), `SIGNER_TOKEN`, `WALLET_ADDRESS`, `SIGNER_SECRET`, and provider-side constraints `ALLOWED_TO` (comma-separated recipients) and `MAX_VALUE`.

## TypeScript packages

- SDK: constructor options `controlPlaneUrl`, `gatewayUrl`, `apiKey`; no environment variables.
- MCP adapter: `ASA_AGENT_API_KEY` (must start with `agent_`), `ASA_CONTROL_PLANE_URL`, `ASA_GATEWAY_URL`.
- Example agent: `ASA_CONTROL_PLANE_URL`, `ASA_GATEWAY_URL`, `ASA_RESOURCE_URL`, `ASA_PAY_TO`.
- Dashboard: `VITE_CONTROL_PLANE_URL` sets the default control plane URL on the sign-in form. The owner key is kept in `sessionStorage` for the tab only.

## Local stack ports

`scripts/dev-up.sh` honours `CP_PORT` (8080), `GW_PORT` (8402), `RESOURCE_PORT` (9402), `SIGNER_PORT` (9403), `INTERNAL_TOKEN`, `SIGNER_TOKEN`, `PAY_TO`, `REQUEST_DEADLINE_SECONDS`, `MAX_RESPONSE_BYTES`, `SUBMITTED_GRACE`, and `UNKNOWN_RELEASE_AFTER`. `scripts/e2e.sh` reads `CP`, `GW`, `RES`, `PAY_TO`, `ASSET`, and `NETWORK`.
