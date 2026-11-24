# Demo script

[Documentation home](index.md)

Preparation: `make up`, then `make web` in a second terminal and open `http://localhost:5173`. The shell steps use `API=http://127.0.0.1:8080` and `KEY=$(cat .dev/api-key)` as in the README's Quick Example. Every basket is synthetic; no money moves.

## Demo A — the plan's example, with explanations (≈2 min)

1. In the interface, keep customer `cus_demo`, tick *Stablecoin (USDC on Base)* under consent, set the default to `card` and the threshold to `1.00`, and save. The preference version appears next to the form title.
2. Choose *Demo Outfitter: Trail running shoes* and click *Compare payment methods*. The banner reads *Recommended: USDC on Base* with the rationale "saves at least your threshold". The savings panel shows the immediate difference (+$3.85), the estimated rewards difference (−$2.7688, labelled *estimate*) and the estimated total (+$1.0812) against the $1.00 threshold.
3. Each card shows the delivered price, fee lines with their source amounts, discount lines, the bold *Immediate cost*, reward lines tagged *estimated reward*, the *Estimated net cost*, and the exact amount charged to the method (`134.59 USDC (base)` versus `$138.44`).
4. *What could change this result* lists the quote expiry and the margin to the runner-up; *Assumptions used* lists the USDC rate used and that rewards exclude fees. The footer carries the evaluation id and every version plus the input hash.

## Demo B — thresholds, constraints and estimates (≈2 min)

1. Raise the threshold to `2.00` and save, then compare again: the card is kept with "a cheaper method exists, but the saving is below your threshold"; the stablecoin card still shows its lower cost and the sensitivity "estimated saving of $1.0812 is below your $2.00 threshold".
2. Choose *Headlamp and batteries*: USDC needs a separate quoted top-up (shown as a blue funding-step banner with the bank-transfer fees). The card wins by $0.71 only because of 129 estimated points; the sensitivity says the result depends on estimated rewards and would favour USDC on immediate costs alone.
3. Tick *This valuation is unstable* and save: the points are now shown but valued at zero and USDC becomes the lowest-cost option.
4. Tick *Require purchase protection*: USDC is excluded as "you require purchase protection, and this method does not offer it" with the *not eligible* tag; the exclusion is a constraint, never a price.
5. Enter a USDC liquidity reserve of `200` and compare *Trail running shoes*: USDC is excluded for insufficient spendable balance.

## Demo C — missing data is not zero (≈1 min)

1. Choose *Demo Pantry: Pantry restock*. The banner reads *Not enough data to compare*: the stablecoin network fee could not be quoted, the option is excluded as *missing data*, and the card's cost is shown without a recommendation.
2. Choose *Gravity water filter*: the merchant does not accept stablecoin for this basket, a definite exclusion, so the card is "only one method is eligible".

## Demo D — approval, handoff and outcome (≈2 min, assisted mode)

1. Compare *Trail running shoes* again and click *Approve 134.59 USDC (base) with USDC on Base*. A green banner shows the handoff reference, status `created` and "executable until …", and notes that the reference is not a payment credential.
2. Try approving the card in a second evaluation of the same basket (via the API, `Idempotency-Key` required): `409 prior_attempt_unresolved`.
3. Post the provider's outcome:

```bash
curl -X POST $API/v1/outcomes -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "evaluation_id": "'$EVAL_ID'", "handoff_id": "'$HANDOFF_ID'", "source": "provider", "event_id": "demo-pay-1",
  "type": "payment", "status": "succeeded", "option_id": "usdc", "occurred_at": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
  "charged": {"asset": {"code": "USDC", "network": "base", "decimals": 6}, "base_units": "134590000"}}'
```

   `derived.savings_basis` is `realized_vs_estimated_baseline` with `realized_immediate_savings_micro_usd: "3850000"`, `estimated_rewards_micro_usd` separately, and a counterfactual marked `observed: false`.
4. `GET $API/v1/reports/summary` shows one settled payment, one realized saving, `chose_preferred: 1`, and the estimated rewards total in its own field.

## Extras

- `POST /v1/evaluations/{id}/refresh` from *Refresh quotes* creates a linked evaluation with new quote validity.
- `make offline-eval` prints milestone O1: six recorded baskets, five recommendations, one `insufficient_data`.
- `.dev/bin/optimizerctl pause -scope ten_dev -reason demo` (with `DATABASE_URL=postgres://optimizer:optimizer@127.0.0.1:54329/optimizer?sslmode=disable`) makes *Compare* answer `503 paused` and any open handoff read as not executable; `resume` restores it.
