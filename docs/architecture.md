# Architecture

## Components

```
iPhone app (SwiftUI)                 Go backend (one service)             Payment provider
┌──────────────────────────┐         ┌───────────────────────────┐        ┌───────────────┐
│ Views                    │  HTTPS  │ httpapi   ── sessions     │  HTTPS │ Customers     │
│ WalletModel (@Observable)│◄───────►│ enrollment (state machine)│◄──────►│ SetupIntents  │
│ URLSessionWalletAPI      │         │ provider/stripe           │        │ PaymentMethods│
│ Keychain (token, pending)│         │ workers (retry loop)      │◄───────│ Webhooks      │
│ Stripe PaymentSheet ─────┼─────────┼───────────────────────────┼───────►│ card entry    │
└──────────────────────────┘         │ store ─► PostgreSQL       │        └───────────────┘
                                     └───────────────────────────┘
```

- **App.** `WalletModel` owns all state the views render. It persists only the session token and an in-flight `PendingSetup` record (idempotency key + attempt ID) in the Keychain; card metadata lives in memory and is refetched. Card entry goes through `CardSetupPresenter`: `StripeCardSetupPresenter` wraps PaymentSheet in setup mode, `SimulatedCardSetupPresenter` is a labeled fixture for non-production modes. `LockController` gates the UI behind device-owner authentication; a privacy shield covers the UI while inactive.
- **Backend.** `httpapi` authenticates every request with an opaque session token (stored hashed) and resolves the user; `enrollment` performs every state transition; `provider` abstracts Stripe; `workers` retries durable operations; `store` is the only persistence layer. `appleauth` verifies Sign in with Apple tokens.
- **Provider.** Holds card credentials, performs setup and authentication, returns references and masked display fields. The SDK talks to it directly for card entry.

## Enrollment state machine

Attempt statuses: `collecting → authentication_required → processing → saved | failed | canceled`.

```
POST /v1/card-setups (Idempotency-Key, consent)
  ├─ existing (user, key)? ──► replay: same attempt, fresh client credentials if still open
  └─ new: insert attempt(collecting) + consent in one tx
          ensure provider customer (idempotent on user ID)
          create provider setup (Idempotency-Key = attempt ID)
          mint client session (client secret + ephemeral key + publishable key)

App presents the sheet ──► SDK ⇄ provider (card fields, 3-D Secure, return URL)

Confirmation — always from the provider, never from the client callback:
  Reconcile(attempt):
    fetch setup from provider
    setup.customer ≠ user's customer ─────────────► failed(customer_mismatch)
    succeeded: fetch payment method
      pm.customer ≠ user's customer ──────────────► failed(customer_mismatch)
      insert saved_card (UNIQUE provider+pm ref) ─► saved   (replays and races hit the unique key)
    requires_action ──────────────────────────────► authentication_required
    processing ───────────────────────────────────► processing + enqueue reconcile_setup
    canceled ─────────────────────────────────────► canceled
    requires_payment_method(+last error) ─────────► collecting (failure_code = decline code)
  Triggers: GET /v1/card-setups/{id} (poll), verified webhook, worker op, stale sweep.

POST /v1/card-setups/{id}/close {outcome}  (sheet dismissed)
  reconcile first (a late success is saved) → cancel at provider → reconcile → canceled | failed
```

The row lock on `setup_attempts` plus the unique index on `(provider, payment_method_ref)` make concurrent polls, duplicate webhook deliveries, and worker retries converge on one card. `TestWebhookDedupeAndSingleCard` fires six duplicate deliveries interleaved with six polls and asserts exactly one card.

## Removal and account deletion

- `DELETE /v1/cards/{id}`: in one transaction the card becomes `removal_pending` and a `detach_card` operation is enqueued; then detachment is attempted inline. Success → `removed` (hidden from the list). Provider outage → the card stays visible as **Removal pending** (HTTP 202) and the worker retries with exponential backoff until the provider confirms, or until a `payment_method.detached` webhook arrives. The local row is never hidden before the provider confirms.
- `DELETE /v1/account`: sessions are revoked and the user becomes `deleting` immediately; all cards become `removal_pending`; a `delete_account` operation detaches each card, deletes the provider customer, drops the customer mapping, and scrubs the user (email removed, Apple subject released). A later sign-in with the same Apple ID creates a brand-new account. Sign-in during `deleting` is refused with 409.

## Webhooks

`POST /v1/webhooks/provider` verifies the `Stripe-Signature` header (HMAC-SHA256 over `t.payload`, 5-minute tolerance, constant-time compare), records the event ID in `provider_events` (duplicates are acknowledged with `{"status":"duplicate"}` and skipped), and then uses the payload only to locate the record: the resulting state is always fetched from the provider. A handler error forgets the event ID and returns 500 so the provider retries.

## Worker

Every 15 s (configurable) the worker claims due `pending_operations` with `FOR UPDATE SKIP LOCKED`, leases them, runs them, and on failure schedules a retry with backoff from 30 s up to 1 h. It also reconciles open attempts idle for 10 minutes and cancels those idle for 24 hours (`failure_code = expired`).

## What is stored where

| Data | App | Backend / DB | Provider |
|---|---|---|---|
| Card number, CVC | never (SDK fields only) | never | yes |
| Provider refs (customer, setup, payment method) | setup client secret + ephemeral key in memory during the sheet only | yes | yes |
| Brand, last 4, expiry, nickname, preferred | in memory | yes | brand/last4/expiry |
| Session token | Keychain (hashed server-side) | hash only | — |
| Apple subject, email | — | yes (email only if verified; scrubbed on deletion) | — |
| Consent (purpose, version, time) | — | yes | — |

Logs: method, route pattern, status, latency. Errors from the provider are logged without URLs or identifiers.

## Later contactless phase

Nothing here binds a saved card to a device credential. Tap-to-pay will need Apple's NFC & SE Platform approval, network tokenization and device provisioning through a payments partner, and its own enrollment and removal paths. The account/wallet UI and the provider abstraction are reusable; the `saved_cards` table is not a credential store.
