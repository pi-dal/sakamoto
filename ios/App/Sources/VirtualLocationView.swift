import SwiftUI
import Combine
import CoreLocation
import Libbox
import SakamotoKit

@MainActor
final class VirtualLocationModel: NSObject, ObservableObject, @preconcurrency CLLocationManagerDelegate {
    let tunnel: TunnelControlling
    let supportsSimulation: Bool
    @Published var settings = VirtualLocationStore.load()
    @Published var latitude = ""
    @Published var longitude = ""
    @Published var accuracy = ""
    @Published var routeText = ""
    @Published var routeDuration = "300"
    @Published var routeInterval = "1"
    @Published var favorites = VirtualLocationStore.favorites()
    @Published var snapshot = VirtualLocationSnapshot()
    @Published var vpnRunning = false
    @Published var locationServicesEnabled: Bool?
    @Published var snapshotAvailable = false
    @Published var busy = false
    @Published var trustChecked = false
    @Published var fingerprint = ""
    @Published var certificateProfileURL: URL?
    @Published var message: String?
    @Published var observed: String?
    @Published var observing = false
    @Published var restorationGuideRequested = false
    private let guideDefaults: UserDefaults
    private var observingRealLocation = false
    private let locationManager = CLLocationManager()
    private var observationTimeout: Task<Void, Never>?
    private var certificateServer: MobilelocationCertificateServer?
    private var certificateTimeout: Task<Void, Never>?
    private var backgroundTask: UIBackgroundTaskIdentifier = .invalid

