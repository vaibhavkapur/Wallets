#!/usr/bin/env bash
# Stop the local stack started by scripts/dev-up.sh.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
if [ -f .dev/pids ]; then
  while read -r pid; do
    kill "$pid" 2>/dev/null || true
  done < .dev/pids
  rm -f .dev/pids
  sleep 1
fi
pkill -f ".dev/bin/devstack" 2>/dev/null || true
echo "stack stopped"
