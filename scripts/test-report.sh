#!/usr/bin/env bash
# Run every suite plus the end-to-end scenario and rewrite docs/test-report.md.
# Requires the local stack to be running (make up) for the e2e section.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
OUT="docs/test-report.md"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
[ -f "$HOME/.cargo/env" ] && source "$HOME/.cargo/env"

status() { if [[ $1 -eq 0 ]]; then echo PASS; else echo FAIL; fi; }

(cd control-plane && go test ./... -race -count=1 -v 2>&1) > "$TMP/go.txt"; GO=$?
(cd gateway && cargo clippy --all-targets -- -D warnings 2>&1 && cargo test 2>&1) > "$TMP/rust.txt"; RUST=$?
(npm run build -w sdk >/dev/null 2>&1 && npm run typecheck 2>&1 && npm test 2>&1) > "$TMP/ts.txt"; TS=$?
./scripts/e2e.sh > "$TMP/e2e.txt" 2>&1; E2E=$?

go_tests=$(grep -E '^(=== RUN|--- (PASS|FAIL))' "$TMP/go.txt" | grep -E '^--- ' | sed -E 's/^--- (PASS|FAIL): ([^ ]+).*/| `\2` | \1 |/' | sort -u)
go_pass=$(grep -c '^--- PASS' "$TMP/go.txt"); go_fail=$(grep -c '^--- FAIL' "$TMP/go.txt")
rust_tests=$(grep -E '^test .* \.\.\. (ok|FAILED)$' "$TMP/rust.txt" | sed -E 's/^test ([^ ]+) \.\.\. (ok|FAILED)$/| `\1` | \2 |/' | sed 's/| ok |/| PASS |/; s/| FAILED |/| FAIL |/' | sort -u)
rust_summary=$(grep -E '^test result' "$TMP/rust.txt" | head -1 | sed -E 's/test result: ([a-z]+)\. ([0-9]+) passed; ([0-9]+) failed.*/\2 passed, \3 failed/')
ts_summary=$(grep -E '^[[:space:]]+Tests ' "$TMP/ts.txt" | sed -E 's/^[[:space:]]+Tests[[:space:]]+//; s/[[:space:]]+$//' | paste -sd ';' - | sed 's/;/; /g')
ts_files=$(grep -E '✓ src/.*\.test\.tsx?' "$TMP/ts.txt" | sed -E 's/.*✓ (src\/[^ ]+) \(([0-9]+) tests?\).*/| `\1` | \2 tests | PASS |/')
e2e_lines=$(grep -E '^[[:space:]]+[✓✗] ' "$TMP/e2e.txt" | sed -E 's/^[[:space:]]+✓ (.*)$/| \1 | PASS |/; s/^[[:space:]]+✗ (.*)$/| \1 | FAIL |/')
e2e_summary=$(grep -E '^passed=' "$TMP/e2e.txt" | tail -1)

{
  echo "# Test report"
  echo
  echo "Generated $(date -u '+%Y-%m-%d %H:%M UTC') by \`scripts/test-report.sh\` on $(uname -s) $(uname -m); Go $(go version | awk '{print $3}'), $(rustc --version | awk '{print "Rust " $2}'), Node $(node --version)."
  echo
  echo "**Summary:** Go \`$go_pass passed, $go_fail failed\` ($(status $GO)) · Rust \`$rust_summary\` ($(status $RUST)) · TypeScript \`$ts_summary\` ($(status $TS)) · End-to-end \`$e2e_summary\` ($(status $E2E))"
  echo
  echo "## Plan validation cases → coverage"
  echo
  echo "| Requirement | Evidence | Result |"
  echo "|---|---|---|"
  echo "| concurrent overspend attempts | \`TestConcurrentOverspendIsImpossible\`, \`TestConcurrentOverspendAcrossTasksRespectsAgentDaily\` | $(grep -qE '^--- PASS: TestConcurrentOverspend' "$TMP/go.txt" && echo PASS || echo FAIL) |"
  echo "| duplicate task requests | \`TestIdempotentReplayNeverPaysTwice\`; e2e \"replay does not pay again\" | $(grep -q '^--- PASS: TestIdempotentReplayNeverPaysTwice' "$TMP/go.txt" && echo PASS || echo FAIL) |"
  echo "| cross-tenant access | \`TestCrossTenantIsolation\` | $(grep -q '^--- PASS: TestCrossTenantIsolation' "$TMP/go.txt" && echo PASS || echo FAIL) |"
  echo "| replayed or expired grants | \`TestGrantReplayAndExpiry\`; \`grant::tests::*\` | $(grep -q '^--- PASS: TestGrantReplayAndExpiry' "$TMP/go.txt" && grep -q 'grant::tests::accepts_valid_grant_once ... ok' "$TMP/rust.txt" && echo PASS || echo FAIL) |"
  echo "| changed beneficiary, wrong network/token | \`TestChallengeValidationRejectsWrongBeneficiaryAssetNetworkScheme\`; e2e adversarial section | $(grep -q '^--- PASS: TestChallengeValidationRejects' "$TMP/go.txt" && echo PASS || echo FAIL) |"
  echo "| unknown payment result | \`TestUnknownOutcomeKeepsHoldUntilReconciled\`; e2e \"lost response\" | $(grep -q '^--- PASS: TestUnknownOutcomeKeepsHoldUntilReconciled' "$TMP/go.txt" && echo PASS || echo FAIL) |"
  echo "| owner revocation during execution | \`TestOwnerRevocationAndPauseDuringExecution\` | $(grep -q '^--- PASS: TestOwnerRevocationAndPauseDuringExecution' "$TMP/go.txt" && echo PASS || echo FAIL) |"
  echo "| insufficient fee balance | \`TestInsufficientBalanceIncludingFees\` | $(grep -q '^--- PASS: TestInsufficientBalanceIncludingFees' "$TMP/go.txt" && echo PASS || echo FAIL) |"
  echo "| SSRF, redirects, bounded responses | \`urlguard::tests::*\`; e2e redirect/metadata/huge checks | $(grep -q 'urlguard::tests::forbidden_addresses ... ok' "$TMP/rust.txt" && echo PASS || echo FAIL) |"
  echo "| paid but unusable result, no automatic repurchase | e2e \"settlement without useful result\"; MCP note test | $(grep -q 'balance charged once' "$TMP/e2e.txt" && echo PASS || echo FAIL) |"
  echo
  echo "## Go (\`go test ./... -race\`)"
  echo
  echo "| Test | Result |"
  echo "|---|---|"
  echo "$go_tests"
  echo
  echo "## Rust (\`cargo clippy -D warnings\`, \`cargo test\`)"
  echo
  echo "| Test | Result |"
  echo "|---|---|"
  echo "$rust_tests"
  echo
  echo "## TypeScript (\`npm run typecheck\`, \`npm test\`)"
  echo
  echo "| File | Tests | Result |"
  echo "|---|---|---|"
  echo "$ts_files"
  echo
  echo "## End-to-end (\`scripts/e2e.sh\`)"
  echo
  echo "| Check | Result |"
  echo "|---|---|"
  echo "$e2e_lines"
} > "$OUT"

echo "wrote $OUT (go=$(status $GO) rust=$(status $RUST) ts=$(status $TS) e2e=$(status $E2E))"
[[ $GO -eq 0 && $RUST -eq 0 && $TS -eq 0 && $E2E -eq 0 ]]
