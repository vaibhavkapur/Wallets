#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$ROOT_DIR/.local"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

(
  cd "$ROOT_DIR/backend"
  GOCACHE="$ROOT_DIR/.local/go-cache" \
  DATA_PATH="$ROOT_DIR/.local/recurring-state.json" \
  DEMO_MODE="${DEMO_MODE:-true}" \
  ADDR="${ADDR:-127.0.0.1:8090}" \
  ALLOWED_ORIGIN="${ALLOWED_ORIGIN:-http://127.0.0.1:5190}" \
  go run ./cmd/server
) &
SERVER_PID=$!

echo "API starting on http://127.0.0.1:8090 · UI starting on http://127.0.0.1:5190"
cd "$ROOT_DIR/web"
npm run dev
