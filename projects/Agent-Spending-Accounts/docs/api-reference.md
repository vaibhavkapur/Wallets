# API Reference

[Documentation home](index.md)

All amounts are **decimal strings of base units** (`"1000000"` = 1 USDC at 6 decimals). JSON numbers for amounts are rejected with `400 invalid_request`. Timestamps are RFC 3339 UTC.

Errors: `{"error": {"code": "<stable_code>", "message": "...", "details": {...}}}`. Policy denials are `403` with one of the codes listed under [Denial codes](#denial-codes).

Authentication is a bearer token:

| Prefix | Who | Where |
|---|---|---|
| `owner_…` | account owner | control plane owner endpoints |
| `agent_…` | software agent | control plane agent endpoints, gateway |
| operator token | operator (`OPERATOR_TOKEN`) | `POST /v1/organizations` |
| internal token | Rust gateway only (`INTERNAL_TOKEN`) | `/internal/v1/*` |

## Control plane — owner endpoints (`owner_` key)

| Method & path | Body | Returns |
|---|---|---|
| `POST /v1/organizations` *(operator)* | `{name}` | `{organization, owner_api_key}` — key shown once |
| `GET /v1/organization` | | organization |
| `POST /v1/organization/pause` / `resume` | | organization. Pause blocks all new spending; in-flight payments stay tracked |
| `POST /v1/agents` | `{name}` | `{agent, agent_api_key}` — key shown once |
| `GET /v1/agents`, `GET /v1/agents/{id}` | | agents |
| `POST /v1/agents/{id}/revoke` | | agent. Credential stops working immediately |
| `PUT /v1/agents/{id}/policy` | policy (below) | new immutable `BudgetPolicyVersion`; becomes the agent's active policy. Tasks created under earlier versions can no longer spend |
| `GET /v1/agents/{id}/policy`, `GET /v1/agents/{id}/policy/versions` | | current / history |
| `POST /v1/funding/deposits`, `/withdrawals` | `{asset, network, amount, reference}` | balance. Withdrawals cannot touch reserved funds (`409`) |
| `GET /v1/balances` | | `{balances: [{asset, network, available, reserved}]}` |
| `GET /v1/ledger?limit=` | | `{entries}` double-entry legs |
| `GET /v1/tasks?agent_id=&limit=` | | `{tasks}` |
| `POST /v1/tasks/{id}/close` | | task |
| `GET /v1/payment-attempts?task_id=&limit=` | | `{payment_attempts}` |
| `GET /v1/receipts?task_id=&limit=` | | `{receipts}` |
| `GET /v1/audit?limit=` | | `{events}` |

Policy body:

```json
{
  "asset": {"asset": "0x036C…", "network": "base-sepolia", "decimals": 6, "scheme": "exact"},
  "per_request_max": "500000", "per_task_max": "5000000",
  "agent_daily_max": "20000000", "org_daily_max": "100000000",
  "fee_allowance": "0",
  "endpoints": [{"url_prefix": "https://api.provider.example/v1", "methods": ["GET","POST"], "recipients": ["0x2096…"]}],
  "max_task_seconds": 86400, "grant_ttl_seconds": 60
}
```

Validation: `per_request_max ≤ per_task_max ≤ agent_daily_max ≤ org_daily_max`; every endpoint needs ≥ 1 recipient; prefixes are absolute http(s) URLs without query/fragment/userinfo; `grant_ttl_seconds ∈ [1,300]`.

## Control plane — agent endpoints (`agent_` key)

| Method & path | Body | Returns |
|---|---|---|
| `POST /v1/tasks` | `{description?, budget?, duration_seconds?}` | task. Budget and deadline are **capped** by policy; agents cannot widen them |
| `GET /v1/tasks/{id}` | | task (own tasks only) |
| `GET /v1/tasks/{id}/budget` | | `{task_budget, held, committed, remaining, per_request_max, fee_allowance, spendable, asset}` |
| `GET /v1/tasks/{id}/receipts` | | `{receipts}` |
| `GET /v1/payment-attempts/{id}` | | attempt (own only) |

## Gateway — agent endpoint (`agent_` key)

`POST /v1/tasks/{task_id}/paid-requests`

```json
{
  "idempotency_key": "task-123:weather:lisbon",
  "method": "GET",
  "url": "https://api.provider.example/v1/weather?city=Lisbon",
  "headers": {"accept": "application/json"},
  "body": "optional utf-8 body",            // or "body_base64"
  "max_amount": "200000"                     // optional ceiling below policy
}
```

Response `200`:

```json
{
  "attempt_id": "pay_…",
  "replay": false,
  "payment": {"status": "settled", "amount": "100000", "asset": "0x036C…", "network": "base-sepolia", "pay_to": "0x2096…", "transaction": "0x…", "payer": "0x…"},
  "service": {"http_status": 200, "delivered": true, "content_type": "application/json", "body_bytes": 123, "truncated": false, "body": "{…}"},
  "receipt": { … }
}
```

`payment.status` values: `settled`, `failed`, `unknown` (reservation held; poll `GET /v1/payment-attempts/{id}`), `not_required` (resource answered 2xx without a 402), `not_attempted` (resource errored before any payment). Replays (`replay: true`) return the original attempt's state and never pay again.

Gateway-specific error codes (`403` unless noted): `url_rejected`, `destination_rejected` (SSRF/DNS), `endpoint_not_allowed`, `challenge_rejected` (beneficiary/asset/network/scheme/amount), `challenge_malformed` (`502`), `redirect_refused` (`502`), `resource_unreachable` (`502`), `signer_unavailable` (`502`), `grant_invalid` (`500`), `control_plane_unavailable` (`503`), plus any control-plane denial code.

## Internal — gateway ↔ control plane (`INTERNAL_TOKEN`)

| Method & path | Purpose |
|---|---|
| `GET /internal/v1/grant-public-key` | `{kid, alg: "EdDSA", public_key_base64}` |
| `POST /internal/v1/paid-requests/begin` | `{agent_key, task_id, idempotency_key, method, url}` → `201 {attempt, context}` or `200 {replay: true, attempt, receipt}`; `403` denial (attempt recorded as rejected) |
| `POST /internal/v1/paid-requests/{id}/authorize` | `{quote}` → `201 {grant_token, grant_id, reservation_id, expires_at, amount_max, fee_max}`; `403` denial; `400 requirements_hash_mismatch` |
| `POST /internal/v1/paid-requests/{id}/abandon` | `{code, detail}` — close an initiated attempt that will not be paid |
| `POST /internal/v1/paid-requests/{id}/submitted` | `{grant_id, payment_hash}` — **write-ahead**; consumes the grant (`409` on replay/expiry, `403` on grant mismatch) |
| `POST /internal/v1/paid-requests/{id}/outcome` | `{payment: {status, submission_ref, payer_ref, settled_amount, fee_actual, error}, service: {…}}` → `{attempt, receipt}` |

Outcome transition rules (enforced in `store.ResolveAttempt`): `settled` requires prior `submitted` and `settled_amount ≤ reserved`, `fee_actual ≤ fee_allowance`; `unknown` requires prior `submitted`; `failed`/`expired` release the hold; terminal states are immutable.

## Denial codes

`org_paused`, `agent_revoked`, `task_closed` (also used when a task's policy version was superseded), `task_deadline_passed`, `task_not_owned_by_agent`, `url_invalid`, `endpoint_not_allowed`, `method_not_allowed`, `recipient_not_approved`, `wrong_asset`, `wrong_network`, `unsupported_scheme`, `resource_mismatch`, `per_request_limit_exceeded`, `task_budget_exceeded`, `agent_daily_limit_exceeded`, `org_daily_limit_exceeded`, `insufficient_balance`, `amount_overflow`, `quote_timeout_invalid`.
