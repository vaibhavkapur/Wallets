# API Reference

[Documentation home](index.md)

The application API is described in [`contracts/openapi.yaml`](../contracts/openapi.yaml); the engine contract in [`contracts/engine-v1.schema.json`](../contracts/engine-v1.schema.json) and [Evaluation Contract](evaluation-contract.md).

All amounts are **decimal strings of integer base units** with an explicit asset (`{"asset": {"code": "USDC", "network": "base", "decimals": 6}, "base_units": "134590000"}`); comparable values are signed **micro-USD strings** (`"97500000"` is $97.50). A JSON number for an amount is rejected. Timestamps are RFC 3339 UTC. Unknown fields are rejected with `400 invalid_request`.

Errors: `{"error": {"code": "<stable_code>", "message": "..."}}`.

Authentication is a bearer API key (`uno_opt_…`) created with `optimizerctl create-key`. Keys are tenant-scoped and carry explicit scopes; recommendation access and execution access are separate:

| Scope | Grants |
|---|---|
| `evaluations:read` | `GET /v1/evaluations/{id}`, `GET /v1/catalog/baskets` |
| `evaluations:write` | `POST /v1/evaluations`, `POST /v1/evaluations/{id}/refresh` |
| `preferences:read`, `preferences:write` | customer preferences |
| `handoffs:write` | `POST /v1/evaluations/{id}/execution-handoffs` |
| `handoffs:read` | `GET /v1/execution-handoffs/{id}` (executor revalidation) |
| `outcomes:write` | `POST /v1/outcomes` |
| `reports:read` | `GET /v1/reports/summary` |

`GET /v1/meta` needs any valid key and returns `{mode, tenant_id, scopes}`. `GET /healthz` is unauthenticated.

## Evaluations

| Method & path | Body | Returns |
|---|---|---|
| `POST /v1/evaluations` | `{customer_id, merchant_id, basket_ref, attribution?}`; optional `Idempotency-Key` header | `201` evaluation; `200` with `Idempotent-Replay: true` on replay |
| `GET /v1/evaluations/{id}` | | evaluation; `status` is `current` or `expired` as of the read |
| `POST /v1/evaluations/{id}/refresh` | optional `Idempotency-Key` | `201` new evaluation with `refreshed_from` set; quotes are re-obtained, the cache is bypassed |

Evaluation:

```json
{
  "id": "eval_…", "customer_id": "cus_1", "merchant_id": "mer_demo_outfitter",
  "basket": {"basket_id": "bsk_…", "basket_hash": "sha256:…", "provider": "recorded", "merchant_name": "…", "basket_ref": "trail-running-shoes", "title": "…", "lines": [...], "synthetic": true},
  "mode": "assisted", "status": "current", "disclosure": "recommendation_only",
  "refreshed_from": null, "preference_policy_id": "pref_…", "input_sha256": "…",
  "versions": {"contract": "1", "engine": "0.1.0", "formula": "net-cost-v1", "rounding": "micro-usd-directed-v1", "policy": "policy-2026-10-02.1", "preference": "pref_…:v1"},
  "warnings": [], "evaluated_at": "…", "expires_at": "…", "created_at": "…",
  "result": { "outcome": "preferred", "rationale": "savings_meet_threshold", "preferred_option_id": "usdc", "tied_option_ids": [], "lowest_cost_option_ids": ["usdc"], "baseline_option_id": "card", "savings_vs_baseline": {...}, "expires_at": "…", "options": [...], "assumptions": [...], "sensitivities": [...] }
}
```

`result` is the engine's response verbatim. `disclosure` is always `recommendation_only`: an evaluation charges nobody and realizes no saving. `warnings` may contain `default_method_not_offered`.

