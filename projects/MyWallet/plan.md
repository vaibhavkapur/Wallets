# Uno Wallet iOS App Plan

Updated: 3 October 2026

Status: Proposed MVP for the United States

Build a native iPhone app where a user can securely add a real debit or credit card, see it in a polished wallet, and remove it. The first version saves cards through a payment provider. Contactless payments are a separate later milestone.

The user has chosen the United States and card saving before payments. SwiftUI is the proposed interface framework; Go and PostgreSQL follow the existing project's backend conventions. This document is a plan; no app or live integration has been created.

## First version

- Sign in with Apple and restore the user's saved card list after signing in again.
- Add a supported card through the provider's native card-entry component.
- Complete any additional authentication requested during setup.
- Display card brand, last four digits, expiration, an optional nickname, and saved status.
- Select a preferred card for display, rename a card, and remove it.
- Lock access with system authentication and provide account deletion.

Payment execution, NFC, importing cards from Apple Wallet, balances, transaction history, rewards optimization, and card issuing are outside this release. Selecting a preferred card only changes this app's presentation.

Success means a consenting pilot user can add a supported real card in the provider's live environment, see the correct masked details after relaunching, and remove it successfully. A sample card or successful local form validation does not satisfy this milestone.

## Interface direction

Use the simplicity and card focus of Apple Wallet as inspiration, with an original Uno identity and composition. Proposed styling: native typography, a dark neutral background, a restrained teal accent, large rounded card tiles, subtle depth, and short expansion animations. Support light appearance, Dynamic Type, VoiceOver, and Reduce Motion.

1. **Welcome:** concise explanation of secure card storage and Sign in with Apple.
2. **Wallet:** Uno title, a visible Add Card action, a compact stack of saved cards, and a useful empty state. Cards show only masked details. Tapping a card opens its details.
3. **Add card:** a brief saving-consent explanation followed by the provider's native sheet. Customize its supported appearance to fit Uno; let the SDK handle card fields and authentication.
4. **Card details:** enlarged card tile, nickname, brand, last four digits, expiry, preferred-card setting, and Remove Card. Show “Saved”; do not imply bank endorsement, available funds, or payment readiness.
5. **Settings:** app lock, privacy information, sign out, and account deletion.

Keep the home screen focused on cards. Do not add placeholder balances, fabricated purchases, a nonfunctional Pay button, or simulated system authentication. Use original card art unless an issuer supplies authorized assets.