    init(tunnel: TunnelControlling, supportsSimulation: Bool = VirtualLocationStore.supportsNetworkSimulation, guideDefaults: UserDefaults = .standard) {
        self.tunnel = tunnel; self.supportsSimulation = supportsSimulation; self.guideDefaults = guideDefaults
        super.init()
        latitude = String(settings.latitude); longitude = String(settings.longitude); accuracy = String(settings.accuracy)
        if let route = VirtualLocationStore.routeDraft() {
            routeText = route.points.map { "\($0.latitude), \($0.longitude)" }.joined(separator: "\n")
            routeDuration = String(route.durationSeconds); routeInterval = String(route.intervalSeconds)
        }
        locationManager.delegate = self
        locationManager.desiredAccuracy = kCLLocationAccuracyBest
        if let raw = try? LocationAuthorityStore.load() {
            fingerprint = LocationAuthorityStore.fingerprint(raw)
            trustChecked = LocationAuthorityStore.wasTrusted(raw)
        }
    }
    func proposed(enabled: Bool) throws -> VirtualLocationSettings {
        guard let lat = Double(latitude.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(of: ",", with: ".")), let lon = Double(longitude.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(of: ",", with: ".")), let meters = Int(accuracy.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            throw TunnelProfile.InvalidProfile("Enter decimal coordinates and a whole-number accuracy")
        }
        let value = VirtualLocationSettings(enabled: enabled, latitude: lat, longitude: lon, accuracy: meters)
        try value.validate(); return value
    }
    @discardableResult
    func apply(enabled: Bool, useEditedCoordinates: Bool = true) async -> Bool {
        guard !busy else { return false }; busy = true; defer { busy = false }
        do {
            var value = settings
            if useEditedCoordinates { value = try proposed(enabled: enabled) }
            value.enabled = enabled
            value.route = nil
            if enabled {
                guard supportsSimulation else { throw TunnelProfile.InvalidProfile("This method supports iOS through version 26") }
                guard trustChecked else { throw TunnelProfile.InvalidProfile("Install the certificate, enable full trust, then Check certificate trust") }
            }
            let state = try await tunnel.status()
            vpnRunning = state == .running
            if state == .running {
                snapshot = try await tunnel.location(settings: value)
                guard snapshot.enabled == enabled else { throw TunnelProfile.InvalidProfile("Location proxy state was not confirmed. Refresh diagnostics and retry.") }
                snapshotAvailable = true
                message = enabled ? "Location proxy enabled. Follow the activation steps, then request a fresh location in your target app." : "Simulation stopped. Restore Wi-Fi, turn Airplane Mode off, and re-enable Location Services to obtain a fresh real location."
            } else if state == .starting || state == .stopping {
                throw TunnelProfile.InvalidProfile("Wait for the VPN to finish changing state")
            } else {
                try VirtualLocationStore.save(value)
                snapshot = VirtualLocationSnapshot()
                snapshotAvailable = false
                message = enabled ? "Saved. Connect Sakamoto VPN to start network-location simulation." : "Simulation is off."
            }
            settings = value
            vpnRunning = state == .running
            return true
        } catch { snapshotAvailable = false; message = error.localizedDescription; return false }
    }
    func stopForRestoration() async {
        guard await apply(enabled: false, useEditedCoordinates: false) else { return }
        guideDefaults.set(LocationGuideFlow.restoration.rawValue, forKey: "sakamoto.location.guideFlow")
        guideDefaults.set(0, forKey: "sakamoto.location.networkGuideStep")
        guideDefaults.set(false, forKey: "sakamoto.location.cacheRecoveryActive")
        restorationGuideRequested = true
    }
    func verificationHint(since baseline: Int64?) -> String {
        guard vpnRunning else { return "Connect Sakamoto VPN before checking simulation." }
        guard snapshotAvailable else { return "Proxy diagnostics are unavailable. Refresh diagnostics or reconnect the VPN." }
        guard snapshot.enabled else { return "The location proxy is off. Return to Connect Wi-Fi and start, then tap Start simulation at selected place." }
        guard locationServicesEnabled == true else { return "Enable Location Services before requesting a fresh position." }
        if let baseline, snapshot.patched > baseline {
            return "A new response was rewritten during this check. Verify the reported position separately; GPS or app caches can still take precedence."
        }
        if snapshot.requests == 0 {
            return "No location request has reached the proxy. Ask Maps for a fresh fix and refresh diagnostics. If the count stays zero, the system may be using a cached/GPS fix or bypassing the proxy."
        }
        return "No new rewrite during this check. Request a fresh fix and refresh diagnostics. Check TLS failures and pass-through counts; earlier rewrites do not confirm this attempt."
    }
    func startRoute() async {
        do { try await applyRoute(VirtualLocationRoute.parse(routeText, duration: routeDuration.trimmingCharacters(in: .whitespaces), interval: routeInterval.trimmingCharacters(in: .whitespaces))) }
        catch { message = error.localizedDescription }
    }
    func pauseRoute(_ paused: Bool) async {
        guard var route = snapshot.route else { return }; route.paused = paused
        do { try await applyRoute(route) } catch { message = error.localizedDescription }
    }
    func holdRoute() async {
        do { try await applyRoute(nil) } catch { message = error.localizedDescription }
    }
    private func applyRoute(_ route: VirtualLocationRoute?) async throws {
        guard !busy else { return }; busy = true; defer { busy = false }
        guard supportsSimulation, trustChecked else { throw TunnelProfile.InvalidProfile("Install and check certificate trust on iOS through version 26 first") }
        guard try await tunnel.status() == .running else { throw TunnelProfile.InvalidProfile("Connect Sakamoto VPN before starting or controlling a route") }
        let current = try await tunnel.location(settings: nil)
        let meters: Int
        if route == nil || route?.id == current.route?.id { meters = settings.accuracy }
        else {
            guard let edited = Int(accuracy.trimmingCharacters(in: .whitespacesAndNewlines)), (1...1000).contains(edited) else { throw TunnelProfile.InvalidProfile("Use accuracy 1 to 1000 meters") }
            meters = edited
        }
        let point = route?.points.first ?? LocationWaypoint(latitude: current.latitude ?? settings.latitude, longitude: current.longitude ?? settings.longitude)
        let value = VirtualLocationSettings(enabled: true, latitude: point.latitude, longitude: point.longitude, accuracy: meters, route: route)
        snapshot = try await tunnel.location(settings: value); snapshotAvailable = true; vpnRunning = true; settings = value
        if let route { try VirtualLocationStore.saveRouteDraft(route) }
        if route == nil { latitude = String(point.latitude); longitude = String(point.longitude) }
        message = route == nil ? "Route stopped. Simulation holds the current point; turn simulation off to restore normal responses." : "Route \(snapshot.routeState?.lowercased() ?? "started"). Movement continues in the VPN extension when you leave this page."
    }
    func refreshLocationServices() async {
        locationServicesEnabled = await Task.detached { CLLocationManager.locationServicesEnabled() }.value
        if locationServicesEnabled == false, observing {
            cancelObservation()
            observed = "Fresh measurement paused: Location Services is off. Enable it in Settings → Privacy & Security → Location Services when you want to measure a new system location."
        }
    }
    func refresh() async {
        await refreshLocationServices()
        guard !busy else { return }
        vpnRunning = (try? await tunnel.status()) == .running
        if vpnRunning {
            do { snapshot = try await tunnel.location(settings: nil); snapshotAvailable = true; settings.route = snapshot.route }
            catch { snapshotAvailable = false; snapshot = VirtualLocationSnapshot(); snapshot.error = "Location diagnostics unavailable — reconnect the VPN and retry" }
        } else { snapshot = VirtualLocationSnapshot(); snapshotAvailable = false; settings = VirtualLocationStore.load() }
    }
    private func authority(create: Bool) throws -> String {
        if let raw = try LocationAuthorityStore.load() {
            var error: NSError?; MobilelocationValidateAuthorityJSON(raw, &error)
            if let error { throw error }; return raw
        }
        guard create else { throw TunnelProfile.InvalidProfile("Install the location certificate first") }
        var error: NSError?
        let raw = MobilelocationGenerateAuthorityJSON(&error)
        if let error { throw error }
        try LocationAuthorityStore.save(raw); fingerprint = LocationAuthorityStore.fingerprint(raw); trustChecked = false
        return raw
    }
    private func server(create: Bool) throws -> MobilelocationCertificateServer {
        if let certificateServer { return certificateServer }
        var error: NSError?
        let raw = try authority(create: create)
        guard let server = MobilelocationNewCertificateServer(raw, &error) else { if let error { throw error }; throw TunnelProfile.InvalidProfile("Could not start certificate setup") }
        if let error { throw error }
        certificateServer = server; return server
    }
    func openAppSettings() {
        UIApplication.shared.open(URL(string: UIApplication.openSettingsURLString)!) { [weak self] opened in
            if !opened { Task { @MainActor in self?.message = "Open Settings manually, then follow the setup guide’s paths." } }
        }
    }
    func openSetupSettings(_ destination: LocationSetupSettingsDestination) {
        message = "In Settings, follow: \(destination.manualPath). iOS may open a different page even when the shortcut is accepted."
        UIApplication.shared.open(destination.url) { [weak self] opened in
            if !opened {
                Task { @MainActor in
                    self?.message = "Open \(destination.manualPath). The shortcut is unavailable on this device."
                    self?.openAppSettings()
                }
            }
        }
    }
    func installCertificate() {
        do {
            let server = try server(create: true)
            let profile = FileManager.default.temporaryDirectory.appendingPathComponent("sakamoto-location.mobileconfig")
            try Data(server.profile().utf8).write(to: profile, options: .atomic)
            certificateProfileURL = profile
            guard let url = URL(string: server.downloadURL()) else { return }
            if backgroundTask == .invalid {
                backgroundTask = UIApplication.shared.beginBackgroundTask(withName: "Location certificate download") { [weak self] in
                    Task { @MainActor in self?.closeCertificateServer() }
                }
            }
            certificateTimeout?.cancel()
            certificateTimeout = Task { [weak self] in
                try? await Task.sleep(nanoseconds: 30_000_000_000)
                guard !Task.isCancelled else { return }; self?.closeCertificateServer()
            }
            UIApplication.shared.open(url)
            message = "In Settings, install the downloaded profile. Then open General → About → Certificate Trust Settings and enable full trust for Sakamoto Location CA. Return here to check trust."
        } catch { message = error.localizedDescription }
    }
    func checkTrust() async {
        guard !busy else { return }; busy = true; defer { busy = false }
        do {
            let server = try server(create: false)
            guard let url = URL(string: server.probeURL()) else { return }
            let configuration = URLSessionConfiguration.ephemeral
            configuration.connectionProxyDictionary = ["HTTPEnable": 0, "HTTPSEnable": 0]
            configuration.timeoutIntervalForRequest = 8; configuration.timeoutIntervalForResource = 8
            let session = URLSession(configuration: configuration); defer { session.invalidateAndCancel() }
            let (data, response) = try await session.data(from: url)
            guard (response as? HTTPURLResponse)?.statusCode == 200, String(decoding: data, as: UTF8.self) == server.token() else { throw TunnelProfile.InvalidProfile("Certificate trust check returned an unexpected result") }
            let raw = try authority(create: false)
            fingerprint = LocationAuthorityStore.fingerprint(raw); LocationAuthorityStore.markTrusted(fingerprint)
            trustChecked = true; message = "Certificate trust verified by iOS. You can enable simulation."
        } catch {
            trustChecked = false; LocationAuthorityStore.markTrusted("")
            message = "Certificate trust was not verified. Install the profile and enable full trust in General → About → Certificate Trust Settings."
        }
        closeCertificateServer()
    }
    func closeCertificateServer() {
        certificateTimeout?.cancel(); certificateTimeout = nil
        try? certificateServer?.close(); certificateServer = nil
        if backgroundTask != .invalid { UIApplication.shared.endBackgroundTask(backgroundTask); backgroundTask = .invalid }
    }
    var mapPoint: LocationWaypoint {
        if let lat = Double(latitude.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(of: ",", with: ".")), let lon = Double(longitude.trimmingCharacters(in: .whitespacesAndNewlines).replacingOccurrences(of: ",", with: ".")), lat.isFinite, lon.isFinite, (-90...90).contains(lat), (-180...180).contains(lon) { return LocationWaypoint(latitude: lat, longitude: lon) }
        return LocationWaypoint(latitude: settings.latitude, longitude: settings.longitude)
    }
    var mapRoutePoints: [LocationWaypoint] { (try? VirtualLocationRoute.parse(routeText, duration: "300", interval: "1").points) ?? [] }
    func selectMapPosition(_ point: LocationWaypoint) {
        latitude = String(point.latitude); longitude = String(point.longitude)
        message = "Location selected. Apply location to use this point."
    }
    func selectMapRoute(_ points: [LocationWaypoint]) {
        routeText = points.map { "\($0.latitude), \($0.longitude)" }.joined(separator: "\n")
        if let route = try? VirtualLocationRoute.parse(routeText, duration: routeDuration, interval: routeInterval) { try? VirtualLocationStore.saveRouteDraft(route) }
        message = "Route selected. Start route when you are ready to move."
    }
    func select(_ point: SavedLocation) {
        latitude = String(point.latitude); longitude = String(point.longitude)
        message = "Selected \(point.name). Apply coordinates to use this position."
    }
    func saveFavorite(name: String) {
        do {
            let value = try proposed(enabled: false)
            let point = SavedLocation(name: name, latitude: value.latitude, longitude: value.longitude)
            guard !point.name.isEmpty else { throw TunnelProfile.InvalidProfile("Give this location a name") }
            favorites.removeAll { $0.name == point.name }
            favorites.insert(point, at: 0); favorites = Array(favorites.prefix(20))
            try VirtualLocationStore.saveFavorites(favorites)
        } catch { message = error.localizedDescription }
    }
    func deleteFavorites(_ offsets: IndexSet) { favorites.remove(atOffsets: offsets); try? VirtualLocationStore.saveFavorites(favorites) }
    func observeLocation(restoringRealLocation: Bool = false) {
        guard locationServicesEnabled != false else {
            observed = "Fresh measurement paused: Location Services is off. Enable it in Settings → Privacy & Security → Location Services when you want to measure a new system location."
            return
        }
        guard !observing else { return }; observing = true; observed = nil
        observingRealLocation = restoringRealLocation
        observationTimeout?.cancel()
        observationTimeout = Task { [weak self] in
            try? await Task.sleep(nanoseconds: 10_000_000_000)
            guard !Task.isCancelled else { return }; self?.cancelObservation(); self?.observed = "No fresh location within 10 seconds. Check Location Services and try again."
        }
        switch locationManager.authorizationStatus {
        case .notDetermined: locationManager.requestWhenInUseAuthorization()
        case .authorizedAlways, .authorizedWhenInUse: locationManager.requestLocation()
        default: cancelObservation(); observed = "Allow Location access for Sakamoto in Settings to measure the reported position."
        }
    }
    func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        Task { await refreshLocationServices() }
        if observing, manager.authorizationStatus == .authorizedAlways || manager.authorizationStatus == .authorizedWhenInUse { manager.requestLocation() }
    }
    func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard observing, let fix = locations.last, abs(fix.timestamp.timeIntervalSinceNow) < 30, fix.horizontalAccuracy >= 0 else { return }
        let restoring = observingRealLocation
        cancelObservation()
        let target = CLLocation(latitude: snapshot.latitude ?? settings.latitude, longitude: snapshot.longitude ?? settings.longitude)
        let distance = fix.distance(from: target)
        let point = String(format: "%.6f, %.6f · ±%.0f m", fix.coordinate.latitude, fix.coordinate.longitude, fix.horizontalAccuracy)
        observed = point + "\n" + (restoring ? "Fresh system location received. Compare these coordinates with your real surroundings to confirm restoration." : distance <= max(100, Double(settings.accuracy) * 3) ? "Reported position is near the selected point." : "Reported position differs by \(Int(distance)) m. GPS or cached positioning may take precedence.")
    }
    func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) { cancelObservation(); observed = "Could not obtain a fresh location. Check Location Services and retry." }
    func cancelObservation() { observationTimeout?.cancel(); observationTimeout = nil; locationManager.stopUpdatingLocation(); observing = false; observingRealLocation = false }
    var routeSummary: String {
        guard let route = snapshot.route else { return "No route running" }
        return "\(snapshot.routeState ?? "Unknown") · \(Int(snapshot.routeElapsed ?? 0)) / \(route.durationSeconds) s · \(route.points.count) points · \(String(format: "%.1f", route.distance / Double(route.durationSeconds) * 3.6)) km/h"
    }
    var report: String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "unknown"
        return "Sakamoto \(version) · iOS \(UIDevice.current.systemVersion)\nNetwork location diagnostic · \(Date().ISO8601Format())\nSelected WGS84: \(settings.latitude), \(settings.longitude); accuracy: \(settings.accuracy) m\nVPN running: \(vpnRunning); requested: \(settings.enabled); proxy enabled: \(snapshot.enabled)\nSystem Location Services: \(locationServicesEnabled.map { $0 ? "on" : "off" } ?? "not checked")\nCA trust checked: \(trustChecked); fingerprint: \(fingerprint)\nTLS connections: \(snapshot.connections); requests: \(snapshot.requests); rewritten: \(snapshot.patched); passed through: \(snapshot.skipped); TLS failures: \(snapshot.tlsFailures)\nLast rewrite: \(snapshot.lastPatch > 0 ? Date(timeIntervalSince1970: Double(snapshot.lastPatch)).ISO8601Format() : "none")\nDetail: \(snapshot.error)\nRoute: \(routeSummary); current WGS84: \(snapshot.latitude ?? settings.latitude), \(snapshot.longitude ?? settings.longitude)\nObserved: \(observed ?? "not measured")"
    }
}

