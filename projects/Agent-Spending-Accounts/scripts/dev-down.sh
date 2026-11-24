#!/usr/bin/env bash
# Stop a stack started with scripts/dev-up.sh --daemon
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ -f "$ROOT/.dev/pids" ]]; then
  while read -r pid; do kill "$pid" 2>/dev/null || true; done < "$ROOT/.dev/pids"
  rm -f "$ROOT/.dev/pids"
  echo "stopped"
else
  echo "no running stack recorded"
fi
