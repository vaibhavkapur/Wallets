import SwiftUI
import MyWalletKit

/// MyWallet's visual identity: native type, a dark neutral canvas, one restrained
/// teal accent, and large rounded tiles with subtle depth. Original art only;
/// brand names appear as text, never as issuer or network logos.
enum Theme {
    static let accent = Color(red: 0.00, green: 0.62, blue: 0.60)
    static let canvasDark = Color(red: 0.07, green: 0.08, blue: 0.09)
    static let tileCornerRadius: CGFloat = 22

    /// Page background adapting to the color scheme.
    static func canvas(_ scheme: ColorScheme) -> Color {
        scheme == .dark ? canvasDark : Color(uiColor: .systemGroupedBackground)
    }

    /// Original palettes keyed by brand so tiles are distinguishable without
    /// relying on trademarked colors or marks.
    static func tileGradient(for brand: String) -> LinearGradient {
        let colors: [Color]
        switch brand.lowercased() {
        case "visa":
            colors = [Color(red: 0.13, green: 0.19, blue: 0.32), Color(red: 0.07, green: 0.11, blue: 0.20)]
        case "mastercard":
            colors = [Color(red: 0.30, green: 0.17, blue: 0.14), Color(red: 0.15, green: 0.09, blue: 0.08)]
        case "amex", "american_express":
            colors = [Color(red: 0.11, green: 0.28, blue: 0.30), Color(red: 0.05, green: 0.15, blue: 0.17)]
        case "discover":
            colors = [Color(red: 0.32, green: 0.22, blue: 0.10), Color(red: 0.17, green: 0.11, blue: 0.05)]
        default:
            colors = [Color(red: 0.22, green: 0.23, blue: 0.26), Color(red: 0.11, green: 0.12, blue: 0.14)]
        }
        return LinearGradient(colors: colors, startPoint: .topLeading, endPoint: .bottomTrailing)
    }

    /// Short expansion animation, disabled under Reduce Motion.
    static func motion(_ reduce: Bool) -> Animation? {
        reduce ? nil : .spring(duration: 0.32, bounce: 0.12)
    }
}

/// Shown over the UI while the app is inactive so card tiles do not appear
/// in the app switcher snapshot.
struct PrivacyShieldView: View {
    var body: some View {
        ZStack {
            Theme.canvasDark.ignoresSafeArea()
            Text("MyWallet")
                .font(.system(.largeTitle, design: .rounded, weight: .semibold))
                .foregroundStyle(Theme.accent)
        }
        .accessibilityHidden(true)
    }
}

/// Small pill used for "Saved", "Preferred", and fixture labels.
struct Pill: View {
    let text: String
    var tint: Color = Theme.accent

    var body: some View {
        Text(text)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(tint.opacity(0.18), in: Capsule())
            .foregroundStyle(tint)
    }
}
