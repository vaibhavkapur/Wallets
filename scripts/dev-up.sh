#!/usr/bin/env bash
# Build the engine and the Go binaries, then start the local stack in the
# background: embedded PostgreSQL, the Rust engine, and the API (devstack).
# Usage: scripts/dev-up.sh [shadow|assisted]   (default: assisted)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
MODE="${1:-assisted}"
[ -f "$HOME/.cargo/env" ] && source "$HOME/.cargo/env"
mkdir -p .dev
if [ -f .dev/pids ]; then
  echo "stack already running (.dev/pids exists); run scripts/dev-down.sh first" >&2
  exit 1
fi
(cd engine && cargo build -q -p optimizer-service)
(cd orchestrator && go build -o ../.dev/bin/devstack ./cmd/devstack && go build -o ../.dev/bin/optimizerctl ./cmd/optimizerctl && go build -o ../.dev/bin/offline-eval ./cmd/offline-eval)
nohup .dev/bin/devstack -root "$ROOT" -mode "$MODE" > .dev/devstack.log 2>&1 &
echo $! > .dev/pids
# Wait for our own process (another service may already answer on the port).
for _ in $(seq 1 200); do
  if grep -q "optimizer listening" .dev/devstack.log 2>/dev/null && curl -sf http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
    echo "API ready at http://127.0.0.1:8080 (mode: $MODE); key in .dev/api-key; logs in .dev/devstack.log"
    exit 0
  fi
  if ! kill -0 "$(cat .dev/pids)" 2>/dev/null; then break; fi
  sleep 0.3
done
echo "API did not become healthy; see .dev/devstack.log" >&2
scripts/dev-down.sh
exit 1
