# Deployment

[Documentation home](index.md)

## Local stack

`make up` (`scripts/dev-up.sh --daemon`) runs the control plane with an embedded PostgreSQL, the mock signer, the mock x402 resource with its facilitator lookup, and the gateway as background processes; `make down` stops them. Run `scripts/dev-up.sh` without `--daemon` to keep them in the foreground. `make dashboard` serves the owner console with Vite.

## Docker Compose

```bash
docker compose up --build
docker compose ps
```

Compose builds distroless images from `control-plane/Dockerfile` (targets `controlplane`, `worker`, `mocksigner`, `mockresource`) and `gateway/Dockerfile`, and starts PostgreSQL 16 (`5432`, volume `pgdata`), the control plane (`8080`), the mock signer (`9403`), the mock resource (`9402`), and the gateway (`8402`). Secrets default to development placeholders; set `INTERNAL_TOKEN`, `SIGNER_TOKEN`, `OPERATOR_TOKEN`, and `GRANT_SIGNING_SEED` in a `.env` file. The gateway container runs with `ALLOW_HTTP` and `ALLOW_PRIVATE_TARGETS` because the mock resource is on the private Compose network; remove both when pointing at real resources.

Use `docker compose logs controlplane gateway` to follow authorizations, submissions, and reconciliation. `docker compose down -v` deletes the database volume.

## Separate processes

For a production-style layout run `control-plane/cmd/controlplane` with `RUN_WORKERS=false`, one or more `control-plane/cmd/worker` processes against the same `DATABASE_URL`, and one or more gateways. All worker loops are idempotent and guarded by row locks, so several instances may run concurrently. Serve the dashboard's static build (`npm run build -w dashboard`, output in `dashboard/dist`) from any static host and set `ALLOWED_ORIGINS` on the control plane accordingly.

## Pause and recovery

`POST /v1/organization/pause` stops all new spending for one organization; stopping the gateway stops new payments for every organization while reconciliation keeps running. Back up PostgreSQL together with the grant signing seed and the internal token. After a restart, `authorized` attempts whose grants expired are released by the recovery worker, and `submitted`/`unknown` attempts are reconciled through `FACILITATOR_URL`; nothing is released without evidence or the configured window.

## Scope

This repository ships fixtures for the signing provider, the resource server, and the facilitator. Before live use, replace them with validated provider adapters behind `gateway/src/signer.rs` and `workers.SettlementVerifier`, pin the x402 scheme/network/facilitator versions, confirm custody and withdrawal responsibilities, and re-run the suites in [Testing](testing.md) on the chosen test network.
