# Threat model and required cases

[Documentation home](index.md)

Mapping of the plan's required validation cases to the enforcing code and the test that proves it.

| Case | Enforced in | Proven by |
|---|---|---|
| Concurrent overspend attempts (task, agent-daily, org-daily, balance) | `store.Reserve` row-lock order org→agent→task→balance; usage recomputed under lock | `TestConcurrentOverspendIsImpossible`, `TestConcurrentOverspendAcrossTasksRespectsAgentDaily` (40/20 goroutines, `-race`) |
| Duplicate task requests (idempotency) | `payment_attempts UNIQUE(task_id, idempotency_key)`; `begin` replays the stored outcome; `authorize` on a non-initiated attempt is `409` | `TestIdempotentReplayNeverPaysTwice`; e2e "idempotent replay"; MCP default key = hash(task, method, url, body) |
| Cross-tenant access | Every read is scoped by `org_id`; agents only see own tasks/attempts; balances keyed by org | `TestCrossTenantIsolation` |
| Replayed grant | Rust jti set + Go consumes grant on `submitted` (`409`) | `grant::tests::accepts_valid_grant_once`, `TestGrantReplayAndExpiry` |
| Expired grant | JWT `exp` (≤ 300 s) checked by Rust; Go refuses `submitted` after expiry; worker releases unsubmitted holds after expiry+margin | `grant::tests::rejects_wrong_key_expired_and_wrong_audience`, `TestGrantReplayAndExpiry` |
| Changed beneficiary | Policy recipients per endpoint (Go `ValidateQuote`) and Rust `select_requirement`; grant binds `pay_to` and `req_hash` | `TestChallengeValidationRejects…`, `x402::tests::rejects_changed_beneficiary_even_if_listed_first`, `grant::tests::payload_mutation_after_grant_is_detected`, e2e |
| Wrong network / token / scheme | Policy pins one asset/network/scheme; both sides reject mismatches | same tests + e2e `wrong-network`, `wrong-asset` |
| Malicious resource instructions | The resource server only supplies a 402 challenge; nothing it returns can change policy. Oversized/malformed challenges rejected; `extra` is opaque | `x402::tests::parse_challenge_never_panics`, e2e `malformed` |
| Unknown payment result | `unknown` keeps the hold; reconciliation via facilitator lookup; never inferred from a lost HTTP response | `TestUnknownOutcomeKeepsHoldUntilReconciled`, e2e `lost` |
| Owner revocation during execution | Revoked agent credential rejected at `begin` and agent API; in-flight `submitted` attempt still resolvable | `TestOwnerRevocationAndPauseDuringExecution`, e2e |
| Facilitator outage | Verifier errors leave reservations untouched; only explicit `settled`/`not_found` evidence changes state | `workers.Reconcile` (logs and continues) |
| Insufficient fee balance | Hold is `amount + fee_allowance`; `insufficient_balance` when available < total | `TestInsufficientBalanceIncludingFees` |
| SSRF / redirects / DNS changes | Rust `urlguard`: https-only (dev flag for http), no userinfo/fragment, port allowlist, forbidden IP ranges (RFC1918, loopback, link-local incl. 169.254.169.254, CGNAT, multicast, v4-mapped v6, NAT64, ULA), DNS resolved once and **pinned** for the connection, redirects never followed | `urlguard::tests::*`, `literal_private_hosts_are_rejected_before_connecting`, e2e `redirect`, `metadata endpoint denied` |
| Bounded response size and duration | `client::Fetcher` caps body bytes (truncation flagged, `delivered=false`) and wall-clock deadline | e2e `huge`; `REQUEST_DEADLINE_SECONDS` |
| Paid but missing/unusable result | `ServiceOutcome` recorded separately from settlement; receipts show both; no automatic repurchase (idempotency + explicit MCP note) | `TestHappyPathSettlesAndWritesReceipt`, e2e `pay-then-fail`, `mcp` note test |
| Agent widening its own limits | Task budget/deadline capped by policy; no agent endpoint mutates policy or funding; policy change supersedes old tasks | `TestPolicyChangeClosesOldTasksAndAgentsCannotWidenLimits` |
| Keys / signing capability in model context | Gateway holds only an internal token and a signer credential; the signer API accepts a constrained authorization only; tokens/payment headers never logged | `cmd/mocksigner` constraints (`ALLOWED_TO`, `MAX_VALUE`); log scan in e2e run |
| Floating point deciding spending | Go `money.Amount` (big.Int), Rust `u128`, TS `bigint`/branded strings; JSON numbers rejected | `money_test.go`, `amount::tests`, `amounts.test.ts`, `TestInternalEndpointsRequireInternalTokenAndAmountsAreStrings` |
| Ledger consistency | Balanced journals enforced by `postJournal`; `LedgerInvariant` compares ledger sums with balance rows | asserted after every balance read in the Go API tests |

## Trust boundaries

- **Agent → gateway**: agent key only. Can request purchases within policy; cannot see other tasks; cannot change anything about budgets or recipients.
- **Gateway → control plane**: shared internal token; the gateway receives per-request context and single-use grants, never owner credentials or the grant signing key.
- **Gateway → signer**: a constrained EIP-3009-style authorization (`to`, `value`, `validBefore`, `nonce`). The provider can and should enforce its own recipient/value ceilings (the mock does).
- **Gateway → resource server**: untrusted. Pinned, bounded, redirect-free HTTP; the challenge is data, not instructions.
- **Owner → control plane**: owner key; the only principal that can fund, withdraw, set policy, approve beneficiaries, pause, or revoke.

## Known gaps before live use

- The signer and facilitator are mocks; real adapters must be validated in the integration spike (scheme payload details, settlement semantics, facilitator query API).
- Owner keys are long-lived bearer secrets; add rotation, scoped operator roles, and MFA for destructive actions.
- Daily windows are UTC calendar days; confirm this matches customer expectations.
- Rate limiting and per-agent concurrency caps are not implemented (the reservation lock serializes per organization, which bounds damage but not load).
