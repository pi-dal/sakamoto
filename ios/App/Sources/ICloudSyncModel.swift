import Foundation
import SwiftUI
import Libbox
import SakamotoKit

// App-side owner of the iCloud sync feature (Settings → Sync sources to
// iCloud). Composition layer only:
//
//   * settings state  — persisted in UserDefaults (sakamoto.icloud.sync
//                       settings) and mirrored into the SakamotoKit
//                       ICloudSyncStore actor;
//   * payload staging — built from the shared ConfigStore staging state
//                       (nodes.txt / policy.json / subscriptions.json /
//                       conf/<file>.conf); nothing here reads arbitrary disk
//                       paths, so the sync scope can never silently widen;
//   * adoption        — downloads are validated through the SAME Go bridge
//                       entry points the Config tab uses (ParseShareLink,
//                       ParseConfContentJSON) and committed through the
//                       store's existing commit* methods, so synced data can
//                       never bypass the validation every manual import
//                       goes through.
//
// NEVER synced (enforced twice: payload construction + the store's
// allowlist): generated sing-box config content, Tailscale auth key
// (Keychain-only), API secrets, .srs, logs, sockets, the baseline state.

@MainActor
final class ICloudSyncModel: ObservableObject {

    static let settingsKey = "sakamoto.icloud.sync.settings"

    @Published private(set) var settings: ICloudSyncSettings
    @Published private(set) var status: ICloudSyncStatus = .disabled
    @Published private(set) var syncing = false
    @Published private(set) var notice: String?
    /// Additional source paths as an editable single string (one per line).
    @Published var additionalPathsDraft: String = ""
    @Published private(set) var directoryLabel = "App iCloud container / Documents / sakamoto"
    private static let bookmarkKey = "sakamoto.icloud.sync.directory.bookmark"
    private var selectedDirectory: URL?

    private let store: ConfigStore
    private let syncStore: ICloudSyncStore
    private let defaults: UserDefaults

    init(store: ConfigStore,
         defaults: UserDefaults = .standard,
         container: ICloudContainer = UbiquityICloudContainer(),
         clock: ICloudSyncClock = ICloudSystemClock()) {
        self.store = store
        self.defaults = defaults
        if let bookmark = defaults.data(forKey: Self.bookmarkKey) {
            var stale = false
            if let url = try? URL(resolvingBookmarkData: bookmark, options: [], relativeTo: nil, bookmarkDataIsStale: &stale), !stale {
                selectedDirectory = url
                directoryLabel = "Selected folder: \(url.lastPathComponent)"
            } else {
                directoryLabel = "Selected folder needs authorization again"
            }
        }
        let restored: ICloudSyncSettings
        if let data = defaults.data(forKey: Self.settingsKey),
           let decoded = ICloudSyncSettings.decode(data) {
            restored = decoded
        } else {
            restored = ICloudSyncSettings()
        }
        let localDirectory = Self.makeLocalDirectory(defaults: defaults)
        self.settings = restored
        self.additionalPathsDraft = restored.additionalSourcePaths.joined(separator: "\n")
        self.syncStore = ICloudSyncStore(
            settings: restored,
            localFilesystem: LocalFilesystem(),
            cloudFilesystem: CloudFilesystem(),
            container: container,
            clock: clock,
            localDirectory: localDirectory,
        )
    }

