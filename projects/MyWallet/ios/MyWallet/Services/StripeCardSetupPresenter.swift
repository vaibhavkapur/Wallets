import SwiftUI
import UIKit
import MyWalletKit
#if canImport(StripePaymentSheet)
import StripePaymentSheet
#endif

/// Presents Stripe's PaymentSheet in setup mode. Card fields and any
/// additional authentication are handled by the SDK, which talks to Stripe
/// directly; the app never sees the card number or CVC. The SDK's
/// completion is only a hint — `WalletModel` confirms the result with the
/// backend before a card appears in the wallet.
@MainActor
final class StripeCardSetupPresenter: CardSetupPresenter {
    func present(session: ClientSession, attemptID: String) async -> CardSetupOutcome {
        #if canImport(StripePaymentSheet)
        guard session.provider == "stripe" else {
            return .failed(message: "The server is not configured for Stripe.")
        }
        guard let host = Self.topViewController() else {
            return .failed(message: "MyWallet could not open secure card entry. Please try again.")
        }
        STPAPIClient.shared.publishableKey = session.publishableKey

        var configuration = PaymentSheet.Configuration()
        configuration.merchantDisplayName = "MyWallet"
        configuration.customer = .init(id: session.customerRef, ephemeralKeySecret: session.ephemeralKeySecret)
        configuration.returnURL = "mywallet://stripe-redirect"
        configuration.allowsDelayedPaymentMethods = false
        configuration.primaryButtonLabel = "Save Card"
        configuration.appearance = Self.appearance()
        // Manual card entry only. Apple Pay is intentionally not configured,
        // and the backend restricts the SetupIntent to the card type.

        let sheet = PaymentSheet(setupIntentClientSecret: session.setupClientSecret, configuration: configuration)
        return await withCheckedContinuation { continuation in
            sheet.present(from: host) { result in
                switch result {
                case .completed:
                    continuation.resume(returning: .completed)
                case .canceled:
                    continuation.resume(returning: .canceled)
                case .failed(let error):
                    continuation.resume(returning: .failed(message: error.localizedDescription))
                }
            }
        }
        #else
        _ = session
        _ = attemptID
        return .failed(message: "The Stripe SDK is not linked in this build. Add the StripePaymentSheet package to the MyWallet target.")
        #endif
    }

    #if canImport(StripePaymentSheet)
    /// Adapts the sheet to MyWallet: native type, our teal accent, rounded fields.
    private static func appearance() -> PaymentSheet.Appearance {
        var a = PaymentSheet.Appearance()
        a.colors.primary = UIColor(Theme.accent)
        a.cornerRadius = 12
        a.font.base = UIFont.preferredFont(forTextStyle: .body)
        a.primaryButton.cornerRadius = 14
        return a
    }

    private static func topViewController() -> UIViewController? {
        let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
        let window = scenes.flatMap(\.windows).first { $0.isKeyWindow } ?? scenes.first?.windows.first
        var top = window?.rootViewController
        while let presented = top?.presentedViewController { top = presented }
        return top
    }
    #endif
}
