import SwiftUI
import AppIntents

struct SystemSurfacesView: View {
    var body: some View {
        List {
            Section("Home Screen") {
                Text("VPN connection")
                Text("Add the sakamoto widget from the Home Screen widget gallery. Small, medium and large sizes show the VPN state; medium and large also show the selected node and last latency test.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            Section("Shortcuts") {
                ShortcutsLink { SakamotoShortcuts.updateAppShortcutParameters() }
                    .shortcutsLinkStyle(.automaticOutline)
                NavigationLink("Connect when an app opens") { AppAutomationView() }
                Text("Connect VPN · Disconnect VPN · Toggle VPN · Get VPN status")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            Section("Control Center") {
                Text("VPN connection")
                Text("On iOS 18 or later, edit Control Center and add the sakamoto VPN control.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            Section {
                Text("Connect once in sakamoto to configure and authorize the VPN. Widgets on iOS 17 and later, Shortcuts and Control Center use the saved profile directly. Manually disconnecting also pauses on-demand connection. The system confirms whether the tunnel is running; network verification is a separate check.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Widgets & Shortcuts")
        .navigationBarTitleDisplayMode(.inline)
    }
}
