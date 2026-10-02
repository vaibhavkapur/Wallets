# Card-versus-Stablecoin Payment Optimizer

Helps a customer choose between an eligible card and a stablecoin payment for the same basket by comparing estimated net cost — delivered price plus incremental fees, minus immediately usable discounts, minus conservatively valued rewards — and explaining why one wins, what was assumed, and what could change the result. Unknown fees and unverified eligibility are never treated as zero; purchase protection, credit use, refund terms and settlement are shown as trade-offs, never priced. A customer-approved choice can be handed to an execution service as a constrained reference, and outcomes are measured with realized savings kept separate from estimates.

> **[Read the full documentation](docs/index.md)**

Built with Rust (pure calculation engine and internal service), Go and PostgreSQL (orchestration, persistence, handoffs, outcomes), and TypeScript (generated SDK and React comparison interface). All quotes are synthetic and replayed by a recorded provider; no merchant, card network or stablecoin network is contacted and no funds move.

## Getting Started

```bash
# Requires Go 1.27+, Rust via rustup, Node.js 20+, Python 3.9+, curl
make install     # npm workspaces
make test        # Rust unit/golden/property tests, independent reference check, Go integration tests on the real engine + embedded PostgreSQL, TypeScript
make up          # embedded PostgreSQL, engine and API on :8080 in assisted mode (make up-shadow for shadow mode)
make e2e         # end-to-end scenario: evaluation, approval handoff, outcomes, report
make web         # comparison interface at http://localhost:5173
make down
```

See [Getting Started](docs/getting-started.md) for prerequisites, the local stack, the SDK and operator tasks.

## Quick Example

```bash
API=http://127.0.0.1:8080; KEY=$(cat .dev/api-key)

# Customer preferences: default method, saving threshold, consent, constraints, point valuation
curl -X PUT $API/v1/customers/cus_1/preferences -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "default_option_id": "card", "min_savings_threshold_minor": "100",
  "consented_rails": ["card", "stablecoin"], "consent_funding_step": true, "allowed_rails": null,
  "require_purchase_protection": false, "avoid_credit": false, "liquidity_reserves": [],
  "point_valuations": [{"program_id": "pts_demo", "valuation_version": "v1", "micro_usd_per_point": "10000", "stability": "stable"}]}'

# Evaluate a basket against current quotes; the result is a recommendation, not a payment
curl -X POST $API/v1/evaluations -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: shoes-1' \
  -d '{"customer_id": "cus_1", "merchant_id": "mer_demo_outfitter", "basket_ref": "trail-running-shoes"}'

# Hand off the customer's approved choice at exactly the evaluated amount due (assisted mode)
curl -X POST $API/v1/evaluations/$EVAL_ID/execution-handoffs -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -H 'Idempotency-Key: shoes-1-approval' -d '{
  "option_id": "usdc",
  "approval": {"customer_id": "cus_1", "approved_at": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'", "reference": "app-approval-1",
               "approved_amount": {"asset": {"code": "USDC", "network": "base", "decimals": 6}, "base_units": "134590000"}}}'

# Record what the provider observed; realized savings are measured against the recorded baseline estimate
curl -X POST $API/v1/outcomes -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' -d '{
  "evaluation_id": "'$EVAL_ID'", "handoff_id": "'$HANDOFF_ID'", "source": "provider", "event_id": "prov-1",
  "type": "payment", "status": "succeeded", "option_id": "usdc", "occurred_at": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
  "charged": {"asset": {"code": "USDC", "network": "base", "decimals": 6}, "base_units": "134590000"}}'

curl $API/v1/reports/summary -H "Authorization: Bearer $KEY"
```

Replace `$EVAL_ID` and `$HANDOFF_ID` with values from the preceding responses. Amounts are base-unit decimal strings with an explicit asset; comparable values are micro-USD strings (`"97500000"` is $97.50). For the trail-shoes basket the response prefers USDC with an immediate saving of $3.85, an estimated rewards difference of −$2.7688 and an estimated total of $1.0812 against the $1.00 threshold. See the [API Reference](docs/api-reference.md), [Calculation Rules](docs/calculation.md) and [Demo Script](docs/demo-script.md).
