import SwiftUI
import MyWalletKit

struct RootView: View {
    @Environment(WalletModel.self) private var model
    @Environment(LockController.self) private var lock
    @Environment(SimulatedCardSetupPresenter.self) private var simulated
    @Environment(\.colorScheme) private var scheme
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        @Bindable var simulated = simulated
        ZStack {
            Theme.canvas(scheme).ignoresSafeArea()
            switch model.phase {
            case .starting:
                ProgressView("Opening your wallet…")
                    .tint(Theme.accent)
            case .signedOut:
                WelcomeView()
            case .signedIn:
                NavigationStack {
                    WalletView()
                }
                .tint(Theme.accent)
            }
            if lock.isLocked && model.phase == .signedIn {
                LockView()
                    .transition(.opacity)
                    .zIndex(1)
            }
        }
        .alert("Something went wrong", isPresented: Binding(
            get: { model.alertMessage != nil },
            set: { if !$0 { model.alertMessage = nil } }
        )) {
            Button("OK", role: .cancel) { model.alertMessage = nil }
        } message: {
            Text(model.alertMessage ?? "")
        }
        .sheet(item: $simulated.request, onDismiss: { simulated.dismissed() }) { request in
            SimulatedCardEntrySheet(request: request)
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { lock.evaluateOnForeground() }
        }
        .onAppear { lock.evaluateOnForeground() }
    }
}

/// Full-screen lock. Unlocking always goes through the system prompt.
struct LockView: View {
    @Environment(LockController.self) private var lock

    var body: some View {
        ZStack {
            Theme.canvasDark.ignoresSafeArea()
            VStack(spacing: 20) {
                Image(systemName: "lock.fill")
                    .font(.system(size: 44))
                    .foregroundStyle(Theme.accent)
                Text("MyWallet is locked")
                    .font(.title2.weight(.semibold))
                    .foregroundStyle(.white)
                if let err = lock.lastError {
                    Text(err)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal)
                }
                Button {
                    Task { await lock.unlock() }
                } label: {
                    Text("Unlock with \(lock.biometryName)")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .tint(Theme.accent)
                .padding(.horizontal, 40)
            }
        }
        .task { await lock.unlock() }
    }
}

/// DEVELOPMENT FIXTURE sheet used instead of the provider's native card
/// entry in non-production modes. It is visibly labeled and contains no
/// card fields.
struct SimulatedCardEntrySheet: View {
    let request: SimulatedCardSetupPresenter.Request
    @Environment(SimulatedCardSetupPresenter.self) private var presenter

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Label("Development fixture — not a real card entry form", systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                    Text("In the real build this is the provider's native sheet. Choose what the provider should report for attempt \(request.attemptID).")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                Section {
                    ForEach(SimulatedCardSetupPresenter.Action.allCases) { action in
                        Button(role: action == .cancel ? .cancel : nil) {
                            Task { await presenter.perform(action) }
                        } label: {
                            Text(action.title)
                        }
                        .disabled(presenter.isWorking)
                    }
                }
            }
            .navigationTitle("Simulated card entry")
            .navigationBarTitleDisplayMode(.inline)
        }
        .interactiveDismissDisabled(presenter.isWorking)
    }
}
