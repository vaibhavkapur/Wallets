import Foundation
import Observation
import MyWalletKit

/// DEVELOPMENT FIXTURE. Stands in for the provider's native sheet in the
/// `fixtures` and `dev-backend` modes. It shows an explicitly labeled
/// simulation sheet whose choices drive either the in-memory fixture API or
/// the local backend's /dev endpoints. It never collects card details.
@MainActor
@Observable
final class SimulatedCardSetupPresenter: CardSetupPresenter {
    enum Backend {
        case none
        case fixture(FixtureWalletAPI)
        case devServer(DevBackendClient)
    }

    enum Action: String, CaseIterable, Identifiable {
        case saveVisa, saveMastercard, decline, processing, cancel
        var id: String { rawValue }

        var title: String {
            switch self {
            case .saveVisa: return "Simulate saved Visa •••• 4242"
            case .saveMastercard: return "Simulate saved Mastercard •••• 4444"
            case .decline: return "Simulate issuer decline"
            case .processing: return "Simulate provider still processing"
            case .cancel: return "Cancel"
            }
        }
    }

    struct Request: Identifiable {
        let id = UUID()
        let attemptID: String
        let session: ClientSession
    }

    var backend: Backend = .none
    /// Settable so the hosting view can bind a sheet to it; clearing it
    /// externally (swipe-to-dismiss) is reported through `dismissed()`.
    var request: Request?
    private(set) var isWorking = false
    @ObservationIgnored private var continuation: CheckedContinuation<CardSetupOutcome, Never>?

    func present(session: ClientSession, attemptID: String) async -> CardSetupOutcome {
        if case .none = backend {
            return .failed(message: "Simulation backend is not configured.")
        }
        return await withCheckedContinuation { c in
            continuation = c
            request = Request(attemptID: attemptID, session: session)
        }
    }

    /// Runs the chosen simulation against the backend, then reports the
    /// outcome the real sheet would have reported.
    func perform(_ action: Action) async {
        guard let request else { return }
        isWorking = true
        defer { isWorking = false }
        let outcome: CardSetupOutcome
        switch action {
        case .saveVisa:
            await complete(request.attemptID, brand: "visa", last4: "4242", month: 12, year: 2030)
            outcome = .completed
        case .saveMastercard:
            await complete(request.attemptID, brand: "mastercard", last4: "4444", month: 3, year: 2029)
            outcome = .completed
        case .decline:
            await setOutcome(request.attemptID, "declined", code: "card_declined")
            outcome = .failed(message: "Your card was declined (simulated).")
        case .processing:
            await setOutcome(request.attemptID, "processing", code: nil)
            outcome = .completed
        case .cancel:
            outcome = .canceled
        }
        finish(outcome)
    }

    /// Sheet dismissed without a choice.
    func dismissed() {
        if continuation != nil { finish(.canceled) }
    }

    private func finish(_ outcome: CardSetupOutcome) {
        request = nil
        continuation?.resume(returning: outcome)
        continuation = nil
    }

    private func complete(_ attemptID: String, brand: String, last4: String, month: Int, year: Int) async {
        switch backend {
        case .fixture(let api):
            await api.simulateCompletion(attemptID: attemptID, card: .init(brand: brand, last4: last4, expMonth: month, expYear: year))
        case .devServer(let dev):
            await dev.complete(attemptID: attemptID, brand: brand, last4: last4, month: month, year: year)
        case .none:
            break
        }
    }

    private func setOutcome(_ attemptID: String, _ outcome: String, code: String?) async {
        switch backend {
        case .fixture(let api):
            if outcome == "declined" {
                await api.simulateDecline(attemptID: attemptID, code: code ?? "card_declined")
            } else {
                await api.simulateProcessing(attemptID: attemptID)
            }
        case .devServer(let dev):
            await dev.setOutcome(attemptID: attemptID, outcome: outcome, code: code)
        case .none:
            break
        }
    }
}

/// Calls the local backend's fixture-only endpoints (PROVIDER=fake).
struct DevBackendClient {
    let baseURL: URL
    let credentials: CredentialStore

    func complete(attemptID: String, brand: String, last4: String, month: Int, year: Int) async {
        await post("/dev/card-setups/\(attemptID)/complete",
                   ["brand": brand, "last4": last4, "exp_month": month, "exp_year": year])
    }

    func setOutcome(attemptID: String, outcome: String, code: String?) async {
        var body: [String: Any] = ["outcome": outcome]
        if let code { body["code"] = code }
        await post("/dev/card-setups/\(attemptID)/outcome", body)
    }

    private func post(_ path: String, _ body: [String: Any]) async {
        guard let token = credentials.read(.sessionToken) else { return }
        var req = URLRequest(url: baseURL.appending(path: path))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.httpBody = try? JSONSerialization.data(withJSONObject: body)
        _ = try? await URLSession.shared.data(for: req)
    }
}
