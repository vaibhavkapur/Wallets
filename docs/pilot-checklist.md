# Validation before the pilot

Each item from the plan's "Validation before the pilot" section, with what already covers it automatically and what still has to be done by hand against Stripe test mode and on physical devices. Real-card support stays unclaimed until every row is checked.

| Plan item | Automated coverage | Remaining manual step |
|---|---|---|
| Successful setup, additional authentication, rejection, cancellation, interrupted-app recovery | Go: `TestAddCardHappyPathAndReplay`, `TestSetupStateTracking` (requires_action, decline code, close-as-failed, close-after-success, processing → worker), `TestStaleSetupsExpire`. Kit: `addCardHappyPath`, `sheetCanceledClosesAttempt`, `declineThenFailureShowsMessage`, `clientReportedCancelButProviderSucceededStillSaves`, `processingStaysPendingThenResolvesOnResume`, `interruptedBeforeSheetIsReplayedNotDuplicated` | Repeat in `stripe` mode with test cards `4242…4242`, `4000 0025 0000 3155` (3-D Secure, including the return via `mywallet://stripe-redirect` from a bank-app simulation), `4000 0000 0000 0002`; force-quit the app mid-sheet and relaunch |
| Replay requests and webhooks; a card appears once and stays with the right account | Go: `TestWebhookDedupeAndSingleCard` (6 concurrent duplicate deliveries + 6 polls → 1 card), idempotent `POST /v1/card-setups`; Stripe: `TestWebhookSignature` | `stripe trigger setup_intent.succeeded` and the dashboard "resend" button against a running backend |
| Cross-account reads, setup status, changes, removal must fail | Go: `TestCrossAccountAccessFails`, `TestCustomerMismatchIsRejected`; Stripe client: `TestClientSessionRejectsForeignSetup` | — |
| Provider failure during removal and account deletion; operations recoverable and visibly pending | Go: `TestRemoveCardPendingUntilProviderConfirms`, `TestDetachedWebhookCompletesPendingRemoval`, `TestDeleteAccountRecoverable`. Kit: `renamePreferAndRemove`, `deleteAccountSignsOutAndReportsStatus` | Block egress to `api.stripe.com` from the backend during a removal; confirm the tile shows "Removal pending" and clears after connectivity returns |
| Inspect requests, storage, screenshots, logs, crash reporting for card data or secrets | Go: `GET` never returns `client_session`; log middleware records route patterns only; card JSON asserted to contain no number/CVC/secret keys. Kit: Keychain-only persistence, in-memory cards | Proxy the device (e.g. Proxyman) through one full enrollment; export the app container; review backend logs; confirm no crash reporter is linked (none is today) |
| Card persistence, sign-out cleanup, VoiceOver, larger text, Reduce Motion, real-device authentication | Go: `TestSignInRestoresSameAccount`, `TestSignOutRevokesSession`. Kit: `signInThenRestoreKeepsSameAccountAndCards`, `signOutClearsLocalState`, `expiredSessionSignsOut`; `CardDisplay.accessibilityLabel` tested | On device: VoiceOver pass over all five screens (tiles read "nickname, brand, ending in 4 2 4 2, expires MM/YY, Saved, preferred card"); Dynamic Type at AX5; Reduce Motion on (tile animations are disabled via `Theme.motion`); Face ID lock/unlock and the app-switcher shield |
| With the production account, save and remove an authorized real card without a purchase; confirm in app and provider records | — | Pilot only: activated live keys on the production host, consenting cardholders, physical iPhones via TestFlight; verify the PaymentMethod on the Customer in the Stripe dashboard, then its detachment |

## Decisions remaining

Unchanged from the plan; none are resolved by code.

- Whether Stripe approves the standalone card-storage use case, or which card-vault partner will support it. The provider is behind `internal/provider.Provider`; a second implementation plugs in without touching the API or the app.
- Final app name, MyWallet visual assets (the asset catalog has an empty `AppIcon` set), bundle identifier (placeholder `com.mywallet.ios` in `project.pbxproj`, `Info.plist`, `KeychainCredentialStore`), and Apple Developer team.
- Deployment environment, and the SDK-compatible minimum iOS version (the project targets iOS 17 for `@Observable`; stripe-ios 26.x requires iOS 15+).
- Privacy and retention terms (what the consent text version `2026-10` will finally say; how long audit rows are kept), provider costs, and the first consenting pilot users.
- `STRIPE_API_VERSION` must match the value the resolved stripe-ios release uses for ephemeral keys (`2020-08-27` for 26.12.1) before the first test-mode run.
