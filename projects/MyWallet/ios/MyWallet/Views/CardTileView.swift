import SwiftUI
import MyWalletKit

/// The card tile: original MyWallet art keyed by brand, masked details only.
struct CardTileView: View {
    let card: SavedCard
    var isHighlighted = false

    private var expired: Bool { CardDisplay.isExpired(card) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top) {
                Text(CardDisplay.title(for: card))
                    .font(.headline)
                    .lineLimit(1)
                Spacer()
                if card.isPreferred {
                    Pill(text: "Preferred", tint: .white)
                }
            }
            Spacer(minLength: 24)
            Text(CardDisplay.maskedNumber(card))
                .font(.system(.title2, design: .monospaced, weight: .medium))
                .tracking(2)
            Spacer(minLength: 12)
            HStack(alignment: .lastTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(card.nickname == nil ? "Expires" : CardDisplay.brandName(card.brand))
                        .font(.caption2)
                        .textCase(.uppercase)
                        .foregroundStyle(.white.opacity(0.7))
                    Text(CardDisplay.expiry(card))
                        .font(.subheadline.weight(.medium))
                        .foregroundStyle(expired ? .orange : .white)
                }
                Spacer()
                statusPill
            }
        }
        .foregroundStyle(.white)
        .padding(20)
        .frame(maxWidth: .infinity, minHeight: 190, alignment: .leading)
        .background(Theme.tileGradient(for: card.brand))
        .overlay {
            RoundedRectangle(cornerRadius: Theme.tileCornerRadius, style: .continuous)
                .strokeBorder(isHighlighted ? Theme.accent : .white.opacity(0.08), lineWidth: isHighlighted ? 2 : 1)
        }
        .clipShape(RoundedRectangle(cornerRadius: Theme.tileCornerRadius, style: .continuous))
        .shadow(color: .black.opacity(0.28), radius: 14, y: 8)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(CardDisplay.accessibilityLabel(for: card) + (expired ? ", expired" : ""))
    }

    @ViewBuilder
    private var statusPill: some View {
        switch card.state {
        case .saved:
            if expired {
                Pill(text: "Expired", tint: .orange)
            } else {
                Pill(text: "Saved", tint: .white)
            }
        case .removalPending:
            Pill(text: "Removal pending", tint: .orange)
        case .removed:
            Pill(text: "Removed", tint: .secondary)
        }
    }
}
