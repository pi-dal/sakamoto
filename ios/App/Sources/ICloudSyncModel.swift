import Foundation
import SwiftUI
import Mobilecore
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

    private let store: ConfigStore
    private let syncStore: ICloudSyncStore
    private let defaults: UserDefaults

    init(store: ConfigStore,
         defaults: UserDefaults = .standard,
         container: ICloudContainer = UbiquityICloudContainer(),
         clock: ICloudSyncClock = ICloudSystemClock()) {
        self.store = store
        self.defaults = defaults
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
        var entries: [ICloudSyncPayload.Entry] = []
        var skipped: [String] = []

        let nodesText = store.nodesSources.nodesText
        if nodesText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            if settings.additionalSourcePaths.contains("nodes.txt") { skipped.append("nodes.txt") }
        } else {
            entries.append(.init(name: "nodes.txt", data: Data(nodesText.utf8)))
        }

        if let policyData = try? store.policy.encoded(), !store.policy.rules.isEmpty {
            entries.append(.init(name: "policy.json", data: policyData))
        } else if settings.additionalSourcePaths.contains("policy.json") {
            skipped.append("policy.json")
        }

        if !store.nodesSources.subscriptions.isEmpty,
           let data = try? JSONEncoder().encode(store.nodesSources.subscriptions) {
            entries.append(.init(name: "subscriptions.json", data: data))
        } else if settings.additionalSourcePaths.contains("subscriptions.json") {
            skipped.append("subscriptions.json")
        }

        // Rule conf + relative includes (consent-gated). iOS keeps only the
        // imported main conf; remote HTTP(S) includes stay importer-managed
        // and their URLs are NOT uploaded (docs/icloud.md). If the imported
        // conf references local relative includes, they are reported as
        // pending so the user knows the cloud copy is not self-contained.
        if settings.includesConf, let imported = store.importedSource,
           !Self.isRemoteURL(imported.displaySource) {
            let baseName = (imported.displaySource as NSString).lastPathComponent
            if baseName.lowercased().hasSuffix(".conf") {
                entries.append(.init(name: "conf/" + baseName, data: Data(imported.content.utf8)))
            } else {
                skipped.append("conf (imported source has no .conf name)")
            }
            if let content = imported.content.data(using: .utf8),
               let includes = try? ICloudSyncConf.localIncludes(ofConfContent: content),
               !includes.isEmpty {
                skipped.append("conf includes pending on this device: " + includes.joined(separator: ", "))
            }
        }

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
        syncing = true
        notice = nil
        Task {
            defer { syncing = false }
            let (payload, skipped) = buildPayload()
            let outcome = await syncStore.syncNow(staging: payload.entries)
            let effectiveSkipped = skipped + outcome.skipped
            adopt(outcome.downloads)
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
    private func adopt(_ downloads: [ICloudSyncOutcome.Download]) {
        for download in downloads {
            switch download.name {
            case "nodes.txt":
                let links = String(decoding: download.data, as: UTF8.self)
                    .split(whereSeparator: { $0.isNewline })
                    .map { $0.trimmingCharacters(in: .whitespaces) }
                    .filter { !$0.isEmpty }
                var book = NodesSourcesBook()
                for link in links {
                    var bridgeError: NSError?
                    guard let info = MobilecoreParseShareLink(link, &bridgeError) else { continue }
                    book = book.adding(node: StagedNode(
                        rawLink: link,
                        tag: info.tag,
                        type: info.type,
                        server: info.server,
                    ))
                }
                // Synced nodes are authoritative for the nodes half;
                // subscription metadata stays device-local unless its own
                // file downloads below.
                let next = NodesSourcesBook(nodes: book.nodes, subscriptions: store.nodesSources.subscriptions)
                store.commitNodesSources(next)

            case "policy.json":
                if let book = PolicyBook.decode(download.data) {
                    store.commitPolicy(book)
                }

            case "subscriptions.json":
                if let list = try? JSONDecoder().decode([StagedSubscription].self, from: download.data) {
                    let next = NodesSourcesBook(nodes: store.nodesSources.nodes, subscriptions: list)
                    store.commitNodesSources(next)
                }

            case let name where name.hasPrefix("conf/") && name.hasSuffix(".conf"):
                let content = String(decoding: download.data, as: UTF8.self)
                var bridgeError: NSError?
                _ = MobilecoreParseConfContentJSON(content, &bridgeError)
                guard bridgeError == nil else {
                    notice = "synced conf \(name) failed validation; kept the current import"
                    continue
                }
                store.commitImport(ImportedSource(
                    displaySource: name,
                    content: content,
                    importedAt: Date(),
                ))

            default:
                continue
            }
        }
    }
}
