# API Reference

[Documentation home](index.md)

The application uses `/v1`. Public schemas and most endpoints are generated into [OpenAPI](../contracts/openapi.json); [TypeScript models](../web/src/schema.d.ts) derive from it. These are application endpoints, not ACP provider endpoints.

## Authentication and Requests

Create a sandbox session, then send its `uno_session` cookie. Operator sessions additionally require the configured operator token. Amounts are decimal strings in USD cents, such as `"18294"`.

Mutations require `Content-Type: application/json`, including `{}` requests. Unknown fields and bodies over 16 KiB are rejected. Browser mutations must originate from `APP_ORIGIN`; cross-site requests are rejected. Execution, refund, and order-cancellation requests require an `Idempotency-Key` of 8–128 letters, numbers, underscores, or dashes.

Errors contain `error` and `message`. A `409` commonly means stale approval, invalid state, or idempotency conflict. Refresh and inspect the existing attempt before retrying.

## Customer Endpoints

- `POST /v1/session`: `{}` creates a fixture session; `{ "role": "operator", "token": "..." }` requests operator access.
- `GET /v1/dashboard`: catalog, cards, checkouts, attempts, orders, refunds, audit, budget, and pause status. Customer sessions do not receive operator ledger/job data.
- `POST /v1/purchase-intents`: `product_id`, `variant`, `quantity`, `max_total`, and `postal_code` define an exact request.
- `POST /v1/purchase-intents/{id}/quotes`: creates or returns an active checkout and sandbox offers.
- `GET /v1/checkouts/{id}`: current checkout snapshot.
- `POST /v1/checkouts/{id}/selection`: `version`, `merchant_id`, `card_id`, and `scenario`; invalidates earlier approval.
- `POST /v1/checkouts/{id}/approval`: approve exact `version`; returns a saved approval.
- `POST /v1/checkouts/{id}/execute`: `approval_id` and an idempotency key; reserves budget and queues payment. `202` does not mean payment completed.
- `POST /v1/checkouts/{id}/cancel`: revoke approval and close an unsubmitted checkout.
- `POST /v1/checkouts/{id}/challenge`: `{ "success": true }` or false resolves a sandbox issuer challenge, not real issuer authentication.
- `POST /v1/checkouts/{id}/reconcile`: look up an existing uncertain payment; no new charge.
- `GET /v1/orders/{id}`: receipt with separate payment and fulfillment states.
- `POST /v1/orders/{id}/refund-requests`: `amount`, `reason`, and idempotency key request a partial/full refund.
- `POST /v1/orders/{id}/cancel`: `amount: "0"`, `reason`, and idempotency key. The service computes the remaining eligible refund.
- `POST /v1/cards/{id}/enroll`: enables a predefined card fixture; no card details collected.

## Operator Endpoints

- `POST /v1/operator/pause`: `{ "paused": true }` or false stops/resumes new approvals and execution for the sandbox customer. Recovery and refunds remain available.
- `POST /v1/operator/orders/{id}/advance`: `{}` advances a valid mock order from confirmed to shipped, then shipped to delivered, without changing payment.

## Example: Create and Review

Replace placeholder identifiers with values returned by preceding requests.

```bash
curl -c /tmp/uno-demo.cookies -X POST http://127.0.0.1:8088/v1/session \
  -H 'Content-Type: application/json' -d '{}'

curl -b /tmp/uno-demo.cookies -X POST http://127.0.0.1:8088/v1/purchase-intents \
  -H 'Content-Type: application/json' \
  -d '{"product_id":"studio-pro","variant":"Midnight","quantity":1,"max_total":"20000","postal_code":"94103"}'

curl -b /tmp/uno-demo.cookies -X POST http://127.0.0.1:8088/v1/purchase-intents/{intent_id}/quotes \
  -H 'Content-Type: application/json' -d '{}'

curl -b /tmp/uno-demo.cookies http://127.0.0.1:8088/v1/checkouts/{checkout_id}
```

Review merchant, item, delivery, total, card, and version before approval. Then:

```bash
curl -b /tmp/uno-demo.cookies -X POST http://127.0.0.1:8088/v1/checkouts/{checkout_id}/approval \
  -H 'Content-Type: application/json' -d '{"version":1}'

curl -b /tmp/uno-demo.cookies -X POST http://127.0.0.1:8088/v1/checkouts/{checkout_id}/execute \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: example-purchase-001' \
  -d '{"approval_id":"approval_replace_me"}'
```

Use the actual version and approval ID. Keep the worker running; retrieve `order_id` after completion and read its receipt. Reuse the key only for an identical retry. Unknown payment outcomes block another attempt.

## Signed Sandbox Webhooks

`POST /v1/webhooks/sandbox` accepts `{ "id": "evt_unique", "tenant": "sandbox-customer", "checkout_id": "checkout_..." }`.

Send `X-Webhook-Timestamp` as Unix seconds and `X-Webhook-Signature` as hex HMAC-SHA256 of `<timestamp>.<exact raw JSON body>` using `WEBHOOK_SECRET`. Events more than five minutes old or one minute in the future are rejected; event IDs are deduplicated.

A verified event only schedules authoritative lookup; its payload cannot declare success or supply an amount. This webhook contract is documented here rather than generated into OpenAPI.
