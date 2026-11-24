# Configuration

[Documentation home](index.md)

## Environment Variables

The Go binaries read exported environment variables directly; no `.env` file loader is included. [`.env.example`](../.env.example) lists local development defaults.

- `ADDR`: HTTP API bind address (default `127.0.0.1:8089`).
- `APP_ORIGIN`: allowed browser CORS origin (default `http://127.0.0.1:5189`; set `http://127.0.0.1:8089` when serving built static files from Go).
- `DATABASE_URL`: PostgreSQL connection string. When unset, `cmd/server` falls back to `FileStore`. `cmd/worker` requires `DATABASE_URL`.
- `STATE_FILE`: path to local JSON state file when `DATABASE_URL` is unset (default `.local/rewards-state.json`).
- `WEB_DIST`: optional path to compiled frontend assets (`web/dist`) served directly by the Go server with SPA fallback.
- `OPERATOR_TOKEN`: shared secret gating operator-only routes (`/v1/reconciliation-exceptions`, `/v1/ledger/*`, `/v1/system/pause`, `/v1/merchants` POST, sandbox controls). Defaults to `local-operator` for local development.
- `WEBHOOK_SECRET`: HMAC-SHA256 secret verifying `/v1/webhooks/payout-provider` callbacks. Defaults to `local-webhook-secret` for local development.
- `SEED_PILOT_DATA`: when `"true"` (default), seeds three pilot merchants, prefunded campaigns, and sample orders for `cust_alex` if the default tenant is empty.
- `SANDBOX_UI_SIMULATOR`: when `"true"` (default), enables `POST /v1/sandbox/simulate-merchant-event` so the local Merchant Campaigns tab can exercise HMAC-verified webhooks without exposing merchant secrets to browser JavaScript. Disable (`false`) outside local sandbox demos.
- `TEST_DATABASE_URL`: enables `TestPostgresStoreIntegration` against a live PostgreSQL instance during `go test`.

## Development Ports

Vite binds strictly to `127.0.0.1:5189` and proxies `/v1` and `/healthz` to `127.0.0.1:8089` (`web/vite.config.ts`). Docker Compose exposes PostgreSQL on `127.0.0.1:54329` and the combined Go + built frontend container on `127.0.0.1:8089`.

## Fixture Credentials Warning

`local-operator`, `local-webhook-secret`, and the seeded pilot merchant secrets (`whsec_roast_pilot_1001`, etc.) are published sandbox fixtures. Never deploy with fixture secrets or `SANDBOX_UI_SIMULATOR=true`.
