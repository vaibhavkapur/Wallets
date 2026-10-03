import Foundation

/// Display helpers shared by views and tests. Card art is original MyWallet
/// styling keyed by brand; no issuer assets are used.
public enum CardDisplay {
    public static func brandName(_ brand: String) -> String {
        switch brand.lowercased() {
        case "visa": return "Visa"
        case "mastercard": return "Mastercard"
        case "amex", "american_express": return "American Express"
        case "discover": return "Discover"
        case "diners", "diners_club": return "Diners Club"
        case "jcb": return "JCB"
        case "unionpay": return "UnionPay"
        default: return brand.capitalized
        }
    }

    /// Title shown on the tile: nickname when set, else the brand.
    public static func title(for card: SavedCard) -> String {
        card.nickname ?? brandName(card.brand)
    }

    /// Masked number for display, e.g. "•••• 4242".
    public static func maskedNumber(_ card: SavedCard) -> String { "•••• \(card.last4)" }

    public static func expiry(_ card: SavedCard) -> String {
        String(format: "%02d/%02d", card.expMonth, card.expYear % 100)
    }

    public static func isExpired(_ card: SavedCard, now: Date = Date(), calendar: Calendar = .current) -> Bool {
        let comps = calendar.dateComponents([.year, .month], from: now)
        guard let y = comps.year, let m = comps.month else { return false }
        return card.expYear < y || (card.expYear == y && card.expMonth < m)
    }

    public static func stateLabel(_ card: SavedCard) -> String {
        switch card.state {
        case .saved: return "Saved"
        case .removalPending: return "Removal pending"
        case .removed: return "Removed"
        }
    }

    /// VoiceOver label with every visible fact, in reading order.
    public static func accessibilityLabel(for card: SavedCard) -> String {
        var parts = [String]()
        if let nick = card.nickname { parts.append(nick) }
        parts.append(brandName(card.brand))
        parts.append("ending in \(card.last4.map(String.init).joined(separator: " "))")
        parts.append("expires \(expiry(card))")
        parts.append(stateLabel(card))
        if card.isPreferred { parts.append("preferred card") }
        return parts.joined(separator: ", ")
    }

    /// Text shown on the tile for the enrollment status.
    public static func enrollmentStatusText(_ status: SetupAttempt.Status) -> String {
        switch status {
        case .collecting: return "Entering card details"
        case .authenticationRequired: return "Waiting for your bank's verification"
        case .processing: return "The provider is confirming your card"
        case .saved: return "Saved"
        case .failed: return "Not saved"
        case .canceled: return "Canceled"
        }
    }
}
