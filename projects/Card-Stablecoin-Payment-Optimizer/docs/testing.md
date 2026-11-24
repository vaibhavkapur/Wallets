# Testing

[Documentation home](index.md)

## Unit and integration suites

```bash
make test-rust       # cargo clippy --all-targets -D warnings + cargo test (unit, golden, property)
make test-fixtures   # independent Python check of the golden vectors and the recorded dataset
make test-go         # go vet + go test ./... -race (real engine binary + embedded PostgreSQL)
make test-ts         # typecheck every workspace, run the Vitest suites
make test            # all of the above
```

**Golden vectors** (`fixtures/golden/v1`, 56 files) are the shared truth. Each holds a full contract request and either a hand-worked expectation (outcome, rationale, preferred, tied and lowest-cost options, savings, expiry, sensitivity codes, and per option the status, exclusion codes, immediate cost, estimated rewards, net cost and amount due) or an expected request error. They are built by `fixtures/tools/build_golden.py` from one base scenario and cover: the plan's example, thresholds below/at/above the saving, an exact tie, no default, stale quotes, basket mismatch, unverified and rejected acceptance and jurisdiction, consent, purchase protection, credit avoidance, rail restriction, unknown and missing fees, funding steps including credit-card funding, FX fees, fees in unvaluable assets, insufficient funds and liquidity reserves, unverified funds, duplicate and already-included and non-usable and unverified discounts, 100% discounts, reward caps (partial, exhausted), unverified rewards, points with stable and unstable valuations and caps, rounding, non-par stablecoin rates, 18-decimal assets, overflow, missing rates, and twelve malformed requests.

`fixtures/tools/independent_check.py` recomputes every calculation vector with `fractions.Fraction` from the documented rules and shares no code with the engine. The Rust test `golden_vectors` runs every vector through the engine and additionally checks that lines add up to totals, eligible options have breakdowns and the preferred option is eligible; `evaluation_is_deterministic_and_round_trips` and `responses_never_contain_json_numbers_for_amounts` cover serialization. Property tests (`proptest`, 2000 cases each) vary amounts up to `i128::MAX`, decimals, rates, caps, points, funds, reserves and thresholds and assert totality (no panic), determinism, breakdown consistency, invariance under option order, monotonicity (a higher fee never lowers the cost, a larger discount never raises it), cap bounds and that an eligible stablecoin payment fits the spendable balance. Service tests cover authentication, the input hash, 400/422 mapping and the body limit.

**Go** (`orchestrator/internal/integration`) starts the engine binary (building it if needed) and an embedded PostgreSQL once per package and drives the HTTP API: provider contract for the recorded adapter, milestone O1 over the recorded dataset, golden requests round-tripping through the Go types without loss, authentication and scopes with cross-tenant isolation, the shadow-mode flow (consent defaults, threshold application, versioned preferences, missing default, liquidity reserves, idempotent replay and key reuse, refusal of handoffs, customer-reported selections, refresh, report), the assisted flow (approval binding, excluded options, outcome ingestion with realized savings and explicit counterfactual, replay and conflicting outcomes, partial and excessive refunds, charges that differ from the estimate, report aggregates), expiry, pause and reconciliation (sweep to `unknown`, resolution by a failed outcome), twelve concurrent handoffs opening exactly one attempt, and the append-only triggers. Unit tests cover canonical amounts, asset equality, strict decoding, the engine client's hash and error mapping, and outbox signing and backoff.

**TypeScript** covers exact formatting (`$0.012375`, values beyond 2^53, thousands separators, signs) and canonical-input rejection, completeness of the explanation catalog against the schema enums, rendering of real engine responses captured as fixtures (totals, estimate labels, savings split, exclusions, funding steps, no approval after expiry), and the preferences form's dollars-to-minor-units conversion and payload.

## End-to-end scenario

```bash
make up
make e2e
make offline-eval
make down
```

`scripts/e2e.sh` runs 26 checks against the stack in assisted mode: evaluation of the trail-shoes basket with the expected savings split, recommendation-only disclosure, idempotent replay and key reuse, a customer without consent, an unknown network fee yielding `insufficient_data`, approval refusals (no key, wrong amount, wrong customer), a successful handoff, a second attempt blocked until reconciled, executor revalidation, a settled payment realizing $3.85 against the explicit counterfactual, event replay, a contradicting outcome, refunds above and within the charge, a new approval after settlement, the summary report, and refresh. It exits non-zero on any failure. `make offline-eval` runs milestone O1 against the stack's engine.

## Recorded report

`scripts/test-report.sh` runs every suite, the end-to-end scenario and the offline evaluation, and rewrites [Test Report](test-report.md) with the dated results. Treat that file as a record of one run, not proof that tests ran on every later commit; current command output is authoritative.

## Expected invariants

Equal requests give equal results; an unknown fee is never zero; an exact tie is never broken arbitrarily; the threshold is inclusive; a handoff never names an excluded option or an amount other than the engine's; two unresolved attempts never coexist for one customer and basket; snapshots never change after they are written; realized savings are never reported for a shadow observation, a failed payment, a differing charge or the baseline method itself; no amount passes through a floating-point number in any runtime. No merchant, card network, wallet or stablecoin network is covered by the fixture suites.