Errors: `404 not_found` (unknown basket or evaluation), `409 idempotency_key_reused`, `502 invalid_quote_data` (the provider's data was rejected by the engine or a reward rule version changed contents), `503 quotes_unavailable`, `503 engine_unavailable`, `503 paused`.

## Preferences

| Method & path | Body | Returns |
|---|---|---|
| `GET /v1/customers/{id}/preferences` | | `{customer_id, policy_id, version, is_default, preferences}` |
| `PUT /v1/customers/{id}/preferences` | preferences (below) | new immutable version; `422 invalid_preferences` on validation failure |

```json
{
  "default_option_id": "card",
  "min_savings_threshold_minor": "100",
  "consented_rails": ["card", "stablecoin"],
  "consent_funding_step": true,
  "allowed_rails": null,
  "require_purchase_protection": false,
  "avoid_credit": false,
  "liquidity_reserves": [{"asset": {"code": "USDC", "network": "base", "decimals": 6}, "base_units": "20000000"}],
  "point_valuations": [{"program_id": "pts_demo", "valuation_version": "v1", "micro_usd_per_point": "10000", "stability": "stable"}]
}
```

The defaults when nothing is stored: no default method, a $1.00 threshold, consent to `card` only, no funding step, no constraints.

## Execution handoffs (assisted mode)

| Method & path | Body | Returns |
|---|---|---|
| `POST /v1/evaluations/{id}/execution-handoffs` | `{option_id, approval: {customer_id, approved_at, approved_amount, reference}}`; `Idempotency-Key` required | `201` handoff; `200` on replay |
| `GET /v1/execution-handoffs/{id}` | | handoff with `executable` and `not_executable_reason` (`status_succeeded`, `status_failed`, `status_unknown`, `expired`, `paused`) recomputed on every read |

Handoff: `{id, evaluation_id, customer_id, merchant_id, basket_hash, option_id, rail, quote_id, amount, approval, status, executable, not_executable_reason, expires_at, created_at, updated_at}`. `status` is `created`, `succeeded`, `failed` or `unknown`. The id is a reference, not a payment credential; the same payload is written to the outbox as `{"version": "1", "type": "execution_handoff.created", "handoff": {...}}`.

Errors (`409` unless noted): `shadow_mode`, `evaluation_not_executable` (evaluated in shadow mode), `evaluation_expired`, `option_not_eligible`, `approval_mismatch` (customer, asset or amount differs from the evaluation's `amount_due`), `approval_out_of_window` (approved before the evaluation or more than 30 s in the future), `prior_attempt_unresolved`, `idempotency_key_reused`, `422 unknown_option`, `400 invalid_request` (missing key or reference), `503 paused`.

## Outcomes

| Method & path | Body | Returns |
|---|---|---|
| `POST /v1/outcomes` | outcome (below) | `201` outcome with `derived`; `200` when the `(source, event_id)` was already recorded with the same payload |

```json
{
  "evaluation_id": "eval_…", "handoff_id": "hand_…",
  "source": "provider", "event_id": "prov-evt-1",
  "type": "payment", "status": "succeeded", "option_id": "usdc", "occurred_at": "…",
  "charged": {"asset": {"code": "USDC", "network": "base", "decimals": 6}, "base_units": "134590000"},
  "refund": null
}
```

- `source` is `provider`, `executor` or `customer_report`. Provider and executor outcomes must reference a handoff; a customer report (shadow mode) must not.
- `type` `payment` with `status` `succeeded` requires `charged`. `type` `refund` requires `refund: {amount, conversion_cost?, initiated_at?, completed_at?}` and a handoff whose payment succeeded.
- A payment outcome moves the handoff from `created`/`unknown` to its status. A contradicting outcome on a settled or failed handoff is `409 conflicting_outcome`; a different method is `409 method_mismatch`; a refund beyond the charge is `409 refund_exceeds_charge`; a replayed event id with different contents is `409 event_id_reused`.

`derived` for a payment: `matches_recommendation`, `charge_difference_base_units` (charged minus estimated `amount_due`, same asset), `estimated_amount_due`, `realized_immediate_savings_micro_usd` (only when `savings_basis` is `realized_vs_estimated_baseline`), `savings_basis` (`recommendation_only`, `payment_not_succeeded`, `method_not_in_evaluation`, `no_baseline`, `baseline_method_used`, `charge_differs_from_estimate`, `realized_vs_estimated_baseline`), `estimated_rewards_micro_usd` and `counterfactual: {baseline_option_id, baseline_immediate_cost_micro_usd, basis: "estimate_at_evaluation", observed: false}`. For a refund: `refund_asset_matches_charge`, `refunded_total_base_units`, `charged_base_units`, `fully_refunded`, `conversion_cost_known`, `seconds_to_complete`.

## Catalog and reports

| Method & path | Returns |
|---|---|
| `GET /v1/catalog/baskets` | `{baskets: [{merchant_id, merchant_name, basket_ref, title, lines, synthetic}]}` |
| `GET /v1/reports/summary?from=&to=` | evaluations (total, by outcome and mode, recommended, recommended non-baseline, exclusion code counts, warning counts), handoffs (by status, chose preferred, chose baseline, unresolved), outcomes (payments by status, refunds, charges differing from the estimate, realized immediate savings and count, estimated rewards, shadow selections and how many followed the recommendation). Default window: the last 30 days. |

## Engine service (internal)

`POST /v1/evaluate` with `Authorization: Bearer <ENGINE_AUTH_TOKEN>` and an evaluation request returns `{input_sha256, result}`; `400 malformed_request` for unparseable input, `422 <request_error_code>` with `field` for invalid input, `401 unauthorized`, `413` above 1 MiB. `GET /v1/versions` returns the contract, engine, formula and rounding versions. The service is not exposed to customers or partners.
