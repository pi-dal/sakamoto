import SwiftUI

/// Keep native switch thumbs visible when the app's monochrome tint becomes
/// white in Dark Mode. This style affects switches, not navigation/buttons.
struct SakamotoSwitchStyle: ToggleStyle {
    func makeBody(configuration: Configuration) -> some View {
        Toggle(configuration)
            .toggleStyle(.switch)
            .tint(Color(red: 0.27, green: 0.40, blue: 0.64))
    }
}

/// Native platform surface styling shared by the iOS app.
///
/// Liquid Glass is available only on iOS 26. Older supported releases keep the
/// same hierarchy with a native material fallback instead of a fake blur.
extension View {
    /// Top-level destinations share Home's grouped canvas. Native large titles
    /// collapse on scroll; the iOS 26 tab bar keeps its system Liquid Glass.
    func sakamotoRootPage() -> some View {
        self
            .listStyle(.insetGrouped)
            .scrollContentBackground(.hidden)
            .background(Color(uiColor: .systemGroupedBackground))
            .navigationBarTitleDisplayMode(.inline)
            .toolbarBackground(.visible, for: .navigationBar)
    }

    @ViewBuilder
    func sakamotoGlassCard(cornerRadius: CGFloat = 20) -> some View {
        if #available(iOS 26, *) {
            self.glassEffect(.regular, in: .rect(cornerRadius: cornerRadius))
        } else {
            self.background(.thinMaterial, in: RoundedRectangle(cornerRadius: cornerRadius))
        }
    }

    @ViewBuilder
    func sakamotoGlassButton(prominent: Bool = false) -> some View {
        if #available(iOS 26, *) {
            if prominent {
                self.buttonStyle(.glassProminent)
            } else {
                self.buttonStyle(.glass)
            }
        } else if prominent {
            self.buttonStyle(.borderedProminent)
        } else {
            self.buttonStyle(.bordered)
        }
    }
}
