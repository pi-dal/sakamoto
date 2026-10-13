import SwiftUI

/// One task per screen. Page position survives Settings, map editing and reopening.
struct LocationSetupGuide: View {
    @ObservedObject var model: VirtualLocationModel
    var showRoute: () -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @Environment(\.scenePhase) private var scenePhase
    @AppStorage("sakamoto.location.setupWizardStep") private var storedStep = 0
    @State private var choosingPosition = false
    private let titles = ["Download your certificate", "Install the profile", "Enable full trust", "Choose a place", "Connect and start", "Turn Location Services off", "Turn Location Services back on", "Verify the result"]
    private var step: Int { min(max(storedStep, 0), titles.count - 1) }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                VStack(alignment: .leading, spacing: 10) {
                    HStack {
                        Text("Step \(step + 1) of \(titles.count)").font(.subheadline).foregroundStyle(.secondary)
                        Spacer()
                        Menu {
                            ForEach(titles.indices, id: \.self) { index in
                                Button("\(index + 1). \(titles[index])") { storedStep = index }
                            }
                        } label: { Text("Jump to step").frame(minHeight: 44) }
                    }
                    ProgressView(value: Double(step + 1), total: Double(titles.count))
                        .accessibilityLabel("Setup progress")
                }.padding()
                ScrollView {
                    VStack(alignment: .leading, spacing: 20) {
                        Text(titles[step]).font(.title2.bold()).accessibilityAddTraits(.isHeader)
                        if model.supportsSimulation { content }
                        else { Text("Network-location simulation is unavailable on iOS 27 and later. You can close this guide and use the other settings.") }
                        if model.busy { ProgressView("Checking or updating…") }
                        if let message = model.message { Text(message).font(.callout).textSelection(.enabled) }
                    }
                    .frame(maxWidth: 600, alignment: .leading)
                    .frame(maxWidth: .infinity)
                    .padding()
                }.id(step)
                Divider()
                HStack(spacing: 16) {
                    Button { storedStep = step - 1 } label: { Text("Previous").frame(minWidth: 72, minHeight: 44) }
                        .disabled(step == 0)
                    Spacer()
                    Button(step == titles.count - 1 ? "Done" : "Next") {
                        if step == titles.count - 1 { dismiss() }
                        else { storedStep = step + 1 }
                    }.buttonStyle(.borderedProminent).frame(minHeight: 44)
                }.padding().background(Color(uiColor: .systemBackground))
            }
            .controlSize(.large)
            .background(Color(uiColor: .systemGroupedBackground))
            .navigationTitle("Location setup")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } } }
            .sheet(isPresented: $choosingPosition) {
                LocationMapPicker(isRoute: false, points: [model.mapPoint], center: model.mapPoint) { points in
                    if let point = points.first { model.selectMapPosition(point) }
                }
            }
            .task(id: step) {
                await model.refresh()
                guard step == titles.count - 1 else { return }
                while !Task.isCancelled {
                    do { try await Task.sleep(nanoseconds: 2_000_000_000) } catch { return }
                    await model.refresh()
                }
            }
            .onChange(of: storedStep) { _ in model.cancelObservation(); model.message = nil }
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
            settingsAction("Open profile settings", destination: .profiles)
            Text("If Profile Downloaded is missing, use General → VPN & Device Management → Sakamoto Location Certificate. Downloading alone does not install it.")
        case 2:
            Text("Turn on full trust for Sakamoto Location CA, then return here and check it.")
            settingsAction("Open certificate trust settings", destination: .certificateTrust)
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
            Button("Open Home to connect VPN") { dismiss(); openURL(URL(string: "sakamoto://home")!) }
            Label(model.vpnRunning ? "VPN connected" : "VPN not connected", systemImage: model.vpnRunning ? "checkmark.circle" : "circle")
            Button(model.snapshot.enabled ? "Apply selected place" : "Start at selected place") { Task { await model.apply(enabled: true) } }
                .disabled(model.busy || !model.trustChecked || !model.vpnRunning).buttonStyle(.borderedProminent)
            if !model.trustChecked { Button("Review certificate trust") { storedStep = 2 } }
            Text("Starting is explicit. Next only changes the guide page. If you have no VPN profile, add one in Config first.").font(.footnote).foregroundStyle(.secondary)
            Button("Open moving-route controls", action: showRoute)
        case 5:
            Text("With simulation running, open Settings and turn the global Location Services switch OFF. Return here, then tap Next.")
            settingsAction("Open Location Services", destination: .locationServices)
            Label(locationServicesStatus, systemImage: model.locationServicesEnabled == true ? "location" : "location.slash")
            Text("This temporarily pauses location requests for all apps. The next page will ask you to turn it back on before checking the result.").font(.callout)
        case 6:
            Text("Open Settings again and turn Location Services ON. Return here to check a fresh location.")
            settingsAction("Open Location Services", destination: .locationServices)
            Label(locationServicesStatus, systemImage: model.locationServicesEnabled == true ? "location" : "location.slash")
            Text("Apps cannot request fresh locations while the global switch is off. Keep it on when verifying. Switching it off and back on does not prevent GPS or cached fixes from taking precedence.").font(.callout)
            Text("Sakamoto cannot change this system switch for you.").font(.footnote).foregroundStyle(.secondary)
        default:
            Text("Keep Location Services ON. Ask your target app for a new location, then compare it with the place you selected.")
            Button("Check reported location") { model.observeLocation() }
                .disabled(model.observing || model.locationServicesEnabled == false).buttonStyle(.borderedProminent)
            if model.locationServicesEnabled == false { settingsAction("Turn Location Services back on", destination: .locationServices) }
            if model.observing { ProgressView("Waiting for a fresh location…") }
            if let observed = model.observed { Text(observed).textSelection(.enabled) }
            Text("Rewritten responses: \(model.snapshot.patched)").font(.callout)
            Text("A rewrite proves interception; the reported position is the separate result. GPS and caches can still win.").font(.footnote).foregroundStyle(.secondary)
            Button("Open Sakamoto location permissions") { model.openAppSettings() }
            Button("Stop simulation", role: .destructive) { Task { await model.apply(enabled: false, useEditedCoordinates: false) } }.disabled(model.busy)
            Text("Stopping restores normal responses. Apps may keep their last fix until they request another location.").font(.footnote).foregroundStyle(.secondary)
        }
    }
    private var locationServicesStatus: String {
        guard let enabled = model.locationServicesEnabled else { return "Checking system Location Services…" }
        return enabled ? "Location Services: ON" : "Location Services: OFF — turn it back on to verify"
    }
    private func settingsAction(_ title: String, destination: LocationSetupSettingsDestination) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Button(title) { model.openSetupSettings(destination) }.buttonStyle(.bordered)
            Text("If Settings opens elsewhere: \(destination.manualPath)")
                .font(.footnote).foregroundStyle(.secondary).textSelection(.enabled)
        }
    }
}
