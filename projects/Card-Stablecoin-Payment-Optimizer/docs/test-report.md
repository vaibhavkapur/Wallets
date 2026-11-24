# Test report

Generated 2026-10-02 23:55 UTC by `scripts/test-report.sh` on Darwin arm64; Go go1.27.1, Rust 1.99.0, Node v20.20.2, Python 3.9.6.

**Summary:** Rust `30 passed, 0 failed` (PASS) · Reference check `44 checked, 12 malformed skipped, 0 mismatches; 6 checked, 0 malformed skipped, 0 mismatches` (PASS) · Go `18 passed, 0 failed` (PASS) · TypeScript `8 passed (8); 7 passed (7)` (PASS) · End-to-end `passed=26 failed=0` (PASS) · Offline evaluation (O1) `6 baskets: 6 matched, 0 failed; 5 produced a recommendation (map[insufficient_data:1 preferred:5])` (PASS)

## Plan validation cases → coverage

| Requirement (plan.md) | Evidence | Result |
|---|---|---|
| zero/negative invalid amounts | golden `900-zero-delivered-price`, `901-negative-amount`; `amount::tests::parses_canonical_strings_only` | PASS |
| large base-unit values and overflow | golden `071-eighteen-decimal-asset`, `072-amount-out-of-range`; `properties::evaluation_is_total_deterministic_and_consistent` | PASS |
| 100% discounts | golden `052-discount-exceeds-price`, `053-hundred-percent-discount-earns-no-rewards` | PASS |
| reward caps already consumed | golden `060`, `061`, `065`; `properties::cashback_never_exceeds_the_remaining_cap` | PASS |
| incompatible assets | golden `015-incompatible-asset`, `035-fee-in-unvaluable-asset` | PASS |
| stale quotes | golden `010-stale-quote`; `TestExpiryPauseAndReconciliation` | PASS |
| missing conversion fees | golden `031-missing-conversion-fee`; e2e "unknown network fee yields insufficient data" | PASS |
| duplicate discounts | golden `050-duplicate-discount-counted-once`, `904-conflicting-duplicate-discount` | PASS |
| unstable point valuation | golden `064-points-with-unstable-valuation` | PASS |
| partial refunds | `TestAssistedHandoffsAndOutcomes`; e2e refund checks | PASS |
| a quote changing before execution | `TestExpiryPauseAndReconciliation` (expired evaluation refuses handoff; refresh) | PASS |
| ties and unknown inputs in golden fixtures | golden `005-exact-tie`, `030`, `043`, `073`; `independent_check.py` | PASS |
| never switch rails after an uncertain result | `TestExpiryPauseAndReconciliation`, `TestConcurrentHandoffsOpenExactlyOneAttempt`; e2e "blocked until reconciled" | PASS |
| savings reported separately from estimated rewards; explicit counterfactual | `TestAssistedHandoffsAndOutcomes`; e2e "counterfactual baseline is explicit and unobserved" | PASS |
| recommendation-only release never claims a charge or a saving | `TestShadowModeEvaluations`; e2e "recommendation only" | PASS |
| same test vectors across runtimes | `golden_vectors` (Rust), `independent_check.py`, `TestGoldenRequestsRoundTripThroughGoTypes`, `TestOfflineEvaluation` | PASS |
| snapshots are immutable | `TestSnapshotsAreAppendOnly` | PASS |
| separate recommendation and execution permissions; tenant isolation | `TestAuthenticationAndScopes` | PASS |

## Rust (`cargo clippy -D warnings` + `cargo test --workspace`)

| Test | Result |
|---|---|
| `a_higher_fee_never_lowers_the_cost` | PASS |
| `a_larger_discount_never_raises_the_cost` | PASS |
| `amount::tests::checked_helpers_report_overflow` | PASS |
| `amount::tests::directed_division` | PASS |
| `amount::tests::json_numbers_are_rejected` | PASS |
| `amount::tests::micro_usd_round_trips_signed_values` | PASS |
| `amount::tests::parses_canonical_strings_only` | PASS |
| `amount::tests::range_boundaries` | PASS |
| `arbitrary_json_never_panics_the_deserializer` | PASS |
| `arbitrary_strings_never_panic_the_parser` | PASS |
| `cashback_never_exceeds_the_remaining_cap` | PASS |
| `eligible_stablecoin_payment_fits_spendable_funds` | PASS |
| `engine::tests::default_kept_when_it_is_cheapest` | PASS |
| `engine::tests::equal_costs_are_a_tie_even_with_a_default` | PASS |
| `engine::tests::missing_data_blocks_a_recommendation` | PASS |
| `engine::tests::no_default_picks_unique_lowest` | PASS |
| `engine::tests::snake_matches_wire_names` | PASS |
| `engine::tests::threshold_boundary_is_inclusive` | PASS |
| `engine::tests::tie_between_cheaper_alternatives_stays_a_tie` | PASS |
| `evaluation_is_deterministic_and_round_trips` | PASS |
| `evaluation_is_total_deterministic_and_consistent` | PASS |
| `golden_vectors` | PASS |
| `option_order_does_not_change_the_result` | PASS |
| `responses_never_contain_json_numbers_for_amounts` | PASS |
| `tests::evaluates_and_binds_result_to_input_hash` | PASS |
| `tests::malformed_json_is_a_400_and_invalid_input_a_422` | PASS |
| `tests::oversized_bodies_are_refused` | PASS |
| `tests::rejects_missing_wrong_and_malformed_credentials` | PASS |
| `tests::schema_export_covers_request_and_response` | PASS |
| `tests::short_tokens_are_refused_at_startup` | PASS |

