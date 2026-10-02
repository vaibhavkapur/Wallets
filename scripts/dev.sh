#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$project_root"
mkdir -p .local
export GOCACHE="$project_root/.local/go-cache"
export OPERATOR_TOKEN="${OPERATOR_TOKEN:-local-operator}"
export WEBHOOK_SECRET="${WEBHOOK_SECRET:-local-webhook-secret}"
export DATA_FILE="${DATA_FILE:-$project_root/.local/state.json}"
export WEB_DIST="$project_root/web/dist"
(cd backend && go build -o ../.local/uno-server ./cmd/server)
.local/uno-server &
api_pid=$!
(cd web && exec npm run dev) &
web_pid=$!
cleanup() { kill "$api_pid" "$web_pid" 2>/dev/null || true; }
trap cleanup EXIT INT TERM
wait "$api_pid" "$web_pid"
