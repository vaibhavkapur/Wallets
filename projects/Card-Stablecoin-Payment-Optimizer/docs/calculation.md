# Calculation Rules

[Documentation home](index.md)

Formula `net-cost-v1`, rounding `micro-usd-directed-v1`. This is the normative description of what the engine computes. The Rust
crate `engine/crates/optimizer-core` implements it; `fixtures/tools/independent_check.py`
is a second implementation used only to check the golden vectors.

## Units

- Inputs are integer base units of an explicit asset (`code`, optional
  `network`, `decimals`), serialized as canonical decimal strings. JSON
  numbers are rejected.
- All comparable values are **micro-USD** (1 USD = 1,000,000), signed, in
  checked `i128` arithmetic. Any component above 10^24 USD is out of range.
- Conversions: USD cents × 10,000. A stablecoin amount `a` with `d` decimals at
  rate `num/den` USD per whole unit is `a × num × 10^6 / (den × 10^d)`.
- **Directed rounding**: amounts that increase the customer's cost (price,
  fees, amount due) round **up**; amounts that reduce it (discounts, rewards,
  points) round **down**. An estimate never understates what the customer pays.

## Per option

```
immediate cost       = delivered price + Σ known fees − Σ applied discounts
reward base          = delivered price − Σ applied discounts          (fees excluded)
estimated rewards    = Σ counted rewards (cashback or valued points)
estimated net cost   = immediate cost − estimated rewards
amount due (asset)   = (delivered price − Σ applied discounts + Σ fees charged to the method) in the payment asset, rounded up
```

Fees charged to the method are those denominated in the payment asset,
excluding `funding` and `conversion` fees (they belong to the separate step).

### Discounts
A discount is applied only if it is **not** already in the quote, is
`verified`, is `immediately_usable`, and can be valued. Duplicates by id are
counted once; conflicting duplicates reject the request. Applied discounts
never exceed the delivered price.

### Rewards
- Unverified rules count as zero.
- Cashback: `floor(base × bps / 10,000)`, then capped by remaining cap
  (`limit − consumed`, in cents). An exhausted cap counts as zero.
- Points: `floor(base_micro × milli_points_per_usd / 10^9)` points, capped by
  remaining points, valued at the customer's stable valuation. A missing or
  `unstable` valuation counts the points at zero and reports a sensitivity
  with the hypothetical value.

## Exclusions

An option is excluded (never priced as zero or guessed) when any of these holds.
`missing_data` exclusions make the whole evaluation `insufficient_data`:

| code | kind |
|---|---|
| method_unavailable, unsupported_jurisdiction, merchant_does_not_accept, not_consented | definite |
| rail_not_allowed, purchase_protection_required, credit_not_allowed | definite |
| incompatible_asset, funding_source_not_permitted, funding_not_consented, insufficient_funds | definite |
| jurisdiction_unverified, merchant_acceptance_unverified, stale_quote, basket_mismatch | missing data |
| rate_unavailable, unknown_required_fee, amount_out_of_range, funds_unverified | missing data |

- A quote is stale when `valid_until <= evaluated_at` (exclusive).
- Every required fee kind for the rail (from policy) must be `known` or
  `not_applicable`; any `unknown` fee, or a fee in an asset that cannot be
  valued, excludes the option.
- Funds: `spendable = max(available − reserve, 0) + funding step amount`;
  the option is excluded when `spendable < amount due`. Unknown funds exclude.

## Decision

Among eligible options, by estimated net cost:

1. One eligible option → `preferred`, `only_eligible_option`.
2. The default (baseline) is among the lowest: unique → `preferred`
   (`default_is_lowest_cost`); otherwise `tie`.
3. Default eligible but not lowest: if `cost(default) − lowest < threshold`
   → keep the default (`savings_below_threshold`); else unique lowest →
   `savings_meet_threshold`, otherwise `tie`.
4. No default: unique lowest → `lowest_cost_no_default`, otherwise `tie`.

Exactly equal costs are a tie, never an arbitrary pick. The threshold is
inclusive.

## Explanations

The response carries machine-readable components: per-option `exclusions` and
`notes`, global `assumptions` (rate used, point valuation used, reward base
excludes fees, amount due is an upper bound) and `sensitivities`
(quote expiry, margin to the runner-up, whether the result depends on
estimated rewards, unstable valuations excluded, savings below threshold,
excluded options that could change the result). Savings versus the baseline
report the immediate difference and the estimated rewards difference
separately.
