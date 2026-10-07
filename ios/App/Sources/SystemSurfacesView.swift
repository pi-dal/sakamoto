import SwiftUI

struct SystemSurfacesView: View {
    var body: some View {
        List {
            Section("Home Screen") {
                Text("VPN connection")
                Text("Add the sakamoto widget from the Home Screen widget gallery. Small, medium and large sizes show the VPN state; medium and large also show the selected node and last latency test.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            Section("Shortcuts") {
                Text("Connect VPN")
                Text("Disconnect VPN")
                Text("Toggle VPN")
                Text("Get VPN status")
            }
            Section("Control Center") {
                Text("VPN connection")
                Text("On iOS 18 or later, edit Control Center and add the sakamoto VPN control.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            Section {
                Text("Connect once in sakamoto to configure and authorize the VPN. System actions open the app and use that saved profile. The system confirms whether the tunnel is running; network verification is a separate check.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Widgets & Shortcuts")
        .navigationBarTitleDisplayMode(.inline)
    }
}
