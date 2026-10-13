import SwiftUI

/// One task per screen. Setup and optional cache recovery survive Settings and reopening.
struct LocationSetupGuide: View {
    @ObservedObject var model: VirtualLocationModel
    var showRoute: () -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @Environment(\.scenePhase) private var scenePhase
    @AppStorage("sakamoto.location.setupWizardStep") private var storedStep = 0
    @AppStorage("sakamoto.location.cacheRecoveryActive") private var recovering = false
    @AppStorage("sakamoto.location.cacheRecoveryStep") private var recoveryStep = 0
    @State private var choosingPosition = false
    private let setupTitles = ["Download your certificate", "Install the profile", "Enable full trust", "Choose a place", "Connect and start", "Verify the result"]
    private let recoveryTitles = ["Pause Location Services", "Restart your device", "Reconnect and resume simulation", "Enable location and verify"]
    private var titles: [String] { recovering ? recoveryTitles : setupTitles }
    // Old guide pages 6–8 all resume at verification, without prescribing another toggle.
    private var step: Int { min(max(recovering ? recoveryStep : storedStep, 0), titles.count - 1) }
    private var pageID: String { "\(recovering)-\(step)" }

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
                        } label: { Text("Jump to step").frame(minHeight: 44) }
                    }
                    ProgressView(value: Double(step + 1), total: Double(titles.count))
                        .accessibilityLabel(recovering ? "Cache recovery progress" : "Setup progress")
                }.padding()
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        Text(titles[step]).font(.title2.bold()).accessibilityAddTraits(.isHeader)
                        if model.supportsSimulation {
                            if recovering { recoveryContent } else { content }
                        } else { Text("Network-location simulation is unavailable on iOS 27 and later. You can close this guide and use the other settings.") }
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
                    Button(step == titles.count - 1 ? (recovering ? "Verify" : "Done") : "Next") {
                        if step == titles.count - 1 {
                            if recovering { finishRecovery() } else { dismiss() }
                        } else { go(to: step + 1) }
                    }.buttonStyle(.borderedProminent).frame(minHeight: 44)
                }.padding().background(Color(uiColor: .systemBackground))
            }
            .controlSize(.large)
            .background(Color(uiColor: .systemGroupedBackground))
            .navigationTitle(recovering ? "Refresh a stale location" : "Location setup")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } } }
            .sheet(isPresented: $choosingPosition) {
                LocationMapPicker(isRoute: false, points: [model.mapPoint], center: model.mapPoint) { points in
                    if let point = points.first { model.selectMapPosition(point) }
                }
            }
            .task(id: pageID) {
                await model.refresh()
                guard !recovering, step == setupTitles.count - 1 else { return }
                while !Task.isCancelled {
                    do { try await Task.sleep(nanoseconds: 2_000_000_000) } catch { return }
                    await model.refresh()
                }
            }
            .onChange(of: pageID) { _ in model.cancelObservation(); model.message = nil }
            .onChange(of: scenePhase) { phase in if phase == .active { Task { await model.refresh() } } }
            .onDisappear { model.cancelObservation(); model.closeCertificateServer() }
        }
    }

    @ViewBuilder private var content: some View {
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
            Text("Connect Sakamoto VPN using your configured profile. Then return to Virtual location and tap Continue setup to come back to this page.")
            openHomeAction
            startAction
            if !model.trustChecked { Button("Review certificate trust") { storedStep = 2 } }
            Text("Starting is explicit. Next only changes the guide page. If you have no VPN profile, add one in Config first.").font(.footnote).foregroundStyle(.secondary)
            Button("Open moving-route controls", action: showRoute)
        default:
            Text("Ask your target app for a new location, then compare it with the place you selected.")
            Label(locationServicesStatus, systemImage: model.locationServicesEnabled == true ? "location" : "location.slash")
            if model.locationServicesEnabled == false {
                Text("The global switch pauses GPS, Wi-Fi and cellular location for apps. Enable it when you want a fresh system-location measurement.")
                settingsAction("Try opening Location Services", destination: .locationServices)
            }
            Button("Check reported location") { model.observeLocation() }
                .disabled(model.observing || model.locationServicesEnabled == false).buttonStyle(.borderedProminent)
            if model.observing { ProgressView("Waiting for a fresh location…") }
            if let observed = model.observed { Text(observed).textSelection(.enabled) }
            Text("Rewritten responses: \(model.snapshot.patched)").font(.callout)
            Text("A rewrite proves interception; the reported position is the separate result. GPS and caches can still win.").font(.footnote).foregroundStyle(.secondary)
            Button("Refresh a stale location") { recoveryStep = 0; recovering = true }
            Text("Use the optional recovery guide if the target app keeps its old position. An immediate off/on toggle is not a reliable cache reset.").font(.footnote).foregroundStyle(.secondary)
            Button("Open Sakamoto location permissions") { model.openAppSettings() }
            Button("Stop simulation", role: .destructive) { Task { await model.apply(enabled: false, useEditedCoordinates: false) } }.disabled(model.busy)
            Text("Stopping restores normal responses. Apps may keep their last fix until they request another location; persistent stale results may need a device restart.").font(.footnote).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private var recoveryContent: some View {
        switch step {
        case 0:
            Text("If the position stays stale, open Settings and turn the global Location Services switch OFF. Keep it off through the restart and VPN recovery pages.")
            settingsAction("Try opening Location Services", destination: .locationServices)
            Label(locationServicesStatus, systemImage: model.locationServicesEnabled == true ? "location" : "location.slash")
            Text("This pauses system location for all apps, including GPS and network positioning. It does not force apps to use the simulated point.").font(.callout)
            Button("Return to verification", action: finishRecovery)
        case 1:
            Text("With Location Services off, restart this device using its power controls. After unlocking, open Sakamoto → Settings → Virtual location → Continue setup. Tap Next to restore the VPN.")
            Text("Restarting can help when iOS keeps an old position. Verify the result after reconnecting; GPS and other location sources may still take precedence.").font(.callout)
            Text("Your place in this guide is saved. Sakamoto cannot restart the phone or change the system location switch for you.").font(.footnote).foregroundStyle(.secondary)
        case 2:
            Text("Keep Location Services off while you reconnect Sakamoto VPN. Return here and explicitly resume simulation before enabling system location.")
            openHomeAction
            startAction
            Text("The VPN and location proxy must be ready before apps request their next fix. Recheck certificate trust if it is no longer verified.").font(.footnote).foregroundStyle(.secondary)
            if !model.trustChecked { Button("Review certificate trust") { recovering = false; storedStep = 2 } }
        default:
            Label(model.vpnRunning ? "VPN connected" : "VPN not connected", systemImage: model.vpnRunning ? "checkmark.circle" : "circle")
            Label(model.snapshotAvailable && model.snapshot.enabled ? "Network-location proxy enabled" : "Network-location proxy not confirmed", systemImage: model.snapshotAvailable && model.snapshot.enabled ? "checkmark.circle" : "circle")
            Text("Once the VPN and simulation are running, open Settings and enable Location Services. Then tap Verify and request a fresh location in the target app.")
            settingsAction("Try opening Location Services", destination: .locationServices)
            Label(locationServicesStatus, systemImage: model.locationServicesEnabled == true ? "location" : "location.slash")
            Text("Leaving the global switch off prevents apps from obtaining new system locations. If the result still differs, GPS, caches or the OS may be taking precedence.").font(.callout)
        }
    }
    private var openHomeAction: some View {
        Button("Open Home to connect VPN") { dismiss(); openURL(URL(string: "sakamoto://home")!) }
    }
    private var startAction: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label(model.vpnRunning ? "VPN connected" : "VPN not connected", systemImage: model.vpnRunning ? "checkmark.circle" : "circle")
            Button(model.snapshot.enabled ? "Apply selected place" : "Start at selected place") { Task { await model.apply(enabled: true) } }
                .disabled(model.busy || !model.trustChecked || !model.vpnRunning).buttonStyle(.borderedProminent)
        }
    }
    private func go(to value: Int) {
        if recovering { recoveryStep = value } else { storedStep = value }
    }
    private func finishRecovery() { storedStep = setupTitles.count - 1; recovering = false }
    private var locationServicesStatus: String {
        guard let enabled = model.locationServicesEnabled else { return "Checking system Location Services…" }
        return enabled ? "Location Services: ON" : "Location Services: OFF — fresh measurements paused"
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
