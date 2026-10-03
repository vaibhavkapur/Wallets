import Foundation
import Testing
@testable import MyWalletKit

@MainActor
struct Harness {
    let api = FixtureWalletAPI()
    let credentials = InMemoryCredentialStore()
    let preferences = InMemoryPreferenceStore()
    let presenter = ScriptedCardSetupPresenter()
    let model: WalletModel

    init() {
        model = WalletModel(api: api, credentials: credentials, preferences: preferences, presenter: presenter,
                            pollInterval: .zero, maxPolls: 3, sleep: { _ in })
    }

    func signIn(_ subject: String = "subject-1") async {
        await model.signIn(identityToken: subject, nonce: nil)
        #expect(model.phase == .signedIn)
    }
}

@MainActor
@Suite struct WalletModelTests {
    @Test func startsSignedOutWithoutToken() async {
        let h = Harness()
        await h.model.start()
        #expect(h.model.phase == .signedOut)
    }

    @Test func signInThenRestoreKeepsSameAccountAndCards() async {
        let h = Harness()
        await h.signIn()
        let userID = h.model.user?.id
        await h.model.addCard()
        // Sheet "completed" but nothing happened at the provider yet: pending.
        #expect(h.model.cards.isEmpty)

        // Second launch with the same keychain contents.
        let relaunch = WalletModel(api: h.api, credentials: h.credentials, preferences: h.preferences, presenter: h.presenter,
                                   pollInterval: .zero, sleep: { _ in })
        await relaunch.start()
        #expect(relaunch.phase == .signedIn)
        #expect(relaunch.user?.id == userID)
    }

    @Test func addCardHappyPath() async {
        let h = Harness()
        await h.signIn()
        h.presenter.onPresent = { _, attemptID in await h.api.simulateCompletion(attemptID: attemptID, card: .visa) }
        await h.model.addCard()
        #expect(h.model.enrollment == .idle)
        #expect(h.credentials.pendingSetup == nil)
        #expect(h.model.lastSavedCardID != nil)
        await Task.yield()
        await h.model.refreshCards()
        #expect(h.model.cards.count == 1)
        #expect(h.model.cards.first?.last4 == "4242")
        #expect(h.model.cards.first?.isPreferred == true)
        #expect(h.presenter.presentedSessions.count == 1)
        #expect(h.model.alertMessage == nil)
    }

    @Test func sheetCanceledClosesAttempt() async {
        let h = Harness()
        await h.signIn()
        h.presenter.outcome = .canceled
        await h.model.addCard()
        #expect(h.model.enrollment == .idle)
        #expect(h.credentials.pendingSetup == nil)
        #expect(h.model.cards.isEmpty)
        #expect(h.model.alertMessage == nil)
    }

    @Test func declineThenFailureShowsMessage() async {
        let h = Harness()
        await h.signIn()
        h.presenter.outcome = .failed(message: "Your card was declined.")
        h.presenter.onPresent = { _, attemptID in await h.api.simulateDecline(attemptID: attemptID, code: "insufficient_funds") }
        await h.model.addCard()
        #expect(h.model.enrollment == .idle)
        #expect(h.model.alertMessage == "Your card was declined.")
        #expect(h.model.cards.isEmpty)
    }

    @Test func clientReportedCancelButProviderSucceededStillSaves() async {
        let h = Harness()
        await h.signIn()
        h.presenter.outcome = .canceled
        h.presenter.onPresent = { _, attemptID in await h.api.simulateCompletion(attemptID: attemptID, card: .mastercard) }
        await h.model.addCard()
        await h.model.refreshCards()
        #expect(h.model.cards.count == 1)
        #expect(h.model.cards.first?.brand == "mastercard")
    }

    @Test func processingStaysPendingThenResolvesOnResume() async {
        let h = Harness()
        await h.signIn()
        h.presenter.onPresent = { _, attemptID in await h.api.simulateProcessing(attemptID: attemptID) }
        await h.model.addCard()
        guard case .pending(let attempt) = h.model.enrollment else {
            Issue.record("expected pending enrollment, got \(h.model.enrollment)")
            return
        }
        #expect(attempt.status == .processing)
        #expect(h.credentials.pendingSetup?.attemptID == attempt.id)

        // Provider finishes while the app is backgrounded.
        await h.api.simulateCompletion(attemptID: attempt.id, card: .amex)
        await h.model.didBecomeActive()
        #expect(h.model.enrollment == .idle)
        await h.model.refreshCards()
        #expect(h.model.cards.first?.brand == "amex")
    }

