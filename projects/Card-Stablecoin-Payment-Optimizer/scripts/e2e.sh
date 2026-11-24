#!/usr/bin/env bash
# End-to-end scenario against a running stack in assisted mode (make up).
# Exercises evaluation, preferences, idempotency, insufficient data, approval
# binding, one-unresolved-attempt, outcomes, refunds, and the report. Exits
# non-zero on any failed check.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
API="${OPTIMIZER_API_URL:-http://127.0.0.1:8080}"
KEY="${OPTIMIZER_API_KEY:-$(cat "$ROOT/.dev/api-key" 2>/dev/null)}"
if [ -z "$KEY" ]; then echo "no API key: set OPTIMIZER_API_KEY or run make up" >&2; exit 2; fi
RUN="$(date -u +%Y%m%d%H%M%S)-$$"
CUS="cus_e2e_$RUN"
passed=0; failed=0
ok()   { passed=$((passed+1)); echo "  ✓ $1"; }
bad()  { failed=$((failed+1)); echo "  ✗ $1 — $2"; }
check(){ if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "got '$2', want '$3'"; fi; }

req() { # method path [body] [extra header]
  local method=$1 path=$2 body=${3:-} header=${4:-}
  local args=(-s -o /tmp/e2e-body.$$ -w '%{http_code}' -X "$method" -H "Authorization: Bearer $KEY" -H "Content-Type: application/json")
  [ -n "$header" ] && args+=(-H "$header")
  [ -n "$body" ] && args+=(--data-binary "$body")
  STATUS=$(curl "${args[@]}" "$API$path"); BODY=$(cat /tmp/e2e-body.$$)
}
jget() { python3 -c "
import json,sys
v=json.loads(sys.argv[1])
for k in sys.argv[2].split('.'):
    if isinstance(v,list): v=v[int(k)]
    else: v=v.get(k) if isinstance(v,dict) else None
print('null' if v is None else (json.dumps(v) if isinstance(v,(dict,list)) else ('true' if v is True else 'false' if v is False else v)))" "$1" "$2"; }
option_due() { python3 -c "
import json,sys; e=json.loads(sys.argv[1])
print(json.dumps([o for o in e['result']['options'] if o['option_id']==sys.argv[2]][0]['breakdown']['amount_due']))" "$1" "$2"; }
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

echo "evaluation and preferences"
req GET /v1/meta; check "meta reports assisted mode" "$(jget "$BODY" mode)" assisted
req PUT "/v1/customers/$CUS/preferences" '{"default_option_id":"card","min_savings_threshold_minor":"100","consented_rails":["card","stablecoin"],"consent_funding_step":true,"allowed_rails":null,"require_purchase_protection":false,"avoid_credit":false,"liquidity_reserves":[],"point_valuations":[{"program_id":"pts_demo","valuation_version":"e2e","micro_usd_per_point":"10000","stability":"stable"}]}'
check "preferences stored as version 1" "$STATUS/$(jget "$BODY" version)" "200/1"
req POST /v1/evaluations "{\"customer_id\":\"$CUS\",\"merchant_id\":\"mer_demo_outfitter\",\"basket_ref\":\"trail-running-shoes\"}" "Idempotency-Key: e2e-$RUN-1"
EV="$BODY"; EVID=$(jget "$EV" id)
check "trail shoes: stablecoin preferred above threshold" "$STATUS/$(jget "$EV" result.preferred_option_id)/$(jget "$EV" result.rationale)" "201/usdc/savings_meet_threshold"
check "savings reported separately: immediate +3.85, rewards -2.7688, total +1.0812" "$(jget "$EV" result.savings_vs_baseline.immediate_micro_usd)/$(jget "$EV" result.savings_vs_baseline.estimated_rewards_micro_usd)/$(jget "$EV" result.savings_vs_baseline.estimated_total_micro_usd)" "3850000/-2768800/1081200"
check "evaluation is labelled recommendation only" "$(jget "$EV" disclosure)" recommendation_only
req POST /v1/evaluations "{\"customer_id\":\"$CUS\",\"merchant_id\":\"mer_demo_outfitter\",\"basket_ref\":\"trail-running-shoes\"}" "Idempotency-Key: e2e-$RUN-1"
check "idempotent replay returns the same evaluation" "$STATUS/$(jget "$BODY" id)" "200/$EVID"
req POST /v1/evaluations "{\"customer_id\":\"$CUS\",\"merchant_id\":\"mer_demo_outfitter\",\"basket_ref\":\"camp-stove\"}" "Idempotency-Key: e2e-$RUN-1"
check "same key with a different body is refused" "$STATUS/$(jget "$BODY" error.code)" "409/idempotency_key_reused"
req POST /v1/evaluations "{\"customer_id\":\"${CUS}_new\",\"merchant_id\":\"mer_demo_outfitter\",\"basket_ref\":\"trail-running-shoes\"}"
check "customer without consent: card is the only eligible option" "$(jget "$BODY" result.rationale)/$(jget "$BODY" result.options.1.exclusions.0.code)" "only_eligible_option/not_consented"
req POST /v1/evaluations "{\"customer_id\":\"$CUS\",\"merchant_id\":\"mer_demo_pantry\",\"basket_ref\":\"pantry-restock\"}"
check "unknown network fee yields insufficient data, not a zero fee" "$(jget "$BODY" result.outcome)/$(jget "$BODY" result.options.1.exclusions.0.code)" "insufficient_data/unknown_required_fee"
req GET "/v1/evaluations/$EVID"; check "stored evaluation is readable and current" "$STATUS/$(jget "$BODY" status)" "200/current"

echo "execution handoff"
DUE=$(option_due "$EV" usdc); CARD_DUE=$(option_due "$EV" card)
WRONG=$(python3 -c "import json,sys; d=json.loads(sys.argv[1]); d['base_units']=str(int(d['base_units'])+1); print(json.dumps(d))" "$DUE")
approval() { echo "{\"option_id\":\"$1\",\"approval\":{\"customer_id\":\"$2\",\"approved_at\":\"$(now)\",\"approved_amount\":$3,\"reference\":\"e2e-$RUN-$4\"}}"; }
req POST "/v1/evaluations/$EVID/execution-handoffs" "$(approval usdc "$CUS" "$DUE" a)"
check "handoff without Idempotency-Key is refused" "$STATUS" 400
req POST "/v1/evaluations/$EVID/execution-handoffs" "$(approval usdc "$CUS" "$WRONG" b)" "Idempotency-Key: e2e-$RUN-h1"
check "approval for a different amount is refused" "$STATUS/$(jget "$BODY" error.code)" "409/approval_mismatch"
req POST "/v1/evaluations/$EVID/execution-handoffs" "$(approval usdc someone_else "$DUE" c)" "Idempotency-Key: e2e-$RUN-h2"
check "approval by a different customer is refused" "$STATUS/$(jget "$BODY" error.code)" "409/approval_mismatch"
req POST "/v1/evaluations/$EVID/execution-handoffs" "$(approval usdc "$CUS" "$DUE" d)" "Idempotency-Key: e2e-$RUN-h3"
HO="$BODY"; HOID=$(jget "$HO" id)
check "approved stablecoin payment handed off" "$STATUS/$(jget "$HO" status)/$(jget "$HO" executable)" "201/created/true"
req POST "/v1/evaluations/$EVID/execution-handoffs" "$(approval card "$CUS" "$CARD_DUE" e)" "Idempotency-Key: e2e-$RUN-h4"
check "second attempt on another rail is blocked until reconciled" "$STATUS/$(jget "$BODY" error.code)" "409/prior_attempt_unresolved"
req GET "/v1/execution-handoffs/$HOID"; check "executor revalidation sees an executable reference" "$(jget "$BODY" executable)" true

echo "outcomes and measurement"
outcome() { echo "{\"evaluation_id\":\"$EVID\",\"handoff_id\":\"$HOID\",\"source\":\"provider\",\"event_id\":\"e2e-$RUN-$1\",\"type\":\"$2\",\"status\":\"$3\",\"option_id\":\"usdc\",\"occurred_at\":\"$(now)\"$4}"; }
req POST /v1/outcomes "$(outcome p1 payment succeeded ",\"charged\":$DUE")"
check "settled payment at the estimate realizes immediate savings of 3.85" "$STATUS/$(jget "$BODY" derived.savings_basis)/$(jget "$BODY" derived.realized_immediate_savings_micro_usd)" "201/realized_vs_estimated_baseline/3850000"
check "counterfactual baseline is explicit and unobserved" "$(jget "$BODY" derived.counterfactual.baseline_option_id)/$(jget "$BODY" derived.counterfactual.observed)" "card/false"
req POST /v1/outcomes "$(outcome p1 payment succeeded ",\"charged\":$DUE")"
check "replayed event is recorded once" "$STATUS" 200
req POST /v1/outcomes "$(outcome p2 payment failed "")"
check "contradicting outcome needs manual reconciliation" "$STATUS/$(jget "$BODY" error.code)" "409/conflicting_outcome"
req GET "/v1/execution-handoffs/$HOID"; check "handoff is settled and no longer executable" "$(jget "$BODY" status)/$(jget "$BODY" executable)" "succeeded/false"
REFUND_BIG='{"amount":{"asset":{"code":"USDC","network":"base","decimals":6},"base_units":"134590001"},"conversion_cost":null,"initiated_at":null,"completed_at":null}'
REFUND_PART='{"amount":{"asset":{"code":"USDC","network":"base","decimals":6},"base_units":"8450000"},"conversion_cost":null,"initiated_at":null,"completed_at":null}'
req POST /v1/outcomes "$(outcome r1 refund succeeded ",\"refund\":$REFUND_BIG")"
check "refund above the charge is refused" "$STATUS/$(jget "$BODY" error.code)" "409/refund_exceeds_charge"
req POST /v1/outcomes "$(outcome r2 refund succeeded ",\"refund\":$REFUND_PART")"
check "partial refund recorded with its asset and conversion-cost status" "$STATUS/$(jget "$BODY" derived.refunded_total_base_units)/$(jget "$BODY" derived.conversion_cost_known)" "201/8450000/false"
req POST "/v1/evaluations/$EVID/execution-handoffs" "$(approval card "$CUS" "$CARD_DUE" f)" "Idempotency-Key: e2e-$RUN-h5"
check "a settled attempt no longer blocks a new approval" "$STATUS" 201
req GET "/v1/reports/summary"
check "summary separates realized savings from estimated rewards" "$(python3 -c "import json,sys; s=json.loads(sys.argv[1])['outcomes']; print(int(s['realized_savings_count'])>=1 and s['realized_immediate_savings_micro_usd']!=s['estimated_rewards_micro_usd'])" "$BODY")" True
req POST "/v1/evaluations/$EVID/refresh"
check "refresh creates a new evaluation linked to the original" "$STATUS/$(jget "$BODY" refreshed_from)" "201/$EVID"

rm -f /tmp/e2e-body.$$
echo "passed=$passed failed=$failed"
[ "$failed" -eq 0 ]
