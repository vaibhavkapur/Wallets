# Recurring Purchase Agent — Project Plan

Updated: 2 October 2026  
Status: Proposed standalone MVP  
Primary stack: Go and TypeScript; optional Rust optimizer reuse

## Product plan

### Customer and value

Help consumers reorder predictable essentials while controlling price, quantity, timing, merchant, and payment method. Start with customer-defined schedules rather than attempting to infer household inventory.

### MVP scope

- A narrow category such as coffee or household supplies, with exact product and pack-size matching.
- Fixed schedules with timezone-aware execution, reminders, skip-next, pause, and cancel controls.
- Maximum delivered price, minimum interval between purchases, preferred merchant, and monthly budget.
- Explicit substitution rules; substitutions require approval unless previously authorized precisely.
- A scheduled quote and approval request for every initial purchase.
- Order tracking to prevent duplicates when a previous order is still pending or delayed.
- Payment selection through Agent Checkout; Payment Optimizer is optional.

### Execution flow

1. Customer defines a reorder rule and selects the initial approval mode.
2. Before each due date, check account status, previous orders, merchant availability, and the current rule version.
3. Obtain a fresh quote and compare product, delivery, price, and budget constraints.
4. Present the proposed order for approval; expire unapproved proposals without purchasing.
5. Execute through Agent Checkout and record the outcome against that schedule occurrence.
6. Set the next occurrence without producing catch-up purchases after outages unless explicitly requested.

Unattended mode is a later milestone. It requires a provider-supported payment permission, merchant support, clear price and frequency constraints, and revocation checks at execution time. Each occurrence is still a separately tracked purchase. Do not equate a scheduling rule with permission to charge a stored card.

### Build milestones

- **C1 — Reminders:** validate schedules and customer demand through proposed orders without automatic payment.
- **C2 — Assisted reorders:** combine fresh quotes, approval, payment, and fulfillment tracking.
- **C3 — Limited delegation:** pilot unattended exact-item purchases only when mandate and payment support are proven.

### Success and release gates

- Test timezone changes, daylight-saving transitions where applicable, job retries, outages, stockouts, price changes, and rule revocation races.
- A schedule occurrence cannot create two orders; outstanding or uncertain orders block another attempt.
- Measure approval-to-purchase conversion, retained reorder rules, repeat orders, skip rate, and unwanted-purchase reports.
- Verify pause behavior and the distinction between stopping a future order and canceling one already submitted.
- Advance to unattended mode only after demonstrating correct constraint enforcement and a workable customer remedy for mistakes.

### Commercial hypothesis and dependencies

Include the feature in a subscription or offer a household plan. Disclose any merchant commissions. Depend on reliable merchant inventory and order APIs, supported stored-credential arrangements, and sustainable support economics.

## Scope and assumptions

- Start in one provider-supported jurisdiction with a small pilot. USD is the modeling currency; launch geography and legal entity remain decisions.
- Use established payment, credential, custody/signing, conversion, and payout providers as applicable. Confirm commercial access and the permitted funds flow before live use.
- Estimates assume approximately three engineers plus product, part-time design, and payments operations support. External approvals and pilot observation add calendar time.
- No own token issuance, lending, speculative trading, or yield product is included.
- This project can be implemented independently. Reuse sibling projects through versioned APIs or libraries when useful; do not require all five products to launch.
- Product targets, example limits, timelines, and architecture below are proposals, not measured results or provider commitments.

## Independent launch and protocol choices

The first version can create reminders and approved reorder proposals through a direct merchant checkout integration. If Agent Checkout is available, reuse it through an explicit API contract; otherwise implement only the narrow merchant/payment adapter needed for this project. Payment Optimizer remains optional.