    @Test func interruptedBeforeSheetIsReplayedNotDuplicated() async {
        let h = Harness()
        await h.signIn()
        // Simulate a crash after the attempt was created but before the
        // sheet: a pending record with a key but no attempt ID.
        let key = UUID().uuidString
        let token = h.credentials.read(.sessionToken)!
        let first = try! await h.api.startSetup(token: token, idempotencyKey: key, consentVersion: currentConsentVersion)
        h.credentials.pendingSetup = PendingSetup(idempotencyKey: key, attemptID: nil)

        await h.model.didBecomeActive()
        // The replay found the same attempt still collecting and closed it.
        let after = try! await h.api.getSetup(token: token, id: first.id)
        #expect(after.status == .canceled)
        #expect(h.credentials.pendingSetup == nil)
        #expect(h.model.enrollment == .idle)
    }

    @Test func renamePreferAndRemove() async {
        let h = Harness()
        await h.signIn()
        h.presenter.onPresent = { _, id in await h.api.simulateCompletion(attemptID: id, card: .visa) }
        await h.model.addCard()
        h.presenter.onPresent = { _, id in await h.api.simulateCompletion(attemptID: id, card: .amex) }
        await h.model.addCard()
        await h.model.refreshCards()
        #expect(h.model.cards.count == 2)
        let visa = h.model.cards.first { $0.brand == "visa" }!
        let amex = h.model.cards.first { $0.brand == "amex" }!

        await h.model.rename(visa, to: "  Everyday ")
        #expect(h.model.cards.first { $0.id == visa.id }?.nickname == "Everyday")
        await h.model.rename(visa, to: nil)
        #expect(h.model.cards.first { $0.id == visa.id }?.nickname == nil)

        await h.model.setPreferred(amex)
        #expect(h.model.preferredCard?.id == amex.id)
        #expect(h.model.cards.filter(\.isPreferred).count == 1)

        // Provider outage: the card stays visible as removal pending.
        await h.api.setProviderOutage(true)
        await h.model.remove(visa)
        #expect(h.model.cards.first { $0.id == visa.id }?.state == .removalPending)
        await h.api.simulateProviderRecovered()
        await h.model.refreshCards()
        #expect(h.model.cards.map(\.id) == [amex.id])

        await h.model.remove(amex)
        #expect(h.model.cards.isEmpty)
    }

    @Test func signOutClearsLocalState() async {
        let h = Harness()
        await h.signIn()
        h.presenter.onPresent = { _, id in await h.api.simulateCompletion(attemptID: id) }
        await h.model.addCard()
        await h.model.refreshCards()
        #expect(!h.model.cards.isEmpty)
        await h.model.signOut()
        #expect(h.model.phase == .signedOut)
        #expect(h.model.cards.isEmpty)
        #expect(h.model.user == nil)
        #expect(h.credentials.read(.sessionToken) == nil)
        #expect(h.credentials.pendingSetup == nil)
    }

    @Test func expiredSessionSignsOut() async {
        let h = Harness()
        await h.signIn()
        h.credentials.write("ses_garbage", for: .sessionToken)
        await h.model.refreshCards()
        #expect(h.model.phase == .signedOut)
        #expect(h.credentials.read(.sessionToken) == nil)
    }

    @Test func deleteAccountSignsOutAndReportsStatus() async {
        let h = Harness()
        await h.signIn("to-delete")
        await h.api.setProviderOutage(true)
        await h.model.deleteAccount()
        #expect(h.model.accountDeletion == .deleting)
        #expect(h.model.phase == .signedOut)
        // While deleting, the same Apple account cannot sign in.
        await h.model.signIn(identityToken: "to-delete", nonce: nil)
        #expect(h.model.phase == .signedOut)
        #expect(h.model.alertMessage?.contains("being deleted") == true)
        await h.api.simulateProviderRecovered()
        h.model.alertMessage = nil
        await h.model.signIn(identityToken: "to-delete", nonce: nil)
        #expect(h.model.phase == .signedIn)
        #expect(h.model.cards.isEmpty)
    }

    @Test func appLockPreferencePersists() {
        let h = Harness()
        #expect(!h.model.appLockEnabled)
        h.model.appLockEnabled = true
        #expect(h.preferences.appLockEnabled)
    }
}
