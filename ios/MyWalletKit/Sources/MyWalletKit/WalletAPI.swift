import Foundation

/// Errors surfaced by `WalletAPI` implementations.
public enum APIError: Error, Equatable, Sendable {
    /// The session is invalid or expired; the app must sign in again.
    case unauthorized
    /// The backend rejected the request with a structured error.
    case server(status: Int, code: String, message: String)
    /// The request could not reach the backend.
    case transport(String)
    /// The response could not be decoded.
    case decoding(String)

    public var userMessage: String {
        switch self {
        case .unauthorized: return "Your session has ended. Please sign in again."
        case .server(_, let code, let message):
            switch code {
            case "provider_unavailable": return "The card provider could not be reached. Please try again."
            case "account_deleting": return "This account is being deleted. Please try again later."
            default: return message
            }
        case .transport: return "MyWallet could not reach the server. Check your connection and try again."
        case .decoding: return "MyWallet received an unexpected response. Please try again."
        }
    }
}

/// The application API the app depends on. `URLSessionWalletAPI` talks to
/// the Go backend; `FixtureWalletAPI` is an explicitly labeled in-memory
/// stand-in for previews and tests.
public protocol WalletAPI: Sendable {
    func signInWithApple(identityToken: String, nonce: String?) async throws -> SignInResponse
    func signOut(token: String) async throws
    func me(token: String) async throws -> User
    func listCards(token: String) async throws -> [SavedCard]
    func startSetup(token: String, idempotencyKey: String, consentVersion: String) async throws -> SetupAttempt
    func getSetup(token: String, id: String) async throws -> SetupAttempt
    func closeSetup(token: String, id: String, failed: Bool) async throws -> SetupAttempt
    func renameCard(token: String, id: String, nickname: String?) async throws -> SavedCard
    func setPreferredCard(token: String, id: String) async throws -> SavedCard
    func removeCard(token: String, id: String) async throws -> SavedCard
    func deleteAccount(token: String) async throws -> AccountDeletionStatus
}

// MARK: - URLSession implementation

public final class URLSessionWalletAPI: WalletAPI {
    public let baseURL: URL
    private let session: URLSession
    private let decoder = WalletJSON.decoder()
    private let encoder = WalletJSON.encoder()

    public init(baseURL: URL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
    }

    public func signInWithApple(identityToken: String, nonce: String?) async throws -> SignInResponse {
        var body: [String: String] = ["identity_token": identityToken]
        if let nonce { body["nonce"] = nonce }
        return try await send("POST", "/v1/auth/apple", token: nil, body: body)
    }

    public func signOut(token: String) async throws {
        let _: Empty = try await send("POST", "/v1/auth/sign-out", token: token, body: nil as Empty?)
    }

    public func me(token: String) async throws -> User {
        try await send("GET", "/v1/me", token: token, body: nil as Empty?)
    }

    public func listCards(token: String) async throws -> [SavedCard] {
        struct Wrapper: Decodable { let cards: [SavedCard] }
        let w: Wrapper = try await send("GET", "/v1/cards", token: token, body: nil as Empty?)
        return w.cards
    }

    public func startSetup(token: String, idempotencyKey: String, consentVersion: String) async throws -> SetupAttempt {
        struct Body: Encodable { let consent: Consent; struct Consent: Encodable { let accepted: Bool; let version: String } }
        return try await send("POST", "/v1/card-setups", token: token, body: Body(consent: .init(accepted: true, version: consentVersion)),
                              headers: ["Idempotency-Key": idempotencyKey])
    }

    public func getSetup(token: String, id: String) async throws -> SetupAttempt {
        try await send("GET", "/v1/card-setups/\(id)", token: token, body: nil as Empty?)
    }

    public func closeSetup(token: String, id: String, failed: Bool) async throws -> SetupAttempt {
        try await send("POST", "/v1/card-setups/\(id)/close", token: token, body: ["outcome": failed ? "failed" : "canceled"])
    }

    public func renameCard(token: String, id: String, nickname: String?) async throws -> SavedCard {
        // Encode an explicit null to clear the nickname.
        let raw: [String: Any] = ["nickname": nickname as Any? ?? NSNull()]
        let data = try JSONSerialization.data(withJSONObject: raw)
        return try await sendRaw("PATCH", "/v1/cards/\(id)", token: token, body: data, headers: [:])
    }

    public func setPreferredCard(token: String, id: String) async throws -> SavedCard {
        try await send("PATCH", "/v1/cards/\(id)", token: token, body: ["is_preferred": true])
    }

    public func removeCard(token: String, id: String) async throws -> SavedCard {
        try await send("DELETE", "/v1/cards/\(id)", token: token, body: nil as Empty?)
    }

    public func deleteAccount(token: String) async throws -> AccountDeletionStatus {
        struct Wrapper: Decodable { let status: AccountDeletionStatus }
        let w: Wrapper = try await send("DELETE", "/v1/account", token: token, body: nil as Empty?)
        return w.status
    }

    // MARK: plumbing

    private struct Empty: Codable {}
    private struct ErrorEnvelope: Decodable {
        struct Inner: Decodable { let code: String; let message: String }
        let error: Inner
    }

    private func send<B: Encodable, R: Decodable>(_ method: String, _ path: String, token: String?, body: B?,
                                                  headers: [String: String] = [:]) async throws -> R {
        let data = try body.map { try encoder.encode($0) }
        return try await sendRaw(method, path, token: token, body: data, headers: headers)
    }

    private func sendRaw<R: Decodable>(_ method: String, _ path: String, token: String?, body: Data?,
                                       headers: [String: String]) async throws -> R {
        var req = URLRequest(url: baseURL.appending(path: path))
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        if let body {
            req.httpBody = body
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        for (k, v) in headers { req.setValue(v, forHTTPHeaderField: k) }

        let (data, response): (Data, URLResponse)
        do {
            (data, response) = try await session.data(for: req)
        } catch {
            throw APIError.transport(error.localizedDescription)
        }
        guard let http = response as? HTTPURLResponse else { throw APIError.transport("not an HTTP response") }
        if http.statusCode == 401 { throw APIError.unauthorized }
        if http.statusCode >= 400 {
            if let env = try? decoder.decode(ErrorEnvelope.self, from: data) {
                throw APIError.server(status: http.statusCode, code: env.error.code, message: env.error.message)
            }
            throw APIError.server(status: http.statusCode, code: "http_\(http.statusCode)", message: "Request failed.")
        }
        if R.self == Empty.self { return Empty() as! R }
        do {
            return try decoder.decode(R.self, from: data)
        } catch {
            throw APIError.decoding(String(describing: error))
        }
    }
}
