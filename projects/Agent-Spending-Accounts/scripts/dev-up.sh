#!/usr/bin/env bash
# Start the whole stack locally with no external dependencies:
#   control plane (embedded PostgreSQL), mock signing provider,
#   mock x402 resource server, and the Rust gateway.
#
# Usage: scripts/dev-up.sh            # foreground; Ctrl-C stops everything
#        scripts/dev-up.sh --daemon   # background; pids in .dev/pids
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p .dev
export INTERNAL_TOKEN="${INTERNAL_TOKEN:-dev-internal-token}"
export SIGNER_TOKEN="${SIGNER_TOKEN:-dev-signer-token}"
export CP_PORT="${CP_PORT:-8080}"
export GW_PORT="${GW_PORT:-8402}"
export RESOURCE_PORT="${RESOURCE_PORT:-9402}"
export SIGNER_PORT="${SIGNER_PORT:-9403}"
PAY_TO="${PAY_TO:-0x209693Bc6afc0C5328bA36FaF03C514EF312287C}"

echo "building..."
(cd control-plane && go build -o ../bin/ ./cmd/...)
(cd gateway && cargo build --quiet 2>/dev/null || cargo build)
GW_BIN="$(cd gateway && cargo metadata --format-version 1 --no-deps | jq -r .target_directory)/debug/asa-gateway"

pids=()
cleanup() { echo; echo "stopping..."; for p in "${pids[@]}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; }
trap cleanup EXIT INT TERM

LISTEN_ADDR=":${CP_PORT}" ALLOWED_ORIGINS="http://localhost:5173,http://127.0.0.1:5173" \
  FACILITATOR_URL="http://127.0.0.1:${RESOURCE_PORT}" UNKNOWN_RELEASE_AFTER="${UNKNOWN_RELEASE_AFTER:-24h}" SUBMITTED_GRACE="${SUBMITTED_GRACE:-2m}" \
  ./bin/controlplane --dev-embedded-postgres > .dev/controlplane.log 2>&1 &
pids+=($!)
LISTEN_ADDR=":${SIGNER_PORT}" ALLOWED_TO="$PAY_TO" ./bin/mocksigner > .dev/mocksigner.log 2>&1 &
pids+=($!)
LISTEN_ADDR=":${RESOURCE_PORT}" PAY_TO="$PAY_TO" PUBLIC_URL="http://127.0.0.1:${RESOURCE_PORT}" ./bin/mockresource > .dev/mockresource.log 2>&1 &
pids+=($!)

echo -n "waiting for control plane"
for _ in $(seq 1 120); do
  if curl -fsS "http://127.0.0.1:${CP_PORT}/healthz" >/dev/null 2>&1; then break; fi
  echo -n "."; sleep 0.5
done
echo

LISTEN_ADDR="127.0.0.1:${GW_PORT}" CONTROL_PLANE_URL="http://127.0.0.1:${CP_PORT}" \
  SIGNER_URL="http://127.0.0.1:${SIGNER_PORT}" ALLOW_HTTP=true ALLOW_PRIVATE_TARGETS=true \
  REQUEST_DEADLINE_SECONDS="${REQUEST_DEADLINE_SECONDS:-8}" MAX_RESPONSE_BYTES="${MAX_RESPONSE_BYTES:-1048576}" \
  "$GW_BIN" > .dev/gateway.log 2>&1 &
pids+=($!)
echo -n "waiting for gateway"
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${GW_PORT}/healthz" >/dev/null 2>&1; then break; fi
  echo -n "."; sleep 0.5
done
echo
printf '%s\n' "${pids[@]}" > .dev/pids
cat <<EOF

stack is up:
  control plane   http://127.0.0.1:${CP_PORT}   (logs: .dev/controlplane.log)
  gateway         http://127.0.0.1:${GW_PORT}   (logs: .dev/gateway.log)
  mock resource   http://127.0.0.1:${RESOURCE_PORT}   payTo=${PAY_TO}
  mock signer     http://127.0.0.1:${SIGNER_PORT}
EOF

if [[ "${1:-}" == "--daemon" ]]; then
  trap - EXIT INT TERM
  exit 0
fi
wait
