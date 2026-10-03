# API reference

Base path `/v1`. JSON bodies and responses (`snake_case`, RFC 3339 timestamps). Authenticated endpoints require `Authorization: Bearer ses_…`. Every response carries `Cache-Control: no-store`.

Errors have the shape `{"error":{"code":"…","message":"…"}}`. Common codes: `unauthorized` (401), `not_found` (404; also used for records owned by someone else), `invalid_request` / `invalid_json` (400), `provider_unavailable` (502), `internal` (500).

## Health

`GET /healthz` → `{"status":"ok","provider":"stripe|fake"}` (503 if the database is unreachable).

## Auth

### `POST /v1/auth/apple`
Body: `{"identity_token": "<Apple JWT>", "nonce": "<raw nonce used in the request>"}`.
Verifies the token (signature, issuer, audience, expiry, nonce), finds or creates the user by Apple subject, and issues a session.
- 201 (new account) / 200 (returning): `{"session_token":"ses_…","expires_at":…,"user":{"id","email","status","created_at"}}`
- 401 `invalid_identity`; 409 `account_deleting`; 502 `identity_unavailable`.

### `POST /v1/auth/sign-out` → 204. Revokes the current session.
### `GET /v1/me` → user.

## Card setups

### `POST /v1/card-setups`
Headers: `Idempotency-Key: <1–128 chars>` (required; scoped to the user).
Body: `{"consent":{"accepted":true,"version":"2026-10"}}` (required).
Starts one enrollment attempt, or replays the attempt previously created with the same key.
- 201 (new) / 200 (replay):
  ```json
  {"id":"setup_…","status":"collecting","failure_code":null,"card":null,
   "client_session":{"provider":"stripe","setup_client_secret":"seti_…_secret_…","customer_ref":"cus_…",
                     "ephemeral_key_secret":"ek_…","ephemeral_key_expires_at":"…","publishable_key":"pk_…"},
   "created_at":"…","updated_at":"…"}
  ```
  `client_session` is present only on this response while the attempt is open; it is never returned by `GET` and never logged. A replay of a terminal attempt returns its final state without a session.
- 400 `idempotency_key_required`, `consent_required`; 409 `account_inactive`; 502 `provider_unavailable` (the attempt is marked `failed`; retry with a new key).

### `GET /v1/card-setups/{id}`
Returns the server-verified status, refreshing from the provider while the attempt is open. Statuses: `collecting`, `authentication_required`, `processing`, `saved`, `failed`, `canceled`. `failure_code` carries the provider decline code or one of `provider_unavailable`, `customer_mismatch`, `setup_missing`, `expired`, `client_reported_failure`. `card` is populated once `saved`.

### `POST /v1/card-setups/{id}/close`
Body: `{"outcome":"canceled"|"failed"}` (default `canceled`). Called when the native sheet is dismissed. The provider is consulted first: a setup that already succeeded is saved rather than canceled. Otherwise the provider setup is canceled and the attempt becomes `canceled` or `failed`.

## Cards

### `GET /v1/cards` → `{"cards":[…]}` — the user's cards that are not removed, preferred first, then newest first.

Card shape:
```json
{"id":"card_…","brand":"visa","last4":"4242","exp_month":12,"exp_year":2030,"nickname":null,
 "is_preferred":true,"state":"saved|removal_pending|removed","created_at":"…","updated_at":"…"}
```

### `GET /v1/cards/{id}` → card.

### `PATCH /v1/cards/{id}`
Body: any of `{"nickname":"Everyday"}` (≤ 40 characters, trimmed; `null` or empty clears), `{"is_preferred":true}` (clears the flag on other cards atomically). Unknown fields → 400.

### `DELETE /v1/cards/{id}`
Starts provider removal. 200 with `state: "removed"` when the provider confirmed inline; 202 with `state: "removal_pending"` when it did not (the worker retries). Repeating the call is idempotent.

## Account

### `DELETE /v1/account`
Revokes all sessions, marks cards for removal, and schedules provider cleanup. 200 `{"status":"deleted"}` when cleanup finished inline; 202 `{"status":"deleting"}` when it is pending. Signing in with the same Apple ID while `deleting` returns 409; after completion it creates a new account.

## Webhooks

### `POST /v1/webhooks/provider`
Raw provider payload with `Stripe-Signature`. 400 `invalid_signature` on failure; 200 `{"status":"processed"|"duplicate"}`; 500 `retry` on a transient handler error (the event is forgotten so the retry is processed). Handled types: `setup_intent.succeeded|setup_failed|canceled|requires_action`, `payment_method.detached`, `payment_method.automatically_updated|updated`. Others are acknowledged.

## Development-only (PROVIDER=fake)

`POST /dev/card-setups/{id}/complete` `{"brand","last4","exp_month","exp_year"}` (defaults to a Visa fixture) and `POST /dev/card-setups/{id}/outcome` `{"outcome":"requires_action"|"processing"|"declined"|"canceled","code"}` simulate the cardholder and provider. They require the owner's session and do not exist when a real provider is configured.
