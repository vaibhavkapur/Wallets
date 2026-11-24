# Protocol Versions

[Documentation home](index.md)

## Application Contract

The application API is **1.0.0**, using OpenAPI **3.1.0** and `/v1` endpoints. `backend/cmd/schema` generates `contracts/openapi.json`; TypeScript models derive from it. This application schema is distinct from merchant protocols.

## ACP Client

`backend/internal/partners/clients.go` pins `API-Version: 2026-01-30`. It supports checkout-session create, retrieve, update, complete, and cancel. Completion sends a scoped Stripe token in `payment_data`.

The client validates identifiers, requires HTTPS and a merchant token, disables redirects, sends idempotency headers for mutations, and treats connection/server errors as uncertain outcomes. Errors omit sensitive response bodies.

It is a transport client verified against local fixtures, not a participating merchant. It is not wired into `SandboxMerchant` or the demo orchestrator.

## Stripe Test Tokens

The separate client pins **2026-04-22.preview** for shared payment issued tokens. It accepts only `sk_test_` keys and scopes a payment-method reference by seller profile, currency, maximum amount, and expiry. Returned tokens must be non-live, active, and match that scope.

The browser does not mount Stripe Payment Element or collect real credentials. Payment-method registration, chosen-card enrollment, issuer interaction, revocation, and commercial access remain integration work. No provider API calls occur in the standard demo.

## Deferred Protocols

There is no UCP adapter, AP2 mandate implementation, MCP server, or Rust verification component. Local fingerprints establish application-level consent only, not AP2 conformance.

## Reference Sources

Sources checked during implementation on 2 October 2026; revalidate versions and access with actual partners:

- [ACP checkout](https://www.agenticcommerce.dev/docs/reference/checkout)
- [ACP architecture](https://www.agenticcommerce.dev/docs/concepts/architecture)
- [Stripe shared payment tokens](https://docs.stripe.com/agentic-commerce/concepts/shared-payment-tokens?agent-seller=agent)
- [Stripe token creation, pinned preview](https://docs.stripe.com/api/shared-payment/issued-token/create?api-version=2026-04-22.preview)
