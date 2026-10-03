import SwiftUI
import MyWalletKit

struct SettingsView: View {
    @Environment(WalletModel.self) private var model
    @Environment(LockController.self) private var lock
    @Environment(\.appMode) private var mode
    @Environment(\.dismiss) private var dismiss
    @State private var confirmSignOut = false
    @State private var confirmDelete = false
    @State private var isWorking = false
    @State private var lockToggle = false

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Toggle("Require \(lock.biometryName) to open", isOn: $lockToggle)
                        .tint(Theme.accent)
                        .onChange(of: lockToggle) { _, on in
                            guard on != lock.isEnabled else { return }
                            Task {
                                let ok = await lock.setEnabled(on)
                                if !ok { lockToggle = lock.isEnabled }
                            }
                        }
                    if let err = lock.lastError {
                        Text(err).font(.footnote).foregroundStyle(.secondary)
                    }
                } header: {
                    Text("App lock")
                } footer: {
                    Text("Uses your device's Face ID, Touch ID, or passcode. Card tiles are also hidden in the app switcher.")
                }

                Section("Privacy") {
                    privacyRow("Where card details go", "You type card details into the payment provider's secure form. They travel directly to the provider, never through MyWallet.")
                    privacyRow("What MyWallet stores", "A provider reference, the card brand, last four digits, expiry, and your nickname. Never the full number or security code.")
                    privacyRow("What saving means", "The provider has saved the card for display in MyWallet. It does not grant MyWallet permission to charge it, and it does not indicate available funds.")
                    privacyRow("Removal", "Removing a card detaches it at the provider. Deleting your account removes all cards and your account data.")
                }

                Section("Account") {
                    if let email = model.user?.email {
                        HStack { Text("Signed in as"); Spacer(); Text(email).foregroundStyle(.secondary) }
                    }
                    // Dialogs are attached to their buttons so they anchor
                    // to them where the system presents them as popovers.
                    Button("Sign out") { confirmSignOut = true }
                        .confirmationDialog("Sign out of MyWallet?", isPresented: $confirmSignOut, titleVisibility: .visible) {
                            Button("Sign out", role: .destructive) {
                                Task {
                                    isWorking = true
                                    await model.signOut()
                                    isWorking = false
                                    dismiss()
                                }
                            }
                        } message: {
                            Text("Your cards stay saved with the provider and will reappear when you sign in again.")
                        }
                    Button("Delete account", role: .destructive) { confirmDelete = true }
                        .confirmationDialog("Delete your MyWallet account?", isPresented: $confirmDelete, titleVisibility: .visible) {
                            Button("Delete account and remove all cards", role: .destructive) {
                                Task {
                                    isWorking = true
                                    await model.deleteAccount()
                                    isWorking = false
                                    dismiss()
                                }
                            }
                        } message: {
                            Text("All saved cards are removed from the provider and your account data is deleted. This cannot be undone.")
                        }
                }
                .disabled(isWorking)

                Section("About") {
                    HStack { Text("Mode"); Spacer(); Text(mode.rawValue).foregroundStyle(.secondary) }
                    HStack { Text("Version"); Spacer(); Text(Self.version).foregroundStyle(.secondary) }
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .onAppear { lockToggle = lock.isEnabled }
        }
    }

    private func privacyRow(_ title: String, _ body: String) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title).font(.subheadline.weight(.semibold))
            Text(body).font(.footnote).foregroundStyle(.secondary)
        }
        .padding(.vertical, 2)
        .accessibilityElement(children: .combine)
    }

    private static var version: String {
        let info = Bundle.main.infoDictionary
        let short = info?["CFBundleShortVersionString"] as? String ?? "0"
        let build = info?["CFBundleVersion"] as? String ?? "0"
        return "\(short) (\(build))"
    }
}
