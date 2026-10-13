import SwiftUI

/// Optional, in-place setup: every action uses the real location controls.
struct LocationSetupGuide: View {
    @ObservedObject var model: VirtualLocationModel
    @Binding var expanded: Bool
    var choosePosition: () -> Void
    var showRoute: () -> Void
    var showDiagnostics: () -> Void
    @Environment(\.openURL) private var openURL

    var body: some View {
        Section {
            DisclosureGroup(isExpanded: $expanded) {
                if model.supportsSimulation {
                    step(1, "Download the location certificate") {
                        Text("Sakamoto creates a certificate for this device. Tap below, allow the profile download in Safari, then return here. This certificate lets Sakamoto read and change supported Apple network-positioning responses.")
                        Button("Download certificate profile") { model.installCertificate() }
                            .disabled(model.busy)
                        if let profile = model.certificateProfileURL {
                            ShareLink("Share certificate profile instead", item: profile)
                        }
                    }
                    step(2, "Install the downloaded profile") {
                        Text("In Settings, open Profile Downloaded → Install and follow the prompts. If it is missing, use General → VPN & Device Management → Sakamoto Location Certificate.")
                        Button("Open Sakamoto settings") { model.openAppSettings() }
                        Text("This opens Sakamoto’s settings. Go back to the main Settings screen and follow the path above. Downloading a profile alone does not install it.")
                    }
                    step(3, "Enable full trust and verify it") {
                        Text("In Settings, go to General → About → Certificate Trust Settings. Turn on full trust for Sakamoto Location CA, then return here and run the check.")
                        Button("Open Sakamoto settings") { model.openAppSettings() }
                        Button("Check certificate trust") { Task { await model.checkTrust() } }
                            .disabled(model.busy)
                        Label(model.trustChecked ? "Certificate trust last verified" : "Certificate trust needs verification", systemImage: model.trustChecked ? "checkmark.circle" : "circle")
                        Text("Go back to the main Settings screen to follow this path; iOS does not provide a direct link to Certificate Trust Settings. You can remove the certificate later in VPN & Device Management.")
                    }
                    step(4, "Choose the place to simulate") {
                        Text("Search or move the map, choose a point, then tap Done. Keep accuracy at 25 meters to start. Selecting a point only edits your draft.")
                        Button("Choose on map", action: choosePosition)
                    }
                    step(5, "Connect the VPN and enable simulation") {
                        Text("Open Home and connect Sakamoto using your configured profile. If you have no profile yet, add one in Config first. Return to Settings → Virtual location, then enable simulation below. Saving a location while disconnected waits for the VPN.")
                        Button("Go to Home to connect VPN") { openURL(URL(string: "sakamoto://home")!) }
                        Label(model.vpnRunning ? "VPN connected" : "VPN not connected", systemImage: model.vpnRunning ? "checkmark.circle" : "circle")
                        Button(model.snapshot.enabled ? "Apply selected location" : "Enable simulation at selected place") {
                            Task { await model.apply(enabled: true) }
                        }.disabled(model.busy || !model.trustChecked || !model.vpnRunning)
                        Text("For movement, choose 2–32 stops and a duration in Moving route, then tap Start route. Start is always explicit.")
                        Button("Go to Moving route", action: showRoute)
                    }
                    step(6, "Check the location apps receive") {
                        Text("Ask your target app for a new location. Then check the reported location here and compare it with your selected point. GPS and cached fixes may take precedence, even when responses are rewritten.")
                        Button("Check reported location") {
                            model.observeLocation()
                            showDiagnostics()
                        }.disabled(model.observing)
                        Button("Go to diagnostics", action: showDiagnostics)
                        Text("Turning off Simulate network location restores normal responses. Apps may keep their last fix until they request another location.")
                    }
                } else {
                    Text("This method supports iOS through version 26. Network-location simulation is unavailable on iOS 27 and later.")
                }
            } label: {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Step-by-step setup")
                    Text(nextAction).font(.footnote).foregroundStyle(.secondary)
                }
            }
            if let message = model.message, expanded {
                Text(message).font(.footnote).foregroundStyle(.secondary).textSelection(.enabled)
            }
            if model.busy, expanded { ProgressView("Checking or updating…") }
        } header: { Text("Setup guide") } footer: {
            Text("Follow the steps in order, or collapse this guide to use the controls directly. Setup never starts simulation automatically.")
        }
    }
    private var nextAction: String {
        if !model.supportsSimulation { return "Unavailable on this iOS version" }
        if !model.trustChecked { return "Next: install the certificate and check trust" }
        if !model.vpnRunning { return "Next: choose a place and connect the VPN" }
        if !model.snapshotAvailable { return "Next: refresh diagnostics to check simulation" }
        if !model.snapshot.enabled { return "Next: enable simulation at your selected place" }
        return "Simulation active · check the reported location"
    }
    private func step<Content: View>(_ number: Int, _ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("\(number). \(title)").font(.headline).accessibilityAddTraits(.isHeader)
            content()
                .font(.subheadline)
                .textSelection(.enabled)
        }
        .buttonStyle(.borderless)
        .padding(.vertical, 12)
    }
}
