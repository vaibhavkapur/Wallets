# MyWallet (iOS)

A native iPhone app where a user signs in with Apple, adds a real debit or credit card through the payment provider's native card-entry sheet, sees it as a polished masked tile, renames it, picks a preferred card, and removes it. The first version saves cards through Stripe's PaymentSheet in setup mode; contactless payments are a separate later milestone.

> **[Read the full documentation](docs/index.md)** · Plan: [plan.md](plan.md)

Built with SwiftUI (app), a small Swift package `MyWalletKit` (models, API client, wallet state machine, fixtures), Go and PostgreSQL (backend). Full card numbers and CVVs never reach the app's own code, the backend, the database, or logs: the provider SDK collects them directly and the backend stores only provider references plus masked display fields.

## Status

| Milestone (plan § Build sequence) | State |
|---|---|
| 1. Confirm the integration (Stripe business-model fit, production eligibility, SDK/API pin) | **Open** — external dependency; see [Decisions remaining](docs/pilot-checklist.md#decisions-remaining) |
| 2. Build the interface (five SwiftUI screens, accessibility, labeled fixtures) | Done |
| 3. Sandbox enrollment (login, Go API, provider setup, verified status, persistent display) | Done against the fake provider; Stripe client implemented and unit-tested against a stub, not yet run against Stripe test mode |
| 4. Lifecycle (removal, account deletion, retries, app lock, privacy controls, safe logs) | Done |
| 5. Real-card pilot (live account, physical iPhones, TestFlight) | Not started — real-card support is **unclaimed** until the pilot passes |

## Getting started

```bash
# Requires Go 1.22+, macOS with Xcode 16+ (for the app) or Command Line Tools (for the Kit tests), curl, jq
make test            # Go suite (embedded PostgreSQL, race detector) + MyWalletKit suite
make run-dev         # API on 127.0.0.1:8080 with PROVIDER=fake, DEV_SIGN_IN=1, embedded PostgreSQL
make smoke           # curl walkthrough: sign in, idempotent setup, verified save, rename, remove, delete
open ios/MyWallet.xcodeproj   # Debug scheme runs in `fixtures` mode with no network
```

See [Getting Started](docs/getting-started.md) for pointing the app at the local backend (`dev-backend` mode) and for the Stripe / Sign in with Apple configuration needed for the `stripe` mode.

## Layout

```
backend/      Go service: auth, card setups, cards, webhooks, account deletion, retry worker
  cmd/server              entry point (env-configured; --dev-embedded-postgres for local use)
  migrations/             schema (users, sessions, provider_customers, consent_records,
                          saved_cards, setup_attempts, provider_events, pending_operations)
  internal/appleauth      Sign in with Apple token verification (JWKS, aud, exp, nonce) + dev verifier
  internal/provider       provider interface; stripe/ (REST + webhook signatures); fake/ (fixture)
  internal/enrollment     the state machine: start/reconcile/close setups, removal, deletion, events
  internal/httpapi        routes, session auth, JSON, tests
  internal/workers        durable retry loop (detach, delete account, reconcile, expire stale setups)
ios/
  MyWallet.xcodeproj     iOS 17+, Swift 6, Xcode 16 synchronized folders, stripe-ios 26.12.1 (up to next major)
  MyWallet/              SwiftUI app: Welcome, Wallet, Add Card consent, Card Details, Settings,
                          Stripe presenter, simulated presenter (fixtures), app lock, privacy shield
  MyWalletKit/           Swift package: models, URLSession API client, Keychain store,
                          WalletModel (@Observable), FixtureWalletAPI, tests (Swift Testing)
docs/                     architecture, API, database, getting started, pilot checklist
scripts/                  kit-test.sh (CLT-friendly swift test), smoke.sh
```

## What "saved" means

A card tile reads **Saved** when the backend has fetched the provider's setup result, checked that it belongs to the signed-in user's provider customer, fetched the payment method's masked fields, and stored the reference. A client-side success callback alone never creates a card. "Saved" does not imply bank endorsement, available funds, or payment readiness, and v1 grants no authority for unattended charges (`usage=on_session`).

## Verification

`make test` runs 15 Go API scenarios (idempotent setup replay, server-verified saving, concurrent duplicate webhooks producing exactly one card, cross-account reads/changes/removals all failing, customer-mismatch rejection, pending removal during a provider outage with worker recovery, recoverable account deletion, stale-setup expiry), Stripe client wire-format and webhook-signature tests, Sign in with Apple verification tests (audience, issuer, expiry, HS256 rejection, nonce binding, key rotation), and 18 MyWalletKit tests covering the wallet state machine. See [docs/pilot-checklist.md](docs/pilot-checklist.md) for the manual checks that still gate the pilot.
