# Card-versus-Stablecoin Payment Optimizer — Project Plan

Updated: 2 October 2026  
Status: Proposed standalone MVP  
Primary stack: Rust calculation engine, Go orchestration, TypeScript interface and SDK

## Product plan

### Customer and value

Help customers choose between an eligible card and a stablecoin payment for the same basket. Optimize customer value after fees and constraints rather than the merchant's processing cost alone.

### MVP scope

- The same merchants and purchase category used by Agent Checkout.
- At least one merchant with a real stablecoin acceptance path and a measurable payment-specific offer or cost difference.
- One stablecoin, network, and quote provider; customer approval for every payment.
- Compare existing spendable balances first. Optional funding or conversion is shown as a separate quoted step.
- Explanations showing why a method wins, the assumptions used, and what could change the result.
- A minimum customer-configurable savings threshold before recommending a less familiar payment method.

### Decision model

For each eligible option, calculate:

`Estimated net cost = delivered price + incremental customer payment/FX/funding/network fees - immediately usable discounts - conservatively valued eligible rewards`

Do not double-count discounts already included in the merchant quote. Exclude unavailable methods, unsupported jurisdictions, stale quotes, insufficient funds, and options outside the customer's consent.

Keep purchase protection, return terms, settlement uncertainty, credit preferences, and liquidity needs as explicit constraints or displayed trade-offs. Do not turn them into invented cash values.

Illustrative example, not a provider quote: a $100 card purchase with $3 of eligible rewards has an estimated net cost of $97. A $4 stablecoin discount with $1.50 of incremental costs produces $97.50. The card wins by $0.50 before non-price considerations.

### Build milestones

- **O1 — Offline evaluation:** evaluate a versioned dataset of real merchant baskets using recorded quotes and independently checked calculations.
- **O2 — Shadow mode:** generate recommendations without changing how customers pay; review stale data and incorrect eligibility.
- **O3 — Assisted pilot:** present both methods and execute the customer's approved choice.
- **O4 — Integration:** offer the optimizer to Agent Checkout and the Recurring Purchase Agent through a deterministic API.

### Success and release gates

- Verify cost calculations, fee rounding, reward caps, point valuations, and quote expiry across boundary cases.
- Measure recommendation coverage, quote-to-execution differences, customer selection, and savings against the customer's stated default method.
- Report immediate realized savings separately from estimated rewards and future redemption value.
- Never switch rails after an uncertain payment result. Reconcile first and obtain new approval where required.
- Validate refunds, their asset/currency, conversion costs, and timing for both methods.

### Commercial hypothesis and dependencies

Bundle optimization into a subscription or charge partner agents for evaluations. Merchant-funded discounts can improve economics, but model the consumer benefit independently of platform revenue.

Do not automatically fund stablecoin purchases with a credit card in the MVP. It creates separate fee, reward-eligibility, and transaction-classification questions. Continue only if real baskets show savings or another customer-valued advantage after all costs.

## Scope and assumptions

- Start in one provider-supported jurisdiction with a small pilot. USD is the modeling currency; launch geography and legal entity remain decisions.
- Use established payment, credential, custody/signing, conversion, and payout providers as applicable. Confirm commercial access and the permitted funds flow before live use.
- Estimates assume approximately three engineers plus product, part-time design, and payments operations support. External approvals and pilot observation add calendar time.
- No own token issuance, lending, speculative trading, or yield product is included.
- This project can be implemented independently. Reuse sibling projects through versioned APIs or libraries when useful; do not require all five products to launch.
- Product targets, example limits, timelines, and architecture below are proposals, not measured results or provider commitments.

## Independent launch and integrations

The optimizer can launch as a recommendation API and comparison interface using provider quotes. It does not need to execute payments or wait for Agent Checkout to exist. When combined with checkout, the approved result is handed to the execution service through a versioned contract.

The initial merchant set should support comparable card and stablecoin quotes for the same basket. A recommendation-only release must not claim to have charged a customer or realized savings.

