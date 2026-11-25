# API Reference

[Documentation home](index.md)

The OpenAPI 3.1 specification is generated at [`contracts/openapi.json`](../contracts/openapi.json) via `make contracts`. All authenticated routes require a signed `uno_recurring_session` cookie issued by `POST /v1/session`. Monetary amounts are always serialized as decimal-string minor units (`"amount_minor": "2502"`).

## Health, Session, and Catalog

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `GET` | `/healthz` | Public | Health status, runtime mode (`sandbox`/`production`), and current UTC clock. |
| `POST` | `/v1/session` | Public | Opens a tenant-isolated session cookie (`role`: `"customer"` or `"operator"` with `operator_token`). |
| `GET` | `/v1/catalog` | Public | Narrow specialty coffee and household catalog plus scoped payment methods. |
| `GET` | `/v1/workspace` | Session | Tenant aggregate view: rules, occurrences, consents, budget summaries, outbox notifications, audits, global pause, and sandbox faults. |

## Recurring Rules and Controls

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/recurring-rules` | Create rule `v1` with schedule (`calendar` or `interval`), exact product/pack constraints, substitution policy, and approval mode (`C1`–`C3`). |
| `PATCH` | `/v1/recurring-rules/{id}` | Create a new immutable `RecurringRuleVersion`, recompute `next_run_at_utc`, and supersede pending proposals from older versions. |
| `POST` | `/v1/recurring-rules/{id}/pause` | Pause rule to block future occurrences. Returns `already_submitted_orders` so callers distinguish pausing future runs from canceling submitted orders. |
| `POST` | `/v1/recurring-rules/{id}/resume` | Resume a paused rule with `next_run_at_utc` computed strictly in the future (no catch-up backlog). |
| `POST` | `/v1/recurring-rules/{id}/cancel` | Permanently cancel a rule and close pending unapproved proposals. |
| `POST` | `/v1/recurring-rules/{id}/skip-next` | Mark the single upcoming schedule slot `skipped` and advance `next_run_at_utc`. |
| `GET` | `/v1/recurring-rules/{id}/occurrences` | List past and upcoming occurrences with linked quotes, proposals, consents, reservations, and checkout state. |
| `POST` | `/v1/recurring-rules/{id}/mandates` | Grant a scoped C3 `delegated_mandate` `ConsentRecord` bound to current rule version, SKU, merchant, max amount, and expiry. |
| `POST` | `/v1/consents/{id}/revoke` | Immediately revoke a consent or delegated mandate. |

## Proposals, Occurrences, and Reconciliation

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/proposals/{id}/approve` | Approve an exact current proposal (`expected_quote_digest`, `expected_total_minor`, `accept_substitution`), write `ConsentRecord`, reserve budget, and submit checkout. |
| `POST` | `/v1/proposals/{id}/reject` | Decline a pending proposal without purchasing. |
| `GET` | `/v1/occurrences/{id}` | Return full scheduling, quote, proposal, consent, budget reservation, payment, and fulfillment details. |
| `POST` | `/v1/occurrences/{id}/reconcile` | Reconcile a `needs_reconciliation` (`timeout_unknown`) occurrence by original idempotency key without creating a duplicate charge. |
| `POST` | `/v1/occurrences/{id}/cancel-order` | Attempt merchant cancellation and refund for an already-submitted order (distinct from pausing the rule). |

## Webhooks, Operator Controls, and Sandbox Endpoints

| Method | Path | Auth | Description |
| --- | --- | --- | --- |
| `POST` | `/v1/webhooks/merchant` | HMAC (`X-Uno-Signature`) | Verified fulfillment (`shipped`, `delivered`, `delayed`, `canceled`) and partial/full refund updates. |
| `POST` | `/v1/operator/pause` | Operator session | Toggle product-level emergency pause (`{"paused": true, "reason": "..."}`). Returns `403` for customer sessions. |
| `POST` | `/v1/sandbox/tick` | Session (`DEMO_MODE=true`) | Advance fake clock (`advance_minutes` or `set_time_utc`) and execute one worker recovery scan. |
| `PUT` | `/v1/sandbox/faults` | Session (`DEMO_MODE=true`) | Configure price multiplier, pack-size change, stock status, substitute SKU, payment fault, or C3 pilot gate. |
| `POST` | `/v1/sandbox/fulfill/{id}` | Session (`DEMO_MODE=true`) | Simulate merchant delivery or partial refund webhook for an occurrence. |

## Example Request: Create Rule

```bash
curl -c cookies.txt -X POST http://127.0.0.1:8090/v1/session \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"household-demo","role":"customer"}'

curl -b cookies.txt -X POST http://127.0.0.1:8090/v1/recurring-rules \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Morning Ethiopia Coffee",
    "approval_mode": "C2_ASSISTED_REORDER",
    "schedule": {
      "kind": "interval",
      "timezone": "America/New_York",
      "dst_policy": "shift_forward_first",
      "leap_day_policy": "clamp_month_end",
      "local_hour": 9,
      "local_minute": 0,
      "interval_days": 14,
      "min_interval_days": 10,
      "proposal_ttl_minutes": 1440
    },
    "product": {
      "sku": "COFFEE-ETHIOPIA-12OZ",
      "pack_size_value": 12,
      "pack_size_unit": "oz",
      "quantity": 1,
      "preferred_merchant_id": "roaster_direct",
      "max_delivered_price": {"amount_minor":"3000","asset":"USD","network":"fiat","decimals":2},
      "monthly_budget": {"amount_minor":"7500","asset":"USD","network":"fiat","decimals":2},
      "substitution_policy": "approval_required",
      "payment_method_id": "pm_card_everyday",
      "shipping_postal_code": "10001"
    }
  }'
```
