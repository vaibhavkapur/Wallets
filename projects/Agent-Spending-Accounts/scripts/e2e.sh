#!/usr/bin/env bash
# End-to-end exercise of the running stack (scripts/dev-up.sh).
# Provisions an organization, funds it, sets a policy, and runs paid requests
# through the Rust gateway against the mock x402 resource, including the
# adversarial cases from plan.md's validation section.
set -euo pipefail
CP="${CP:-http://127.0.0.1:${CP_PORT:-8080}}"
GW="${GW:-http://127.0.0.1:${GW_PORT:-8402}}"
RES="${RES:-http://127.0.0.1:${RESOURCE_PORT:-9402}}"
PAY_TO="${PAY_TO:-0x209693Bc6afc0C5328bA36FaF03C514EF312287C}"
ASSET="${ASSET:-0x036CbD53842c5426634e7929541eC2318f3dCF7e}"
NETWORK="${NETWORK:-base-sepolia}"

pass=0; fail=0
ok()   { pass=$((pass+1)); echo "  ✓ $1"; }
bad()  { fail=$((fail+1)); echo "  ✗ $1"; echo "    $2" | head -c 600; echo; }
expect_eq() { if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1 (got '$2', want '$3')" "${4:-}"; fi; }

echo "== provisioning"
ORG=$(curl -fsS -X POST "$CP/v1/organizations" -H 'content-type: application/json' -d '{"name":"Acme Research"}')
OWNER=$(echo "$ORG" | jq -r .owner_api_key)
AGENT=$(curl -fsS -X POST "$CP/v1/agents" -H "authorization: Bearer $OWNER" -H 'content-type: application/json' -d '{"name":"research-agent"}')
AGENT_ID=$(echo "$AGENT" | jq -r .agent.id); AGENT_KEY=$(echo "$AGENT" | jq -r .agent_api_key)
POLICY=$(cat <<EOF
{"asset":{"asset":"$ASSET","network":"$NETWORK","decimals":6,"scheme":"exact"},
 "per_request_max":"500000","per_task_max":"1000000","agent_daily_max":"5000000","org_daily_max":"20000000","fee_allowance":"0",
 "endpoints":[{"url_prefix":"$RES/v1","methods":["GET","POST"],"recipients":["$PAY_TO"]}],
 "max_task_seconds":3600,"grant_ttl_seconds":60}
EOF
)
curl -fsS -X PUT "$CP/v1/agents/$AGENT_ID/policy" -H "authorization: Bearer $OWNER" -H 'content-type: application/json' -d "$POLICY" >/dev/null
curl -fsS -X POST "$CP/v1/funding/deposits" -H "authorization: Bearer $OWNER" -H 'content-type: application/json' \
  -d "{\"asset\":\"$ASSET\",\"network\":\"$NETWORK\",\"amount\":\"3000000\",\"reference\":\"e2e\"}" >/dev/null
TASK=$(curl -fsS -X POST "$CP/v1/tasks" -H "authorization: Bearer $AGENT_KEY" -H 'content-type: application/json' -d '{"description":"weather research","budget":"1000000"}' | jq -r .id)
echo "  org=$(echo "$ORG" | jq -r .organization.id) agent=$AGENT_ID task=$TASK"

paid() { # idem url [extra-json]
  curl -sS -o /tmp/asa_e2e_body -w '%{http_code}' -X POST "$GW/v1/tasks/$TASK/paid-requests" -H "authorization: Bearer $AGENT_KEY" \
    -H 'content-type: application/json' -d "{\"idempotency_key\":\"$1\",\"method\":\"GET\",\"url\":\"$2\"${3:+,$3}}"
}
balance() { curl -fsS "$CP/v1/balances" -H "authorization: Bearer $OWNER" | jq -r '.balances[0] | "\(.available)/\(.reserved)"'; }

echo "== happy path"
code=$(paid w1 "$RES/v1/weather?city=Lisbon")
expect_eq "paid request accepted (200)" "$code" 200 "$(cat /tmp/asa_e2e_body)"
expect_eq "payment settled" "$(jq -r .payment.status /tmp/asa_e2e_body)" settled "$(cat /tmp/asa_e2e_body)"
expect_eq "service delivered" "$(jq -r .service.delivered /tmp/asa_e2e_body)" true
expect_eq "amount is a string in base units" "$(jq -r .payment.amount /tmp/asa_e2e_body)" 100000
expect_eq "receipt links policy+payment+service" "$(jq -r '.receipt | (.policy_version_id != null and .submission_ref != "" and .service.delivered == true)' /tmp/asa_e2e_body)" true
expect_eq "balance after settle (available/reserved)" "$(balance)" "2900000/0"

echo "== idempotent replay"
code=$(paid w1 "$RES/v1/weather?city=Lisbon")
expect_eq "replay returns the original attempt" "$(jq -r .replay /tmp/asa_e2e_body)" true
expect_eq "replay does not pay again" "$(balance)" "2900000/0"

echo "== adversarial resource behaviour"
code=$(paid adv-redirect "$RES/v1/weather?mode=redirect")
expect_eq "redirect refused" "$(jq -r .error.code /tmp/asa_e2e_body)" redirect_refused "$(cat /tmp/asa_e2e_body)"
code=$(paid adv-benef "$RES/v1/weather?mode=changed-beneficiary")
expect_eq "changed beneficiary rejected" "$(jq -r .error.code /tmp/asa_e2e_body)" challenge_rejected "$(cat /tmp/asa_e2e_body)"
code=$(paid adv-net "$RES/v1/weather?mode=wrong-network")
expect_eq "wrong network rejected" "$(jq -r .error.code /tmp/asa_e2e_body)" challenge_rejected
code=$(paid adv-asset "$RES/v1/weather?mode=wrong-asset")
expect_eq "wrong asset rejected" "$(jq -r .error.code /tmp/asa_e2e_body)" challenge_rejected
code=$(paid adv-price "$RES/v1/weather?mode=overpriced")
expect_eq "overpriced quote rejected" "$(jq -r .error.code /tmp/asa_e2e_body)" challenge_rejected
code=$(paid adv-malformed "$RES/v1/weather?mode=malformed")
expect_eq "malformed challenge rejected" "$(jq -r .error.code /tmp/asa_e2e_body)" challenge_malformed
code=$(paid adv-outside "$RES/v2/other")
expect_eq "endpoint outside allowlist denied" "$(jq -r .error.code /tmp/asa_e2e_body)" endpoint_not_allowed
code=$(paid adv-ssrf "http://169.254.169.254/latest/meta-data")
expect_eq "metadata endpoint denied" "$(jq -r .error.code /tmp/asa_e2e_body)" endpoint_not_allowed
code=$(paid adv-cap "$RES/v1/weather?city=X" '"max_amount":"1"')
expect_eq "caller max_amount below price rejected" "$(jq -r .error.code /tmp/asa_e2e_body)" challenge_rejected
expect_eq "nothing held after rejections" "$(balance)" "2900000/0"

echo "== settlement without useful result"
code=$(paid adv-ptf "$RES/v1/weather?mode=pay-then-fail")
expect_eq "payment settled" "$(jq -r .payment.status /tmp/asa_e2e_body)" settled "$(cat /tmp/asa_e2e_body)"
expect_eq "service NOT delivered" "$(jq -r .service.delivered /tmp/asa_e2e_body)" false
expect_eq "http 500 recorded" "$(jq -r .service.http_status /tmp/asa_e2e_body)" 500
expect_eq "balance charged once (no automatic repurchase)" "$(balance)" "2800000/0"

echo "== oversized response is bounded"
code=$(paid adv-huge "$RES/v1/weather?mode=huge")
expect_eq "truncated flag set" "$(jq -r .service.truncated /tmp/asa_e2e_body)" true "$(cat /tmp/asa_e2e_body | head -c 300)"
expect_eq "service not delivered when truncated" "$(jq -r .service.delivered /tmp/asa_e2e_body)" false

echo "== lost response after payment (unknown outcome)"
code=$(paid adv-lost "$RES/v1/weather?mode=lost")
expect_eq "outcome reported unknown" "$(jq -r .payment.status /tmp/asa_e2e_body)" unknown "$(cat /tmp/asa_e2e_body)"
expect_eq "reservation retained while unknown" "$(balance)" "2600000/100000"
ATTEMPT=$(jq -r .attempt_id /tmp/asa_e2e_body)
echo -n "  waiting for reconciliation via facilitator lookup"
for _ in $(seq 1 30); do
  st=$(curl -fsS "$CP/v1/payment-attempts/$ATTEMPT" -H "authorization: Bearer $AGENT_KEY" | jq -r .status)
  [[ "$st" == "settled" ]] && break; echo -n "."; sleep 1
done; echo
expect_eq "reconciled to settled" "$st" settled
expect_eq "hold committed after reconciliation" "$(balance)" "2600000/0"

echo "== task budget cap"
code=$(paid cap1 "$RES/v1/summarize")   # 250000, POST required
expect_eq "GET on POST-only resource is not paid" "$(jq -r .payment.status /tmp/asa_e2e_body)" not_attempted "$(cat /tmp/asa_e2e_body)"
code=$(curl -sS -o /tmp/asa_e2e_body -w '%{http_code}' -X POST "$GW/v1/tasks/$TASK/paid-requests" -H "authorization: Bearer $AGENT_KEY" -H 'content-type: application/json' \
  -d "{\"idempotency_key\":\"cap2\",\"method\":\"POST\",\"url\":\"$RES/v1/summarize\",\"headers\":{\"content-type\":\"text/plain\"},\"body\":\"long text\"}")
expect_eq "POST paid request settled" "$(jq -r .payment.status /tmp/asa_e2e_body)" settled "$(cat /tmp/asa_e2e_body)"
code=$(paid cap3 "$RES/v1/weather?city=A"); code=$(paid cap4 "$RES/v1/weather?city=B"); code=$(paid cap5 "$RES/v1/weather?city=C")
code=$(paid cap6 "$RES/v1/weather?city=D")
expect_eq "task budget (1,000,000) enforced" "$(jq -r .error.code /tmp/asa_e2e_body)" task_budget_exceeded "$(cat /tmp/asa_e2e_body)"
BUDGET=$(curl -fsS "$CP/v1/tasks/$TASK/budget" -H "authorization: Bearer $AGENT_KEY")
expect_eq "agent budget view shows remaining capacity" "$(echo "$BUDGET" | jq -r .remaining)" 50000 "$BUDGET"

echo "== owner controls"
curl -fsS -X POST "$CP/v1/organization/pause" -H "authorization: Bearer $OWNER" >/dev/null
TASK2=$(curl -sS -X POST "$CP/v1/tasks" -H "authorization: Bearer $AGENT_KEY" -H 'content-type: application/json' -d '{"description":"x"}' | jq -r '.error.code')
expect_eq "paused org blocks new tasks" "$TASK2" org_paused
curl -fsS -X POST "$CP/v1/organization/resume" -H "authorization: Bearer $OWNER" >/dev/null
curl -fsS -X POST "$CP/v1/agents/$AGENT_ID/revoke" -H "authorization: Bearer $OWNER" >/dev/null
code=$(paid rev1 "$RES/v1/weather?city=Z")
expect_eq "revoked agent rejected at gateway" "$code" 401 "$(cat /tmp/asa_e2e_body)"
RECEIPTS=$(curl -fsS "$CP/v1/receipts" -H "authorization: Bearer $OWNER" | jq '.receipts | length')
expect_eq "owner sees receipts" "$([[ $RECEIPTS -ge 6 ]] && echo yes)" yes
AUDIT=$(curl -fsS "$CP/v1/audit?limit=500" -H "authorization: Bearer $OWNER" | jq '[.events[].action] | unique | length')
expect_eq "audit trail recorded many action types" "$([[ $AUDIT -ge 8 ]] && echo yes)" yes

echo
echo "passed=$pass failed=$fail"
[[ $fail -eq 0 ]]
