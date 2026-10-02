# Agent Checkout — Project Plan

Updated: 2 October 2026

Status: Proposed standalone MVP

Primary stack: Go and TypeScript; optional Rust verification component

## Product plan

### Customer and value

Serve consumers who want help completing a specific purchase while selecting an eligible card that fits their rewards and protection preferences. Initial distribution is a first-party application; external shopping assistants are a later integration channel.

Example request: “Buy this exact headphone model from an approved retailer for at most $200 delivered. Show me the best eligible card before paying.”

### MVP scope

- One shopping category and one to three integrated merchants.
- Product URL or structured product selection, with unambiguous model, variant, and quantity.
- Live delivered-price quote and stock confirmation.
- Card comparison using versioned reward rules and disclosed valuation assumptions.
- A review screen showing merchant, product, delivery, total, selected card, estimated rewards, and return terms.
- Customer approval, provider-managed credential collection, scoped payment execution, and issuer authentication when required.
- Order receipt, status tracking, cancellation requests where available, and a verified refund path.

The MVP excludes unattended purchases, universal merchant coverage, unrestricted browser purchasing, and a new card-issuing program.

### Purchase flow

1. Convert the request into a structured intent and resolve missing purchase-critical information.
2. Retrieve merchant quotes and normalize the total, variant, delivery, and return terms.
3. Rank eligible enrolled cards using reward rules, customer preferences, and actual payment compatibility.
4. Present the exact checkout and obtain authenticated approval.
5. Revalidate quote and permission, reserve the budget if applicable, and obtain the provider's scoped credential.
6. Complete checkout and handle any additional customer authentication.
7. Reconcile payment and order outcomes; record the receipt and release or commit reservations appropriately.

### Build milestones

- **A1 — Discovery:** one merchant and one provider confirm an executable credential and checkout path.
- **A2 — Sandbox:** complete a purchase, authentication challenge, cancellation, and refund using test credentials.
- **A3 — Closed pilot:** launch with approximately 20 opted-in users and conservative transaction limits.
- **A4 — Evaluation:** review failures, repeat usage, reward discrepancies, support burden, and willingness to pay.

### Success and release gates

- Every submitted purchase has a matching valid approval and correct checkout version.
- Duplicate requests, expired approvals, altered carts, and ambiguous payment timeouts pass adversarial and recovery tests.
- Establish a baseline completion rate and report failures by merchant, issuer, provider, and product logic.
- Track time from intent to receipt, repeat purchases, support incidents, and estimated versus verified rewards.
- Prove at least one completed refund and a reconciled ledger before expanding the pilot.

### Commercial hypothesis and dependencies

Test a consumer subscription first; evaluate API fees for partner agents later. Any merchant commission must be disclosed and must not secretly influence customer payment rankings.

The main dependencies are card credential access, merchant checkout participation, reward data quality, merchant categorization, and a provider-supported authentication flow. If payment access fails, validate the recommendation and approval experience through hosted checkout while keeping agent execution explicitly unproven.

## Scope and assumptions

- Start in one provider-supported jurisdiction with a small pilot. USD is the modeling currency; launch geography and legal entity remain decisions.
- Use established payment, credential, custody/signing, conversion, and payout providers as applicable. Confirm commercial access and the permitted funds flow before live use.
- Estimates assume approximately three engineers plus product, part-time design, and payments operations support. External approvals and pilot observation add calendar time.
- No own token issuance, lending, speculative trading, or yield product is included.
- This project can be implemented independently. Reuse sibling projects through versioned APIs or libraries when useful; do not require all five products to launch.
- Product targets, example limits, timelines, and architecture below are proposals, not measured results or provider commitments.

## Protocol and partner choices

