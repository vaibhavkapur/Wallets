import SwiftUI
import MyWalletKit
#if canImport(StripeCore)
import StripeCore
#endif

@main
struct MyWalletApp: App {
    @Environment(\.scenePhase) private var scenePhase
    @State private var environment = AppEnvironment.fromBundle()
    @State private var isShieldVisible = false

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(environment.model)
                .environment(environment.lock)
                .environment(environment.simulatedPresenter)
                .environment(\.appMode, environment.mode)
                .overlay {
                    // Obscure card tiles in the app switcher.
                    if isShieldVisible {
                        PrivacyShieldView().transition(.opacity)
                    }
                }
                .task { await environment.model.start() }
                .onOpenURL { url in environment.handle(url) }
        }
        .onChange(of: scenePhase) { _, phase in
            switch phase {
            case .active:
                isShieldVisible = false
                Task { await environment.model.didBecomeActive() }
            case .inactive:
                isShieldVisible = true
            case .background:
                isShieldVisible = true
                environment.lock.didEnterBackground()
            @unknown default:
                break
            }
        }
    }
}

/// How the app is wired: labeled fixtures, a local development backend, or
/// the real provider. Read from Info.plist (MyWalletMode / MyWalletAPIBaseURL), which
/// the build settings fill in per configuration.
enum AppMode: String {
    /// No network. Explicitly labeled fixture cards for UI work.
    case fixtures
    /// Local Go backend with PROVIDER=fake and DEV_SIGN_IN=1.
    case devBackend = "dev-backend"
    /// Real backend with Stripe PaymentSheet and Sign in with Apple.
    case stripe

    var usesRealProvider: Bool { self == .stripe }
    var allowsDevSignIn: Bool { self != .stripe }
    var fixtureLabel: String? {
        switch self {
        case .fixtures: return "Fixture mode — no real cards"
        case .devBackend: return "Development backend — no real cards"
        case .stripe: return nil
        }
    }
}

private struct AppModeKey: EnvironmentKey {
    static let defaultValue: AppMode = .fixtures
}

extension EnvironmentValues {
    var appMode: AppMode {
        get { self[AppModeKey.self] }
        set { self[AppModeKey.self] = newValue }
    }
}

/// Composition root.
@MainActor
final class AppEnvironment {
    let mode: AppMode
    let model: WalletModel
    let lock: LockController
    let simulatedPresenter: SimulatedCardSetupPresenter

    static func fromBundle() -> AppEnvironment {
        let info = Bundle.main.infoDictionary ?? [:]
        let mode = (info["MyWalletMode"] as? String).flatMap(AppMode.init(rawValue:)) ?? .fixtures
        let base = (info["MyWalletAPIBaseURL"] as? String).flatMap(URL.init(string:))
        return AppEnvironment(mode: mode, baseURL: base)
    }

    init(mode: AppMode, baseURL: URL?) {
        self.mode = mode
        let preferences = UserDefaultsPreferenceStore()
        let credentials: CredentialStore = KeychainCredentialStore()
        let simulated = SimulatedCardSetupPresenter()
        self.simulatedPresenter = simulated

        let api: WalletAPI
        let presenter: CardSetupPresenter
        switch mode {
        case .fixtures:
            let fixture = FixtureWalletAPI()
            Task { await fixture.setLatency(.milliseconds(250)) }
            api = fixture
            simulated.backend = .fixture(fixture)
            presenter = simulated
        case .devBackend:
            let url = baseURL ?? URL(string: "http://127.0.0.1:8080")!
            let client = URLSessionWalletAPI(baseURL: url)
            api = client
            simulated.backend = .devServer(DevBackendClient(baseURL: url, credentials: credentials))
            presenter = simulated
        case .stripe:
            guard let url = baseURL else {
                preconditionFailure("MyWalletAPIBaseURL is required in stripe mode")
            }
            api = URLSessionWalletAPI(baseURL: url)
            presenter = StripeCardSetupPresenter()
        }
        self.model = WalletModel(api: api, credentials: credentials, preferences: preferences, presenter: presenter)
        self.lock = LockController(preferences: preferences)
    }

    /// Return from an external authentication challenge (3-D Secure in a
    /// bank app or Safari) lands here via the mywallet:// URL scheme.
    func handle(_ url: URL) {
        #if canImport(StripeCore)
        _ = StripeAPI.handleURLCallback(with: url)
        #endif
    }
}
