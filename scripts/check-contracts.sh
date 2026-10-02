#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "$0")/.." && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
cd "$project_root/backend"
GOCACHE="$project_root/.local/go-cache" go run ./cmd/schema > "$scratch/openapi.json"
diff -u ../contracts/openapi.json "$scratch/openapi.json"
cd ../web
./node_modules/.bin/openapi-typescript ../contracts/openapi.json -o "$scratch/schema.d.ts"
diff -u src/schema.d.ts "$scratch/schema.d.ts"
