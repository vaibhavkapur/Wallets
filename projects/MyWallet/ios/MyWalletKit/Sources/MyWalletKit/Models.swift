import Foundation

// Application records as returned by the MyWallet backend. None of these ever
// carry a full card number or CVV; the app only receives masked fields.

public struct User: Codable, Hashable, Sendable {
    public let id: String
    public let email: String?
    public let status: String
    public let createdAt: Date

    public init(id: String, email: String?, status: String, createdAt: Date) {
        self.id = id
        self.email = email
        self.status = status
        self.createdAt = createdAt
    }
}

public struct SignInResponse: Codable, Sendable {
    public let sessionToken: String
    public let expiresAt: Date
    public let user: User
}

public struct SavedCard: Codable, Identifiable, Hashable, Sendable {
    public enum State: String, Codable, Sendable {
        case saved
        case removalPending = "removal_pending"
        case removed
    }

    public let id: String
    public let brand: String
    public let last4: String
    public let expMonth: Int
    public let expYear: Int
    public let nickname: String?
    public let isPreferred: Bool
    public let state: State
    public let createdAt: Date
    public let updatedAt: Date

    public init(id: String, brand: String, last4: String, expMonth: Int, expYear: Int, nickname: String?,
                isPreferred: Bool, state: State, createdAt: Date, updatedAt: Date) {
        self.id = id
        self.brand = brand
        self.last4 = last4
        self.expMonth = expMonth
        self.expYear = expYear
        self.nickname = nickname
        self.isPreferred = isPreferred
        self.state = state
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }
}

public struct ClientSession: Codable, Hashable, Sendable {
    public let provider: String
    public let setupClientSecret: String
    public let customerRef: String
    public let ephemeralKeySecret: String
    public let ephemeralKeyExpiresAt: Date
    public let publishableKey: String

    public init(provider: String, setupClientSecret: String, customerRef: String, ephemeralKeySecret: String,
                ephemeralKeyExpiresAt: Date, publishableKey: String) {
        self.provider = provider
        self.setupClientSecret = setupClientSecret
        self.customerRef = customerRef
        self.ephemeralKeySecret = ephemeralKeySecret
        self.ephemeralKeyExpiresAt = ephemeralKeyExpiresAt
        self.publishableKey = publishableKey
    }
}

public struct SetupAttempt: Codable, Identifiable, Hashable, Sendable {
    public enum Status: String, Codable, Sendable {
        case collecting
        case authenticationRequired = "authentication_required"
        case processing
        case saved
        case failed
        case canceled

        public var isTerminal: Bool { self == .saved || self == .failed || self == .canceled }
    }

    public let id: String
    public let status: Status
    public let failureCode: String?
    public let card: SavedCard?
    public let clientSession: ClientSession?
    public let createdAt: Date
    public let updatedAt: Date

    public init(id: String, status: Status, failureCode: String?, card: SavedCard?, clientSession: ClientSession?,
                createdAt: Date, updatedAt: Date) {
        self.id = id
        self.status = status
        self.failureCode = failureCode
        self.card = card
        self.clientSession = clientSession
        self.createdAt = createdAt
        self.updatedAt = updatedAt
    }
}

public enum AccountDeletionStatus: String, Codable, Sendable {
    case deleting
    case deleted
}

/// A card-setup attempt the app started and may need to finish after a
/// relaunch. Persisted before the network call so an interrupted enrollment
/// can be resumed (or replayed with the same idempotency key).
public struct PendingSetup: Codable, Hashable, Sendable {
    public let idempotencyKey: String
    public var attemptID: String?

    // Snake-case conversion would map attempt_id to "attemptId".
    enum CodingKeys: String, CodingKey {
        case idempotencyKey = "idempotency_key"
        case attemptID = "attempt_id"
    }

    public init(idempotencyKey: String, attemptID: String? = nil) {
        self.idempotencyKey = idempotencyKey
        self.attemptID = attemptID
    }
}

// MARK: - JSON coding

public enum WalletJSON {
    /// Decoder matching the backend's snake_case fields and RFC 3339 times
    /// (which may carry more than three fractional digits).
    public static func decoder() -> JSONDecoder {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            guard let date = parseRFC3339(s) else {
                throw DecodingError.dataCorrupted(.init(codingPath: decoder.codingPath, debugDescription: "bad date \(s)"))
            }
            return date
        }
        return d
    }

    public static func encoder() -> JSONEncoder {
        let e = JSONEncoder()
        e.keyEncodingStrategy = .convertToSnakeCase
        e.dateEncodingStrategy = .iso8601
        return e
    }

    static func parseRFC3339(_ raw: String) -> Date? {
        // Normalise fractional seconds to at most three digits.
        var s = raw
        if let dot = s.firstIndex(of: "."),
           let end = s[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
            let frac = s[s.index(after: dot)..<end]
            let trimmed = String(frac.prefix(3))
            s.replaceSubrange(dot..<end, with: trimmed.isEmpty ? "" : "." + trimmed)
        }
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = f.date(from: s) { return d }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: s)
    }
}
