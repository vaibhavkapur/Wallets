#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_SPEC="$(mktemp)"
trap 'rm -f "$TMP_SPEC"' EXIT

(
  cd "$ROOT_DIR/backend"
  GOCACHE="$ROOT_DIR/.local/go-cache" go run ./cmd/schema > "$TMP_SPEC"
)

if ! diff -u "$ROOT_DIR/contracts/openapi.json" "$TMP_SPEC"; then
  echo "ERROR: contracts/openapi.json is out of date. Run 'make contracts'." >&2
  exit 1
fi
echo "contracts/openapi.json matches backend schema."
