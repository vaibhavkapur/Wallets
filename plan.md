# Agent Spending Accounts — Project Plan

Updated: 2 October 2026  
Status: Proposed standalone developer MVP  
Primary stack: Go control plane, Rust payment gateway, TypeScript dashboard and SDK

## Product plan

### Customer and value

Serve developers and organizations running software agents that need paid APIs, data, or compute. Give each agent narrowly scoped spending authority and give the account owner visibility into cost per task.

Example policy: “This research agent may spend up to $5 on approved data providers for this task, with a $20 daily organization limit.” These amounts are illustrative policy values.

### MVP scope

- Organization and agent identities with owner-managed funding and permissions.
- One stablecoin/network and a provider-managed wallet/signing path.
- Per-request, per-task, per-agent, and organization-wide limits, including fees and pending payments.
- An allowlist of resource endpoints and bound payment recipients; a model cannot approve a new beneficiary.
- Three to five paid API integrations using one supported x402 scheme.
- Receipts linking task, resource, quote, policy version, payment, and service response.
- A small developer SDK, an authenticated API, and an optional MCP interface.
- Pause, revoke, balance, budget, and transaction views for account owners.

Withdrawals, deposits, and destination changes are owner-controlled. Agents cannot increase their limits or enable automatic top-ups. Customer organizations have isolated budgets and balances; wallet architecture and accounting must prevent cross-tenant spending.

### Paid-resource flow

1. The agent requests a resource through the spending gateway.
2. The gateway validates the endpoint, quoted payment requirements, beneficiary, network, asset, and supported scheme.
3. The policy engine atomically reserves the maximum authorized cost across all applicable budgets.
4. The isolated signing component authorizes only the approved payment payload.
5. The gateway submits the paid request, tracks settlement, and records the response outcome.
6. Reconcile actual cost and release unused reservations only when safe.

Use bounded request and task deadlines. A server requesting payment is not inherently trustworthy. Restrict redirects and prevent agent-supplied URLs from accessing internal services. Payment success and useful service delivery are separate outcomes; record both and avoid paying again merely because a response was lost.

### Build milestones

- **S1 — Developer discovery:** three design partners identify paid-resource workflows and agree on required budget controls.
- **S2 — Test network:** complete paid requests and adversarial budget tests without real funds.
- **S3 — Capped pilot:** run a small allowlisted service catalog using owner-funded balances.
- **S4 — Developer release:** publish integration documentation and evaluate retention, support cost, and pricing.

### Success and release gates

- Concurrent requests cannot bypass a task, agent, or organization budget.
- Wrong asset/network, expired authorization, replayed proof, changed beneficiary, and malicious resource instructions are rejected.
- Private keys and general signing capability never enter model context.
- Owner revocation prevents new spending; already submitted transactions remain tracked until resolved.
- Measure paid-task completion, useful-result rate, cost per successful task, latency added by payment, and developer retention.
- Track paid requests that fail to deliver usable results and provide an explicit dispute or support path where supported. Do not promise atomic service delivery from payment settlement alone.

### Commercial hypothesis and dependencies

Test a developer subscription with included usage and transparent overage fees. Existing wallet infrastructure is a dependency, so differentiation must come from budget governance, task-level cost visibility, reliable integrations, and operational controls.

Exclude trading, DeFi, arbitrary contract calls, lending, and open-ended agent-to-agent transfers. Require design-partner demand and viable provider economics before building a general-purpose marketplace.

## Scope and assumptions

- Start in one provider-supported jurisdiction with a small pilot. USD is the modeling currency; launch geography and legal entity remain decisions.
- Use established payment, credential, custody/signing, conversion, and payout providers as applicable. Confirm commercial access and the permitted funds flow before live use.
- Estimates assume approximately three engineers plus product, part-time design, and payments operations support. External approvals and pilot observation add calendar time.
- No own token issuance, lending, speculative trading, or yield product is included.
- This project can be implemented independently. Reuse sibling projects through versioned APIs or libraries when useful; do not require all five products to launch.
- Product targets, example limits, timelines, and architecture below are proposals, not measured results or provider commitments.

