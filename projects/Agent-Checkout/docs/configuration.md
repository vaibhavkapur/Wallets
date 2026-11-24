# Configuration

[Documentation home](index.md)

## Environment Variables

The Go programs read exported environment variables directly; no automatic `.env` loader is configured. [`.env.example`](../.env.example) lists development values.

- `PAYMENT_MODE`: defaults to `sandbox`; API and worker reject other modes.
- `DATABASE_URL`: selects PostgreSQL. API and external worker must use the same database.
- `DATA_FILE`: JSON state path without PostgreSQL. `make dev` supplies the absolute `.local/state.json` path; execution from `backend/` defaults to `../.local/state.json`.
- `ADDR`: API bind address, default `127.0.0.1:8088`.
- `APP_ORIGIN`: accepted browser mutation origin, default `http://127.0.0.1:5188`. Set `http://127.0.0.1:8088` when serving the built UI directly from Go.
- `WEB_DIST`: static frontend directory. The development script supplies an absolute `web/dist` path; execution from `backend/` defaults to `../web/dist`.
- `SESSION_SECRET`: cookie-signing secret. If absent, the API generates one at startup, invalidating old cookies after restart. Use a stable private value for persistent test sessions.
- `OPERATOR_TOKEN`: gates operator sessions. No direct-server default; `make dev` and Compose supply fixture `local-operator`.
- `WEBHOOK_SECRET`: sandbox webhook HMAC secret. Webhooks are disabled when absent. The development runner supplies `local-webhook-secret`.
- `SECURE_COOKIES`: `true` sets the cookie's Secure attribute; use behind HTTPS. Defaults to false for localhost.
- `WORKER_MODE`: `external` disables the embedded worker and requires PostgreSQL. Other values use the embedded worker.
- `TEST_DATABASE_URL`: enables the PostgreSQL integration test; separate from the application database setting.

## Browser and Development Ports

Vite binds to `127.0.0.1:5188` with strict port selection and proxies to `127.0.0.1:8088`. These values live in `web/vite.config.ts`. If changing ports, update the proxy and `APP_ORIGIN` together. Stop existing processes on those ports before launching another copy.

## Sessions and Sandbox Access

The demo endpoint creates the same fixture customer for every local browser. Sessions use signed HttpOnly, SameSite=Strict cookies and expire after eight hours. Operator sessions use that customer's data and can pause purchases, reconcile payments, and advance mock fulfillment. This is a single-customer local demonstration, not production identity or a global multi-tenant operations console.

Fixture operator and webhook values are published test data. Do not use them for a deployed service. Real payment keys are neither needed nor accepted by the application runtime.

See [API Reference](api-reference.md) and [Deployment](deployment.md) for requests and live-use requirements.
