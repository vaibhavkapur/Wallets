import Foundation

/// Result reported by the native card-entry component. It is only a hint:
/// the wallet model always confirms the final state with the backend, which
/// in turn verifies it with the provider.
public enum CardSetupOutcome: Equatable, Sendable {
    case completed
    case canceled
    case failed(message: String)
}

/// Presents the provider's native card-entry sheet for a setup session.
/// The Stripe implementation lives in the app target; the fixture
/// implementations here never touch a real card.
@MainActor
public protocol CardSetupPresenter: AnyObject {
    func present(session: ClientSession, attemptID: String) async -> CardSetupOutcome
}

/// Fixture presenter used by tests: returns a scripted outcome and lets the
/// test simulate the provider side through a callback.
@MainActor
public final class ScriptedCardSetupPresenter: CardSetupPresenter {
    public var outcome: CardSetupOutcome
    public var onPresent: ((ClientSession, String) async -> Void)?
    public private(set) var presentedSessions: [ClientSession] = []

    public init(outcome: CardSetupOutcome = .completed) {
        self.outcome = outcome
    }

    public func present(session: ClientSession, attemptID: String) async -> CardSetupOutcome {
        presentedSessions.append(session)
        await onPresent?(session, attemptID)
        return outcome
    }
}
