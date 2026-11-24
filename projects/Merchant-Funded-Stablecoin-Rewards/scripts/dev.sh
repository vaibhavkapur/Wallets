#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$ROOT_DIR/.local"

cleanup() {
  if [[ -n "${API_PID:-}" ]]; then kill "$API_PID" 2>/dev/null || true; fi
  if [[ -n "${WEB_PID:-}" ]]; then kill "$WEB_PID" 2>/dev/null || true; fi
}
trap cleanup EXIT INT TERM

(
  cd "$ROOT_DIR/backend"
  GOCACHE="$ROOT_DIR/.local/go-cache" \
  ADDR="127.0.0.1:8089" \
  STATE_FILE="$ROOT_DIR/.local/rewards-state.json" \
  OPERATOR_TOKEN="${OPERATOR_TOKEN:-local-operator}" \
  WEBHOOK_SECRET="${WEBHOOK_SECRET:-local-webhook-secret}" \
  go run ./cmd/server
) &
API_PID=$!

(
  cd "$ROOT_DIR/web"
  npm run dev
) &
WEB_PID=$!

echo "API: http://127.0.0.1:8089"
echo "UI:  http://127.0.0.1:5189"
echo "Operator token: ${OPERATOR_TOKEN:-local-operator}"

wait
