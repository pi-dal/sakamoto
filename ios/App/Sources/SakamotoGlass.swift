import SwiftUI

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
            .navigationBarTitleDisplayMode(.large)
            .toolbarBackground(.hidden, for: .navigationBar)
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
