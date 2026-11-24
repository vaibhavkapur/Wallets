# Evaluation Contract (version 1)

[Documentation home](index.md)

The engine's request and response are defined by the Rust types in `engine/crates/optimizer-core/src/contract.rs` and exported as JSON Schema to [`contracts/engine-v1.schema.json`](../contracts/engine-v1.schema.json) (`make schema`). The TypeScript types and the Go structs are derived from and tested against it. Unknown fields are rejected; amounts are canonical decimal strings; JSON numbers for amounts are refused.

## Request

```json
{
  "contract_version": "1",
  "evaluated_at": "2026-10-02T12:00:00Z",
  "basket": {"basket_id": "bsk_…", "merchant_id": "mer_…", "basket_hash": "sha256:…"},
  "policy": {
    "policy_version": "policy-2026-10-02.1", "comparison_currency": "USD",
    "supported_stablecoins": [{"code": "USDC", "network": "base", "decimals": 6}],
    "required_fee_kinds": {"card": ["payment_surcharge", "fx"], "stablecoin": ["network"], "with_funding_step": ["funding", "conversion"]},
    "permitted_funding_sources": ["bank_transfer", "debit_card", "fiat_balance"]
  },
  "preferences": {
    "preference_version": "pref_…:v1", "default_option_id": "card",
    "min_savings_threshold": {"asset": {"code": "USD", "decimals": 2}, "base_units": "100"},
    "hard_constraints": {"allowed_rails": null, "require_purchase_protection": false, "avoid_credit": false},
    "point_valuations": [{"program_id": "pts_demo", "valuation_version": "v1", "micro_usd_per_point": "10000", "stability": "stable"}]
  },
  "options": [ /* PaymentOption, see below */ ]
}
```

A `PaymentOption` has `option_id`, `rail` (`card` | `stablecoin`), `label`, `payment_asset`, a `quote` (`quote_id`, `provider`, `basket_hash`, `delivered_price`, `accepted_asset`, `usd_rate` as `{usd_num, usd_den}` for non-USD assets, `obtained_at`, `valid_until`), `availability` (`method_available`, `jurisdiction` and `merchant_acceptance` as `verified` | `unverified` | `rejected`, `customer_consent`), `fees` (`fee_id`, `kind` ∈ `payment_surcharge` | `fx` | `funding` | `conversion` | `network` | `other`, `status` ∈ `known` | `unknown` | `not_applicable`, `amount` only when known), `discounts` (`discount_id`, `amount`, `included_in_quote`, `immediately_usable`, `eligibility`), `rewards` (`rule_id`, `rule_version`, `eligibility`, `earn` as `{type: "cashback", rate_bps}` or `{type: "points", program_id, milli_points_per_usd}`, optional `cap: {limit, consumed}`), `funds` (`status`, `available`, `reserve`), an optional `funding_step` (`source`, `amount`, `quote_id`, `valid_until`, `customer_consent`) and `tradeoffs` (`purchase_protection` ∈ `yes` | `no` | `unknown`, `uses_credit`, `refund_asset`, `refund_timing`, `settlement`, `notes`).

Limits: 1–16 options; at most 64 fees, discounts or rewards per option; asset decimals ≤ 18; cashback rate ≤ 10,000 bps.

## Response

Top level: `contract_version`, `engine_version`, `formula_version`, `rounding_version`, `policy_version`, `preference_version`, `evaluated_at`, `basket_id`, `outcome` (`preferred` | `tie` | `insufficient_data` | `no_eligible_option`), `rationale` (`default_is_lowest_cost` | `savings_meet_threshold` | `savings_below_threshold` | `lowest_cost_no_default` | `only_eligible_option` | `equal_net_cost` | `options_missing_data` | `no_eligible_option`), `preferred_option_id`, `tied_option_ids`, `lowest_cost_option_ids`, `baseline_option_id`, `savings_vs_baseline` (`immediate_micro_usd`, `estimated_rewards_micro_usd`, `estimated_total_micro_usd`, `threshold_micro_usd`, `meets_threshold`), `expires_at`, `options`, `assumptions`, `sensitivities`.

Each `OptionResult` carries `status` (`eligible` | `excluded`), `exclusions` (`code`, `kind` ∈ `definite` | `missing_data`, `params`), a `breakdown` whenever the amounts could be computed (delivered price, fee lines, discount lines with `applied` and `not_applied_reason`, `immediate_cost_micro_usd`, reward lines with `counted`, `cap_applied`, `points` and `not_counted_reason`, `estimated_rewards_total_micro_usd`, `estimated_net_cost_micro_usd`, `amount_due` in the payment asset), the `funding_step` with its fees, `notes`, and the pass-through `tradeoffs`.

`assumptions`, `sensitivities` and `notes` are `Explanation` components: `{code, option_id, params}` with string parameters. The SDK's `explain()` renders every code; the full lists of `ExclusionCode` and `ExplanationCode` are in the schema and in [Calculation Rules](calculation.md).

## Request errors

A structurally invalid request is rejected as a whole with a `RequestError` `{code, field, message}` where `field` is a JSON-pointer-like path. Codes: `unsupported_contract_version`, `unsupported_comparison_currency`, `invalid_options`, `duplicate_option_id`, `unknown_default_option`, `invalid_threshold`, `invalid_amount`, `invalid_asset`, `invalid_rate`, `invalid_quote`, `invalid_fee`, `conflicting_duplicate_discount`, `invalid_reward`, `invalid_funds`, `invalid_funding_step`, `invalid_point_valuation`, `arithmetic_overflow`.

## Compatibility

`contract_version` must equal `"1"`. Adding an enum variant or a field is a new contract version; the orchestrator refuses a result whose `contract_version` differs from the one it implements. Formula and rounding versions change independently of the contract when the rules in [Calculation Rules](calculation.md) change, so stored evaluations remain attributable to the rules that produced them.
