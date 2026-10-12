import SwiftUI
import Combine
import CoreLocation
import Libbox
import SakamotoKit

@MainActor
final class VirtualLocationModel: NSObject, ObservableObject, @preconcurrency CLLocationManagerDelegate {
    let tunnel: TunnelControlling
    @Published var settings = VirtualLocationStore.load()
    @Published var latitude = ""
    @Published var longitude = ""
    @Published var accuracy = ""
    @Published var favorites = VirtualLocationStore.favorites()
    @Published var snapshot = VirtualLocationSnapshot()
    @Published var vpnRunning = false
    @Published var snapshotAvailable = false
    @Published var busy = false
    @Published var trustChecked = false
    @Published var fingerprint = ""
    @Published var certificateProfileURL: URL?
    @Published var message: String?
    @Published var observed: String?
    @Published var observing = false
    private let locationManager = CLLocationManager()
    private var observationTimeout: Task<Void, Never>?
    private var certificateServer: MobilelocationCertificateServer?
    private var certificateTimeout: Task<Void, Never>?
    private var backgroundTask: UIBackgroundTaskIdentifier = .invalid

    init(tunnel: TunnelControlling) {
        self.tunnel = tunnel
        super.init()
        latitude = String(settings.latitude); longitude = String(settings.longitude); accuracy = String(settings.accuracy)
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
    func apply(enabled: Bool, useEditedCoordinates: Bool = true) async {
        guard !busy else { return }; busy = true; defer { busy = false }
        do {
            var value = settings
            if useEditedCoordinates { value = try proposed(enabled: enabled) }
            value.enabled = enabled
            if enabled {
                guard VirtualLocationStore.supportsNetworkSimulation else { throw TunnelProfile.InvalidProfile("This method supports iOS through version 26") }
                guard trustChecked else { throw TunnelProfile.InvalidProfile("Install the certificate, enable full trust, then Check certificate trust") }
            }
            let state = try await tunnel.status()
            vpnRunning = state == .running
            if state == .running {
                snapshot = try await tunnel.location(settings: value)
                snapshotAvailable = true
                message = enabled ? "Enabled. Request a new location in the target app, then check the evidence below." : "Simulation stopped. Apps may keep their last location until they request a fresh fix."
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
        } catch { snapshotAvailable = false; message = error.localizedDescription }
    }
    func refresh() async {
        vpnRunning = (try? await tunnel.status()) == .running
        if vpnRunning {
            do { snapshot = try await tunnel.location(settings: nil); snapshotAvailable = true }
            catch { snapshotAvailable = false; snapshot = VirtualLocationSnapshot(); snapshot.error = "Location diagnostics unavailable — reconnect the VPN and retry" }
        } else { snapshot = VirtualLocationSnapshot(); snapshotAvailable = false }
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
    func observeLocation() {
        guard !observing else { return }; observing = true; observed = nil
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
        if observing, manager.authorizationStatus == .authorizedAlways || manager.authorizationStatus == .authorizedWhenInUse { manager.requestLocation() }
    }
    func locationManager(_ manager: CLLocationManager, didUpdateLocations locations: [CLLocation]) {
        guard observing, let fix = locations.last, abs(fix.timestamp.timeIntervalSinceNow) < 30, fix.horizontalAccuracy >= 0 else { return }
        cancelObservation()
        let target = CLLocation(latitude: settings.latitude, longitude: settings.longitude)
        let distance = fix.distance(from: target)
        let point = String(format: "%.6f, %.6f · ±%.0f m", fix.coordinate.latitude, fix.coordinate.longitude, fix.horizontalAccuracy)
        observed = point + "\n" + (distance <= max(100, Double(settings.accuracy) * 3) ? "Reported position is near the selected point." : "Reported position differs by \(Int(distance)) m. GPS or cached positioning may take precedence.")
    }
    func locationManager(_ manager: CLLocationManager, didFailWithError error: Error) { cancelObservation(); observed = "Could not obtain a fresh location. Check Location Services and retry." }
    func cancelObservation() { observationTimeout?.cancel(); observationTimeout = nil; locationManager.stopUpdatingLocation(); observing = false }
    var report: String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "unknown"
        return "Sakamoto \(version) · iOS \(UIDevice.current.systemVersion)\nNetwork location diagnostic · \(Date().ISO8601Format())\nSelected WGS84: \(settings.latitude), \(settings.longitude); accuracy: \(settings.accuracy) m\nVPN running: \(vpnRunning); requested: \(settings.enabled); proxy enabled: \(snapshot.enabled)\nCA trust checked: \(trustChecked); fingerprint: \(fingerprint)\nTLS connections: \(snapshot.connections); requests: \(snapshot.requests); rewritten: \(snapshot.patched); passed through: \(snapshot.skipped); TLS failures: \(snapshot.tlsFailures)\nLast rewrite: \(snapshot.lastPatch > 0 ? Date(timeIntervalSince1970: Double(snapshot.lastPatch)).ISO8601Format() : "none")\nDetail: \(snapshot.error)\nObserved: \(observed ?? "not measured")"
    }
}

struct VirtualLocationView: View {
    @StateObject private var model: VirtualLocationModel
    @State private var namingFavorite = false
    @State private var favoriteName = ""
    init(tunnel: TunnelControlling) { _model = StateObject(wrappedValue: VirtualLocationModel(tunnel: tunnel)) }
    var body: some View {
        List {
            Section {
                Toggle("Simulate network location", isOn: Binding(get: { model.settings.enabled }, set: { enabled in Task { await model.apply(enabled: enabled, useEditedCoordinates: enabled) } }))
                    .disabled(model.busy || !VirtualLocationStore.supportsNetworkSimulation)
                LabeledContent("Status", value: model.vpnRunning && !model.snapshotAvailable ? "Unknown" : model.snapshot.enabled ? "Active" : model.settings.enabled ? (model.vpnRunning ? "Not active" : "Waiting for VPN") : "Off")
                if !VirtualLocationStore.supportsNetworkSimulation { Text("Network-location simulation is unavailable on iOS 27 and later.").font(.footnote).foregroundStyle(.secondary) }
                if model.busy { ProgressView("Updating…") }
                if let message = model.message { Text(message).font(.footnote).foregroundStyle(.secondary).textSelection(.enabled) }
            } footer: {
                Text("Requires Sakamoto VPN and a trusted device certificate. Changes Wi-Fi and cellular network-positioning responses; GPS and cached fixes can still take precedence. Turning off restores normal responses.")
            }
            Section("Position · WGS84") {
                coordinateField("Latitude", text: $model.latitude)
                coordinateField("Longitude", text: $model.longitude)
                coordinateField("Accuracy (meters)", text: $model.accuracy)
                Button(model.settings.enabled ? "Apply coordinates" : "Save coordinates") { Task { await model.apply(enabled: model.settings.enabled) } }.disabled(model.busy)
                Button("Save favorite") { favoriteName = ""; namingFavorite = true }
            }
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
                if model.observing { ProgressView("Waiting for a fresh location…") }
                if let observed = model.observed { Text(observed).font(.footnote).textSelection(.enabled) }
                ShareLink("Share diagnostic report", item: model.report)
            } header: { Text("Diagnostics") } footer: { Text("A rewritten response proves interception. Check the reported position separately. Shared reports include your selected coordinates.") }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Virtual location")
        .navigationBarTitleDisplayMode(.inline)
        .alert("Save favorite", isPresented: $namingFavorite) {
            TextField("Location name", text: $favoriteName)
            Button("Save") { model.saveFavorite(name: favoriteName) }
            Button("Cancel", role: .cancel) {}
        }
        .task { await model.refresh() }
        .onDisappear { model.cancelObservation(); model.closeCertificateServer() }
    }
    private func coordinateField(_ label: String, text: Binding<String>) -> some View {
        HStack { Text(label); TextField(label, text: text).multilineTextAlignment(.trailing).keyboardType(.numbersAndPunctuation).autocorrectionDisabled().textInputAutocapitalization(.never).accessibilityLabel(label) }
    }
}
