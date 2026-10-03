import AuthenticationServices
import CryptoKit
import SwiftUI
import MyWalletKit

struct WelcomeView: View {
    @Environment(WalletModel.self) private var model
    @Environment(\.appMode) private var mode
    @Environment(\.colorScheme) private var scheme
    @State private var nonce = ""
    @State private var isSigningIn = false

    var body: some View {
        VStack(spacing: 0) {
            Spacer()
            VStack(alignment: .leading, spacing: 18) {
                Text("MyWallet")
                    .font(.system(size: 44, weight: .bold, design: .rounded))
                    .foregroundStyle(Theme.accent)
                Text("Your cards, kept safely in one place.")
                    .font(.title2.weight(.semibold))
                point("lock.shield", "Card details go straight to our payment provider. MyWallet only keeps a reference and the last four digits.")
                point("eye.slash", "Your full card number is never stored on this device or on MyWallet's servers.")
                point("person.badge.key", "Sign in with Apple keeps your wallet tied to your Apple account, so it is there when you come back.")
            }
            .padding(.horizontal, 28)
            Spacer()
            VStack(spacing: 12) {
                SignInWithAppleButton(.signIn) { request in
                    nonce = Self.randomNonce()
                    request.requestedScopes = [.email]
                    request.nonce = Self.sha256(nonce)
                } onCompletion: { result in
                    Task { await handle(result) }
                }
                .signInWithAppleButtonStyle(scheme == .dark ? .white : .black)
                .frame(height: 50)
                .accessibilityLabel("Sign in with Apple")

                if mode.allowsDevSignIn {
                    Button {
                        Task {
                            isSigningIn = true
                            await model.signIn(identityToken: "dev:\(deviceSubject)", nonce: nil)
                            isSigningIn = false
                        }
                    } label: {
                        Text("Continue with a development account")
                            .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                    .disabled(isSigningIn)
                    if let label = mode.fixtureLabel {
                        Pill(text: label, tint: .orange)
                    }
                }
                Text("Saving a card does not authorize payments. You can remove a card or delete your account at any time.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .padding(.horizontal, 28)
            .padding(.bottom, 24)
        }
        .background(Theme.canvas(scheme))
    }

    private func point(_ symbol: String, _ text: String) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: symbol)
                .font(.title3)
                .foregroundStyle(Theme.accent)
                .frame(width: 28)
                .accessibilityHidden(true)
            Text(text)
                .font(.body)
                .foregroundStyle(.secondary)
        }
    }

    private func handle(_ result: Result<ASAuthorization, Error>) async {
        switch result {
        case .success(let auth):
            guard let credential = auth.credential as? ASAuthorizationAppleIDCredential,
                  let data = credential.identityToken,
                  let token = String(data: data, encoding: .utf8) else {
                model.alertMessage = "Apple did not return an identity token. Please try again."
                return
            }
            await model.signIn(identityToken: token, nonce: nonce)
        case .failure(let error):
            if let authError = error as? ASAuthorizationError, authError.code == .canceled { return }
            model.alertMessage = error.localizedDescription
        }
    }

    /// Stable per-install subject for the development backend.
    private var deviceSubject: String {
        let key = "mywallet.devSubject"
        if let s = UserDefaults.standard.string(forKey: key) { return s }
        let s = UUID().uuidString.lowercased()
        UserDefaults.standard.set(s, forKey: key)
        return s
    }

    private static func randomNonce() -> String {
        SymmetricKey(size: .bits256).withUnsafeBytes { Data($0) }.map { String(format: "%02x", $0) }.joined()
    }

    private static func sha256(_ s: String) -> String {
        SHA256.hash(data: Data(s.utf8)).map { String(format: "%02x", $0) }.joined()
    }
}
