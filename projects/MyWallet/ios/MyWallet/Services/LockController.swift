import Foundation
import LocalAuthentication
import Observation
import MyWalletKit

/// App lock backed by the system's device-owner authentication (Face ID,
/// Touch ID, or passcode). Nothing here simulates authentication: if the
/// policy cannot be evaluated the app stays locked and says why.
@MainActor
@Observable
final class LockController {
    private(set) var isLocked = false
    private(set) var lastError: String?
    private let preferences: PreferenceStore
    private var backgroundedAt: Date?

    /// Grace period before re-locking after a brief switch away.
    var relockAfter: TimeInterval = 20

    init(preferences: PreferenceStore) {
        self.preferences = preferences
        isLocked = preferences.appLockEnabled
    }

    var isEnabled: Bool { preferences.appLockEnabled }

    /// Human-readable name of the available biometry, for button labels.
    var biometryName: String {
        let ctx = LAContext()
        _ = ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: nil)
        switch ctx.biometryType {
        case .faceID: return "Face ID"
        case .touchID: return "Touch ID"
        case .opticID: return "Optic ID"
        default: return "Passcode"
        }
    }

    func didEnterBackground() {
        backgroundedAt = Date()
        if isEnabled { isLocked = true }
    }

    /// Called when the UI appears or returns; decides whether to require unlock.
    func evaluateOnForeground() {
        guard isEnabled else { isLocked = false; return }
        if let at = backgroundedAt, Date().timeIntervalSince(at) < relockAfter, !isLocked {
            return
        }
        isLocked = true
    }

    /// Turning the lock on requires that the device can actually authenticate.
    func setEnabled(_ enabled: Bool) async -> Bool {
        if enabled {
            let ok = await authenticate(reason: "Turn on app lock for MyWallet")
            guard ok else { return false }
        }
        preferences.appLockEnabled = enabled
        isLocked = false
        return true
    }

    func unlock() async {
        if await authenticate(reason: "Unlock MyWallet to view your cards") {
            isLocked = false
        }
    }

    private func authenticate(reason: String) async -> Bool {
        let ctx = LAContext()
        ctx.localizedCancelTitle = "Cancel"
        var evalError: NSError?
        guard ctx.canEvaluatePolicy(.deviceOwnerAuthentication, error: &evalError) else {
            lastError = evalError?.localizedDescription ?? "Device authentication is not available."
            return false
        }
        do {
            let ok = try await ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)
            if ok { lastError = nil }
            return ok
        } catch {
            lastError = (error as? LAError)?.code == .userCancel ? nil : error.localizedDescription
            return false
        }
    }
}
