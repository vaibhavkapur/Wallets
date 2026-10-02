# Configuration

[Documentation home](index.md)

## Loading settings

All binaries read environment variables; nothing loads `.env` automatically. `.env.example` lists every variable with development values. `make up` needs none of them: `devstack` supplies its own database, engine token and seeded key.

## API (`orchestrator/cmd/optimizerd`)

- `DATABASE_URL`: PostgreSQL DSN. Required. Migrations run on start under an advisory lock.
- `ENGINE_URL`: engine base URL, default `http://127.0.0.1:8081`.
- `ENGINE_AUTH_TOKEN`: shared secret presented to the engine. Required.
- `OPTIMIZER_MODE`: `shadow` (default) or `assisted`. Shadow mode records evaluations and customer-reported selections and refuses handoffs; an evaluation produced in shadow mode can never be handed off, even after the mode changes.
- `OPTIMIZER_POLICY_FILE`: versioned platform policy, default `config/policy.v1.json` (strictly decoded; `policy_version` and `comparison_currency: USD` required).
- `OPTIMIZER_RECORDED_DIR`: recorded quote dataset, default `fixtures/recorded/v1`.
- `OPTIMIZER_LISTEN_ADDR`: default `127.0.0.1:8080`.
- `EXECUTOR_WEBHOOK_URL`, `EXECUTOR_WEBHOOK_SECRET`: set together to deliver outbox events to the execution service with an HMAC-SHA256 signature (`X-Optimizer-Signature: v1=…` over `<timestamp>.<body>`, plus `X-Optimizer-Event-Id`, `X-Optimizer-Topic`, `X-Optimizer-Timestamp`). The secret must be at least 24 bytes. When unset, events stay in the outbox and a startup log line says so.

Fixed behaviour: quote cache entries are served only while every quote keeps at least 30 s of validity; the expired-handoff sweep runs every 30 s with a 1 min grace; outbox relay polls every 2 s, batch 20, lease 30 s, backoff `1s × 2^attempts` capped at 5 min.

## Engine (`engine/crates/optimizer-service`)

- `ENGINE_AUTH_TOKEN`: required, at least 24 bytes; the service refuses to start otherwise. Compared in constant time.
- `ENGINE_LISTEN_ADDR`: default `127.0.0.1:8081`.

The body limit is 1 MiB. The engine has no other configuration: policy is part of every request.

## Operator tool (`orchestrator/cmd/optimizerctl`)

- `DATABASE_URL`: required.

Commands: `create-tenant -id -name`, `create-key -tenant [-label] [-scopes a,b]` (all scopes by default; the secret is printed once), `revoke-key -id`, `pause [-scope global|TENANT] -reason`, `resume [-scope …]`.

## Offline evaluation (`orchestrator/cmd/offline-eval`)

- `ENGINE_AUTH_TOKEN`: required. Flags: `-dataset` (default `fixtures/recorded/v1`), `-engine-url`, `-json`.

## Local stack (`orchestrator/cmd/devstack`)

Flags: `-mode` (`shadow` | `assisted`), `-listen` (`127.0.0.1:8080`), `-engine` (path to `optimizer-engine`, default `engine/target/debug/optimizer-engine`), `-root`, `-pg-port` (`54329`). Data persists in `.dev/postgres/`; the development key is kept in `.dev/api-key` across restarts.

## TypeScript packages

- SDK: constructor options `baseUrl`, `apiKey` (omit when a proxy injects it), `fetch`; no environment variables.
- Comparison UI dev server: `OPTIMIZER_API_URL` (default `http://127.0.0.1:8080`) and `OPTIMIZER_API_KEY` (default: contents of `.dev/api-key`) are read by `vite.config.ts` and used by the `/api` proxy. The browser never receives the key.

## Local stack ports

| Service | Address |
|---|---|
| API | `127.0.0.1:8080` |
| Engine | free loopback port chosen by devstack (see `.dev/devstack.log`) |
| Embedded PostgreSQL | `127.0.0.1:54329` |
| Comparison UI | `localhost:5173` |
