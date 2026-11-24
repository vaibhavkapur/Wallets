#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'kill "${server_pid:-}" 2>/dev/null || true; rm -rf "$scratch"' EXIT INT TERM
cd "$project_root/web"
npm run build
cd ../backend
GOCACHE="$project_root/.local/go-cache" go build -o "$scratch/server" ./cmd/server
ADDR=127.0.0.1:8099 APP_ORIGIN=http://127.0.0.1:8099 DATA_FILE="$scratch/state.json" WEB_DIST="$project_root/web/dist" OPERATOR_TOKEN=e2e-operator "$scratch/server" &
server_pid=$!
wait "$server_pid"
