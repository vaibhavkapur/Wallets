# Test report

Generated 2026-10-02 23:37 UTC by `scripts/test-report.sh` on Darwin arm64; Go go1.27.1, Rust 1.99.0, Node v20.20.2.

**Summary:** Go `17 passed, 0 failed` (PASS) · Rust `27 passed, 0 failed` (PASS) · TypeScript `10 passed | 1 skipped (11); 3 passed (3); 2 passed (2)` (PASS) · End-to-end `passed=36 failed=0` (PASS)

## Plan validation cases → coverage

| Requirement | Evidence | Result |
|---|---|---|
| concurrent overspend attempts | `TestConcurrentOverspendIsImpossible`, `TestConcurrentOverspendAcrossTasksRespectsAgentDaily` | PASS |
| duplicate task requests | `TestIdempotentReplayNeverPaysTwice`; e2e "replay does not pay again" | PASS |
| cross-tenant access | `TestCrossTenantIsolation` | PASS |
| replayed or expired grants | `TestGrantReplayAndExpiry`; `grant::tests::*` | PASS |
| changed beneficiary, wrong network/token | `TestChallengeValidationRejectsWrongBeneficiaryAssetNetworkScheme`; e2e adversarial section | PASS |
| unknown payment result | `TestUnknownOutcomeKeepsHoldUntilReconciled`; e2e "lost response" | PASS |
| owner revocation during execution | `TestOwnerRevocationAndPauseDuringExecution` | PASS |
| insufficient fee balance | `TestInsufficientBalanceIncludingFees` | PASS |
| SSRF, redirects, bounded responses | `urlguard::tests::*`; e2e redirect/metadata/huge checks | PASS |
| paid but unusable result, no automatic repurchase | e2e "settlement without useful result"; MCP note test | PASS |

## Go (`go test ./... -race`)

| Test | Result |
|---|---|
| `TestChallengeValidationRejectsWrongBeneficiaryAssetNetworkScheme` | PASS |
| `TestCheckedArithmetic` | PASS |
| `TestConcurrentOverspendAcrossTasksRespectsAgentDaily` | PASS |
| `TestConcurrentOverspendIsImpossible` | PASS |
| `TestCrossTenantIsolation` | PASS |
| `TestGrantReplayAndExpiry` | PASS |
| `TestHappyPathSettlesAndWritesReceipt` | PASS |
| `TestIdempotentReplayNeverPaysTwice` | PASS |
| `TestInsufficientBalanceIncludingFees` | PASS |
| `TestInternalEndpointsRequireInternalTokenAndAmountsAreStrings` | PASS |
| `TestJSONStringsOnly` | PASS |
| `TestOwnerRevocationAndPauseDuringExecution` | PASS |
| `TestParseCanonical` | PASS |
| `TestPartialSettlementReturnsUnusedHold` | PASS |
| `TestPolicyChangeClosesOldTasksAndAgentsCannotWidenLimits` | PASS |
| `TestSQLRoundTrip` | PASS |
| `TestUnknownOutcomeKeepsHoldUntilReconciled` | PASS |

## Rust (`cargo clippy -D warnings`, `cargo test`)

| Test | Result |
|---|---|
| `amount::tests::add_then_sub_is_identity` | PASS |
| `amount::tests::canonical_parsing` | PASS |
| `amount::tests::checked_arithmetic` | PASS |
| `amount::tests::json_strings_only` | PASS |
| `amount::tests::never_panics_on_arbitrary_input` | PASS |
| `amount::tests::round_trips_every_u128` | PASS |
| `client::tests::strips_gateway_owned_headers` | PASS |
| `grant::tests::accepts_valid_grant_once` | PASS |
| `grant::tests::garbage_tokens_are_rejected_without_panicking` | PASS |
| `grant::tests::payload_mutation_after_grant_is_detected` | PASS |
| `grant::tests::quote_amount_above_grant_is_rejected` | PASS |
| `grant::tests::rejects_every_binding_mutation` | PASS |
| `grant::tests::rejects_wrong_key_expired_and_wrong_audience` | PASS |
| `urlguard::tests::forbidden_addresses` | PASS |
| `urlguard::tests::literal_private_hosts_are_rejected_before_connecting` | PASS |
| `urlguard::tests::normalize_never_panics` | PASS |
| `urlguard::tests::path_prefix_requires_segment_boundary` | PASS |
| `urlguard::tests::prefix_matching_is_segment_aware` | PASS |
| `urlguard::tests::rejects_dangerous_shapes` | PASS |
| `urlguard::tests::within_prefix_never_panics` | PASS |
| `x402::tests::arbitrary_requirement_fields_never_select_unapproved_recipient` | PASS |
| `x402::tests::parse_challenge_never_panics` | PASS |
| `x402::tests::payment_header_round_trip` | PASS |
| `x402::tests::rejects_changed_beneficiary_even_if_listed_first` | PASS |
| `x402::tests::rejects_overpriced_malformed_amounts_and_bad_versions` | PASS |
| `x402::tests::selects_matching_requirement_and_hashes_canonically` | PASS |
| `x402::tests::skips_wrong_network_and_asset_then_fails_closed` | PASS |

## TypeScript (`npm run typecheck`, `npm test`)

| File | Tests | Result |
|---|---|---|
| `src/amounts.test.ts` | 4 tests | PASS |
| `src/client.test.ts` | 6 tests | PASS |
| `src/tools.test.ts` | 3 tests | PASS |
| `src/App.test.tsx` | 2 tests | PASS |

## End-to-end (`scripts/e2e.sh`)

| Check | Result |
|---|---|
| paid request accepted (200) | PASS |
| payment settled | PASS |
| service delivered | PASS |
| amount is a string in base units | PASS |
| receipt links policy+payment+service | PASS |
| balance after settle (available/reserved) | PASS |
| replay returns the original attempt | PASS |
| replay does not pay again | PASS |
| redirect refused | PASS |
| changed beneficiary rejected | PASS |
| wrong network rejected | PASS |
| wrong asset rejected | PASS |
| overpriced quote rejected | PASS |
| malformed challenge rejected | PASS |
| endpoint outside allowlist denied | PASS |
| metadata endpoint denied | PASS |
| caller max_amount below price rejected | PASS |
| nothing held after rejections | PASS |
| payment settled | PASS |
| service NOT delivered | PASS |
| http 500 recorded | PASS |
| balance charged once (no automatic repurchase) | PASS |
| truncated flag set | PASS |
| service not delivered when truncated | PASS |
| outcome reported unknown | PASS |
| reservation retained while unknown | PASS |
| reconciled to settled | PASS |
| hold committed after reconciliation | PASS |
| GET on POST-only resource is not paid | PASS |
| POST paid request settled | PASS |
| task budget (1,000,000) enforced | PASS |
| agent budget view shows remaining capacity | PASS |
| paused org blocks new tasks | PASS |
| revoked agent rejected at gateway | PASS |
| owner sees receipts | PASS |
| audit trail recorded many action types | PASS |
