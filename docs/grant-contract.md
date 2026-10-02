# Reservation and execution-grant contract

[Documentation home](index.md)

This is the contract between the Go control plane (`internal/store.Reserve`, `internal/grant`) and the Rust gateway (`src/grant.rs`, `src/flow.rs`). Go owns authoritative budget state; Rust can neither grant nor increase budgets.

## Reservation

`Reserve(attempt, quote)` executes in a single `READ COMMITTED` transaction:

1. `SELECT … FOR UPDATE` on `payment_attempts` (must be `initiated`), then **organization → agent → task → account_balances** in that fixed order. Every reservation for an organization serializes on the organization row.
2. Re-check principals under the lock: organization active, agent active, task active and before deadline, task belongs to the agent, agent's current policy version equals the task's policy version.
3. Match the request URL and method against the policy's endpoint rules; validate the quote's scheme, network, asset, beneficiary, resource prefix, timeout and amount against the policy and the matched rule.
4. Compute usage under the lock: task (all time), agent (current UTC day), organization (current UTC day), each as `Σ held(amount+fee_allowance) + Σ committed(committed_amount+committed_fee)`.
5. `CheckBudgets`: `amount ≤ per_request_max`; `task_used + total ≤ min(task.budget, per_task_max)`; `agent_daily + total ≤ agent_daily_max`; `org_daily + total ≤ org_daily_max`; `available ≥ total`, where `total = amount + fee_allowance` with checked arithmetic.
6. Move `total` from `available` to `reserved`, insert the reservation (`held`, `expires_at = now + grant_ttl`), post a balanced ledger journal, insert the `execution_grants` row, set the attempt to `authorized`, append audit and outbox rows.

A policy denial marks the attempt `rejected` **in the same transaction** and holds nothing. Any other error rolls back entirely.

## Execution grant

Format: JWT, `alg: EdDSA` (Ed25519), issued by Go, verified by Rust with the raw public key (fetched from `/internal/v1/grant-public-key` or configured). No other algorithm is accepted (`alg=none`, HS256, RS256 are rejected by construction).

| Claim | Meaning | Rust check |
|---|---|---|
| `iss` | `asa-control-plane` | exact |
| `aud` | `asa-gateway` | must contain |
| `sub` | agent id | = begin context `agent_id` |
| `jti` | grant id (`grt_…`) | unused locally; = `grant_id` returned by authorize |
| `iat`, `nbf`, `exp` | issued / not-before (iat−5s) / expiry (= reservation expiry, ≤ 300 s) | standard, 5 s leeway |
| `org`, `task`, `att`, `rsv`, `pol` | organization, task, payment attempt, reservation, policy version | org/task/att exact |
| `method`, `url` | normalized request | case-insensitive method, exact URL |
| `pay_to`, `asset`, `network`, `scheme` | the selected requirement | case-insensitive exact |
| `amount_max`, `fee_max` | reservation amount and fee allowance (decimal strings) | `quote.maxAmountRequired ≤ amount_max`; value paid = `quote.maxAmountRequired` |
| `req_hash` | canonical requirements hash | exact |

### Canonical requirements hash

Both sides compute `SHA-256` over the UTF-8 string

```
lower(scheme) "\n" lower(network) "\n" lower(asset) "\n" lower(pay_to) "\n" amount "\n" resource "\n" max_timeout_seconds
```

(`amount` canonical decimal, `resource` verbatim, timeout decimal integer). The gateway sends its hash with the quote; Go recomputes and refuses with `requirements_hash_mismatch` if they differ, then binds the agreed hash into the grant. A resource server that changes any field after the challenge (or a bug that mutates the payload) produces a different hash and the grant no longer matches.

### Single use

- Rust keeps an in-memory `jti` set (pruned at `exp + 60 s`) and refuses a second use.
- Go consumes the grant row when the gateway records `submitted`; a second `submitted` for the same attempt/grant returns `409`, a different grant id `403`, an expired grant `409`.
- Grants are never persisted as tokens; only their ids and consumption timestamps are stored.

## Write-ahead submission and recovery

| Attempt state | Meaning | Recovery |
|---|---|---|
| `authorized` | reservation held, grant issued, nothing transmitted | released by the worker once `grant.expires_at + margin` passes and the grant is unconsumed |
| `submitted` | gateway recorded intent and (possibly) transmitted the payment | if no outcome arrives within `SUBMITTED_GRACE`, promoted to `unknown` |
| `unknown` | transmitted; outcome lost | worker asks the settlement verifier by `payment_hash`; `settled` → commit, `not_found` after `UNKNOWN_RELEASE_AFTER` → release, otherwise keep holding |
| `settled` / `failed` / `expired` / `rejected` | terminal | immutable |

`payment_hash = SHA-256(hex)` of the encoded `X-PAYMENT` header string, computed by the gateway and used as the lookup key at the facilitator. The grant's `exp` also bounds the signed authorization's `validBefore`, so a stale authorization cannot settle after the control plane has given up on it.
