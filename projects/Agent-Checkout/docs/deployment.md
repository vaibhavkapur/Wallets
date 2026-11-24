# Deployment

[Documentation home](index.md)

## Local Containers

```bash
docker compose up --build
```

Compose starts PostgreSQL 17.6 and a multi-stage-built application. Open [http://127.0.0.1:8088](http://127.0.0.1:8088). The compiled frontend and API share an origin. The application runs as a non-root container user and waits for database health.

The database binds to `127.0.0.1:54328`; the app binds to `127.0.0.1:8088`. A named database volume persists across restarts. Compose credentials are local fixtures.

To use PostgreSQL with Vite development:

```bash
docker compose up -d db
DATABASE_URL='postgres://uno:uno_local@127.0.0.1:54328/uno?sslmode=disable' make dev
```

## Standalone Build

```bash
make build
APP_ORIGIN=http://127.0.0.1:8088 \
DATA_FILE="$PWD/.local/state.json" \
WEB_DIST="$PWD/web/dist" \
OPERATOR_TOKEN=local-operator \
  .local/uno-server
```

Stop any API using the same data file before starting this process. Only one process may open the file store.

## Separate PostgreSQL Worker

Export the same `DATABASE_URL` for both processes. From `backend/`, start the API using `WORKER_MODE=external go run ./cmd/server`. In another terminal, run `go run ./cmd/worker`. Set static-file and origin settings to match the UI. File persistence does not support separate workers.

The worker claims durable jobs with leases and retries. Stopping it pauses progress while preserving queued work. Never point it at a different database from the API.

## Live-Use Boundary

This is a local sandbox, not a production payment service. `PAYMENT_MODE` rejects live mode. Publishing its source repository does not publish a running application.

Before a real pilot, complete:

- Merchant participation, real catalog/address/fulfillment integration, and order/refund reconciliation.
- Provider-managed enrollment, chosen-card support, scoped-token lifecycle, and verified issuer authentication.
- Customer identity and operator authentication. The shared demo-session endpoint must be replaced before external user access.
- Secret management, TLS, monitoring, versioned migrations, retention/deletion, and evidence export.
- Geography, contracts, support/refund ownership, and verified rewards and transaction categorization.

The plan's A1 discovery, partner-certified A2 sandbox milestone, A3 pilot, and A4 commercial evaluation remain open. Local fixtures do not satisfy partner or commercial gates.

See [Protocol Versions](protocol-versions.md) and [Configuration](configuration.md).
