# Threat Model

[Documentation home](index.md)

## Assets and trust boundaries

Protected: the integrity of recommendations (no option priced on assumed data, no silent change of what a customer approved), the binding between an approval and the payment that follows, the one-attempt rule, tenant isolation, and the honesty of reported savings. Out of scope here: card credentials, wallet keys and signing access, which never enter this system; the execution service holds them.

Trust: the engine trusts nothing but its own input and is reachable only with a shared token on a private network. The orchestrator trusts PostgreSQL and the policy file, treats quote providers as data sources whose claims are checked (basket hash, validity, verification states), and treats API callers as tenants limited by scopes.

## Threats and mitigations

| Threat | Mitigation |
|---|---|
| Untrusted merchant or tool content changes what is eligible or spendable | Eligibility comes from typed verification states and stored consent, not from text. Unverified states exclude. The engine accepts no free-form instructions. |
| A provider under-reports a fee or omits one | Required fee kinds per rail are policy; a missing or unknown fee excludes the option as missing data. A fee in an asset without a USD value is unknown. |
| A reward rule or point valuation is inflated | Rule versions are content-hashed and immutable per tenant; a changed version rejects the evaluation. Valuations are the customer's own, versioned, and unstable ones count as zero. |
| Double-counted discounts | Discounts already in the quote are not subtracted again; duplicates by id count once; conflicting duplicates reject the request; the sum never exceeds the price. |
| Floating-point or overflow error in a cost | Decimal strings only, `i128` checked arithmetic, component bound of 10^24 USD, directed rounding; property tests over the full range; the same vectors in every runtime. |
| Executing something the customer did not approve | The approval must match the evaluation's customer and exact `amount_due`, be given inside the evaluation window, and name an eligible option; the handoff carries the same facts and is revalidated on read. |
| Switching rails after an uncertain result | Partial unique index on unresolved attempts; expired attempts become `unknown` and keep blocking until an outcome is recorded. |
| Replayed or duplicated requests | Idempotency keys on evaluations and handoffs with fingerprint comparison; outcomes unique per source and event id with payload comparison. |
| Cross-tenant access | Every row carries `tenant_id` and every query filters on it; keys are hashed and scoped. |
| Credential or secret leakage | API keys stored as hashes and shown once; the engine token is never logged; request and response bodies are never logged; the UI receives the key only through a server-side proxy. |
| Claiming savings that did not happen | Realized savings require a settled, executed payment at the estimated charge and are reported against an explicitly unobserved counterfactual; estimates sit in separate fields; shadow observations never realize anything. |
| Forged outbox deliveries to the executor | HMAC-SHA256 over timestamp and body with a shared secret; event id and topic headers for deduplication. |
| Runaway operation during an incident | Global or per-tenant pause refuses evaluations and handoffs and makes open handoffs non-executable while reconciliation continues. |

## Residual risks

Provider adapters are not implemented; a live adapter's correctness in classifying fees, verifying acceptance and reading balances is the main open risk before shadow mode with real quotes. The engine token and webhook secret are shared secrets and must be rotated by redeploying. Reports aggregate per tenant without rate limiting; the API has no per-key rate limit. There is no customer authentication: the API is for tenant systems that already authenticate their customers, and `customer_id` is trusted within a tenant.
