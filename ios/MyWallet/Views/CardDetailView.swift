import SwiftUI
import MyWalletKit

struct CardDetailView: View {
    let cardID: String
    @Environment(WalletModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @Environment(\.colorScheme) private var scheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var nickname = ""
    @State private var isEditingNickname = false
    @State private var confirmRemove = false
    @State private var isWorking = false

    private var card: SavedCard? { model.cards.first { $0.id == cardID } }

    var body: some View {
        Group {
            if let card {
                content(card)
            } else {
                ContentUnavailableView("Card removed", systemImage: "creditcard.trianglebadge.exclamationmark",
                                       description: Text("This card is no longer in your wallet."))
            }
        }
        .background(Theme.canvas(scheme))
        .navigationBarTitleDisplayMode(.inline)
    }

    private func content(_ card: SavedCard) -> some View {
        List {
            Section {
                CardTileView(card: card)
                    .padding(.vertical, 8)
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                    .animation(Theme.motion(reduceMotion), value: card.isPreferred)
            }

            Section("Details") {
                row("Brand", CardDisplay.brandName(card.brand))
                row("Card number", CardDisplay.maskedNumber(card))
                row("Expires", CardDisplay.expiry(card))
                row("Status", CardDisplay.stateLabel(card))
            }

            Section {
                if isEditingNickname {
                    TextField("Nickname", text: $nickname, prompt: Text("e.g. Everyday"))
                        .textInputAutocapitalization(.words)
                        .submitLabel(.done)
                        .onSubmit { Task { await saveNickname(card) } }
                    HStack {
                        Button("Save") { Task { await saveNickname(card) } }
                            .disabled(isWorking)
                        Spacer()
                        Button("Cancel", role: .cancel) { isEditingNickname = false }
                    }
                } else {
                    Button {
                        nickname = card.nickname ?? ""
                        isEditingNickname = true
                    } label: {
                        HStack {
                            Text("Nickname")
                            Spacer()
                            Text(card.nickname ?? "None").foregroundStyle(.secondary)
                        }
                    }
                    .foregroundStyle(.primary)
                    .accessibilityHint("Edit the nickname")
                }
                if card.nickname != nil && !isEditingNickname {
                    Button("Clear nickname") { Task { await model.rename(card, to: nil) } }
                }
            } header: {
                Text("Nickname")
            } footer: {
                Text("Shown in your wallet in place of the brand name.")
            }

            Section {
                Toggle("Preferred card", isOn: Binding(
                    get: { card.isPreferred },
                    set: { on in if on { Task { await model.setPreferred(card) } } }
                ))
                .tint(Theme.accent)
                .disabled(card.isPreferred || card.state != .saved)
            } footer: {
                Text("The preferred card is shown first. This only affects how MyWallet displays your cards.")
            }

            Section {
                if card.state == .removalPending {
                    Label("Removal is pending confirmation from the provider. The card will disappear once it is removed there.", systemImage: "clock")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                } else {
                    Button("Remove Card", role: .destructive) { confirmRemove = true }
                        .disabled(isWorking)
                        // Attached to the button so the dialog anchors to it
                        // where the system presents it as a popover.
                        .confirmationDialog("Remove this card?", isPresented: $confirmRemove, titleVisibility: .visible) {
                            Button("Remove \(CardDisplay.brandName(card.brand)) •••• \(card.last4)", role: .destructive) {
                                Task { await remove(card) }
                            }
                        } message: {
                            Text("The card will be removed from MyWallet and from the provider.")
                        }
                }
            } footer: {
                Text("Removing a card detaches it from the provider. To use it again you will need to add it again.")
            }
        }
        .scrollContentBackground(.hidden)
        .navigationTitle(CardDisplay.title(for: card))
    }

    private func row(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label)
            Spacer()
            Text(value).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }

    private func saveNickname(_ card: SavedCard) async {
        isWorking = true
        let trimmed = nickname.trimmingCharacters(in: .whitespacesAndNewlines)
        await model.rename(card, to: trimmed.isEmpty ? nil : trimmed)
        isWorking = false
        isEditingNickname = false
    }

    private func remove(_ card: SavedCard) async {
        isWorking = true
        await model.remove(card)
        isWorking = false
        if model.cards.first(where: { $0.id == card.id }) == nil {
            dismiss()
        }
    }
}
