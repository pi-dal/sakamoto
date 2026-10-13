import SwiftUI

@MainActor
final class AppUpdateModel: ObservableObject {
    @Published var channel: AppUpdateChannel
    @Published private(set) var result: IOSUpdateResult?
    @Published private(set) var isChecking = false
    @Published private(set) var error: String?
    @Published private(set) var checkedAt: Date?
    @Published var openingNotice: String?
    let installedVersion: String
    let installedBuild: String
    private let service: IOSUpdateChecking
    private var task: Task<Void, Never>?
    private var generation = UUID()
    init(service: IOSUpdateChecking = IOSUpdateService(), bundle: Bundle = .main) {
        self.service = service
        installedVersion = bundle.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.0.0"
        installedBuild = bundle.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "—"
        // Development installs have no receipt. This app currently ships through TestFlight.
        channel = bundle.appStoreReceiptURL?.lastPathComponent == "receipt" ? .appStore : .testFlight
    }
    func check() {
        stop()
        result = nil; error = nil; checkedAt = nil; openingNotice = nil; isChecking = true
        let token = generation
        let selected = channel
        task = Task { [weak self, service] in
            do {
                let result = try await service.check(selected)
                try Task.checkCancellation()
                guard let self, token == self.generation else { return }
                self.result = result; self.checkedAt = Date(); self.isChecking = false
            } catch {
                guard let self, token == self.generation, !Task.isCancelled else { return }
                self.isChecking = false
                self.error = error is IOSUpdateService.UpdateError ? error.localizedDescription : "Could not reach the update source. Check your connection and try again."
            }
        }
    }
    func changeChannel() { stop(); result = nil; error = nil; checkedAt = nil; openingNotice = nil }
    func stop() { generation = UUID(); task?.cancel(); task = nil; isChecking = false }
    deinit { task?.cancel() }
}

struct AppUpdateView: View {
    @StateObject private var model: AppUpdateModel
    init() { _model = StateObject(wrappedValue: AppUpdateModel()) }
    init(model: AppUpdateModel) { _model = StateObject(wrappedValue: model) }
    var body: some View {
        List {
            Section("Installed version") {
                LabeledContent("Version", value: "\(model.installedVersion) (\(model.installedBuild))")
            }
            Section {
                Picker("Update channel", selection: $model.channel) {
                    ForEach(AppUpdateChannel.allCases) { Text($0.title).tag($0) }
                }
                if model.isChecking {
                    HStack(spacing: 12) { ProgressView(); Text("Checking \(model.channel.title)…") }
                        .accessibilityElement(children: .combine)
                } else if let error = model.error {
                    Label("Could not check for updates", systemImage: "exclamationmark.triangle")
                    Text(error).font(.callout).foregroundStyle(.secondary)
                } else if let result = model.result {
                    Text(status(result)).font(.headline).accessibilityAddTraits(.updatesFrequently)
                    if let version = result.version { LabeledContent(model.channel == .testFlight ? "Announced version" : "Store version", value: version) }
                    if let minimum = result.minimumOS { LabeledContent("Requires iOS", value: minimum) }
                    if let date = model.checkedAt { Text("Checked \(date.formatted(date: .omitted, time: .shortened))").font(.footnote).foregroundStyle(.secondary) }
                } else {
                    Text("Check for a newer \(model.channel.title) version.").foregroundStyle(.secondary)
                }
                Button(model.error == nil ? "Check for updates" : "Try again") { model.check() }
                    .disabled(model.isChecking)
                if model.isChecking { Button("Cancel", role: .cancel) { model.stop() } }
            } header: { Text("App updates") } footer: {
                Text(model.channel == .testFlight
                     ? "Checks the latest iOS version announced on GitHub. TestFlight shows the builds available to your Apple ID, including newer builds of the same version."
                     : "Checks the App Store in your device’s region. App Store installs and updates are managed by Apple.")
            }
            Section {
                Button(model.channel == .testFlight ? "Open TestFlight" : "Open App Store") { openStore() }
                    .disabled(model.channel == .appStore && model.result?.isPublished == false)
                if let notice = model.openingNotice { Text(notice).font(.callout).foregroundStyle(.secondary) }
                if model.channel == .testFlight {
                    Text("Existing testers: select sakamoto in TestFlight. New testers need an invitation before a build appears.")
                        .font(.callout).foregroundStyle(.secondary)
                }
                if let url = model.result?.releaseURL, model.channel == .testFlight { Link("View release on GitHub", destination: url) }
            }
            if let notes = model.result?.notes, !notes.isEmpty {
                Section("Release notes") { Text(notes).textSelection(.enabled) }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("App updates")
        .navigationBarTitleDisplayMode(.inline)
        .onChange(of: model.channel) { _ in model.changeChannel() }
        .onDisappear { model.stop() }
    }
    private func status(_ result: IOSUpdateResult) -> String {
        if !result.isPublished { return model.channel == .appStore ? "No App Store release is available in your region yet." : "No TestFlight release has been announced yet." }
        if result.isNewer(than: model.installedVersion) { return "A newer version is available" }
        return model.channel == .testFlight ? "You have the latest announced version" : "You have the latest App Store version"
    }
    private func openStore() {
        if model.channel == .appStore {
            UIApplication.shared.open(IOSUpdateService.appStoreURL) { opened in
                if !opened { model.openingNotice = "Could not open the App Store. Try again when it is available." }
            }
        } else {
            UIApplication.shared.open(URL(string: "itms-beta://")!) { opened in
                if !opened {
                    model.openingNotice = "Install TestFlight, then sign in with the Apple ID used for your tester invitation."
                    UIApplication.shared.open(IOSUpdateService.testFlightStoreURL) { opened in
                        if !opened { model.openingNotice = "Could not open TestFlight or the App Store. Try again when the store is available." }
                    }
                }
            }
        }
    }
}