    /// App-support directory for the baseline state + local source staging
    /// (the iOS analogue of the macOS runtime dir's icloud-state.json).
    static func makeLocalDirectory(defaults: UserDefaults) -> String {
        if let override = defaults.string(forKey: "sakamoto.icloud.sync.localdir"),
           !override.isEmpty {
            return override
        }
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first
            ?? FileManager.default.temporaryDirectory
        let dir = base.appendingPathComponent("sakamoto-sync", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir.path
    }

    func selectDirectory(_ url: URL) {
        guard !syncing else { return }
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        do {
            let bookmark = try url.bookmarkData(options: .minimalBookmark, includingResourceValuesForKeys: nil, relativeTo: nil)
            defaults.set(bookmark, forKey: Self.bookmarkKey)
            selectedDirectory = url
            directoryLabel = "Selected folder: \(url.lastPathComponent)"
            notice = "Folder selected. Choose the same sakamoto folder as the TUI iCloud directory."
        } catch { notice = "Could not save access to this folder. Please select it again." }
    }

    func activate() {
        Task {
            try? await syncStore.updateSettings(settings)
            status = await syncStore.currentStatus()
        }
    }

    // MARK: Settings mutations (every consent change is explicit)

    /// The confirmation dialog action for "Sync sources to iCloud". The copy
    /// in SettingsView names what will be uploaded (node share links,
    /// subscription feed URLs, rule conf possibly containing proxy
    /// credentials) — this method is only reachable through it.
    func confirmEnable() {
        applySettings { settings in
            settings.enabled = true
            // Fresh enable: conf consent defaults to confirmed (docs:
            // include_conf "defaults to true for fresh configurations").
            // An upgraded config keeps nil until this same dialog confirms.
            if settings.includeConf == nil { settings.includeConf = true }
        }
    }

    func disable() {
        applySettings { $0.enabled = false }
    }

    func setIncludeConf(_ value: Bool) {
        applySettings { $0.includeConf = value }
    }

    func commitAdditionalPathsDraft() {
        let paths = additionalPathsDraft
            .split(whereSeparator: { $0 == "\n" || $0 == "," })
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        applySettings { $0.additionalSourcePaths = paths }
    }

    private func applySettings(_ transform: (inout ICloudSyncSettings) -> Void) {
        var next = settings
        transform(&next)
        settings = next
        if let data = try? next.encoded() {
            defaults.set(data, forKey: Self.settingsKey)
        }
        Task {
            do {
                try await syncStore.updateSettings(next)
                notice = nil
            } catch {
                notice = ICloudSyncStore.describe(error)
            }
            status = await syncStore.currentStatus()
        }
    }

    // MARK: Payload construction

    /// What this device currently offers to sync, built ONLY from the
    /// ConfigStore staging state. Empty payloads are skipped with a note —
    /// an empty device must not seed the cloud with empty files.
    func buildPayload() -> (payload: ICloudSyncPayload, skipped: [String]) {
        let bundle = store.sourceBundle
        let entries: [ICloudSyncPayload.Entry] = bundle.files.sorted(by: { $0.key < $1.key }).compactMap { name, body in
            if name.lowercased().hasSuffix(".conf") && !settings.includesConf { return nil }
            return .init(name: name, data: Data(body.utf8))
        }
        let skipped = settings.additionalSourcePaths.filter { bundle.files[$0] == nil }
        return (ICloudSyncPayload(entries: entries), skipped)
    }

    static func isRemoteURL(_ displaySource: String) -> Bool {
        let trimmed = displaySource.trimmingCharacters(in: .whitespaces)
        return trimmed.hasPrefix("http://") || trimmed.hasPrefix("https://")
    }

    // MARK: The pass

    func syncNow() {
        guard !syncing else { return }
        guard settings.enabled else {
            notice = "Sync is disabled — enable it first."
            return
        }
        store.ensureSourceProfile()
        syncing = true
        notice = nil
        Task {
            defer { syncing = false }
            let directory = selectedDirectory
            if defaults.data(forKey: Self.bookmarkKey) != nil && directory == nil {
                notice = "Please choose the TUI iCloud folder again to restore access."
                return
            }
            let scoped = directory?.startAccessingSecurityScopedResource() ?? false
            guard directory == nil || scoped else {
                notice = "Cannot access the selected folder. Please choose it again."
                return
            }
            defer { if scoped { directory?.stopAccessingSecurityScopedResource() } }
            let nodesBefore = store.nodesSources
            let policyBefore = store.policy
            let importBefore = store.importedSource
            let profileBefore = store.selectedProfileID
            let sourceBefore = store.selectedProfile?.sourceBundleJSON
            let (payload, skipped) = buildPayload()
            let outcome = await syncStore.syncNow(
                staging: payload.entries, directoryURL: directory,
                downloadNames: ["nodes.txt", "policy.json", "subscriptions.json"],
                sourceScope: profileBefore ?? "unassigned",
                mainConf: settings.includesConf ? store.sourceBundle.mainConf : "",
                discoverConf: settings.includesConf,
                validateDownload: { name, data in
                    switch name {
                    case "nodes.txt":
                        if !String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { _ = try NodeFileImport.parse(data, validate: ConfigModel.validateNode) }
                    case "policy.json", "subscriptions.json": try ConfigStore.validateStructuredSource(name: name, data: data)
                    default:
                        if name.lowercased().hasSuffix(".conf") {
                            var error: NSError?
                            _ = MobilecoreParseConfContentJSON(String(decoding: data, as: UTF8.self), &error)
                            if let error { throw error }
                        }
                    }
                },
                sourcesAreCurrent: { @MainActor [store] in
                    store.nodesSources == nodesBefore && store.policy == policyBefore && store.importedSource == importBefore && store.selectedProfileID == profileBefore && store.selectedProfile?.sourceBundleJSON == sourceBefore
                }
            )
            let effectiveSkipped = skipped.filter { name in !outcome.downloads.contains(where: { $0.name == name }) } + outcome.skipped
            guard store.nodesSources == nodesBefore, store.policy == policyBefore, store.importedSource == importBefore,
                  store.selectedProfileID == profileBefore, store.selectedProfile?.sourceBundleJSON == sourceBefore else {
                notice = "Local sources changed during sync. Retry without editing; downloads were not adopted."
                return
            }
            do { try adopt(outcome.downloads, discoveredMain: outcome.discoveredMainConf) }
            catch { notice = "Downloaded sources could not be adopted: " + TunnelDiagnostics.sanitized(error.localizedDescription); return }
            status = outcome.status
            if case .failed(_, let message) = outcome.status {
                notice = message
            } else if !effectiveSkipped.isEmpty {
                notice = "Skipped: " + effectiveSkipped.joined(separator: "; ")
            } else {
                notice = nil
            }
        }
    }

    /// Apply downloads through the exact validation path manual imports use.
    private func adopt(_ downloads: [ICloudSyncOutcome.Download], discoveredMain: String? = nil) throws {
        guard !downloads.isEmpty || discoveredMain != nil else { return }
        var bundle = store.sourceBundle
        if let discoveredMain { bundle.mainConf = discoveredMain }
        for download in downloads { bundle.files[download.name] = String(decoding: download.data, as: UTF8.self) }
        try store.adoptSourceBundle(bundle)
    }
}
