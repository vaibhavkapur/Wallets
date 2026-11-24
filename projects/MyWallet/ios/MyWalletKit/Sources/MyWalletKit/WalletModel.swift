import Foundation
import Observation

/// The consent text version the app shows before presenting the sheet. Bump
/// when the wording changes; the backend records it with each attempt.
public let currentConsentVersion = "2026-10"

/// Application state for the wallet. One instance lives for the app's
/// lifetime; views read it and call its intent methods.
@MainActor
@Observable
public final class WalletModel {
    public enum Phase: Equatable, Sendable {
        case starting
        case signedOut
        case signedIn
    }

    /// Progress of the current Add Card flow, for the UI.
    public enum EnrollmentActivity: Equatable, Sendable {
        case idle
        case starting
        case presentingSheet
        case confirming
        /// The provider has not reported a final result yet; the attempt
        /// is kept and re-checked on resume or on demand.
        case pending(SetupAttempt)
    }

    public private(set) var phase: Phase = .starting
    public private(set) var user: User?
    public private(set) var cards: [SavedCard] = []
    public private(set) var isRefreshing = false
    public private(set) var enrollment: EnrollmentActivity = .idle
    public private(set) var accountDeletion: AccountDeletionStatus?
    /// A user-facing message for the last failure; views bind to it.
    public var alertMessage: String?
    /// Set briefly after a successful save so the wallet can highlight it.
    public private(set) var lastSavedCardID: String?

    public var appLockEnabled: Bool {
        get { preferences.appLockEnabled }
        set { preferences.appLockEnabled = newValue }
    }

    public var preferredCard: SavedCard? { cards.first { $0.isPreferred && $0.state != .removed } }

    private let api: WalletAPI
    private let credentials: CredentialStore
    private let preferences: PreferenceStore
    private let presenter: CardSetupPresenter
    private let sleep: @Sendable (Duration) async throws -> Void
    private let pollInterval: Duration
    private let maxPolls: Int

    private var token: String? {
        get { credentials.read(.sessionToken) }
        set { credentials.write(newValue, for: .sessionToken) }
    }

