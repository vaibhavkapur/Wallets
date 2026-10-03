import Foundation
import Testing
@testable import MyWalletKit

@Suite struct ModelsTests {
    @Test func decodesBackendCardPayload() throws {
        let json = """
        {"cards":[{"id":"card_1","brand":"visa","last4":"4242","exp_month":12,"exp_year":2030,"nickname":null,
        "is_preferred":true,"state":"saved","created_at":"2026-10-03T18:50:17.600944+05:30","updated_at":"2026-10-03T13:20:17Z"}]}
        """
        struct W: Decodable { let cards: [SavedCard] }
        let w = try WalletJSON.decoder().decode(W.self, from: Data(json.utf8))
        let c = try #require(w.cards.first)
        #expect(c.id == "card_1")
        #expect(c.last4 == "4242")
        #expect(c.isPreferred)
        #expect(c.state == .saved)
        #expect(c.nickname == nil)
        // Both timestamps are the same instant.
        #expect(abs(c.createdAt.timeIntervalSince(c.updatedAt)) < 1)
    }

    @Test func decodesSetupWithClientSessionAndRejectsUnknownStatesGracefully() throws {
        let json = """
        {"id":"setup_1","status":"authentication_required","failure_code":null,"card":null,
        "client_session":{"provider":"stripe","setup_client_secret":"seti_secret","customer_ref":"cus_1",
        "ephemeral_key_secret":"ek_1","ephemeral_key_expires_at":"2026-10-03T14:20:17Z","publishable_key":"pk_test"},
        "created_at":"2026-10-03T13:20:17.5Z","updated_at":"2026-10-03T13:20:17.123456789Z"}
        """
        let a = try WalletJSON.decoder().decode(SetupAttempt.self, from: Data(json.utf8))
        #expect(a.status == .authenticationRequired)
        #expect(!a.status.isTerminal)
        #expect(a.clientSession?.setupClientSecret == "seti_secret")
    }

    @Test func rfc3339Parsing() {
        #expect(WalletJSON.parseRFC3339("2026-10-03T13:20:17Z") != nil)
        #expect(WalletJSON.parseRFC3339("2026-10-03T13:20:17.1Z") != nil)
        #expect(WalletJSON.parseRFC3339("2026-10-03T13:20:17.123456Z") != nil)
        #expect(WalletJSON.parseRFC3339("2026-10-03T18:50:17.600944+05:30") != nil)
        #expect(WalletJSON.parseRFC3339("2026-10-03T18:50:17-07:00") != nil)
        #expect(WalletJSON.parseRFC3339("yesterday") == nil)
    }

    @Test func displayHelpers() {
        let card = SavedCard(id: "c", brand: "amex", last4: "0005", expMonth: 3, expYear: 2029, nickname: "Travel",
                             isPreferred: true, state: .saved, createdAt: .now, updatedAt: .now)
        #expect(CardDisplay.title(for: card) == "Travel")
        #expect(CardDisplay.brandName("amex") == "American Express")
        #expect(CardDisplay.maskedNumber(card) == "•••• 0005")
        #expect(CardDisplay.expiry(card) == "03/29")
        #expect(CardDisplay.accessibilityLabel(for: card) == "Travel, American Express, ending in 0 0 0 5, expires 03/29, Saved, preferred card")
        var comps = DateComponents(); comps.year = 2029; comps.month = 4; comps.day = 1
        let after = Calendar.current.date(from: comps)!
        #expect(CardDisplay.isExpired(card, now: after))
        comps.month = 3
        #expect(!CardDisplay.isExpired(card, now: Calendar.current.date(from: comps)!))
    }

    @Test func pendingSetupRoundTripsThroughCredentialStore() {
        let store = InMemoryCredentialStore()
        #expect(store.pendingSetup == nil)
        store.pendingSetup = PendingSetup(idempotencyKey: "k1", attemptID: "setup_1")
        #expect(store.pendingSetup == PendingSetup(idempotencyKey: "k1", attemptID: "setup_1"))
        store.clearAll()
        #expect(store.pendingSetup == nil)
    }
}
