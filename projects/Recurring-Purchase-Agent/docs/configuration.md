# Configuration

[Documentation home](index.md)

## Environment Variables

The Go binaries read exported environment variables directly; no `.env` loader is built in. [`.env.example`](../.env.example) lists default local development values.

- `ADDR`: HTTP API bind address, default `127.0.0.1:8090`.
- `DEMO_MODE`: `"true"` (default) enables the controllable sandbox clock, fault injection endpoints, and fulfillment simulator. Set `"false"` to disable sandbox simulation routes and use the system UTC clock.
- `DATABASE_URL`: PostgreSQL connection string. When unset, the server falls back to the local durable JSON file store.
- `DATA_PATH`: JSON file store path when `DATABASE_URL` is unset. Default `.local/recurring-state.json`.
- `RUN_EMBEDDED_WORKER`: `"true"` (default) runs the recovery worker inside `cmd/server`. Set `"false"` when running `cmd/worker` as a separate process against PostgreSQL.
- `WORKER_ID`: Identifier recorded on job leases for standalone `cmd/worker` instances.
- `ALLOWED_ORIGIN`: Allowed browser origin for CORS and CSRF mutation checks, default `http://127.0.0.1:5190`.
- `SESSION_SECRET`: HMAC-SHA256 signing secret for `uno_recurring_session` cookies. Default `local-dev-recurring-secret` for local development; must be replaced with a high-entropy secret outside localhost.
- `OPERATOR_TOKEN`: Shared secret required to open an `operator` session (`POST /v1/session` with `"role":"operator"`). Default `local-operator` for local development only.
- `WEBHOOK_SECRET`: HMAC-SHA256 verification secret for `POST /v1/webhooks/merchant` (`X-Uno-Signature` hex digest). Default `local-webhook-secret` for local development only.
- `SECURE_COOKIES`: Set `"true"` behind HTTPS so session cookies include the `Secure` attribute. Default `"false"` for localhost.
- `AGENT_CHECKOUT_URL`: Optional base URL of a running `Agent-Checkout` instance to delegate checkout execution over HTTP instead of the built-in sandbox checkout adapter.
- `OPTIMIZER_URL`: Optional base URL of a running `Card-Stablecoin-Payment-Optimizer` instance.

## Development Ports

Vite binds strictly to `127.0.0.1:5190` (`web/vite.config.ts`) and proxies `/v1` and `/healthz` to `http://127.0.0.1:8090`. If you change either port, update `web/vite.config.ts`, `ADDR`, and `ALLOWED_ORIGIN` together.

## Sessions and Isolation

Sessions use `HttpOnly`, `SameSite=Lax`, HMAC-signed cookies valid for 12 hours. Every authenticated endpoint scopes all reads and writes exclusively to the `tenant` embedded in the verified cookie; requests cannot read or modify another tenant's rules, occurrences, or consents. Operator-only controls (`POST /v1/operator/pause`) additionally require `role == "operator"`.
