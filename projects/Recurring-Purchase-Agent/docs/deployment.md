# Deployment

[Documentation home](index.md)

## Local Docker Compose Topology

`compose.yaml` runs three containers:

1. `postgres` (PostgreSQL 17 Alpine, initialized with `backend/migrations/001_schema.sql`).
2. `server` (`recurring-server` on `:8090` with `RUN_EMBEDDED_WORKER=false`).
3. `worker` (`recurring-worker` running durable lease polling and reconciliation independently).

```bash
docker compose up --build
```

## Production Integration Boundaries

This repository is a standalone sandbox MVP. Before live consumer or pilot use, complete the following boundaries:

- **Secrets and transport**: Set `DEMO_MODE=false`, `SECURE_COOKIES=true`, and strong unique values for `SESSION_SECRET`, `OPERATOR_TOKEN`, and `WEBHOOK_SECRET`. Serve behind TLS with strict origin configuration (`ALLOWED_ORIGIN`).
- **Database migrations**: Run versioned migrations against managed PostgreSQL with backups, audit retention policies, and connection pooling.
- **Merchant and checkout onboarding**: Replace `SandboxMerchantAdapter` and `SandboxCheckoutAdapter` with contracted merchant ACP/UCP integrations or a production `AGENT_CHECKOUT_URL` deployment. Validate exact SKU/pack-size identity feeds and merchant cancel/refund SLAs.
- **Stored credentials and C3 mandates**: Keep `C3_LIMITED_DELEGATION` disabled until provider-supported off-session mandates (e.g. scoped card credentials or verified AP2 mandates), customer authentication flows, and error-remedy operations are contractually and technically verified.
- **Compliance and support**: Confirm launch geography, legal entity, commission disclosures, dispute/refund ownership, and operator audit retention before moving live funds.
