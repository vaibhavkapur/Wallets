# Demo script

[Documentation home](index.md)

Preparation: `make up`, then `make dashboard` in a second terminal and sign in with the owner key from `POST /v1/organizations`. The shell steps below use `CP`, `GW`, and `RES` as in the README's Quick Example. All demos run against the mock resource; each `?mode=` value triggers one adversarial behaviour.

## Demo A — budgeted purchase (≈1 min)

1. In the dashboard, open *Agents & budgets*, create `research-agent`, and save a policy: per request 0.50, per task 1, agent/day 5, org/day 20, endpoint `http://127.0.0.1:9402/v1` with recipient `0x209693Bc6afc0C5328bA36FaF03C514EF312287C`. Note that the agent cannot spend until a policy exists.
2. On *Overview*, record a deposit of 3 USDC. Available 3, reserved 0.
3. As the agent, create a task and pay for `/v1/weather?city=Lisbon`. The response shows `payment.status: settled`, `service.delivered: true`, the amount `100000`, and a receipt with the policy version.
4. *Tasks* shows cost per task; *Transactions* shows the attempt with beneficiary, quoted and settled amounts, and `delivered`.

## Demo B — the resource misbehaves (≈2 min)

1. `?mode=changed-beneficiary`: the challenge names a different `payTo`. The gateway answers `403 challenge_rejected`; *Transactions* shows a rejected attempt and the balance is unchanged.
2. `?mode=wrong-network`, `?mode=wrong-asset`, `?mode=overpriced`, `?mode=malformed`: each is rejected before any reservation.
3. `?mode=redirect`: `502 redirect_refused`; the gateway never follows redirects.
4. A URL outside the policy (`/v2/other`) or an internal address (`http://169.254.169.254/…`): `403 endpoint_not_allowed`.
5. `?mode=pay-then-fail`: the payment settles but the resource returns 500. The receipt shows `payment_status: settled` and `service.delivered: false`; *Transactions* marks it "not delivered". Repeating the same idempotency key returns the same receipt instead of paying again.

## Demo C — lost response and reconciliation (≈1 min)

1. `?mode=lost`: the resource settles and closes the connection. The gateway reports `payment.status: unknown`; *Overview* shows the amount in *reserved*.
2. Within a few seconds the reconciliation worker queries the facilitator lookup by payment hash and commits the hold. `GET /v1/payment-attempts/{id}` moves to `settled` with the transaction reference; reserved returns to 0.

## Demo D — owner controls (≈1 min)

1. Exhaust the task budget with repeated `/v1/weather` calls: the last one returns `403 task_budget_exceeded`; `GET /v1/tasks/{id}/budget` shows the remaining capacity.
2. Click *Pause all spending*: a new task returns `org_paused`. *Resume*.
3. *Revoke* the agent: the gateway answers `401` for its key. Any payment already submitted is still resolved and visible in *Transactions*.
4. Save a new policy version: tasks created under the previous version stop spending (`task_closed`), new tasks obey the new limits.

## Extras

- `?mode=huge` demonstrates the response size bound (`truncated: true`, `delivered: false`).
- `GET /v1/ledger` and *Ledger & audit* show the balanced journals for every reserve, settle, and release.
- `make example` runs Demos A, B.1, B.5-style replay, and D.1 headlessly and prints the receipts and cost per task.