## Reference check (`fixtures/tools/independent_check.py`)

    44 vectors checked independently, 12 malformed-request vectors skipped, 0 mismatches
    6 vectors checked independently, 0 malformed-request vectors skipped, 0 mismatches

## Go (`go test ./... -race`)

| Test | Result |
|---|---|
| `TestAssetEquality` | PASS |
| `TestAssistedHandoffsAndOutcomes` | PASS |
| `TestAuthenticationAndScopes` | PASS |
| `TestBackoffGrowsAndCaps` | PASS |
| `TestConcurrentHandoffsOpenExactlyOneAttempt` | PASS |
| `TestDecodeStrict` | PASS |
| `TestExpiryPauseAndReconciliation` | PASS |
| `TestGoldenRequestsRoundTripThroughGoTypes` | PASS |
| `TestOfflineEvaluation` | PASS |
| `TestParseUnitsMatchesEngineRules` | PASS |
| `TestRecordedProviderContract` | PASS |
| `TestRejectsResultNotBoundToRequest` | PASS |
| `TestServerErrorsAreUnavailable` | PASS |
| `TestShadowModeEvaluations` | PASS |
| `TestSignIsHMACOverTimestampAndBody` | PASS |
| `TestSnapshotsAreAppendOnly` | PASS |
| `TestSurfacesEngineRejections` | PASS |
| `TestUnitsRejectJSONNumbers` | PASS |

## TypeScript (`npm run typecheck && npm test`)

| File | Tests | Result |
|---|---|---|
| `src/explain.test.ts` | 2 tests | PASS |
| `src/money.test.ts` | 6 tests | PASS |
| `src/Comparison.test.tsx` | 7 tests | PASS |

## End-to-end (`scripts/e2e.sh`)

| Check | Result |
|---|---|
| meta reports assisted mode | PASS |
| preferences stored as version 1 | PASS |
| trail shoes: stablecoin preferred above threshold | PASS |
| savings reported separately: immediate +3.85, rewards -2.7688, total +1.0812 | PASS |
| evaluation is labelled recommendation only | PASS |
| idempotent replay returns the same evaluation | PASS |
| same key with a different body is refused | PASS |
| customer without consent: card is the only eligible option | PASS |
| unknown network fee yields insufficient data, not a zero fee | PASS |
| stored evaluation is readable and current | PASS |
| handoff without Idempotency-Key is refused | PASS |
| approval for a different amount is refused | PASS |
| approval by a different customer is refused | PASS |
| approved stablecoin payment handed off | PASS |
| second attempt on another rail is blocked until reconciled | PASS |
| executor revalidation sees an executable reference | PASS |
| settled payment at the estimate realizes immediate savings of 3.85 | PASS |
| counterfactual baseline is explicit and unobserved | PASS |
| replayed event is recorded once | PASS |
| contradicting outcome needs manual reconciliation | PASS |
| handoff is settled and no longer executable | PASS |
| refund above the charge is refused | PASS |
| partial refund recorded with its asset and conversion-cost status | PASS |
| a settled attempt no longer blocks a new approval | PASS |
| summary separates realized savings from estimated rewards | PASS |
| refresh creates a new evaluation linked to the original | PASS |

## Offline evaluation, milestone O1 (`offline-eval` over `fixtures/recorded/v1`)

| Basket | Outcome | Result |
|---|---|---|
| `r001-trail-running-shoes` | preferred | PASS |
| `r002-camp-stove` | preferred | PASS |
| `r003-water-filter` | preferred | PASS |
| `r004-headlamp-bundle` | preferred | PASS |
| `r005-pantry-restock` | insufficient_data | PASS |
| `r006-coffee-box` | preferred | PASS |

6 baskets: 6 matched, 0 failed; 5 produced a recommendation (map[insufficient_data:1 preferred:5])
