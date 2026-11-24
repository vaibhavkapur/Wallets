#!/usr/bin/env bash
# Run every suite plus the end-to-end scenario and rewrite docs/test-report.md.
# Requires the local stack to be running in assisted mode (make up).
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
OUT="docs/test-report.md"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
[ -f "$HOME/.cargo/env" ] && source "$HOME/.cargo/env"

status() { if [[ $1 -eq 0 ]]; then echo PASS; else echo FAIL; fi; }

(cd engine && cargo clippy --workspace --all-targets -- -D warnings 2>&1 && cargo test --workspace 2>&1) > "$TMP/rust.txt"; RUST=$?
(python3 fixtures/tools/independent_check.py fixtures/golden/v1 2>&1 && python3 fixtures/tools/independent_check.py fixtures/recorded/v1 2>&1) > "$TMP/py.txt"; PY=$?
(cd orchestrator && go vet ./... 2>&1 && go test ./... -race -count=1 -v 2>&1) > "$TMP/go.txt"; GO=$?
(cd web && npm run typecheck 2>&1 && npm test 2>&1) > "$TMP/ts.txt"; TS=$?
./scripts/e2e.sh > "$TMP/e2e.txt" 2>&1; E2E=$?
ENGINE_AUTH_TOKEN=dev-engine-token-0123456789abcdef .dev/bin/offline-eval -dataset "$ROOT/fixtures/recorded/v1" \
  -engine-url "http://$(grep -o 'listening on 127.0.0.1:[0-9]*' .dev/devstack.log | head -1 | sed 's/listening on //')" > "$TMP/o1.txt" 2>&1; O1=$?

rust_tests=$(grep -E '^test .* \.\.\. (ok|FAILED)$' "$TMP/rust.txt" | sed -E 's/^test ([^ ]+) \.\.\. (ok|FAILED)$/| `\1` | \2 |/' | sed 's/| ok |/| PASS |/; s/| FAILED |/| FAIL |/' | sort -u)
rust_pass=$(grep -E '^test result' "$TMP/rust.txt" | sed -E 's/.* ([0-9]+) passed; ([0-9]+) failed.*/\1/' | paste -sd+ - | bc)
rust_fail=$(grep -E '^test result' "$TMP/rust.txt" | sed -E 's/.* ([0-9]+) passed; ([0-9]+) failed.*/\2/' | paste -sd+ - | bc)
py_summary=$(grep -E 'vectors checked' "$TMP/py.txt" | sed -E 's/ vectors checked independently/ checked/; s/ malformed-request vectors skipped/ malformed skipped/' | paste -sd ';' - | sed 's/;/; /g')
go_tests=$(grep -E '^--- (PASS|FAIL)' "$TMP/go.txt" | sed -E 's/^--- (PASS|FAIL): ([^ ]+).*/| `\2` | \1 |/' | sort -u)
go_pass=$(grep -c '^--- PASS' "$TMP/go.txt"); go_fail=$(grep -c '^--- FAIL' "$TMP/go.txt")
ts_summary=$(grep -E '^[[:space:]]+Tests ' "$TMP/ts.txt" | sed -E 's/^[[:space:]]+Tests[[:space:]]+//; s/[[:space:]]+$//' | paste -sd ';' - | sed 's/;/; /g')
ts_files=$(grep -E '✓ src/.*\.test\.tsx?' "$TMP/ts.txt" | sed -E 's/.*✓ (src\/[^ ]+) \(([0-9]+) tests?\).*/| `\1` | \2 tests | PASS |/')
e2e_lines=$(grep -E '^[[:space:]]+[✓✗] ' "$TMP/e2e.txt" | sed -E 's/^[[:space:]]+✓ (.*)$/| \1 | PASS |/; s/^[[:space:]]+✗ (.*)$/| \1 | FAIL |/')
e2e_summary=$(grep -E '^passed=' "$TMP/e2e.txt" | tail -1)
o1_lines=$(grep -E '^(ok|FAIL) ' "$TMP/o1.txt" | sed -E 's/^ok +([^ ]+) +([^ ]+).*/| `\1` | \2 | PASS |/; s/^FAIL +([^ ]+) +([^ ]*).*/| `\1` | \2 | FAIL |/')
o1_summary=$(grep -E 'baskets:' "$TMP/o1.txt" | tail -1)

rs() { grep -q "$1 ... ok" "$TMP/rust.txt" && echo PASS || echo FAIL; }
gt() { grep -q "^--- PASS: $1" "$TMP/go.txt" && echo PASS || echo FAIL; }
ee() { grep -q "✓ $1" "$TMP/e2e.txt" && echo PASS || echo FAIL; }

