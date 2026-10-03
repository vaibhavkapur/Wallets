import Foundation

/// DEVELOPMENT FIXTURE. An in-memory stand-in for the backend used by unit
/// tests, SwiftUI previews, and the `fixtures` app mode. It mimics the
/// backend's state machine (idempotent setup creation, server-side
/// verification before a card appears, pending removal during a provider
/// outage) but never contacts a provider and never represents a real card.
public actor FixtureWalletAPI: WalletAPI {
    public struct FixtureCard: Sendable {
        public var brand: String
        public var last4: String
        public var expMonth: Int
        public var expYear: Int
        public init(brand: String, last4: String, expMonth: Int, expYear: Int) {
            self.brand = brand; self.last4 = last4; self.expMonth = expMonth; self.expYear = expYear
        }
        public static let visa = FixtureCard(brand: "visa", last4: "4242", expMonth: 12, expYear: 2030)
        public static let mastercard = FixtureCard(brand: "mastercard", last4: "4444", expMonth: 3, expYear: 2029)
        public static let amex = FixtureCard(brand: "amex", last4: "0005", expMonth: 8, expYear: 2031)
    }

    private struct Account {
        var user: User
        var cards: [SavedCard] = []
        var attempts: [String: SetupAttempt] = [:]
        var keys: [String: String] = [:] // idempotency key -> attempt id
        var providerState: [String: ProviderState] = [:] // attempt id -> simulated provider
    }

    private enum ProviderState { case collecting(declineCode: String?), requiresAction, processing, succeeded(FixtureCard), canceled }

    private var accounts: [String: Account] = [:] // user id -> account
    private var sessions: [String: String] = [:] // token -> user id
    private var subjects: [String: String] = [:] // apple subject -> user id

    /// When true, removals and account deletion stay pending, as they would
    /// if the provider were unreachable.
    public var providerOutage = false
    /// Latency added to each call so previews feel realistic.
    public var latency: Duration = .zero

    public init() {}

    public func setProviderOutage(_ on: Bool) { providerOutage = on }
    public func setLatency(_ d: Duration) { latency = d }

    // MARK: WalletAPI

    public func signInWithApple(identityToken: String, nonce: String?) async throws -> SignInResponse {
        try await pause()
        guard !identityToken.isEmpty else { throw APIError.server(status: 401, code: "invalid_identity", message: "identity token could not be verified") }
        let subject = identityToken
        let userID: String
        if let existing = subjects[subject] {
            userID = existing
            if accounts[userID]?.user.status != "active" {
                throw APIError.server(status: 409, code: "account_deleting", message: "this account is being deleted")
            }
        } else {
            userID = "usr_fixture_\(UUID().uuidString.prefix(8))"
            subjects[subject] = userID
            accounts[userID] = Account(user: User(id: userID, email: "fixture@example.test", status: "active", createdAt: Date()))
        }
        let token = "ses_fixture_\(UUID().uuidString)"
        sessions[token] = userID
        return SignInResponse(sessionToken: token, expiresAt: Date().addingTimeInterval(3600), user: accounts[userID]!.user)
    }

    public func signOut(token: String) async throws {
        try await pause()
        _ = try account(token)
        sessions[token] = nil
    }

    public func me(token: String) async throws -> User {
        try await pause()
        return try account(token).user
    }

    public func listCards(token: String) async throws -> [SavedCard] {
        try await pause()
        return try account(token).cards.filter { $0.state != .removed }.sorted { a, b in
            if a.isPreferred != b.isPreferred { return a.isPreferred }
            return a.createdAt > b.createdAt
        }
    }

    public func startSetup(token: String, idempotencyKey: String, consentVersion: String) async throws -> SetupAttempt {
        try await pause()
        let userID = try userID(token)
        if let existing = accounts[userID]!.keys[idempotencyKey], let attempt = accounts[userID]!.attempts[existing] {
            return attempt.status.isTerminal ? attempt : withSession(attempt)
        }
        let id = "setup_fixture_\(UUID().uuidString.prefix(8))"
        let attempt = SetupAttempt(id: id, status: .collecting, failureCode: nil, card: nil, clientSession: nil, createdAt: Date(), updatedAt: Date())
        accounts[userID]!.attempts[id] = attempt
        accounts[userID]!.keys[idempotencyKey] = id
        accounts[userID]!.providerState[id] = .collecting(declineCode: nil)
        return withSession(attempt)
    }

    public func getSetup(token: String, id: String) async throws -> SetupAttempt {
        try await pause()
        let userID = try userID(token)
        guard accounts[userID]!.attempts[id] != nil else { throw notFound }
        return reconcile(userID: userID, attemptID: id)
    }

    public func closeSetup(token: String, id: String, failed: Bool) async throws -> SetupAttempt {
        try await pause()
        let userID = try userID(token)
        guard accounts[userID]!.attempts[id] != nil else { throw notFound }
        let before = reconcile(userID: userID, attemptID: id)
        if before.status.isTerminal { return before }
        var code: String? = nil
        if failed { code = before.failureCode ?? "client_reported_failure" }
        accounts[userID]!.providerState[id] = .canceled
        return update(userID: userID, attemptID: id, status: failed ? .failed : .canceled, failureCode: code, card: nil)
    }

    public func renameCard(token: String, id: String, nickname: String?) async throws -> SavedCard {
        try await pause()
        let userID = try userID(token)
        guard let idx = accounts[userID]!.cards.firstIndex(where: { $0.id == id && $0.state != .removed }) else { throw notFound }
        let trimmed = nickname?.trimmingCharacters(in: .whitespacesAndNewlines)
        let c = accounts[userID]!.cards[idx]
        let updated = SavedCard(id: c.id, brand: c.brand, last4: c.last4, expMonth: c.expMonth, expYear: c.expYear,
                                nickname: (trimmed?.isEmpty ?? true) ? nil : trimmed, isPreferred: c.isPreferred, state: c.state,
                                createdAt: c.createdAt, updatedAt: Date())
        accounts[userID]!.cards[idx] = updated
        return updated
    }

    public func setPreferredCard(token: String, id: String) async throws -> SavedCard {
        try await pause()
        let userID = try userID(token)
        guard accounts[userID]!.cards.contains(where: { $0.id == id && $0.state != .removed }) else { throw notFound }
        accounts[userID]!.cards = accounts[userID]!.cards.map { c in
            SavedCard(id: c.id, brand: c.brand, last4: c.last4, expMonth: c.expMonth, expYear: c.expYear, nickname: c.nickname,
                      isPreferred: c.id == id, state: c.state, createdAt: c.createdAt, updatedAt: c.id == id ? Date() : c.updatedAt)
        }
        return accounts[userID]!.cards.first { $0.id == id }!
    }

    public func removeCard(token: String, id: String) async throws -> SavedCard {
        try await pause()
        let userID = try userID(token)
        guard let idx = accounts[userID]!.cards.firstIndex(where: { $0.id == id }) else { throw notFound }
        let c = accounts[userID]!.cards[idx]
        if c.state == .removed { return c }
        let state: SavedCard.State = providerOutage ? .removalPending : .removed
        let updated = SavedCard(id: c.id, brand: c.brand, last4: c.last4, expMonth: c.expMonth, expYear: c.expYear, nickname: c.nickname,
                                isPreferred: state == .removed ? false : c.isPreferred, state: state, createdAt: c.createdAt, updatedAt: Date())
        accounts[userID]!.cards[idx] = updated
        return updated
    }

    public func deleteAccount(token: String) async throws -> AccountDeletionStatus {
        try await pause()
        let userID = try userID(token)
        sessions = sessions.filter { $0.value != userID }
        if providerOutage {
            accounts[userID]!.user = User(id: userID, email: nil, status: "deleting", createdAt: accounts[userID]!.user.createdAt)
            return .deleting
        }
        accounts[userID] = nil
        subjects = subjects.filter { $0.value != userID }
        return .deleted
    }

    // MARK: Simulation controls (what the "cardholder" and provider do)

    /// Finish the sheet with a fixture card; the next poll reports `saved`.
    public func simulateCompletion(attemptID: String, card: FixtureCard = .visa) {
        for (userID, acct) in accounts where acct.attempts[attemptID] != nil {
            accounts[userID]!.providerState[attemptID] = .succeeded(card)
        }
    }

    public func simulateAuthenticationRequired(attemptID: String) { setProvider(attemptID, .requiresAction) }
    public func simulateProcessing(attemptID: String) { setProvider(attemptID, .processing) }
    public func simulateDecline(attemptID: String, code: String = "card_declined") { setProvider(attemptID, .collecting(declineCode: code)) }

    /// Simulates the provider finishing a pending removal (worker retry).
    public func simulateProviderRecovered() {
        providerOutage = false
        for (userID, acct) in accounts {
            accounts[userID]!.cards = acct.cards.map { c in
                guard c.state == .removalPending else { return c }
                return SavedCard(id: c.id, brand: c.brand, last4: c.last4, expMonth: c.expMonth, expYear: c.expYear, nickname: c.nickname,
                                 isPreferred: false, state: .removed, createdAt: c.createdAt, updatedAt: Date())
            }
            if acct.user.status == "deleting" {
                accounts[userID] = nil
                subjects = subjects.filter { $0.value != userID }
            }
        }
    }

    /// Seeds a signed-in account with cards (previews).
    public func seed(token: String, cards: [FixtureCard]) {
        guard let userID = sessions[token] else { return }
        for (i, f) in cards.enumerated() {
            let card = SavedCard(id: "card_fixture_\(UUID().uuidString.prefix(8))", brand: f.brand, last4: f.last4, expMonth: f.expMonth,
                                 expYear: f.expYear, nickname: nil, isPreferred: i == 0, state: .saved,
                                 createdAt: Date().addingTimeInterval(Double(-i) * 86_400), updatedAt: Date())
            accounts[userID]!.cards.append(card)
        }
    }

    // MARK: internals

    private var notFound: APIError { .server(status: 404, code: "not_found", message: "not found") }

    private func pause() async throws {
        if latency > .zero { try await Task.sleep(for: latency) }
    }

    private func userID(_ token: String) throws -> String {
        guard let id = sessions[token], accounts[id] != nil else { throw APIError.unauthorized }
        guard accounts[id]!.user.status == "active" else { throw APIError.unauthorized }
        return id
    }

    private func account(_ token: String) throws -> Account { accounts[try userID(token)]! }

    private func setProvider(_ attemptID: String, _ state: ProviderState) {
        for (userID, acct) in accounts where acct.attempts[attemptID] != nil {
            accounts[userID]!.providerState[attemptID] = state
        }
    }

    private func withSession(_ a: SetupAttempt) -> SetupAttempt {
        let session = ClientSession(provider: "fixture", setupClientSecret: "fixture_secret_\(a.id)", customerRef: "cus_fixture",
                                    ephemeralKeySecret: "ek_fixture", ephemeralKeyExpiresAt: Date().addingTimeInterval(3600),
                                    publishableKey: "pk_test_fixture")
        return SetupAttempt(id: a.id, status: a.status, failureCode: a.failureCode, card: a.card, clientSession: session,
                            createdAt: a.createdAt, updatedAt: a.updatedAt)
    }

    private func reconcile(userID: String, attemptID: String) -> SetupAttempt {
        let current = accounts[userID]!.attempts[attemptID]!
        if current.status.isTerminal { return current }
        switch accounts[userID]!.providerState[attemptID] ?? .collecting(declineCode: nil) {
        case .collecting(let code):
            return update(userID: userID, attemptID: attemptID, status: .collecting, failureCode: code, card: nil)
        case .requiresAction:
            return update(userID: userID, attemptID: attemptID, status: .authenticationRequired, failureCode: nil, card: nil)
        case .processing:
            return update(userID: userID, attemptID: attemptID, status: .processing, failureCode: nil, card: nil)
        case .canceled:
            return update(userID: userID, attemptID: attemptID, status: .canceled, failureCode: nil, card: nil)
        case .succeeded(let f):
            let hasPreferred = accounts[userID]!.cards.contains { $0.isPreferred && $0.state != .removed }
            let card = SavedCard(id: "card_fixture_\(UUID().uuidString.prefix(8))", brand: f.brand, last4: f.last4, expMonth: f.expMonth,
                                 expYear: f.expYear, nickname: nil, isPreferred: !hasPreferred, state: .saved, createdAt: Date(), updatedAt: Date())
            accounts[userID]!.cards.append(card)
            return update(userID: userID, attemptID: attemptID, status: .saved, failureCode: nil, card: card)
        }
    }

    @discardableResult
    private func update(userID: String, attemptID: String, status: SetupAttempt.Status, failureCode: String?, card: SavedCard?) -> SetupAttempt {
        let cur = accounts[userID]!.attempts[attemptID]!
        let next = SetupAttempt(id: cur.id, status: status, failureCode: failureCode, card: card ?? cur.card, clientSession: nil,
                                createdAt: cur.createdAt, updatedAt: Date())
        accounts[userID]!.attempts[attemptID] = next
        return next
    }
}
