# Threat Model & Security Invariants

[Documentation home](index.md)

## Core Trust Boundaries

1. **Merchant Webhook Boundary (`/v1/merchant-events`)**: untrusted callers must not forge orders, inflate reward amounts, or trigger fraudulent reversals. Every merchant event requires `X-Webhook-Timestamp` (replay window ±10 minutes) and constant-time HMAC-SHA256 verification against the server-held merchant secret (`State.MerchantSecrets`). Raw webhook secrets are never returned by `GET /v1/merchants` (`json:"-"` on `Merchant.WebhookSecret`).
2. **Campaign Funding & Cap Enforcement**: deterministic backend code calculates rewards from immutable `CampaignVersion` rules and enforces per-customer caps, campaign budget caps, and reconciled ledger funding coverage inside a serialized database transaction (`SELECT ... FOR UPDATE` in PostgreSQL, mutex + `flock` in `FileStore`). Concurrent requests cannot double-spend campaign capacity.
3. **Beneficiary & Quote Binding**: payouts require an explicit customer-approved `PayoutQuote` bound to an exact `BeneficiaryVersion`, exact eligible reward IDs, exact asset/network/decimals, and expiry. Updating a beneficiary revokes prior versions so queued or quoted payouts can never silently redirect to a newly modified address.
4. **Provider Boundary & Ambiguous Outcomes**: database transactions are never held open across provider calls. Provider timeouts transition attempts to `outcome_unknown`, preserve clearing reservations, open a critical operator exception, and block blind retries until provider status reconciliation completes.
5. **Irreversible Stablecoin Payouts**: post-payout chargebacks and late refunds cannot claw back settled on-chain transfers. The ledger isolates risk via campaign `loss_reserve` accounts and escalates any dispute or reserve shortfall to the operator exception queue.
6. **Tenant Isolation & Operator Controls**: all state, ledger accounts, webhooks, and exceptions are strictly partitioned by `X-Tenant-ID`. Operator endpoints require constant-time comparison against `OPERATOR_TOKEN`. Product-level `issuance_paused` and `payouts_paused` flags allow immediate emergency halt with audit logging.
7. **Precision & Serialization Integrity**: all monetary quantities use checked `int64`/`big.Int` integer math and serialize as decimal strings in JSON; TypeScript uses `BigInt` formatting without floating-point coercion. The optional Rust batch reconciler is strictly read-only with no mutation or signing capability.

## Out of Scope in Sandbox MVP

- Production customer identity/MFA, OAuth/OIDC operator SSO, and HSM/KMS secret rotation.
- Real OFAC/sanctions screening, travel-rule compliance, KYC/KYB verification, and tax reporting (1099/etc.).
- Live custody provider and on-chain signer key management.