- Merchant checkout adapters use **ACP or UCP** only where needed to retrieve or execute a valid checkout. [ACP architecture](https://www.agenticcommerce.dev/docs/concepts/architecture)
- Card credential handling stays with the execution/payment provider. [Scoped credential example](https://docs.stripe.com/agentic-commerce/concepts/shared-payment-tokens.md?agent-seller=agent)
- Select one stablecoin/network and a provider for quotes, acceptance, conversion where needed, and refunds. Account for issuer redemption eligibility and transfer risks. [USDC terms](https://www.circle.com/legal/usdc-terms)
- x402 is not a dependency for comparing retail payment methods.
- MCP is an optional interface for requesting an evaluation; it grants no spending authority by itself.

## Technical design and language ownership

- **Rust — authoritative calculation engine:** a pure, deterministic crate for eligibility, fee normalization, reward caps, exact arithmetic, scoring, and explanation data. Every evaluation includes input, policy, formula, and rounding versions.
- **Go — orchestration:** quote-provider adapters, quote expiry, customer preferences, evaluation persistence, caching, execution handoff, and reconciliation of estimated versus actual costs.
- **TypeScript — React comparison interface and SDK:** cost breakdowns, estimated-benefit labels, customer constraints, and integration into checkout or external agent tools.
- **PostgreSQL:** immutable quote/evaluation snapshots, preference versions, observed outcomes, and experiment attribution.

Run the Rust engine as a small internal service behind a versioned evaluation contract so Go does not require an in-process FFI boundary. Keep it stateless and free of provider credentials. An optional later WebAssembly build can support local previews, but only the server evaluation can authorize an execution handoff.

Use the same test vectors across the native engine and any WebAssembly build. Do not reproduce financial formulas independently in TypeScript.

### Calculation inputs and outputs

Inputs: normalized delivered price, payment-specific discounts, incremental fees, asset/network, reward rules and caps, customer point valuation, quote validity, available funds, and hard customer constraints.

Outputs: eligible options, exclusions with reasons, comparable net costs, preferred option or a tie/insufficient-data result, valuation assumptions, quote expiry, and machine-readable explanation components.

Unknown required fees or unverified eligibility cannot be treated as zero cost. Return insufficient data or exclude that option with a clear explanation.

Amounts use integer fiat minor units or stablecoin base units with explicit asset, network, and decimals. Serialize potentially large integers as decimal strings in JSON; TypeScript must not silently convert them to floating-point numbers. Use explicit rounding rules and checked arithmetic. PostgreSQL is the durable source of truth; caches never decide an available balance.

Use integer/rational or explicitly scaled decimal arithmetic in the Rust engine with checked overflow and documented rounding. Separate merchant/customer costs and immediate discounts from future reward estimates. A lower transaction fee alone does not establish a better customer outcome.

### Records and API surface

Core records: BasketSnapshot, RailQuote, RewardRuleVersion, PreferencePolicy, Evaluation, ExecutionHandoff, and ObservedOutcome.

Proposed application endpoints:

- POST /v1/evaluations — evaluate a basket against current quotes and preferences.
- GET /v1/evaluations/{id} — return versioned results and explanations.
- POST /v1/evaluations/{id}/refresh — obtain fresh quotes and a new evaluation.
- POST /v1/evaluations/{id}/execution-handoffs — create a constrained reference for an approved payment flow.
- POST /v1/outcomes — record authenticated provider/executor outcomes for savings measurement.

Recommendation access and execution access use separate permissions. Evaluation identifiers are never payment credentials.

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

Allow approximately 3–4 weeks after quote access and basic application infrastructure; add 2–3 weeks if the foundation must be built independently. The estimate includes a narrow Rust core and excludes broad network coverage.

- [ ] Obtain comparable real card and stablecoin quotes from at least one merchant.
- [ ] Define the exact formula, eligibility rules, point valuations, fee taxonomy, and rounding behavior.
- [ ] Create independently calculated golden fixtures, including ties and unknown inputs.
- [ ] Implement the Rust calculation crate and authenticated internal evaluation service.
- [ ] Implement Go quote adapters, freshness checks, immutable evaluations, and outcome ingestion.
- [ ] Generate a TypeScript client and build the explanation/comparison interface.
- [ ] Run shadow mode and review incorrect recommendations before any execution integration.
- [ ] Add approved checkout handoff with explicit revalidation and no silent payment-method fallback.
- [ ] Complete the existing O1–O4 evaluation milestones.

## Validation plan

Use Rust unit/property tests and fuzzing for arithmetic boundaries, malformed inputs, integer overflow, rounding, and reward caps. Use Go adapter contract and concurrency tests; use TypeScript type checks and browser tests for displayed totals and risk preferences.

Required cases: zero/negative invalid amounts, large base-unit values, 100% discounts, reward caps already consumed, incompatible assets, stale quotes, missing conversion fees, duplicate discounts, unstable point valuation, partial refunds, and a quote changing before execution.

Compare estimates to observed payment costs. Record the counterfactual baseline explicitly; do not report an unobserved alternative payment as a realized saving.

## Decisions to resolve

- Do real merchants offer a meaningful customer benefit for stablecoin payment?
- Which asset, network, conversion partner, and refund model are supported?
- Which reward rules and valuations can be verified?
- Is this initially a standalone recommendation product or part of a checkout experience?
- Which savings threshold and non-price constraints do customers choose?

## Reference freshness

Protocol and provider references were reviewed for the original plan on 2 October 2026. Revalidate versions, SDK coverage, pricing, eligibility, and partner access at implementation kickoff and before live launch. A sandbox integration does not establish production permission.
