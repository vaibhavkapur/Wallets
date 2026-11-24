# Merchant-funded Stablecoin Rewards — Project Plan

Updated: 2 October 2026  
Status: Proposed standalone MVP  
Primary stack: Go and TypeScript; optional Rust batch reconciliation tooling

## Product plan

### Customer and value

Give merchants a way to fund purchase incentives and customers a transparent cashback balance with optional stablecoin payout. Rewards are a new merchant-funded program; existing card-issuer points are not assumed to be transferable.

### MVP scope

- Three to five pilot merchants and simple fixed-amount or percentage campaigns.
- Merchant campaign budgets, eligible products, customer caps, start/end dates, and explicit return treatment.
- Funding received and reconciled before rewards are promised within a campaign budget.
- Customer reward states: pending, eligible, payout requested, paid, or reversed before payout.
- One supported payout asset/network, recipient enrollment, address verification, and a disclosed minimum payout threshold.
- Statements linking each reward to its source order and showing payout fees, if any.

### Funding and payout flow

1. Merchant agrees to campaign terms and prefunds the applicable budget through a supported arrangement.
2. Each qualifying order reserves campaign capacity and creates a pending reward accrual.
3. Verify purchase attribution, payment, fulfillment where relevant, return-window expiry, and campaign rules.
4. Convert an eligible accrual into a payable reward without allocating the same funding twice.
5. Customer chooses a supported payout destination and approves the payout terms.
6. Submit one idempotent payout, track settlement, and reconcile the resulting liability reduction.

Merchant campaign funds, customer reward liabilities, and operating revenue must remain separately identifiable. Define who holds the funds and bears losses through the provider and merchant agreements before launch.

### Build milestones

- **R1 — Commercial validation:** merchants accept prefunding, fees, attribution, and return rules.
- **R2 — Ledger sandbox:** campaign accruals, partial returns, duplicate events, reversals, and failed payouts reconcile correctly.
- **R3 — Funded pilot:** issue rewards for actual eligible purchases and complete supported customer payouts.
- **R4 — Merchant evaluation:** compare incremental purchases and retention with a holdout or other agreed baseline where practical.

### Success and release gates

- Allocated funding covers pending obligations and payable rewards under the agreed reserve model.
- Campaign caps remain correct under concurrent orders and duplicated attribution events.
- Measure reward issuance cost, payout success, time to availability, customer claim rate, and merchant renewal intent.
- Demonstrate positive contribution after reward funding, conversion, payout, fraud, and support costs.
- Document late refunds and chargebacks after payout. Do not assume an irreversible stablecoin transfer can be clawed back; allocate a reserve and contractual loss responsibility.

### Commercial hypothesis and dependencies

Charge a disclosed campaign or performance fee. The merchant's budget funds the cashback. Reserve yield and token appreciation are not revenue assumptions.

Resolve recipient eligibility, wallet access and recovery, payout geography, custody, required screening, applicable tax reporting, and the permitted use of the chosen payout provider. Decline the launch if compliant, usable payouts or viable small-balance economics are unavailable.

## Scope and assumptions

- Start in one provider-supported jurisdiction with a small pilot. USD is the modeling currency; launch geography and legal entity remain decisions.
- Use established payment, credential, custody/signing, conversion, and payout providers as applicable. Confirm commercial access and the permitted funds flow before live use.
- Estimates assume approximately three engineers plus product, part-time design, and payments operations support. External approvals and pilot observation add calendar time.
- No own token issuance, lending, speculative trading, or yield product is included.
- This project can be implemented independently. Reuse sibling projects through versioned APIs or libraries when useful; do not require all five products to launch.
- Product targets, example limits, timelines, and architecture below are proposals, not measured results or provider commitments.

## Independent launch and provider choices

This project can ingest authenticated order and refund events from participating merchants without depending on Agent Checkout. Its required foundations are funded campaign agreements, correct reward accounting, and a supported payout flow.