{
  echo "# Test report"
  echo
  echo "Generated $(date -u '+%Y-%m-%d %H:%M UTC') by \`scripts/test-report.sh\` on $(uname -s) $(uname -m); Go $(go version | awk '{print $3}'), $(rustc --version | awk '{print "Rust " $2}'), Node $(node --version), Python $(python3 --version | awk '{print $2}')."
  echo
  echo "**Summary:** Rust \`$rust_pass passed, $rust_fail failed\` ($(status $RUST)) · Reference check \`$py_summary\` ($(status $PY)) · Go \`$go_pass passed, $go_fail failed\` ($(status $GO)) · TypeScript \`$ts_summary\` ($(status $TS)) · End-to-end \`$e2e_summary\` ($(status $E2E)) · Offline evaluation (O1) \`$o1_summary\` ($(status $O1))"
  echo
  echo "## Plan validation cases → coverage"
  echo
  echo "| Requirement (plan.md) | Evidence | Result |"
  echo "|---|---|---|"
  echo "| zero/negative invalid amounts | golden \`900-zero-delivered-price\`, \`901-negative-amount\`; \`amount::tests::parses_canonical_strings_only\` | $(rs golden_vectors) |"
  echo "| large base-unit values and overflow | golden \`071-eighteen-decimal-asset\`, \`072-amount-out-of-range\`; \`properties::evaluation_is_total_deterministic_and_consistent\` | $(rs evaluation_is_total_deterministic_and_consistent) |"
  echo "| 100% discounts | golden \`052-discount-exceeds-price\`, \`053-hundred-percent-discount-earns-no-rewards\` | $(rs golden_vectors) |"
  echo "| reward caps already consumed | golden \`060\`, \`061\`, \`065\`; \`properties::cashback_never_exceeds_the_remaining_cap\` | $(rs cashback_never_exceeds_the_remaining_cap) |"
  echo "| incompatible assets | golden \`015-incompatible-asset\`, \`035-fee-in-unvaluable-asset\` | $(rs golden_vectors) |"
  echo "| stale quotes | golden \`010-stale-quote\`; \`TestExpiryPauseAndReconciliation\` | $(gt TestExpiryPauseAndReconciliation) |"
  echo "| missing conversion fees | golden \`031-missing-conversion-fee\`; e2e \"unknown network fee yields insufficient data\" | $(ee 'unknown network fee') |"
  echo "| duplicate discounts | golden \`050-duplicate-discount-counted-once\`, \`904-conflicting-duplicate-discount\` | $(rs golden_vectors) |"
  echo "| unstable point valuation | golden \`064-points-with-unstable-valuation\` | $(rs golden_vectors) |"
  echo "| partial refunds | \`TestAssistedHandoffsAndOutcomes\`; e2e refund checks | $(gt TestAssistedHandoffsAndOutcomes) |"
  echo "| a quote changing before execution | \`TestExpiryPauseAndReconciliation\` (expired evaluation refuses handoff; refresh) | $(gt TestExpiryPauseAndReconciliation) |"
  echo "| ties and unknown inputs in golden fixtures | golden \`005-exact-tie\`, \`030\`, \`043\`, \`073\`; \`independent_check.py\` | $(status $PY) |"
  echo "| never switch rails after an uncertain result | \`TestExpiryPauseAndReconciliation\`, \`TestConcurrentHandoffsOpenExactlyOneAttempt\`; e2e \"blocked until reconciled\" | $(gt TestConcurrentHandoffsOpenExactlyOneAttempt) |"
  echo "| savings reported separately from estimated rewards; explicit counterfactual | \`TestAssistedHandoffsAndOutcomes\`; e2e \"counterfactual baseline is explicit and unobserved\" | $(ee 'counterfactual baseline') |"
  echo "| recommendation-only release never claims a charge or a saving | \`TestShadowModeEvaluations\`; e2e \"recommendation only\" | $(gt TestShadowModeEvaluations) |"
  echo "| same test vectors across runtimes | \`golden_vectors\` (Rust), \`independent_check.py\`, \`TestGoldenRequestsRoundTripThroughGoTypes\`, \`TestOfflineEvaluation\` | $(gt TestOfflineEvaluation) |"
  echo "| snapshots are immutable | \`TestSnapshotsAreAppendOnly\` | $(gt TestSnapshotsAreAppendOnly) |"
  echo "| separate recommendation and execution permissions; tenant isolation | \`TestAuthenticationAndScopes\` | $(gt TestAuthenticationAndScopes) |"
  echo
  echo "## Rust (\`cargo clippy -D warnings\` + \`cargo test --workspace\`)"
  echo
  echo "| Test | Result |"; echo "|---|---|"; echo "$rust_tests"
  echo
  echo "## Reference check (\`fixtures/tools/independent_check.py\`)"
  echo
  sed 's/^/    /' "$TMP/py.txt"
  echo
  echo "## Go (\`go test ./... -race\`)"
  echo
  echo "| Test | Result |"; echo "|---|---|"; echo "$go_tests"
  echo
  echo "## TypeScript (\`npm run typecheck && npm test\`)"
  echo
  echo "| File | Tests | Result |"; echo "|---|---|---|"; echo "$ts_files"
  echo
  echo "## End-to-end (\`scripts/e2e.sh\`)"
  echo
  echo "| Check | Result |"; echo "|---|---|"; echo "$e2e_lines"
  echo
  echo "## Offline evaluation, milestone O1 (\`offline-eval\` over \`fixtures/recorded/v1\`)"
  echo
  echo "| Basket | Outcome | Result |"; echo "|---|---|---|"; echo "$o1_lines"
  echo
  echo "$o1_summary"
} > "$OUT"

echo "wrote $OUT"
echo "Rust $(status $RUST) · Reference $(status $PY) · Go $(status $GO) · TypeScript $(status $TS) · E2E $(status $E2E) · O1 $(status $O1)"
[[ $RUST -eq 0 && $PY -eq 0 && $GO -eq 0 && $TS -eq 0 && $E2E -eq 0 && $O1 -eq 0 ]]
