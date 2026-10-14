import SwiftUI

/// Separate setup, activation, and restoration; every page names its next action.
enum LocationGuideFlow: String {
    case setup, activation, restoration, restorationRefresh
    var isRestoring: Bool { self == .restoration || self == .restorationRefresh }
    var title: String {
        switch self {
        case .setup: return "Location setup"
        case .activation: return "Activate network location"
        case .restoration, .restorationRefresh: return "Restore real location"
        }
    }
    var titles: [String] {
        if self == .setup { return ["Download your certificate", "Install the profile", "Enable full trust", "Choose a place", "Prepare and start simulation", "Verify the result"] }
        let restore = ["Restore Wi-Fi", "Turn off Airplane Mode", "Enable location and verify"]
        if self == .restoration { return restore }
        return ["Turn on Airplane Mode", "Turn Wi-Fi off", "Pause Location Services", isRestoring ? restore[0] : "Connect Wi-Fi and start", restore[1], restore[2]]
    }
    func clampedStep(_ value: Int) -> Int { min(max(value, 0), titles.count - 1) }
    func networkStep(_ value: Int) -> Int { self == .restoration ? value + 3 : value }
}

struct LocationSetupGuide: View {
    @ObservedObject var model: VirtualLocationModel
    var showRoute: () -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @Environment(\.scenePhase) private var scenePhase
    @AppStorage("sakamoto.location.setupWizardStep") private var storedStep = 0
    @AppStorage("sakamoto.location.guideFlow") private var storedFlow = LocationGuideFlow.setup.rawValue
    @AppStorage("sakamoto.location.networkGuideStep") private var networkStep = 0
    @AppStorage("sakamoto.location.cacheRecoveryActive") private var legacyRecovery = false
    @State private var choosingPosition = false
    @State private var verificationBaseline: Int64?
    private var flow: LocationGuideFlow { LocationGuideFlow(rawValue: storedFlow) ?? .setup }
    private var titles: [String] { flow.titles }
    private var step: Int { flow.clampedStep(flow == .setup ? storedStep : networkStep) }
    private var pageID: String { "\(flow.rawValue)-\(step)" }
    private var verifying: Bool { step == titles.count - 1 }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Text("Step \(step + 1) of \(titles.count)").font(.subheadline).foregroundStyle(.secondary)
                        Spacer()
                        Menu {
                            ForEach(titles.indices, id: \.self) { index in
                                Button("\(index + 1). \(titles[index])") { go(to: index) }
                            }
                            Section("Other guides") {
                                Button("Certificate and place setup") { storedStep = 0; begin(.setup) }
                                Button("Activation steps") { begin(.activation) }.disabled(!model.supportsSimulation)
                                Button("Stop and restore real location") { Task { await model.stopForRestoration() } }.disabled(model.busy)
                            }
                        } label: { Text("Jump to step").frame(minHeight: 44) }
                    }
                    ProgressView(value: Double(step + 1), total: Double(titles.count))
                        .accessibilityLabel("Location guide progress")
                }.padding()
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        Text(titles[step]).font(.title2.bold()).accessibilityAddTraits(.isHeader)
                        if model.supportsSimulation || flow.isRestoring {
                            if flow == .setup { setupContent } else { networkContent }
                        } else { Text("Network-location simulation is unavailable on iOS 27 and later. You can still restore real location from Virtual location.") }
                        if model.busy { ProgressView("Checking or updating…") }
                        if let message = model.message { Text(message).font(.callout).textSelection(.enabled) }
                    }
                    .frame(maxWidth: 600, alignment: .leading)
                    .frame(maxWidth: .infinity)
                    .padding()
                }.id(pageID)
                Divider()
                HStack(spacing: 16) {
                    Button { go(to: step - 1) } label: { Text("Previous").frame(minWidth: 72, minHeight: 44) }
                        .disabled(step == 0)
                    Spacer()
                    Button(verifying ? "Close guide" : "Next step") {
                        if verifying { dismiss() } else { go(to: step + 1) }
                    }.buttonStyle(.borderedProminent).frame(minHeight: 44)
                }.padding().background(Color(uiColor: .systemBackground))
            }
            .controlSize(.large)
            .background(Color(uiColor: .systemGroupedBackground))
            .navigationTitle(flow.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } } }
            .sheet(isPresented: $choosingPosition) {
                LocationMapPicker(isRoute: false, points: [model.mapPoint], center: model.mapPoint) { points in
                    if let point = points.first { model.selectMapPosition(point) }
                }
            }
            .task(id: pageID) {
                if legacyRecovery {
                    legacyRecovery = false
                    begin(.activation)
                    return
                }
                verificationBaseline = nil
                model.cancelObservation()
                await model.refresh()
                guard verifying else { return }
                verificationBaseline = model.snapshot.patched
                while !Task.isCancelled {
                    do { try await Task.sleep(nanoseconds: 2_000_000_000) } catch { return }
                    await model.refresh()
                }
            }
            .onChange(of: scenePhase) { phase in if phase == .active { Task { await model.refresh() } } }
            .onDisappear { model.cancelObservation(); model.closeCertificateServer() }
        }
    }

    @ViewBuilder private var setupContent: some View {
        switch step {
        case 0:
            Text("Download the certificate profile in Safari and allow the download. Return here when it finishes.")
            Button("Download certificate") { model.installCertificate() }.disabled(model.busy)
                .buttonStyle(.borderedProminent)
            if let profile = model.certificateProfileURL { ShareLink("Share profile instead", item: profile) }
            Text("This certificate belongs to this device. It lets Sakamoto read and change supported Apple network-positioning responses.").font(.footnote).foregroundStyle(.secondary)
        case 1:
            Text("In Settings, tap Profile Downloaded, then Install. Follow the installation prompts.")
            settingsAction("Try opening profile settings", destination: .profiles)
            Text("If Profile Downloaded is missing, use General → VPN & Device Management → Sakamoto Location Certificate. Downloading alone does not install it.")
        case 2:
            Text("Turn on full trust for Sakamoto Location CA, then return here and check it.")
            settingsAction("Try opening certificate trust settings", destination: .certificateTrust)
            Button("Check certificate trust") { Task { await model.checkTrust() } }.disabled(model.busy)
                .buttonStyle(.borderedProminent)
            Label(model.trustChecked ? "Trust last verified" : "Trust needs verification", systemImage: model.trustChecked ? "checkmark.circle" : "circle")
        case 3:
            Text("Search or tap the map to choose your simulated place, then tap Done. Selecting a place edits your draft; it does not start simulation.")
            Button("Choose on map") { choosingPosition = true }.buttonStyle(.borderedProminent)
            Text(String(format: "Selected: %.6f, %.6f", model.mapPoint.latitude, model.mapPoint.longitude)).font(.callout)
            Text("Keep the default 25-meter accuracy to start. You can adjust it later in Virtual location.").font(.footnote).foregroundStyle(.secondary)
        case 4:
            Text("Use a Wi-Fi network for the activation steps. The guide will show when to pause system location, connect the VPN, and tap Start simulation.")
            Button("Begin activation steps") { begin(.activation) }.buttonStyle(.borderedProminent)
            Text("If you have no VPN profile, add one in Config first. Next step only opens verification; it does not start simulation.").font(.footnote).foregroundStyle(.secondary)
            Button("Verify current setup") { go(to: titles.count - 1) }
            Button("Open moving-route controls", action: showRoute)
        default:
            verificationContent
        }
    }

    @ViewBuilder private var networkContent: some View {
        switch flow.networkStep(step) {
        case 0:
            Text("In Control Center, turn Airplane Mode ON. Keep it on until the guide tells you to turn it off.")
            Text("Cellular service will pause. Wi-Fi may remain enabled, so turn it off separately on the next page.").font(.callout)
            Text("These steps follow the reference project's location-refresh procedure. They do not guarantee that iOS will use the selected point.").font(.footnote).foregroundStyle(.secondary)
        case 1:
            Text("Open Settings → Wi-Fi and turn the Wi-Fi switch OFF. Keep Airplane Mode on.")
            settingsAction("Try opening Wi-Fi settings", destination: .wifi)
            Text("The Control Center Wi-Fi button usually only disconnects from a network; Wi-Fi can remain available for Location Services.").font(.callout)
        case 2:
            Text("Open Settings → Privacy & Security → Location Services and turn the top switch OFF. Leave it off while you restore Wi-Fi on the next page.")
            settingsAction("Try opening Location Services", destination: .locationServices)
            locationStatus
            DisclosureGroup("If the previous attempt kept the old location") {
                Text("You can restart now while Location Services is off. After unlocking, return to Sakamoto → Settings → Virtual location → Continue guide, then tap Next step. Recheck Airplane Mode and Wi-Fi before continuing. Restarting is optional and does not guarantee a cache reset.").font(.callout)
            }
        case 3:
            Text(flow.isRestoring ? "If you turned Wi-Fi off, open Settings → Wi-Fi and turn it back ON. Rejoin your usual network." : "Keep Airplane Mode ON and Location Services OFF. Open Settings → Wi-Fi, turn Wi-Fi ON, and join a network with internet access.")
            settingsAction("Try opening Wi-Fi settings", destination: .wifi)
            if flow.isRestoring {
                Label(model.snapshotAvailable && model.snapshot.enabled ? "Simulation still enabled — stop it first" : model.settings.enabled ? "Simulation still requested — stop it first" : "Simulation stopped", systemImage: model.settings.enabled || model.snapshot.enabled ? "exclamationmark.triangle" : "checkmark.circle")
                if model.settings.enabled || model.snapshot.enabled { stopAction }
                Text("Sakamoto restores its previous proxy settings when simulation stops. Keep the VPN connected if you still need it; you do not need to change your Wi-Fi HTTP proxy for Sakamoto.").font(.footnote).foregroundStyle(.secondary)
            } else {
                Text("Connect Sakamoto VPN from Home, then return to Virtual location → Continue guide. On this page, tap Start simulation at selected place. Wait for Network-location proxy enabled before continuing.")
                openHomeAction
                startAction
                Text("Next step only moves to the next page. It does not connect the VPN or start simulation.").font(.footnote).foregroundStyle(.secondary)
                if !model.trustChecked { Button("Review certificate trust") { storedFlow = LocationGuideFlow.setup.rawValue; storedStep = 2 } }
            }
        case 4:
            Text("Turn Airplane Mode OFF in Control Center. Keep Location Services off until the next page.")
            Text(flow.isRestoring ? "This restores normal cellular connectivity. Simulation must remain off." : "Wait for the VPN to reconnect if changing the network interrupted it. Return to the previous page if the location proxy is no longer enabled.").font(.callout)
        default:
            Text("Open Settings → Privacy & Security → Location Services and turn the top switch ON when you want apps to obtain a fresh location.")
            settingsAction("Try opening Location Services", destination: .locationServices)
            verificationContent
        }
    }

    @ViewBuilder private var verificationContent: some View {
        locationStatus
        if model.locationServicesEnabled == false {
            Text("Location Services is still off. Re-enable it before checking a new position; stopping Sakamoto does not turn this system switch back on.")
            if flow == .setup { settingsAction("Try opening Location Services", destination: .locationServices) }
        }
        Text(flow.isRestoring ? "Open your target app or Maps and request a fresh location. Compare the result with your real surroundings." : "Open your target app or Maps and request a fresh location. Compare the result with your selected place.")
        if flow.isRestoring && (model.settings.enabled || model.snapshot.enabled) {
            Text("Simulation is still enabled. Stop it before checking restoration.")
            stopAction
        }
        Button("Check reported location") { model.observeLocation(restoringRealLocation: flow.isRestoring) }
            .disabled(model.observing || model.locationServicesEnabled != true || (flow.isRestoring && (model.settings.enabled || model.snapshot.enabled))).buttonStyle(.borderedProminent)
        if model.observing { ProgressView("Waiting for a fresh location…") }
        if let observed = model.observed { Text(observed).textSelection(.enabled) }
        if flow.isRestoring {
            Button("Still old? Refresh real location") { begin(.restorationRefresh) }
            Text("The refresh guide includes the full radio/location sequence and an optional restart while location is off.").font(.footnote).foregroundStyle(.secondary)
        } else {
            Label(model.vpnRunning ? "VPN connected" : "VPN not connected", systemImage: model.vpnRunning ? "checkmark.circle" : "circle")
            Label(model.snapshotAvailable && model.snapshot.enabled ? "Network-location proxy enabled" : "Network-location proxy not confirmed", systemImage: model.snapshotAvailable && model.snapshot.enabled ? "checkmark.circle" : "circle")
            Text("New rewrites during this check: \(verificationBaseline.map { max(0, model.snapshot.patched - $0) } ?? 0)").font(.callout)
            Text(model.verificationHint(since: verificationBaseline)).font(.callout)
            Button("Retry activation steps") { begin(.activation) }
            stopAction
        }
        Button("Open Sakamoto location permissions") { model.openAppSettings() }
        ShareLink("Share diagnostic report", item: model.report)
    }
    private var openHomeAction: some View {
        Button("Open Home to connect VPN") { dismiss(); openURL(URL(string: "sakamoto://home")!) }
    }
    private var startAction: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label(model.vpnRunning ? "VPN connected" : "VPN not connected", systemImage: model.vpnRunning ? "checkmark.circle" : "circle")
            Button(model.snapshot.enabled ? "Apply selected place" : "Start simulation at selected place") { Task { await model.apply(enabled: true) } }
                .disabled(model.busy || !model.trustChecked || !model.vpnRunning).buttonStyle(.borderedProminent)
            if !model.trustChecked {
                Button("Check certificate trust to enable Start") { Task { await model.checkTrust() } }.disabled(model.busy)
            } else if !model.vpnRunning {
                Text("Connect the VPN from Home to enable Start.").font(.callout)
            }
            Label(model.snapshotAvailable && model.snapshot.enabled ? "Network-location proxy enabled" : "Network-location proxy not confirmed", systemImage: model.snapshotAvailable && model.snapshot.enabled ? "checkmark.circle" : "circle")
        }
    }
    private var stopAction: some View {
        Button("Stop and restore real location", role: .destructive) { Task { await model.stopForRestoration() } }.disabled(model.busy)
    }
    private func go(to value: Int) {
        if flow == .setup { storedStep = value } else { networkStep = value }
    }
    private func begin(_ value: LocationGuideFlow) { networkStep = 0; storedFlow = value.rawValue }
    private var locationStatus: some View {
        Label(model.locationServicesEnabled.map { $0 ? "Location Services: ON" : "Location Services: OFF — fresh measurements paused" } ?? "Checking system Location Services…", systemImage: model.locationServicesEnabled == true ? "location" : "location.slash")
    }
    private func settingsAction(_ title: String, destination: LocationSetupSettingsDestination) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Button(title) { model.openSetupSettings(destination) }.buttonStyle(.bordered)
            Text(destination.manualPath).font(.callout).textSelection(.enabled)
            Text("iOS may open a different Settings page. Follow the path above if needed.")
                .font(.footnote).foregroundStyle(.secondary)
        }
    }
}