- Use signed merchant webhooks or authenticated order APIs for attribution, returns, and eligibility. ACP/UCP are optional sources of order information, not prerequisites for a rewards campaign.
- Evaluate a partner that supports the actual reward-payout use case, recipient countries, asset/network, identity checks, and custody model. Stripe Connect stablecoin payouts are one candidate with availability restrictions, not a committed integration. [Payout documentation](https://docs.stripe.com/connect/stablecoin-payouts)
- Make conversion and redemption access explicit. Customers may depend on their wallet or off-ramp provider rather than direct issuer redemption. [USDC terms](https://www.circle.com/legal/usdc-terms)
- Neither AP2 nor x402 is required for the initial merchant-funded rewards flow. Payment authorization comes from campaign funding and the customer's payout approval through the chosen provider.

## Technical design and language ownership

- **Go — financial backend:** merchant onboarding records, campaigns, order attribution, reward eligibility, atomic campaign reservations, double-entry ledger, conversion/payout orchestration, provider webhooks, and reconciliation.
- **TypeScript — React applications:** merchant campaign dashboard, customer reward statements and payout flow, and an operator exception queue. Use provider-hosted enrollment components for sensitive information when available.
- **Rust — optional reconciliation utility:** introduce a bounded batch processor only if statement volume or profiling justifies it. It may parse and normalize large settlement files and produce deterministic discrepancy reports; it must not independently change balances or initiate payouts.
- **PostgreSQL:** campaigns, funding events, journal entries, reward accruals, beneficiary versions, payout attempts, and an outbox.
- **Go workers:** return-window checks, eligibility transitions, payout submission, chain/provider status polling where needed, and daily reconciliation.

Start with one Go service plus workers and the TypeScript interfaces. Correct ledger ownership matters more than adding another runtime. Rust is an optimization option after the Go reconciliation path is proven.

### Ledger design and records

Core records: Merchant, CampaignVersion, FundingEvent, CampaignReservation, PurchaseAttribution, RewardAccrual, LedgerAccount, JournalEntry, BeneficiaryVersion, PayoutAttempt, and ReconciliationException.

Maintain distinct ledger accounts for merchant funding, reserved campaign amounts, pending and payable reward obligations, payout clearing, fees, and platform revenue. Use balanced immutable postings and explicit correcting entries. Database funding coverage is reconciled against the actual provider-held assets.

Amounts use integer fiat minor units or stablecoin base units with explicit asset, network, and decimals. Serialize potentially large integers as decimal strings in JSON; TypeScript must not silently convert them to floating-point numbers. Use explicit rounding rules and checked arithmetic. PostgreSQL is the durable source of truth; caches never decide an available balance.

The campaign currency, reward liability currency, and payout asset can differ. Define when conversion occurs, quote expiry, who bears FX changes and fees, and how funding coverage is maintained. Reserve additional costs before promising a reward when applicable.

### API surface

Proposed application endpoints:

- POST /v1/campaigns — create a versioned campaign with caps and return rules.
- POST /v1/merchant-events — ingest authenticated order/refund events.
- GET /v1/rewards — list a customer's accruals and their states.
- POST /v1/payout-quotes — obtain the actual payout amount, asset, destination, fees, and expiry.
- POST /v1/payouts — authorize one payout using an approved quote and beneficiary version.
- GET /v1/payouts/{id} — report provider and settlement status.
- GET /v1/reconciliation-exceptions — provide an operator-only discrepancy queue.

Beneficiary changes require authenticated customer action. A payout queued against an old destination cannot silently use a newly edited one.

### Lifecycle and exception handling

Reward lifecycle: pending, eligible, payout requested, paid, or reversed before payout. Track payout attempts separately as prepared, submitted, pending, succeeded, failed, or outcome unknown.

An expired return window is not the same as final absence of chargeback risk. Define post-payout loss reserves and contractual responsibility. Failed payout retries reuse the logical payout and reconcile unknown outcomes before submitting again. Chain confirmation requirements and reorganization handling follow the selected provider/network design.

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

Allow approximately 4–6 weeks after merchant funding agreements and payout access are confirmed; add 2–3 weeks if basic account, ledger, and worker infrastructure must be established.

- [ ] Obtain three merchant campaign proposals and agree on attribution, caps, prefunding, returns, and fees.
- [ ] Confirm recipient eligibility, payout use-case permission, custody, conversion, and customer recovery.
- [ ] Specify balanced journal entries for funding, reservation, eligibility, payout, reversal, and fees.
- [ ] Implement the Go ledger and campaign reservations with transactional constraints.
- [ ] Build signed order/refund ingestion and idempotent attribution.
- [ ] Build TypeScript merchant, customer, and operator views.
- [ ] Add beneficiary confirmation, payout quotes, provider submission, and reconciliation.
- [ ] Exercise partial returns, late chargebacks, failed payouts, and insufficient campaign funding.
- [ ] Complete the existing R1–R4 funded pilot milestones.

## Validation plan

Use Go unit, PostgreSQL integration, and concurrency tests for accounting and campaign caps. Use TypeScript type checks and browser tests for reward disclosures, destination confirmation, and payout status.

Required accounting checks: every journal balances by asset, duplicate events do not create duplicate obligations, pending plus payable promises stay covered, funding cannot be allocated twice, partial returns reverse the correct amount, and ledger/provider differences become operator exceptions.

Validate address/network mismatches, duplicate payout callbacks, late confirmations, unknown outcomes, conversion expiry, insufficient network-fee coverage, and a customer losing wallet access. If Rust batch tooling is introduced, use cargo test/clippy and shared statement fixtures to prove parity with the Go reference path.

## Decisions to resolve

- Who legally holds campaign funds and owes the customer the reward?
- Are rewards denominated in fiat or token units, and when is conversion fixed?
- Which provider explicitly supports rewards for the intended recipients?
- Who bears fees, late refunds, chargebacks, depegs, and payout failures?
- What minimum payout keeps small balances useful after costs?
- Will merchants renew based on incremental purchases and contribution margin?

## Reference freshness

Protocol and provider references were reviewed for the original plan on 2 October 2026. Revalidate versions, SDK coverage, pricing, eligibility, and partner access at implementation kickoff and before live launch. A sandbox integration does not establish production permission.