- **ACP or UCP:** choose the supported interface of the first merchant for fresh quotes and checkout. [ACP architecture](https://www.agenticcommerce.dev/docs/concepts/architecture)
- **Scoped card credentials:** use an approved provider path, including customer authentication when requested. A saved card or scheduling rule does not automatically authorize a later off-session purchase. [Card credential example](https://docs.stripe.com/agentic-commerce/concepts/shared-payment-tokens.md?agent-seller=agent)
- **AP2:** evaluate for delegated execution only with counterparties that support and verify the relevant mandates. Use supported constraints for exact product, price, timing, and approved payment method. [AP2 specification](https://ap2-protocol.org/ap2/specification/), [UCP AP2 integration](https://ucp.dev/specification/payment/extensions/ap2-mandates/)
- Stablecoin payments are a later option supported through the payment adapter. They are not needed to validate recurring customer demand.

## Technical design and language ownership

- **Go — workflow backend:** rule validation, timezone-aware scheduling, durable occurrence creation, merchant quotes, stock checks, approval requests, execution-time policy checks, checkout handoff, and order reconciliation.
- **TypeScript — React customer and operations interfaces:** schedule creation, product matching, substitution preferences, delivered-price review, reminders, skip/pause controls, and order history.
- **Rust — optional optimizer reuse:** call the separate Rust payment-evaluation service if Card-versus-Stablecoin Payment Optimizer is adopted. Do not write a second Rust scheduler or duplicate the checkout policy engine solely to include the language.
- **PostgreSQL:** versioned rules, occurrences, proposals, approvals, budget reservations, orders, and an outbox.
- **Durable Go workers:** database-backed jobs with leases, bounded retries, and a periodic recovery scan. A database uniqueness constraint prevents two jobs from creating the same occurrence.

Store the customer's IANA timezone, intended local schedule, chosen daylight-saving behavior, and computed next UTC run. Define whether a rule means a calendar schedule or a minimum elapsed interval; do not interchange them.

### Records and API surface

Core records: RecurringRuleVersion, ScheduleOccurrence, ProductConstraint, MerchantQuote, PurchaseProposal, ConsentRecord, BudgetReservation, CheckoutReference, and NotificationEvent.

Proposed application endpoints:

- POST /v1/recurring-rules — create a rule and its approval mode.
- PATCH /v1/recurring-rules/{id} — create a new version of schedule and purchase constraints.
- POST /v1/recurring-rules/{id}/pause — prevent new execution under that rule.
- POST /v1/recurring-rules/{id}/skip-next — skip one identified occurrence.
- GET /v1/recurring-rules/{id}/occurrences — explain upcoming and past actions.
- POST /v1/proposals/{id}/approve — approve an exact current proposal.
- GET /v1/occurrences/{id} — return scheduling, checkout, payment, and fulfillment status.

Amounts use integer fiat minor units or stablecoin base units with explicit asset, network, and decimals. Serialize potentially large integers as decimal strings in JSON; TypeScript must not silently convert them to floating-point numbers. Use explicit rounding rules and checked arithmetic. PostgreSQL is the durable source of truth; caches never decide an available balance.

### Occurrence lifecycle

Each occurrence moves through scheduled, quoting, awaiting approval, approved, executing, and completed; alternative outcomes include skipped, expired, canceled, failed, or needs reconciliation. Model payment and fulfillment independently.

Use a unique logical occurrence identifier tied to the rule and schedule slot, even if the worker restarts. Recheck the current rule version, pause state, approval expiry, previous unresolved orders, and monthly budget immediately before execution.

If an outage spans several schedules, present one proposed recovery action rather than generating a backlog of purchases. Define cancellation races: a pause blocks new submission, but an already submitted payment/order must be reconciled and canceled through the merchant if possible.

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

Allow approximately 3–4 weeks after a usable checkout adapter exists. A standalone build needing checkout and payment execution should add the relevant integration effort, potentially another 4–6 weeks plus provider onboarding.

- [ ] Select one narrow category and validate exact product/pack-size matching.
- [ ] Define calendar-versus-interval semantics, timezone behavior, substitution rules, and budget periods.
- [ ] Implement the Go rule, occurrence, lease, uniqueness, and recovery-scan model.
- [ ] Build TypeScript scheduling and skip/pause interfaces.
- [ ] Implement fresh quotes, approval requests, proposal expiry, and notification delivery.
- [ ] Integrate one checkout path with idempotency and outcome reconciliation.
- [ ] Add duplicate-order prevention across pending and delayed orders.
- [ ] Pilot reminders and assisted reorders before considering delegated execution.
- [ ] Advance through C1–C3 only when provider permission and user demand justify each mode.

## Validation plan

Use Go state-machine, database integration, race, and fake-clock tests. Use TypeScript type checking and browser tests for schedule interpretation, approval, expiry, and pause behavior.

Required cases: daylight-saving gaps and repeated hours, timezone edits, leap dates, worker restarts, duplicated jobs, stale approval, product substitution, pack-size change, partial stock, price increase, monthly-cap rollover, delayed fulfillment, payment timeout, and pause racing with submission.

The main invariant is at most one intended order per occurrence with no new payment while an earlier result is unresolved. A reminder being sent is not evidence that a customer approved or received an order.

## Decisions to resolve

- Which recurring purchases are frequent enough and predictable enough to justify the feature?
- Which merchants provide reliable product identity, availability, checkout, and order status?
- Will users choose a calendar schedule, minimum interval, or both?
- What product substitutions and price changes are permitted?
- Does the chosen payment provider support the intended later delegated mode?
- What evidence is sufficient to advance beyond customer approval on every order?

## Reference freshness

Protocol and provider references were reviewed for the original plan on 2 October 2026. Revalidate versions, SDK coverage, pricing, eligibility, and partner access at implementation kickoff and before live launch. A sandbox integration does not establish production permission.
