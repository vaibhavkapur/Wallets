import SwiftUI
import MyWalletKit

struct WalletView: View {
    @Environment(WalletModel.self) private var model
    @Environment(\.appMode) private var mode
    @Environment(\.colorScheme) private var scheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var showConsent = false
    @State private var consentAccepted = false
    @State private var showSettings = false

    private var activeCards: [SavedCard] { model.cards.filter { $0.state != .removed } }

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                if let label = mode.fixtureLabel {
                    Pill(text: label, tint: .orange)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                enrollmentBanner
                if activeCards.isEmpty && !model.isRefreshing {
                    emptyState
                } else {
                    ForEach(activeCards) { card in
                        NavigationLink(value: card.id) {
                            CardTileView(card: card, isHighlighted: card.id == model.lastSavedCardID)
                        }
                        .buttonStyle(.plain)
                        .accessibilityHint("Opens card details")
                    }
                }
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
            .animation(Theme.motion(reduceMotion), value: activeCards.map(\.id))
        }
        .refreshable { await model.refreshCards() }
        .background(Theme.canvas(scheme))
        .navigationTitle("MyWallet")
        .navigationDestination(for: String.self) { id in
            CardDetailView(cardID: id)
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    showSettings = true
                } label: {
                    Image(systemName: "gearshape")
                }
                .accessibilityLabel("Settings")
            }
        }
        .safeAreaInset(edge: .bottom) {
            Button {
                showConsent = true
            } label: {
                Label("Add Card", systemImage: "plus")
                    .font(.headline)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
            }
            .buttonStyle(.borderedProminent)
            .tint(Theme.accent)
            .disabled(isBusy)
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
            .background(.ultraThinMaterial)
        }
        // Enrollment starts only after the consent sheet has fully dismissed;
        // presenting the provider sheet while it is still animating out would
        // be silently dropped by UIKit.
        .sheet(isPresented: $showConsent, onDismiss: {
            guard consentAccepted else { return }
            consentAccepted = false
            Task { await model.addCard() }
        }) {
            AddCardConsentView { consentAccepted = true }
        }
        .sheet(isPresented: $showSettings) {
            SettingsView()
        }
    }

    private var isBusy: Bool {
        switch model.enrollment {
        case .idle, .pending: return false
        default: return true
        }
    }

    @ViewBuilder
    private var enrollmentBanner: some View {
        switch model.enrollment {
        case .starting, .presentingSheet:
            banner(icon: "creditcard", text: "Opening secure card entry…", progress: true)
        case .confirming:
            banner(icon: "checkmark.shield", text: "Confirming with the provider…", progress: true)
        case .pending(let attempt):
            VStack(alignment: .leading, spacing: 10) {
                banner(icon: "clock", text: CardDisplay.enrollmentStatusText(attempt.status), progress: false)
                HStack {
                    Button("Check again") { Task { await model.checkPendingEnrollment() } }
                        .buttonStyle(.bordered)
                    Button("Cancel setup", role: .destructive) { Task { await model.cancelPendingEnrollment() } }
                        .buttonStyle(.bordered)
                }
                .font(.subheadline)
            }
        case .idle:
            EmptyView()
        }
    }

    private func banner(icon: String, text: String, progress: Bool) -> some View {
        HStack(spacing: 12) {
            if progress {
                ProgressView().tint(Theme.accent)
            } else {
                Image(systemName: icon).foregroundStyle(Theme.accent)
            }
            Text(text).font(.subheadline)
            Spacer()
        }
        .padding(14)
        .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .accessibilityElement(children: .combine)
    }

    private var emptyState: some View {
        VStack(spacing: 14) {
            ZStack {
                RoundedRectangle(cornerRadius: Theme.tileCornerRadius, style: .continuous)
                    .strokeBorder(style: StrokeStyle(lineWidth: 1.5, dash: [8, 6]))
                    .foregroundStyle(.secondary.opacity(0.5))
                    .frame(height: 170)
                VStack(spacing: 8) {
                    Image(systemName: "creditcard")
                        .font(.system(size: 34))
                        .foregroundStyle(Theme.accent)
                    Text("No cards yet")
                        .font(.headline)
                    Text("Add a debit or credit card to keep it here. Only the card's brand, last four digits, and expiry are shown.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 24)
                }
            }
        }
        .padding(.top, 24)
        .accessibilityElement(children: .combine)
    }
}

/// Consent step shown before the provider's native sheet.
struct AddCardConsentView: View {
    /// Called when the user agrees; the caller starts enrollment once this
    /// sheet has finished dismissing.
    let onAgree: () -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.appMode) private var mode

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 20) {
                Text("Save a card to MyWallet")
                    .font(.title2.weight(.semibold))
                point("hand.raised", "By continuing you agree to let MyWallet's payment provider store this card so MyWallet can show it in your wallet.")
                point("creditcard.and.123", "You will enter the card number, expiry, and security code in the provider's secure form. MyWallet never sees or stores them.")
                point("checkmark.shield", "Your bank may ask you to verify. Saving does not authorize any payment, and you can remove the card at any time.")
                if mode.usesRealProvider {
                    Text("Supported: debit and credit cards from major US networks accepted by the provider. Prepaid and some corporate cards may be declined.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                } else if let label = mode.fixtureLabel {
                    Pill(text: label, tint: .orange)
                }
                Spacer()
                Button {
                    onAgree()
                    dismiss()
                } label: {
                    Text("Agree and continue")
                        .font(.headline)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)
                Text("Consent version \(currentConsentVersion)")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .frame(maxWidth: .infinity)
            }
            .padding(24)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
        .presentationDetents([.large])
    }

    private func point(_ symbol: String, _ text: String) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: symbol)
                .foregroundStyle(Theme.accent)
                .frame(width: 26)
                .accessibilityHidden(true)
            Text(text).foregroundStyle(.secondary)
        }
    }
}
