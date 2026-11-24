# API Reference

[Documentation home](index.md)

The canonical machine-readable contract lives at [contracts/openapi.json](../contracts/openapi.json) and is also served at `GET /v1/openapi.json`.

## Conventions

- **Monetary Amounts**: all minor and base unit quantities are serialized as base-10 decimal strings (e.g. `"1000"` = `$10.00 USD`, `"9750000"` = `9.750000 USDC`). Floating-point JSON numbers are rejected.
- **Tenant Isolation**: supply `X-Tenant-ID` (defaults to `default`). Tenants never share merchants, campaigns, rewards, ledger accounts, or exceptions.
- **Role Headers**:
  - Customer mutations accept `X-Customer-ID` and reject mismatches against request payload `customer_id`.
  - Merchant campaign creation accepts `X-Merchant-ID` and enforces ownership.
  - Operator routes require `X-Operator-Token: <token>` (or `Authorization: Bearer <token>`).
- **Signed Webhooks**: `/v1/merchant-events` and `/v1/webhooks/payout-provider` require `X-Webhook-Timestamp` (Unix seconds, within ±10 minutes) and `X-Webhook-Signature: sha256=<hex>`, computed as `HMAC-SHA256(secret, "<timestamp>.<raw_body>")`.

## Health & Contract

- `GET /healthz` — service status and current server clock.
- `GET /v1/openapi.json` — OpenAPI 3.1 schema.
- `GET /v1/system/status` — tenant counters, pause state, open exception count, and last reconciliation time.

## Merchants & Campaigns

- `GET /v1/merchants` — list merchants (webhook secrets redacted; returns `webhook_secret_hint`).
- `POST /v1/merchants` *(operator)* — onboard a merchant (`id`, `name`, `webhook_secret`, `terms_accepted: true`).
- `GET /v1/campaigns?merchant_id=<id>` — list active `CampaignVersion` records with reconciled funding, available balance, reserved fee/loss reserve, and promised/paid totals.
- `POST /v1/campaigns` — create a new campaign or increment version (`campaign_id` optional) with `reward_type` (`fixed` or `percentage`), caps, `eligible_skus`, `return_window_seconds`, `return_treatment` (`pro_rata` or `full_reversal`), `platform_fee_bps`, and `loss_reserve_bps`.
- `POST /v1/campaigns/{id}/funding` — record or reconcile a merchant prefunding deposit (`provider_ref`, `amount_minor`, `reconciled`). Idempotent by `provider_ref`.
- `POST /v1/campaigns/{id}/status` — set campaign status (`active`, `paused`, `ended`).

## Merchant Event Ingestion

- `POST /v1/merchant-events` *(HMAC-SHA256 signed with merchant secret)* — ingest:
  - `order.paid`: validates active campaign window, SKU eligibility, per-customer cap, budget cap, and reconciled funding coverage; atomically reserves `reward + fee + loss_reserve` and creates a `pending` reward.
  - `order.fulfilled`: marks fulfillment separately from payment settlement and starts the return-window countdown.
  - `order.refunded`: reverses partial or full reward prior to payout, or draws loss reserve and opens a `post_payout_chargeback` exception if already paid.
  - `order.chargeback`: handles post-payout disputes without assuming clawback of irreversible stablecoin payouts.
- `POST /v1/sandbox/simulate-merchant-event` *(sandbox only, gated by `SANDBOX_UI_SIMULATOR=true`)* — signs and verifies a merchant event server-side so browser UI demos never receive raw webhook secrets.

## Customer Rewards, Beneficiaries & Payouts

- `GET /v1/rewards?customer_id=<id>` — customer balance breakdown (`pending`, `eligible`, `payout_requested`, `paid`, `reversed`), disclosed minimum payout threshold, fee metadata, active beneficiary, and order-linked statements.
- `GET /v1/beneficiaries?customer_id=<id>` — list immutable `BeneficiaryVersion` history.
- `POST /v1/beneficiaries` — enroll or rotate destination (`asset: "USDC"`, `network: "base"`, valid non-zero EVM `address`, `recovery_disclosed: true`). Revokes previous version so prior quotes cannot silently redirect.
- `POST /v1/beneficiaries/{id}/verify` — mark a pending beneficiary version verified.
- `POST /v1/payout-quotes` — obtain a time-limited quote locking `beneficiary_version_id`, eligible `reward_ids`, `source_amount_base`, `gross_payout_base`, `fee_base`, `net_payout_base`, `fx_rate_bps`, `disclosure`, and `expires_at`. Enforces minimum payout threshold.
- `POST /v1/payouts` *(requires `Idempotency-Key` header or body `idempotency_key`)* — authorize one payout bound to an unexpired, unconsumed quote and active verified beneficiary version (`terms_approved: true`).
- `GET /v1/payouts?customer_id=<id>` — list payout attempts.
- `GET /v1/payouts/{id}` — report logical payout and all attempts with provider reference, tx hash, and confirmations.
- `POST /v1/payouts/{id}/retry` *(requires `Idempotency-Key`)* — retry a logical payout reusing `logical_payout_id` ONLY when the prior attempt is confirmed `failed` and the beneficiary version has not changed. Rejects `outcome_unknown` with `409 Conflict`.

## Provider Webhooks

- `POST /v1/webhooks/payout-provider` *(HMAC-SHA256 signed with `WEBHOOK_SECRET`)* — idempotent by `callback_id`; detects conflicting callbacks and late confirmations after failure by opening operator exceptions.

## Operator Endpoints (`X-Operator-Token` Required)

- `GET /v1/reconciliation-exceptions?status=open` — discrepancy queue.
- `POST /v1/reconciliation-exceptions/{id}/resolve` — resolve an exception with audit note.
- `POST /v1/reconciliation/run` — run one worker eligibility, payout polling, and custody reconciliation cycle immediately.
- `GET /v1/ledger/summary` — double-entry trial balance per asset, account balances, and funding coverage check.
- `GET /v1/ledger/entries` — immutable journal entry log.
- `POST /v1/system/pause` — toggle product-level `issuance_paused` and `payouts_paused` flags with audit reason.
- `POST /v1/sandbox/advance-clock` — advance sandbox server clock (`seconds`) and run worker tick.
- `POST /v1/sandbox/custody-drift` — inject or clear simulated USD custody drift (`drift_usd_minor`) and reconcile.