    public init(api: WalletAPI, credentials: CredentialStore, preferences: PreferenceStore, presenter: CardSetupPresenter,
                pollInterval: Duration = .seconds(1), maxPolls: Int = 8,
                sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) }) {
        self.api = api
        self.credentials = credentials
        self.preferences = preferences
        self.presenter = presenter
        self.pollInterval = pollInterval
        self.maxPolls = maxPolls
        self.sleep = sleep
    }

    // MARK: - Session

    /// Restores a previous sign-in, loads the wallet, and resumes any
    /// interrupted enrollment. Call once at launch.
    public func start() async {
        guard let token else {
            phase = .signedOut
            return
        }
        do {
            user = try await api.me(token: token)
            phase = .signedIn
            await refreshCards()
            await resumePendingSetup()
        } catch APIError.unauthorized {
            clearLocalAccountState()
            phase = .signedOut
        } catch {
            // Offline at launch: stay signed in with an empty list and let
            // the user refresh. Card metadata is intentionally not cached.
            phase = .signedIn
            alertMessage = (error as? APIError)?.userMessage ?? error.localizedDescription
        }
    }

    public func signIn(identityToken: String, nonce: String?) async {
        do {
            let res = try await api.signInWithApple(identityToken: identityToken, nonce: nonce)
            token = res.sessionToken
            user = res.user
            phase = .signedIn
            preferences.hasSeenWelcome = true
            await refreshCards()
            await resumePendingSetup()
        } catch {
            alertMessage = (error as? APIError)?.userMessage ?? error.localizedDescription
        }
    }

    public func signOut() async {
        if let token {
            try? await api.signOut(token: token)
        }
        clearLocalAccountState()
        phase = .signedOut
    }

    /// Called when the app returns to the foreground.
    public func didBecomeActive() async {
        guard phase == .signedIn else { return }
        await refreshCards()
        await resumePendingSetup()
    }

    // MARK: - Cards

    public func refreshCards() async {
        guard let token else { return }
        isRefreshing = true
        defer { isRefreshing = false }
        do {
            cards = try await api.listCards(token: token)
        } catch APIError.unauthorized {
            clearLocalAccountState()
            phase = .signedOut
        } catch {
            alertMessage = (error as? APIError)?.userMessage ?? error.localizedDescription
        }
    }

    public func rename(_ card: SavedCard, to nickname: String?) async {
        guard let token else { return }
        do {
            let updated = try await api.renameCard(token: token, id: card.id, nickname: nickname)
            replace(updated)
        } catch {
            await handle(error)
        }
    }

    public func setPreferred(_ card: SavedCard) async {
        guard let token else { return }
        do {
            _ = try await api.setPreferredCard(token: token, id: card.id)
            await refreshCards()
        } catch {
            await handle(error)
        }
    }

    /// Removes a card. The card stays visible as "Removal pending" until the
    /// backend confirms the provider detached it.
    public func remove(_ card: SavedCard) async {
        guard let token else { return }
        do {
            let updated = try await api.removeCard(token: token, id: card.id)
            if updated.state == .removed {
                cards.removeAll { $0.id == card.id }
            } else {
                replace(updated)
            }
        } catch {
            await handle(error)
        }
    }

    // MARK: - Add card

    /// Runs the full enrollment: start (idempotent), present the native
    /// sheet, then confirm the server-verified result.
    public func addCard() async {
        guard let token, enrollment == .idle || isPendingEnrollment else { return }
        enrollment = .starting
        // Reuse the key of an unfinished attempt so a retry replays rather
        // than creating a second provider object.
        var pending = credentials.pendingSetup ?? PendingSetup(idempotencyKey: UUID().uuidString)
        credentials.pendingSetup = pending
        do {
            var attempt = try await api.startSetup(token: token, idempotencyKey: pending.idempotencyKey, consentVersion: currentConsentVersion)
            pending.attemptID = attempt.id
            credentials.pendingSetup = pending

            if attempt.status.isTerminal {
                finish(attempt)
                return
            }
            guard let session = attempt.clientSession else {
                // Replay of an attempt the sheet already handled; confirm.
                attempt = try await pollUntilTerminal(token: token, id: attempt.id)
                finish(attempt)
                return
            }
            enrollment = .presentingSheet
            let outcome = await presenter.present(session: session, attemptID: attempt.id)
            enrollment = .confirming
            switch outcome {
            case .completed:
                attempt = try await pollUntilTerminal(token: token, id: attempt.id)
            case .canceled:
                attempt = try await api.closeSetup(token: token, id: attempt.id, failed: false)
            case .failed(let message):
                attempt = try await api.closeSetup(token: token, id: attempt.id, failed: true)
                if attempt.status == .failed { alertMessage = message }
            }
            finish(attempt)
        } catch {
            enrollment = .idle
            await handle(error)
        }
    }

    /// Re-checks an enrollment the provider left open.
    public func checkPendingEnrollment() async {
        await resumePendingSetup()
    }

    /// Abandons a pending enrollment (the attempt is closed server-side).
    public func cancelPendingEnrollment() async {
        guard let token, let pending = credentials.pendingSetup, let id = pending.attemptID else {
            credentials.pendingSetup = nil
            enrollment = .idle
            return
        }
        do {
            let attempt = try await api.closeSetup(token: token, id: id, failed: false)
            finish(attempt)
        } catch {
            await handle(error)
        }
    }

    private var isPendingEnrollment: Bool {
        if case .pending = enrollment { return true }
        return false
    }

    private func resumePendingSetup() async {
        guard let token, let pending = credentials.pendingSetup else { return }
        do {
            var attempt: SetupAttempt
            if let id = pending.attemptID {
                attempt = try await api.getSetup(token: token, id: id)
            } else {
                // Crashed before the attempt ID was recorded: replay the key.
                attempt = try await api.startSetup(token: token, idempotencyKey: pending.idempotencyKey, consentVersion: currentConsentVersion)
                credentials.pendingSetup = PendingSetup(idempotencyKey: pending.idempotencyKey, attemptID: attempt.id)
            }
            if attempt.status == .collecting {
                // The sheet is gone; nothing can finish this attempt.
                attempt = try await api.closeSetup(token: token, id: attempt.id, failed: false)
            } else if !attempt.status.isTerminal {
                attempt = try await pollUntilTerminal(token: token, id: attempt.id, polls: 2)
            }
            finish(attempt)
        } catch APIError.server(let status, _, _) where status == 404 {
            credentials.pendingSetup = nil
            enrollment = .idle
        } catch {
            await handle(error)
        }
    }

    private func pollUntilTerminal(token: String, id: String, polls: Int? = nil) async throws -> SetupAttempt {
        var attempt = try await api.getSetup(token: token, id: id)
        var remaining = polls ?? maxPolls
        while !attempt.status.isTerminal && remaining > 0 {
            try await sleep(pollInterval)
            attempt = try await api.getSetup(token: token, id: id)
            remaining -= 1
        }
        return attempt
    }

    private func finish(_ attempt: SetupAttempt) {
        guard attempt.status.isTerminal else {
            enrollment = .pending(attempt)
            return
        }
        credentials.pendingSetup = nil
        enrollment = .idle
        switch attempt.status {
        case .saved:
            lastSavedCardID = attempt.card?.id
            Task { await refreshCards() }
        case .failed:
            if alertMessage == nil { alertMessage = Self.failureMessage(for: attempt.failureCode) }
        default:
            break
        }
    }

    public static func failureMessage(for code: String?) -> String {
        switch code {
        case "customer_mismatch", "setup_missing": return "MyWallet could not verify this card with the provider, so it was not saved."
        case "provider_unavailable": return "The card provider could not be reached. Please try again."
        case "expired": return "This card setup timed out. Please start again."
        case "insufficient_funds", "card_declined", "generic_decline": return "The card issuer declined the setup. The card was not saved."
        case "expired_card": return "This card has expired and cannot be saved."
        case "incorrect_cvc", "incorrect_number": return "The card details could not be verified. Please check them and try again."
        default: return "The card could not be saved. Please try again."
        }
    }

    // MARK: - Account

    public func deleteAccount() async {
        guard let token else { return }
        do {
            let status = try await api.deleteAccount(token: token)
            accountDeletion = status
            clearLocalAccountState()
            phase = .signedOut
        } catch {
            await handle(error)
        }
    }

    // MARK: - Helpers

    private func replace(_ card: SavedCard) {
        if let i = cards.firstIndex(where: { $0.id == card.id }) {
            cards[i] = card
        } else {
            cards.append(card)
        }
    }

    private func handle(_ error: Error) async {
        if case APIError.unauthorized = error {
            clearLocalAccountState()
            phase = .signedOut
            return
        }
        alertMessage = (error as? APIError)?.userMessage ?? error.localizedDescription
    }

    /// Clears everything account-specific held on the device.
    private func clearLocalAccountState() {
        credentials.clearAll()
        user = nil
        cards = []
        enrollment = .idle
        lastSavedCardID = nil
    }
}
