import Foundation
#if canImport(Security)
import Security
#endif

/// Keys for the small set of secrets the app keeps on device. The session
/// token and the in-flight setup record live in the Keychain; card metadata
/// is never cached beyond the in-memory model.
public enum CredentialKey: String, CaseIterable, Sendable {
    case sessionToken = "session_token"
    case pendingSetup = "pending_setup"
}

public protocol CredentialStore: AnyObject, Sendable {
    func read(_ key: CredentialKey) -> String?
    func write(_ value: String?, for key: CredentialKey)
    /// Removes every account-specific secret (sign out, account deletion).
    func clearAll()
}

public extension CredentialStore {
    var pendingSetup: PendingSetup? {
        get {
            guard let raw = read(.pendingSetup), let data = raw.data(using: .utf8) else { return nil }
            // Plain coders: PendingSetup declares its own stable keys.
            return try? JSONDecoder().decode(PendingSetup.self, from: data)
        }
        set {
            let raw = newValue.flatMap { try? JSONEncoder().encode($0) }.flatMap { String(data: $0, encoding: .utf8) }
            write(raw, for: .pendingSetup)
        }
    }
}

/// In-memory store for tests and previews.
public final class InMemoryCredentialStore: CredentialStore, @unchecked Sendable {
    private var values: [CredentialKey: String] = [:]
    private let lock = NSLock()

    public init() {}

    public func read(_ key: CredentialKey) -> String? {
        lock.lock(); defer { lock.unlock() }
        return values[key]
    }

    public func write(_ value: String?, for key: CredentialKey) {
        lock.lock(); defer { lock.unlock() }
        values[key] = value
    }

    public func clearAll() {
        lock.lock(); defer { lock.unlock() }
        values.removeAll()
    }
}

#if canImport(Security)
/// Keychain-backed store. Items are limited to this device and only
/// readable after first unlock, which is what a background refresh needs
/// without exposing the token to backups.
public final class KeychainCredentialStore: CredentialStore, @unchecked Sendable {
    private let service: String

    public init(service: String = "com.mywallet.ios") {
        self.service = service
    }

    private func query(_ key: CredentialKey) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: key.rawValue]
    }

    public func read(_ key: CredentialKey) -> String? {
        var q = query(key)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    public func write(_ value: String?, for key: CredentialKey) {
        let q = query(key)
        guard let value, let data = value.data(using: .utf8) else {
            SecItemDelete(q as CFDictionary)
            return
        }
        let attrs: [String: Any] = [kSecValueData as String: data,
                                    kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        let status = SecItemUpdate(q as CFDictionary, attrs as CFDictionary)
        if status == errSecItemNotFound {
            var add = q
            for (k, v) in attrs { add[k] = v }
            SecItemAdd(add as CFDictionary, nil)
        }
    }

    public func clearAll() {
        for key in CredentialKey.allCases { SecItemDelete(query(key) as CFDictionary) }
    }
}
#endif

// MARK: - Preferences

/// Non-secret, account-independent preferences.
public protocol PreferenceStore: AnyObject, Sendable {
    var appLockEnabled: Bool { get set }
    var hasSeenWelcome: Bool { get set }
}

public final class UserDefaultsPreferenceStore: PreferenceStore, @unchecked Sendable {
    private let defaults: UserDefaults
    public init(defaults: UserDefaults = .standard) { self.defaults = defaults }

    public var appLockEnabled: Bool {
        get { defaults.bool(forKey: "mywallet.appLockEnabled") }
        set { defaults.set(newValue, forKey: "mywallet.appLockEnabled") }
    }

    public var hasSeenWelcome: Bool {
        get { defaults.bool(forKey: "mywallet.hasSeenWelcome") }
        set { defaults.set(newValue, forKey: "mywallet.hasSeenWelcome") }
    }
}

public final class InMemoryPreferenceStore: PreferenceStore, @unchecked Sendable {
    public var appLockEnabled = false
    public var hasSeenWelcome = false
    public init() {}
}