struct VirtualLocationView: View {
    @StateObject private var model: VirtualLocationModel
    @State private var namingFavorite = false
    @State private var favoriteName = ""
    @State private var mapMode: MapMode?
    @State private var showGuide = false
    @AppStorage("sakamoto.location.setupWizardSeen") private var guideSeen = false
    @AppStorage("sakamoto.location.guideFlow") private var storedFlow = LocationGuideFlow.setup.rawValue
    @AppStorage("sakamoto.location.networkGuideStep") private var networkStep = 0
    @Environment(\.scenePhase) private var scenePhase
    private enum MapMode: String, Identifiable { case position, route; var id: String { rawValue } }
    init(tunnel: TunnelControlling, supportsSimulation: Bool = VirtualLocationStore.supportsNetworkSimulation) {
        _model = StateObject(wrappedValue: VirtualLocationModel(tunnel: tunnel, supportsSimulation: supportsSimulation))
    }
    var body: some View {
        ScrollViewReader { proxy in
        List {
            Section("Setup guide") {
                Button("Continue guide") { showGuide = true }
                Text("Follow certificate setup, activation, or real-location restoration one page at a time. Your current guide and page are saved.")
                    .font(.footnote).foregroundStyle(.secondary)
                Button("Begin activation steps") { networkStep = 0; storedFlow = LocationGuideFlow.activation.rawValue; showGuide = true }
                    .disabled(!model.supportsSimulation)
                Button("Restore real location") { Task { await model.stopForRestoration() } }.disabled(model.busy)
                if model.locationServicesEnabled == false {
                    Label("Location Services is off. Fresh system-location measurements are paused.", systemImage: "location.slash")
                    Button("Try opening Location Services") { model.openSetupSettings(.locationServices) }
                    Text(LocationSetupSettingsDestination.locationServices.manualPath).font(.footnote).foregroundStyle(.secondary)
                }
            }
            Section {
                Toggle("Simulate network location", isOn: Binding(get: { model.settings.enabled }, set: { enabled in Task { if enabled { await model.apply(enabled: true) } else { await model.stopForRestoration() } } }))
                    .disabled(model.busy || !model.supportsSimulation)
                LabeledContent("Status", value: model.vpnRunning && !model.snapshotAvailable ? "Unknown" : model.snapshot.enabled ? "Proxy enabled — verify position" : model.settings.enabled ? (model.vpnRunning ? "Not active" : "Waiting for VPN") : "Off")
                if !model.supportsSimulation { Text("Network-location simulation is unavailable on iOS 27 and later.").font(.footnote).foregroundStyle(.secondary) }
                if model.busy { ProgressView("Updating…") }
                if let message = model.message { Text(message).font(.footnote).foregroundStyle(.secondary).textSelection(.enabled) }
            } footer: {
                Text("Requires Sakamoto VPN and a trusted device certificate. Changes Wi-Fi and cellular network-positioning responses; GPS and cached fixes can still take precedence. Turning off restores normal responses.")
            }
            Section("Position") {
                LocationNativeMap(points: model.snapshot.route?.points ?? [model.mapPoint], current: model.snapshot.route == nil ? nil : livePoint, initialCenter: model.mapPoint)
                    .frame(height: 220).listRowInsets(EdgeInsets())
                    .allowsHitTesting(false).accessibilityLabel("Selected location and active route preview")
                Button { mapMode = .position } label: { Label("Choose on map", systemImage: "map") }
                Text(String(format: "Selected: %.6f, %.6f", model.mapPoint.latitude, model.mapPoint.longitude)).font(.caption).foregroundStyle(.secondary)
                coordinateField("Accuracy (meters)", text: $model.accuracy)
                DisclosureGroup("Advanced coordinates · WGS84") {
                    coordinateField("Latitude", text: $model.latitude)
                    coordinateField("Longitude", text: $model.longitude)
                }
                Button(model.settings.enabled ? "Apply location" : "Save location") { Task { await model.apply(enabled: model.settings.enabled) } }.disabled(model.busy)
                Button("Save favorite") { favoriteName = ""; namingFavorite = true }
            }.id("position")
            Section {
                Button { openRouteMap() } label: { Label(model.routeText.isEmpty ? "Create route on map" : "Edit route on map", systemImage: "point.topleft.down.to.point.bottomright.curvepath") }
                if !model.mapRoutePoints.isEmpty { Text("\(model.mapRoutePoints.count) stops · \(String(format: "%.2f", zip(model.mapRoutePoints, model.mapRoutePoints.dropFirst()).reduce(0) { $0 + $1.0.distance(to: $1.1) } / 1000)) km").font(.footnote).foregroundStyle(.secondary) }
                DisclosureGroup("Advanced route coordinates · WGS84") {
                    TextField("Route points", text: $model.routeText, axis: .vertical)
                        .lineLimit(3...8).autocorrectionDisabled().textInputAutocapitalization(.never)
                        .accessibilityLabel("Route points, one latitude and longitude pair per line")
                }
                coordinateField("Duration (seconds)", text: $model.routeDuration)
                coordinateField("Update interval (seconds)", text: $model.routeInterval)
                Text(model.routeSummary).font(.footnote).foregroundStyle(.secondary)
                if let route = model.snapshot.route {
                    ProgressView(value: min(1, (model.snapshot.routeElapsed ?? 0) / Double(route.durationSeconds)))
                    if let lat = model.snapshot.latitude, let lon = model.snapshot.longitude { Text(String(format: "Current: %.6f, %.6f", lat, lon)).font(.caption).textSelection(.enabled) }
                    Button(model.snapshot.routeState == "Paused" ? "Resume route" : "Pause route") { Task { await model.pauseRoute(model.snapshot.routeState != "Paused") } }
                        .disabled(model.busy || !["Moving", "Paused"].contains(model.snapshot.routeState ?? ""))
                    Button("Stop route and hold position") { Task { await model.holdRoute() } }.disabled(model.busy)
                }
                Button(model.snapshot.route == nil ? "Start route" : "Restart with edited route") { Task { await model.startRoute() } }
                    .disabled(model.busy || !model.vpnRunning || !model.trustChecked || !model.supportsSimulation)
            } header: { Text("Moving route") } footer: {
                Text("Choose 2 to 32 stops on the map in travel order. Moves between stops at constant speed for the total duration; add stops to follow road bends. Stops at the last point. Routes do not resume after VPN reconnect.")
            }.id("route")
            if !model.favorites.isEmpty {
                Section("Favorites") {
                    ForEach(model.favorites) { point in Button { model.select(point) } label: { VStack(alignment: .leading) { Text(point.name); Text(String(format: "%.6f, %.6f", point.latitude, point.longitude)).font(.caption).foregroundStyle(.secondary) } } }
                        .onDelete(perform: model.deleteFavorites)
                }
            }
            Section {
                LabeledContent("Trust", value: model.trustChecked ? "Verified" : "Needs setup")
                Button("Install location certificate") { model.installCertificate() }.disabled(model.busy)
                if let profile = model.certificateProfileURL { ShareLink("Export certificate profile", item: profile) }
                Button("Check certificate trust") { Task { await model.checkTrust() } }.disabled(model.busy)
                if !model.fingerprint.isEmpty { Text(model.fingerprint).font(.caption).foregroundStyle(.secondary).textSelection(.enabled).accessibilityLabel("Certificate SHA-256 fingerprint \(model.fingerprint)") }
            } header: { Text("Certificate setup") } footer: { Text("Install the profile in Settings, then enable full trust in General → About → Certificate Trust Settings. This CA belongs to this device; only its public certificate is exported.") }
            Section {
                LabeledContent("TLS connections", value: String(model.snapshot.connections))
                LabeledContent("Location requests", value: String(model.snapshot.requests))
                LabeledContent("Rewritten responses", value: String(model.snapshot.patched))
                LabeledContent("Passed through unchanged", value: String(model.snapshot.skipped))
                if model.snapshot.lastPatch > 0 { LabeledContent("Last rewrite", value: Date(timeIntervalSince1970: Double(model.snapshot.lastPatch)).formatted(date: .abbreviated, time: .standard)) }
                if model.snapshot.tlsFailures > 0 { LabeledContent("TLS failures", value: String(model.snapshot.tlsFailures)) }
                if !model.snapshot.error.isEmpty { Text(model.snapshot.error).font(.footnote).foregroundStyle(.secondary) }
                Button("Refresh proxy diagnostics") { Task { await model.refresh() } }
                Button("Check reported location") { model.observeLocation() }.disabled(model.observing)
                Button("Open Sakamoto location permissions") { model.openAppSettings() }
                if model.observing { ProgressView("Waiting for a fresh location…") }
                if let observed = model.observed { Text(observed).font(.footnote).textSelection(.enabled) }
                ShareLink("Share diagnostic report", item: model.report)
            } header: { Text("Diagnostics") } footer: { Text("A rewritten response proves interception. Check the reported position separately. Shared reports include your selected coordinates.") }
                .id("diagnostics")
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Virtual location")
        .navigationBarTitleDisplayMode(.inline)
        .onAppear { if !guideSeen { guideSeen = true; showGuide = true } }
        .onChange(of: model.restorationGuideRequested) { requested in
            if requested { showGuide = true; model.restorationGuideRequested = false }
        }
        .fullScreenCover(isPresented: $showGuide) {
            LocationSetupGuide(model: model, showRoute: { jump(to: "route", using: proxy) })
        }
        .sheet(item: $mapMode) { mode in
            LocationMapPicker(isRoute: mode == .route, points: mode == .route ? model.mapRoutePoints : [model.mapPoint], center: model.mapPoint, current: livePoint) { points in
                if mode == .route { model.selectMapRoute(points) }
                else if let point = points.first { model.selectMapPosition(point) }
            }.presentationDetents([.large])
        }
        .alert("Save favorite", isPresented: $namingFavorite) {
            TextField("Location name", text: $favoriteName)
            Button("Save") { model.saveFavorite(name: favoriteName) }
            Button("Cancel", role: .cancel) {}
        }
        .task(id: model.settings.route?.id) {
            repeat {
                await model.refresh()
                guard model.snapshot.route != nil else { return }
                do { try await Task.sleep(nanoseconds: 2_000_000_000) } catch { return }
            } while !Task.isCancelled
        }
        .onChange(of: scenePhase) { phase in
            if phase == .active { Task { await model.refresh() } }
        }
        .onDisappear { model.cancelObservation(); model.closeCertificateServer() }
        }
    }
    private func jump(to section: String, using proxy: ScrollViewProxy) {
        showGuide = false
        Task { @MainActor in
            await Task.yield()
            withAnimation { proxy.scrollTo(section, anchor: .top) }
        }
    }
    private var livePoint: LocationWaypoint? {
        guard model.snapshot.enabled, let lat = model.snapshot.latitude, let lon = model.snapshot.longitude else { return nil }
        return LocationWaypoint(latitude: lat, longitude: lon)
    }
    private func openRouteMap() {
        if !model.routeText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, model.mapRoutePoints.isEmpty {
            model.message = "The edited route contains invalid coordinates. Correct or clear Advanced route coordinates before opening the map."
            return
        }
        mapMode = .route
    }
    private func coordinateField(_ label: String, text: Binding<String>) -> some View {
        HStack { Text(label); TextField(label, text: text).multilineTextAlignment(.trailing).keyboardType(.numbersAndPunctuation).autocorrectionDisabled().textInputAutocapitalization(.never).accessibilityLabel(label) }
    }
}