## Independent launch and protocol choices

This product is independent of retail checkout, campaigns, and recurring consumer purchases. Its design partners are developers and organizations whose agents purchase paid digital resources.

- **x402:** use one agreed payment scheme, asset, network, and facilitator for the pilot. Pin protocol versions and validate the supported SDK/provider combination with an end-to-end integration spike. [x402 documentation](https://docs.x402.org/introduction)
- **Wallet/signing provider:** use an isolated provider-managed signing path with explicit tenant boundaries and owner-controlled funding. Verify asset/network support, authorization constraints, recovery, and reconciliation.
- **MCP:** optional tool interface alongside a normal authenticated API and SDK. Tool access never grants the ability to change budgets or approve beneficiaries.
- ACP, UCP, and AP2 retail checkout integrations are not prerequisites for this MVP.
- Confirm stablecoin transfer and redemption implications for account owners. [USDC terms](https://www.circle.com/legal/usdc-terms)

## Technical design and language ownership

- **Go — control plane:** organization/agent identity, owner administration, budget policies, atomic reservations, double-entry accounting where customer funds are represented, approvals, receipts, audit records, billing, and reconciliation.
- **Rust — payment/resource gateway:** parse and validate paid-resource requirements, enforce endpoint and beneficiary allowlists, reject redirects outside policy, validate the exact approved payload, coordinate the isolated signer, submit paid requests, and capture bounded responses.
- **TypeScript — developer experience:** React owner dashboard, a small typed SDK, examples for agent applications, and an optional MCP adapter. If the selected x402/provider integration requires a TypeScript SDK, isolate that adapter behind an internal authenticated interface; it cannot independently grant spending permission.
- **PostgreSQL:** accounts, policy versions, reservations, payment attempts, task receipts, provider events, and ledger entries.
- **Go workers:** reservation recovery, payment reconciliation, statements, notifications, and operational exception handling.

Go owns the authoritative budget state. Rust cannot grant or increase budgets. The Rust gateway acts only on a short-lived, single-use execution grant issued after an atomic Go reservation. Bind the grant to organization, agent, task, endpoint, recipient, asset, network, scheme, amount ceiling, fee allowance, expiry, and a unique identifier.

Use an established authenticated grant/signature format and reviewed libraries; do not invent cryptography. The Rust process and any TypeScript provider adapter never receive a general owner credential. Wallet keys remain with the selected signing provider.

### Execution boundaries

1. TypeScript SDK sends a task-scoped resource request under an agent credential.
2. Rust validates the destination and payment challenge before requesting authorization.
3. Go validates policy and atomically reserves all applicable budgets.
4. Rust verifies the execution grant and the unchanged payment payload.
5. An isolated provider signs only the constrained approved action.
6. Rust records the submission identifier and paid-resource outcome.
7. Go reconciles settlement and commits or releases the reservation safely.

If an outcome is unknown, retain the reservation until reconciliation resolves it. Never infer failure from a lost HTTP response. Authentication between internal components and an audit event for each boundary are required.

### Records and API surface

Core records: Organization, AgentPrincipal, Task, BudgetPolicyVersion, AccountBalance, Reservation, ExecutionGrant, ResourceQuote, PaymentAttempt, ServiceOutcome, Receipt, and LedgerEntry.

Owner-only endpoints include agent creation, budget updates, beneficiary approval, deposits/withdrawals through the provider flow, and revocation.

Agent-facing proposed endpoints:

- POST /v1/tasks — create a task within an owner's policy.
- GET /v1/tasks/{id}/budget — show spendable capacity without granting access to other tasks.
- POST /v1/tasks/{id}/paid-requests — execute an approved resource purchase idempotently.
- GET /v1/tasks/{id}/receipts — retrieve payment and service-delivery evidence.
- GET /v1/payment-attempts/{id} — inspect a pending or reconciled outcome.

Amounts use integer fiat minor units or stablecoin base units with explicit asset, network, and decimals. Serialize potentially large integers as decimal strings in JSON; TypeScript must not silently convert them to floating-point numbers. Use explicit rounding rules and checked arithmetic. PostgreSQL is the durable source of truth; caches never decide an available balance.

For network-native amounts that can exceed ordinary integer ranges, use checked wide-integer arithmetic in Rust and explicit base-unit strings at service boundaries. Go validates amount bounds before database storage. No floating-point calculation may decide whether spending is allowed.

## Engineering invariants

- The model proposes actions; deterministic backend code authorizes spending. Untrusted merchant or tool content cannot change permissions.
- Bind approval to an immutable transaction or rule version. Validate identity, merchant/beneficiary, amount, currency, expiry, and revocation before execution.
- Reserve budgets atomically, including pending actions and fees. Concurrent requests must not spend the same available balance twice.
- Use durable records, idempotency keys, a transactional outbox, verified webhooks, and reconciliation workers. Do not hold a database transaction open across a provider request.
- Treat a provider timeout as an unknown outcome. Reconcile before retrying with a new payment attempt, changing payment methods, or releasing a reservation.
- Separate payment settlement from order fulfillment or service delivery. Track partial refunds and failures explicitly.
- Keep raw card details, wallet keys, reusable credentials, and signing access outside model context and ordinary logs.
- Enforce tenant isolation, role-based operator access, limited retention, audit evidence, and a product-level pause control.
- Before live use, confirm geography, contractual responsibilities, custody boundaries, screening and disclosure obligations, customer recovery, and support ownership for the actual money flow.

## Implementation backlog and effort

Allow approximately 4–6 weeks after selecting a supported x402, wallet, and facilitator combination and confirming three design partners. Add 2–3 weeks if identity, ledger, budget, and worker foundations must be created. Re-estimate after the integration spike, especially if no suitable Rust protocol implementation is available.

- [ ] Validate the paid-resource use case and willingness to pay with three developer teams.
- [ ] Prove one complete test-network payment using the chosen scheme, network, and signer.
- [ ] Specify the Go reservation and short-lived execution-grant contracts.
- [ ] Implement tenant isolation, task/agent/organization caps, and owner-controlled funding.
- [ ] Implement the Rust gateway, endpoint validation, bounded responses, and submission tracking.
- [ ] Integrate a supported SDK through Rust or a constrained TypeScript adapter after compatibility validation.
- [ ] Build the TypeScript client, owner dashboard, and example agent workflow.
- [ ] Add receipts, service-outcome tracking, reconciliation, revocation, and operator pause controls.
- [ ] Complete S1–S4 with a small allowlisted catalog and conservative live limits.

## Validation plan

Use Go race and database integration tests to prove atomic multi-budget reservations. Use Rust cargo test, clippy, property tests, and fuzzing for payment challenge parsing, grant binding, amounts, URLs, and payload mutation. Use TypeScript type checks, SDK contract tests, and dashboard/browser tests.

Required cases: concurrent overspend attempts, duplicate task requests, cross-tenant access, replayed or expired grants, changed beneficiary, wrong network/token, unknown payment result, owner revocation during execution, facilitator outage, and insufficient fee balance.

Test SSRF and redirect controls, including DNS resolution changes and internal-address targets. Bound response size and duration. Test paid-but-missing or unusable results; settlement does not guarantee that a resource server delivered correct work. Do not automatically repurchase on a service error.

## Decisions to resolve

- Which paid resources provide repeatable value for the first design partners?
- Which x402 scheme/network/facilitator supports the required client and signing path?
- What are the actual custody, withdrawal, and recovery responsibilities?
- Which exact constraints can the signing provider enforce in addition to application checks?
- What happens to an unresolved payment at task expiry or account closure?
- What pricing yields positive contribution after provider, network, support, and infrastructure costs?

## Reference freshness

Protocol and provider references were reviewed for the original plan on 2 October 2026. Revalidate versions, SDK coverage, pricing, eligibility, and partner access at implementation kickoff and before live launch. A sandbox integration does not establish production permission.
