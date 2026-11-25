# Protocol Versions

[Documentation home](index.md)

## Application Contract

The application API is **1.0.0**, using OpenAPI **3.1.0** under `/v1`. `backend/cmd/schema` generates [`contracts/openapi.json`](../contracts/openapi.json) and `scripts/check-contracts.sh` detects drift in CI. This application contract is separate from merchant and payment network protocols.

## Optional Sibling Adapters

- **Agent Checkout HTTP Adapter** (`AGENT_CHECKOUT_URL`): Implemented in `backend/internal/recurring/adapters.go` (`AgentCheckoutHTTPAdapter`) to hand off approved occurrences (`POST /v1/recurring-handoff`), reconcile by idempotency key (`GET /v1/recurring-handoff/{key}`), and cancel orders (`POST /v1/orders/{id}/cancel`). Disabled by default when running standalone.
- **Payment Optimizer Client** (`OPTIMIZER_URL`): Optional client stub (`PaymentOptimizerClient`) for invoking the sibling `Card-Stablecoin-Payment-Optimizer` service when adopted. Not required to run Recurring Purchase Agent.

## External Protocol Boundaries (Not Wired to Sandbox Runtime)

- **ACP / UCP merchant feeds**: Sandbox catalog listings annotate each merchant with `ACP` or `UCP` protocol metadata, but all quotes and order updates in the default runtime come from local fixtures (`SandboxMerchantAdapter` / `SandboxCheckoutAdapter`).
- **Scoped card credentials**: Payment methods (`pm_card_everyday`, `pm_card_household`, `pm_usdc_base`) are fixture references. No real card PAN/CVC, Stripe shared payment tokens, or wallet private keys are collected or transmitted.
- **AP2 delegated mandates**: `ConsentRecord` models mandate binding (exact rule version, SKU, merchant, max amount, payment method, expiry, revocation) at the application layer for `C3_LIMITED_DELEGATION` validation; it does not claim external AP2 counterparty conformance.

## Reference Freshness

Protocol and provider references in [plan.md](../plan.md) were reviewed on **2 October 2026**:

- [ACP architecture](https://www.agenticcommerce.dev/docs/concepts/architecture)
- [Stripe shared payment tokens](https://docs.stripe.com/agentic-commerce/concepts/shared-payment-tokens.md?agent-seller=agent)
- [AP2 specification](https://ap2-protocol.org/ap2/specification/)
- [UCP AP2 mandates extension](https://ucp.dev/specification/payment/extensions/ap2-mandates/)

Revalidate protocol versions, SDK support, and partner access before any live integration.