- **ACP over REST:** default merchant adapter for creating, updating, and completing checkout. The merchant owns price, stock, fulfillment, and merchant-of-record duties. Start with the actual protocol supported by the first merchant. [ACP architecture](https://www.agenticcommerce.dev/docs/concepts/architecture)
- **UCP:** alternate merchant adapter when required by a partner. Its documented AP2 extension is a candidate when verified delegated authorization is required. [UCP AP2 extension](https://ucp.dev/specification/payment/extensions/ap2-mandates/)
- **AP2:** implement only with a verified end-to-end participant flow. Local approval records do not establish protocol conformance; pin the supported mandate version. [AP2 specification](https://ap2-protocol.org/ap2/specification/)
- **Scoped card credentials:** evaluate Stripe Shared Payment Tokens or an equivalent partner. Confirm chosen-card enrollment, seller scope, amount/currency/expiry constraints, revocation, and issuer authentication. Provider access remains a launch dependency. [Stripe tokens](https://docs.stripe.com/agentic-commerce/concepts/shared-payment-tokens.md?agent-seller=agent), [agent integration availability](https://docs.stripe.com/agentic-commerce)
- **MCP:** optional authenticated tool adapter for external assistants. It is separate from payment consent. A first-party agent can call the backend directly.

Existing device-wallet enrollment and card metadata do not establish server-side spending access. If only hosted merchant checkout is available, label the pilot as assisted checkout and keep autonomous execution unproven.

## Technical design and language ownership

- **Go — application backend:** HTTP API, merchant adapters, quote retrieval, deterministic reward ranking, consent validation, payment orchestration, webhook ingestion, order status, refund handling, and reconciliation.
- **TypeScript — customer and operator applications:** React purchase review, payment-provider credential UI, issuer challenge handling, receipts, and support views. Add a typed client SDK and MCP adapter if distribution requires them.
- **Rust — optional bounded verification component:** consider a small pure library or isolated process for parsing and validating signed mandate payloads when an audited implementation and concrete interoperability requirement justify it. It is not on the MVP critical path; use established cryptographic libraries and never design custom signature algorithms.
- **Persistence:** PostgreSQL for transactions, consent snapshots, orders, and the outbox. Go workers process durable jobs with retries and reconciliation.
- **Contracts:** versioned OpenAPI and JSON Schema; generate the TypeScript client. Keep provider-specific payloads behind Go interfaces.

Deploy the Go application, worker, and TypeScript application first. Avoid a separate service for every module. If Rust is introduced later, keep its contract small and use reviewed local IPC or an authenticated internal API.

### Records and API surface

Core records: PurchaseIntent, MerchantQuote, CardProfile, RewardRuleVersion, ConsentRecord, PaymentMethodReference, PaymentAttempt, Order, Refund, and AuditEvent.

Proposed application endpoints, not provider protocol endpoints:

- POST /v1/purchase-intents — create the user's structured request.
- POST /v1/purchase-intents/{id}/quotes — fetch compatible merchant offers.
- POST /v1/checkouts/{id}/approval — record authenticated consent to the exact checkout version.
- POST /v1/checkouts/{id}/execute — revalidate approval and perform one logical purchase.
- GET /v1/orders/{id} — report separate payment and fulfillment status.
- POST /v1/orders/{id}/refund-requests — request an eligible refund and track its outcome.

The execution endpoint requires a persisted approval identifier and idempotency key; model-generated text is not evidence of consent.

Amounts use integer fiat minor units or stablecoin base units with explicit asset, network, and decimals. Serialize potentially large integers as decimal strings in JSON; TypeScript must not silently convert them to floating-point numbers. Use explicit rounding rules and checked arithmetic. PostgreSQL is the durable source of truth; caches never decide an available balance.

### State and failure handling

Track checkout as draft, quoted, awaiting approval, approved, executing, completed, expired, or canceled. Track payment independently as created, requires customer action, pending, succeeded, failed, or outcome unknown. Orders and refunds have their own states.

Cart changes invalidate approval unless an existing supported mandate explicitly covers them. An unknown payment result blocks another payment. Recover through provider lookup and merchant order reconciliation before deciding whether execution may resume.

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

Allow 2–3 weeks for initial discovery/foundation if starting from scratch, then approximately 4–6 weeks for a narrow MVP after partner access.

- [ ] Confirm one merchant and one card provider can execute the selected-card flow.
- [ ] Build the Go application, PostgreSQL schema, worker, idempotency records, and outbox.
- [ ] Define checkout, consent, and payment state machines with provider adapters.
- [ ] Build the TypeScript review, secure credential enrollment, challenge, and receipt screens.
- [ ] Implement one ACP or UCP merchant adapter and scoped card payment adapter.
- [ ] Implement fresh-quote checks and deterministic reward calculations.
- [ ] Deliver sandbox purchase, authentication, timeout recovery, cancellation, and refund.
- [ ] Add operator reconciliation and a customer support evidence view.
- [ ] Complete the existing A1–A4 pilot milestones and evaluate subscription demand.

## Validation plan

Use Go unit and integration tests, concurrency tests with the Go race detector, TypeScript type checking and browser flow tests, and adapter contract tests. If a Rust component is adopted, add cargo test, clippy, and malformed-input/property tests for its boundary.

Required scenarios: modified cart after approval, invalid/expired mandate, unsupported card, issuer challenge, duplicated execution, delayed webhook, provider timeout after a successful charge, merchant order failure after payment, partial refund, and customer revocation. Confirm no PAN or reusable credential appears in model traces or logs.

## Decisions to resolve

- Which category, jurisdiction, merchant, provider, and reward-data source are available?
- Can reward eligibility be verified from actual transaction categorization?
- Does the provider support the customer's chosen card and the required authentication flow?
- Is AP2 interoperability needed for launch, or is explicit per-purchase approval sufficient for the initial partner arrangement?
- Who owns refund support and payment/order reconciliation?

## Reference freshness

Protocol and provider references were reviewed for the original plan on 2 October 2026. Revalidate versions, SDK coverage, pricing, eligibility, and partner access at implementation kickoff and before live launch. A sandbox integration does not establish production permission.
