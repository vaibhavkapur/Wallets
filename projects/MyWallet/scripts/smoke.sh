#!/usr/bin/env bash
# End-to-end smoke test against a running dev server (make run-dev).
# Exercises sign-in, idempotent setup creation, provider-verified saving,
# rename, preferred, removal, and account deletion. Requires curl and jq.
set -euo pipefail
API="${API:-http://127.0.0.1:8080}"

say() { printf '\n== %s\n' "$*"; }
req() { # method path [data] [extra curl args...]
  local m=$1 p=$2 d=${3:-}; shift 3 || shift $#
  if [ -n "$d" ]; then
    curl -sS -X "$m" "$API$p" -H "authorization: Bearer ${TOKEN:-}" -H 'content-type: application/json' "$@" -d "$d"
  else
    curl -sS -X "$m" "$API$p" -H "authorization: Bearer ${TOKEN:-}" "$@"
  fi
}

say "health"; curl -sS "$API/healthz"; echo
say "sign in (dev)"
TOKEN=$(curl -sS -X POST "$API/v1/auth/apple" -H 'content-type: application/json' -d '{"identity_token":"dev:smoke-user"}' | jq -r .session_token)
echo "token acquired"

say "start setup (idempotent)"
KEY="smoke-$(date +%s)"
S1=$(req POST /v1/card-setups '{"consent":{"accepted":true,"version":"2026-10"}}' -H "idempotency-key: $KEY")
S2=$(req POST /v1/card-setups '{"consent":{"accepted":true,"version":"2026-10"}}' -H "idempotency-key: $KEY")
SID=$(echo "$S1" | jq -r .id)
[ "$SID" = "$(echo "$S2" | jq -r .id)" ] && echo "replay returned the same attempt $SID"
echo "$S1" | jq '{status, has_client_session: (.client_session != null)}'

say "cardholder completes the sheet (fixture) and the app polls"
req POST "/dev/card-setups/$SID/complete" '{}' >/dev/null
req GET "/v1/card-setups/$SID" | jq '{status, card: .card | {brand, last4, is_preferred}}'
CARD=$(req GET /v1/cards | jq -r '.cards[0].id')

say "rename + preferred"
req PATCH "/v1/cards/$CARD" '{"nickname":"Everyday"}' | jq '{nickname, is_preferred}'

say "cross-account access must fail"
OTHER=$(curl -sS -X POST "$API/v1/auth/apple" -H 'content-type: application/json' -d '{"identity_token":"dev:someone-else"}' | jq -r .session_token)
code=$(curl -sS -o /dev/null -w '%{http_code}' "$API/v1/cards/$CARD" -H "authorization: Bearer $OTHER")
echo "GET other user's card -> $code"

say "remove card"
req DELETE "/v1/cards/$CARD" | jq '{state}'
req GET /v1/cards | jq '.cards | length'

say "delete account"
req DELETE /v1/account | jq .
code=$(curl -sS -o /dev/null -w '%{http_code}' "$API/v1/me" -H "authorization: Bearer $TOKEN")
echo "session after deletion -> $code"
say "done"