Apple requires third-party NFC experiences to remain visually distinguishable from Apple Pay and Apple Wallet. Preserve the original Uno design when contactless payments are introduced. [Apple design requirements](https://developer.apple.com/support/nfc-se-platform/)

## Provider choice and real card access

The proposed first integration is Stripe's iOS PaymentSheet in setup mode with a Customer and SetupIntent. It supports collecting and saving a payment method without creating a purchase charge. Authentication may be requested during setup. [Stripe iOS setup flow](https://docs.stripe.com/payments/mobile/accept-payment?platform=ios&type=setup)

Before live development, confirm that Stripe accepts this standalone card-saving business model and the intended future use. Its standard merchant payment-method storage is not evidence that a general-purpose wallet business is approved. If it does not fit, choose a provider that explicitly supports the intended card-vault use case behind the same application interface.

Use test mode while developing and a properly activated production account for the real-card pilot. Record the provider's actual supported card coverage rather than promising all US cards. Obtain explicit saving consent; v1 grants no authority for unattended charges. Choose setup options consistent with later user-initiated payments.

A successful setup means the provider saved the payment method. It does not guarantee a future payment will succeed. A provider PaymentMethod reference also does not establish a device-bound NFC credential; contactless support will need a separate enrollment path and may require the user to add the card again.

## Architecture

**iPhone app:** SwiftUI views, a small observable application model, an API client, the provider SDK, and system authentication. Keep login credentials in Keychain and minimize cached card metadata. Clear account-specific data on sign out and obscure sensitive views when the app backgrounds.

**Go backend:** one application containing authentication, card enrollment, card metadata, removal, provider webhooks, and a small retry worker. Verify Sign in with Apple on the server and derive the customer mapping from the authenticated account.

**PostgreSQL:** users, provider-customer mappings, saved card references, setup attempts, consent records, and processed webhook IDs. Use durable pending operations to recover interrupted enrollment and deletion.

**Payment provider:** collects card information, holds the payment credentials, performs the setup flow, and returns references and masked display fields. Its SDK communicates directly with the provider for sensitive card entry.

Keep full card numbers and CVV out of our application database, backend requests, logs, analytics, and crash reports. Store only provider references and necessary display metadata. Provider-managed collection reduces our PCI scope; it does not eliminate our responsibilities. [Stripe integration security](https://docs.stripe.com/security/guide)

Deploy one Go service and a managed PostgreSQL database. Keep provider secret keys in server-side secret storage. No microservices, message broker, money ledger, or separate web frontend is needed for this scope.

## Adding a card

1. The signed-in user taps Add Card and agrees to save the card for the disclosed purpose.
2. The app starts the provider's setup sheet. The backend creates or retrieves the correct provider customer and starts a setup attempt when required by the SDK.
3. Card details go from the provider's native component directly to the provider. Limit v1 to manual card entry; omit Apple Pay, Link, bank-account methods, and scanning.
4. The SDK completes setup and any required authentication, including returning to the app after an external challenge.
5. The backend verifies the setup result with the provider and checks that it belongs to the signed-in user's customer. A client success callback alone cannot create a saved card.
6. The backend stores the card reference and masked display fields. The app refreshes the wallet and shows the new card.

Verify webhook signatures, deduplicate deliveries, and reconcile pending attempts on app resume. Repeated taps and network retries must not create duplicate records. Treat provider timeouts as pending until the result is retrieved.

## Application records and APIs

Core records: User, ProviderCustomer, SavedCard, SetupAttempt, ConsentRecord, and ProviderEvent. SavedCard contains an internal ID, owner ID, provider reference, brand, last four digits, expiry, nickname, display preference, and lifecycle state.

Proposed application endpoints, separate from provider API names:

- POST /v1/auth/apple — validate identity and establish a session.
- POST /v1/card-setups — start one setup attempt; require an idempotency key.
- GET /v1/card-setups/{id} — return the server-verified setup status.
- GET /v1/cards — return the current user's masked card list.
- PATCH /v1/cards/{id} — change nickname or display preference.
- DELETE /v1/cards/{id} — start provider removal and finish local removal after confirmation.
- POST /v1/webhooks/provider — process verified provider events.
- DELETE /v1/account — revoke sessions and remove provider-held cards and application data according to the retention policy.

Check ownership on every operation. Do not accept a caller-supplied provider customer ID as authority. Keep setup credentials short-lived and out of URLs and logs.

Track enrollment as collecting, authentication required, processing, saved, failed, or canceled. Track removal as pending or removed. If provider removal is unavailable, show a pending result and retry; hiding the local card is not proof that it was removed from the provider. Stripe detachment permanently disables that PaymentMethod reference, so adding it again requires a new setup. [Stripe removal behavior](https://docs.stripe.com/api/payment_methods/detach)

## Build sequence

1. **Confirm the integration:** validate provider business-model fit, production eligibility, card coverage, SDK compatibility, and setup/removal behavior. Pin the chosen SDK and API versions.
2. **Build the interface:** implement the five SwiftUI screens and accessibility using explicitly labeled development fixtures.
3. **Complete sandbox enrollment:** connect login, the Go API, provider setup, authenticated return flow, verified status updates, and persistent masked card display.
4. **Finish lifecycle handling:** implement removal, account deletion, retries, app locking, privacy controls, and safe operational logs.
5. **Run the real-card pilot:** use an activated live provider account on physical iPhones, confirm saving and removal with consenting cardholders, and distribute through TestFlight.

Provider approval is an external dependency. UI work can proceed while it is pending, but real-card support must remain unclaimed until the live pilot passes.

## Validation before the pilot

- Complete successful setup, additional authentication, rejection, cancellation, and interrupted-app recovery using provider test scenarios.
- Replay requests and webhooks; verify that a card appears once and remains associated with the correct account.
- Attempt cross-account reads, setup status access, changes, and removal; all must fail.
- Simulate provider failure during removal and account deletion; operations must remain recoverable and visibly pending.
- Inspect requests, storage, screenshots used in testing, logs, and crash reporting for unintended card-data or secret exposure.
- Verify card persistence, sign-out cleanup, VoiceOver, larger text, Reduce Motion, and real-device authentication.
- With the production account, save and remove an authorized real card without initiating a purchase. Confirm the result in both our app and the provider's records.

## Later contactless phase

Treat tap-to-pay as a separate project milestone with its own feasibility review. Apple's NFC & SE Platform supports eligible US deployments, but requires Apple approval, appropriate payment partnerships, and a supported device and OS. Network tokenization and device provisioning must be established independently of this saved-card integration. [Apple platform requirements](https://developer.apple.com/support/nfc-se-platform/)

Retain the account and card-focused UI where useful. Revisit enrollment, payment authentication, credential storage, partner contracts, and card removal for that phase; do not assume a database migration turns saved cards into contactless cards.

## Decisions remaining

- Whether Stripe approves the standalone storage use case, or which card-vault partner will support it.
- Final app name, Uno visual assets, bundle identifier, and Apple Developer team.
- Deployment environment and the SDK-compatible minimum iOS version.
- Privacy and retention terms, provider costs, and the first consenting pilot users.

References were checked on 3 October 2026. Confirm current provider behavior and eligibility before implementation and before the live pilot.
