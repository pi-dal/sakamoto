import SwiftUI
import AppIntents

/// Personal App triggers belong to Shortcuts. The user chooses apps there;
/// sakamoto supplies explicit, idempotent connect/disconnect actions.
struct AppAutomationView: View {
    var body: some View {
        List {
            Section {
                Text("Connect once in Home and apply your configuration before creating an automation.")
                    .font(.subheadline)
                ShortcutsLink {
                    SakamotoShortcuts.updateAppShortcutParameters()
                }
                .shortcutsLinkStyle(.automaticOutline)
            } header: { Text("sakamoto actions") } footer: {
                Text("Connect VPN and Disconnect VPN are available automatically in Shortcuts. You do not need to download a shortcut.")
            }

            Section {
                instruction("Choose an App trigger", "Open Shortcuts → Automation → + → App. Tap Choose and select the apps you want.")
                instruction("Connect when opened", "Select Is Opened, then Run Immediately. On older iOS versions, turn off Ask Before Running.")
                instruction("Add Connect VPN", "Choose New Blank Automation, add an action, search for sakamoto and select Connect VPN. Save the automation.")
            } header: { Text("When an app opens") } footer: {
                Text("Use Connect VPN rather than Toggle VPN so reopening the app does not disconnect an already-running tunnel.")
            }

            Section {
                instruction("Create a separate automation", "Choose the same apps with the App trigger, select Is Closed and Run Immediately, then add sakamoto → Disconnect VPN.")
            } header: { Text("When you leave an app · optional") } footer: {
                Text("Is Closed also runs when you switch away from the app. This stops the entire VPN and pauses on-demand connection, including other apps and background transfers. Skip this automation if you want the VPN to stay connected.")
            }

            Section {
                Link(destination: URL(string: "shortcuts://")!) {
                    Label("Open Shortcuts to set up automation", systemImage: "arrow.up.forward.app")
                }
                Link("Apple’s App automation guide", destination: URL(string: "https://support.apple.com/guide/shortcuts/setting-triggers-apde31e9638b/ios")!)
            } footer: {
                Text("Choose Automation after opening Shortcuts. iOS requires you to select the apps and save each automation there; sakamoto cannot create a personal automation for you.")
            }

            Section("How traffic is routed") {
                Text("App automations switch the saved VPN for the device. Traffic still follows your routing rules. They do not restrict the VPN to the selected app; per-app VPN on iOS requires managed apps and device management.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("App automation")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func instruction(_ title: String, _ detail: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).font(.body.weight(.medium))
            Text(detail).font(.subheadline).foregroundStyle(.secondary)
        }
        .padding(.vertical, 4)
    }
}
