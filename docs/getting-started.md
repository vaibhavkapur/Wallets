# Getting started

## Prerequisites

- Go 1.22 or later (the module is pinned to the toolchain in `backend/go.mod`).
- macOS. Xcode 16 or later to build and run the iOS app. The Command Line Tools alone are enough to run the Go suite and the `MyWalletKit` tests.
- `curl` and `jq` for the smoke script.
- No local PostgreSQL is needed for development or tests: an embedded PostgreSQL 16 is started on demand (`--dev-embedded-postgres` / tests). Production expects `DATABASE_URL`.

## 1. Backend with the fake provider

```bash
make test-go      # vet + tests, race detector
make run-dev      # PROVIDER=fake DEV_SIGN_IN=1 on 127.0.0.1:8080
make smoke        # in another terminal
```

`PROVIDER=fake` is an explicitly labeled fixture: every "card" it returns is invented, it refuses to start if `STRIPE_SECRET_KEY` is set, and it exposes two development-only endpoints (`POST /dev/card-setups/{id}/complete` and `/outcome`) that stand in for the cardholder finishing the sheet. `DEV_SIGN_IN=1` accepts identity tokens of the form `dev:<subject>[:<email>]` instead of Apple's; both log a warning at startup.

## 2. App modes

The app reads `MyWalletMode` and `MyWalletAPIBaseURL` from `Info.plist`, which are filled from the `MYWALLET_MODE` and `MYWALLET_API_BASE_URL` build settings on the `MyWallet` target.

| Mode | Network | Card entry | Sign-in | Use |
|---|---|---|---|---|
| `fixtures` (Debug default) | none | labeled simulation sheet | Apple button (needs a team) or "Continue with a development account" | UI work, previews, accessibility passes |
| `dev-backend` | local Go API | labeled simulation sheet driving `/dev` endpoints | same | end-to-end against the real API, worker, and database |
| `stripe` (Release default) | real API | Stripe PaymentSheet | Sign in with Apple only | test-mode development and the live pilot |

To use `dev-backend` on the simulator: set `MYWALLET_MODE = dev-backend` on the Debug configuration (the base URL already defaults to `http://127.0.0.1:8080`) and run `make run-dev`. On a physical device, point `MYWALLET_API_BASE_URL` at your Mac's LAN address; iOS requires HTTPS for non-local hosts unless you add an ATS exception for development.

### Running on the iOS Simulator from the command line

```bash
cd ios
xcodebuild -project MyWallet.xcodeproj -scheme MyWallet \
  -destination 'platform=iOS Simulator,name=iPhone 17' \
  -derivedDataPath build build                     # add MYWALLET_MODE=dev-backend to override the mode
xcrun simctl boot "iPhone 17" && open -a Simulator
xcrun simctl install booted build/Build/Products/Debug-iphonesimulator/MyWallet.app
xcrun simctl launch booted com.mywallet.ios
```

The first build resolves stripe-ios from GitHub (a few minutes). Do not pass `CODE_SIGNING_ALLOWED=NO`: the default ad-hoc simulator signature is what lets the app use the Keychain, and an unsigned build silently fails to store the session token, so "Add Card" does nothing after consent.

The fixture label appears on the Welcome and Wallet screens whenever the mode is not `stripe`.

## 3. Sign in with Apple

1. Register the bundle identifier (placeholder `com.mywallet.ios`; see "Decisions remaining" in the plan) with the Sign in with Apple capability in your Apple Developer account and select the team in the target's Signing settings. The entitlement file `MyWallet/MyWallet.entitlements` is already present.
2. Set `APPLE_BUNDLE_IDS=<bundle id>` on the backend. The server verifies the identity token's RS256 signature against Apple's JWKS, the issuer, the audience, expiry, and the SHA-256 nonce the app generates per request.
3. Apple returns the user's email only on first authorization; the backend records it then (if verified) and never asks again.

## 4. Stripe (test mode first)

1. Create or use a Stripe account. **Before live development, confirm with Stripe that a standalone card-saving wallet is an accepted use of Customer + SetupIntent storage** (plan § Provider choice). The code is provider-abstracted (`internal/provider`) if a different vault partner is required.
2. Set `STRIPE_SECRET_KEY`, `STRIPE_PUBLISHABLE_KEY`, and `STRIPE_WEBHOOK_SECRET` from the test-mode dashboard. The server refuses mixed live/test key pairs.
3. Pin `STRIPE_API_VERSION`. Ephemeral keys are minted with this version and must match the version the iOS SDK requests (`STPAPIClient.apiVersion`). The project depends on stripe-ios 26.12.1 (up to the next major) in `project.pbxproj`; that release uses `2020-08-27`, which is the `.env.example` default. Check the SDK's `StripeCore/StripeCore/Source/API Bindings/STPAPIClient.swift` (`private let APIVersion`) for the value when bumping either side.
4. Create a webhook endpoint for `https://<host>/v1/webhooks/provider` subscribed to `setup_intent.succeeded`, `setup_intent.setup_failed`, `setup_intent.canceled`, `setup_intent.requires_action`, `payment_method.detached`, and `payment_method.automatically_updated`. For local testing, `stripe listen --forward-to 127.0.0.1:8080/v1/webhooks/provider` prints the signing secret.
5. The app's URL scheme `mywallet://stripe-redirect` is registered in `Info.plist` and passed as the PaymentSheet `returnURL`, so a 3-D Secure challenge that leaves the app returns to it.
6. Run the app in `stripe` mode with `MYWALLET_API_BASE_URL` set to the backend. Use Stripe's test cards (e.g. `4242 4242 4242 4242`, `4000 0025 0000 3155` for authentication, `4000 0000 0000 0002` for decline).

What the SetupIntent is created with: `payment_method_types[]=card`, `usage=on_session`, the user's Customer, and an `Idempotency-Key` derived from the attempt ID. Apple Pay, Link, bank accounts, and card scanning are not enabled.

## 5. Production notes

- Deploy one `server` binary and a managed PostgreSQL. Migrations run at startup.
- Keep provider secret keys in server-side secret storage. Only the publishable key is sent to the app, inside a setup response.
- Logs contain method, route pattern, status, and latency only: no headers, bodies, query strings, identifiers, or secrets.
- Run the worker in-process (default) or as a separate replica with `RUN_WORKER=true` on exactly the instances you want; claims use `FOR UPDATE SKIP LOCKED`, so multiple workers are safe.
- Set `SESSION_TTL` to your policy (default 30 days). Sign-out revokes the session server-side; account deletion revokes all sessions immediately.
