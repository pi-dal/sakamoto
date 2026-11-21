import Foundation
import Libbox
import SakamotoKit

// Single owner of the SAVED sing-box config content and its ConfigState
// machine. Before this store existed the app draft lived in ConfigModel
// while HomeModel kept its own copy of the same string — two copies drift,
// and Connect would inject from the stale one. Now Home, Config and
// Settings all read store.content and every saved change funnels through
// save(_:), which runs the bridge transitions (mobilecore
// .ConfigStateTransition): modified → NeedsRegenerate, provider reload →
// Clean. The state machine itself still lives in Go.
//
// Secrets: content here is the host-generated config WITHOUT the Tailscale
// auth key; the key is injected from the Keychain only in
// regenerateAndApply/connect at start time (TailscaleConfigInjection).
//
// Staged policy, nodes and imported confs belong to the selected profile.
// Generation reuses internal/gen through the combined Mobilegen bridge and
// validates immutable candidate snapshots with Libbox before committing.
// Raw links are masked in UI and never enter action messages.
@MainActor
final class ConfigStore: ObservableObject {
    static let storageKey = "sakamoto.config.content"
    static let policyKey = "sakamoto.config.policy"
    static let nodesSourcesKey = "sakamoto.config.nodessources"
    static let importKey = "sakamoto.config.importsource"
    static let profilesKey = "sakamoto.config.profiles"
    static let selectedProfileKey = "sakamoto.config.selectedprofile"
    @Published private(set) var profiles: [TunnelProfile] = []
    @Published private(set) var selectedProfileID: String?
    @Published private(set) var indexNeedsRecovery = false
    @Published private(set) var needsRecoveryChoice = false
    @Published private(set) var recoveryNotice: String?
    @Published private(set) var offersRecoveryImport = false
    private var recoveryInventory: [TunnelProfile] = []
    @Published private(set) var profileError: String?
    func reportProfileError(_ error: Error) { profileError = TunnelDiagnostics.sanitized(error.localizedDescription) }
    var selectedProfile: TunnelProfile? { profiles.first { $0.id == selectedProfileID } }
    @Published private(set) var applying = false
    @Published private(set) var generating = false
    var canConnect: Bool { !applying && !generating && selectedProfile != nil && !content.isEmpty }
    var generationUpgradeMessage: String? {
        guard let profile = selectedProfile, !profile.sourceDigest.isEmpty, profile.hostGeneration == nil else { return nil }
        return "Reimport a current host export from your Mac to preserve Shadowrocket nodes and TLS settings before generating."
    }
    var canGenerate: Bool { generationUpgradeMessage == nil && !generating && (!nodesSources.nodes.isEmpty || !nodesSources.subscriptions.isEmpty || selectedProfile?.hostGeneration?.nodes.isEmpty == false) }

    /// The last saved config — what Connect and Regenerate+Reconnect use.
    @Published private(set) var content: String
    @Published private(set) var configState: ConfigState = .clean
    @Published private(set) var lastAction: String?

    /// Staged policy rules (docs/tui.md Config → Policy semantics; validated
    /// through mobilecore before they reach this store).
    @Published private(set) var policy: PolicyBook
    /// Staged manual nodes and subscription metadata.
    @Published private(set) var nodesSources: NodesSourcesBook
    /// Last-known-good import. Only commitImport replaces it, and only after
    /// the Go bridge validated the content — a failed import never clobbers.
    @Published private(set) var importedSource: ImportedSource?

    // Source drafts can exist before the first profile. Keep them in protected,
    // backup-excluded files too. Preferences contain only a random directory ID.
    private static func draftDirectory(_ defaults: UserDefaults) throws -> URL {
        let key = "sakamoto.privateDraftDirectory"
        let id = defaults.string(forKey: key).flatMap(UUID.init(uuidString:)) ?? UUID()
        var root = try AppPaths.sharedDirectory().appendingPathComponent("PrivateDrafts", isDirectory: true)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        var attributes = URLResourceValues()
        attributes.isExcludedFromBackup = true
        try root.setResourceValues(attributes)
        let directory = root.appendingPathComponent(id.uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        defaults.set(id.uuidString, forKey: key)
        return directory
    }

    private static func readDraft(_ key: String, defaults: UserDefaults) -> Data? {
        if let legacy = defaults.data(forKey: key) {
            do {
                try writeDraft(legacy, key: key, defaults: defaults)
                defaults.removeObject(forKey: key)
            } catch { return legacy }
            return legacy
        }
        guard let directory = try? draftDirectory(defaults) else { return nil }
        return try? Data(contentsOf: directory.appendingPathComponent(key))
    }

    private static func writeDraft(_ data: Data?, key: String, defaults: UserDefaults) throws {
        let file = try draftDirectory(defaults).appendingPathComponent(key)
        if let data {
            try data.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        } else if FileManager.default.fileExists(atPath: file.path) {
            try FileManager.default.removeItem(at: file)
        }
    }

    private func migrateInstalledProfileDrafts() {
        for key in [Self.nodesSourcesKey, Self.importKey] {
            guard let legacy = persistence.data(forKey: key) else { continue }
            do {
                try Self.writeDraft(legacy, key: "legacy-" + key, defaults: persistence)
                persistence.removeObject(forKey: key)
            } catch { reportProfileError(error) }
        }
    }

    private func saveDraft(_ data: Data?, key: String) {
        do {
            try Self.writeDraft(data, key: key, defaults: persistence)
            if profiles.isEmpty { persistence.removeObject(forKey: key) }
        }
        catch { reportProfileError(error) }
    }

    private let persistence: UserDefaults
    let keyStore: TailscaleAuthKeyStoring
    private var pendingLegacyProfile: TunnelProfile?
    private var cachedSourceJSON: String?
    private var cachedSourceBundle: SourceBundle?

    init(persistence: UserDefaults = .standard, keyStore: TailscaleAuthKeyStoring) {
        self.persistence = persistence
        self.keyStore = keyStore
        self.content = persistence.string(forKey: Self.storageKey) ?? ""
        self.policy = PolicyBook()
        self.nodesSources = NodesSourcesBook()
        if let object = persistence.object(forKey: Self.profilesKey) {
            do {
                guard let data = object as? Data else { throw Self.indexError }
                profiles = try JSONDecoder().decode([TunnelProfile].self, from: data)
                guard Set(profiles.map(\.id)).count == profiles.count,
                      profiles.allSatisfy({ UUID(uuidString: $0.id) != nil }) else { throw Self.indexError }
            } catch {
                requireIndexRecovery()
                return
            }
        }
        let primaryIDs = profiles.map(\.id)
        let backupIDs = persistence.stringArray(forKey: Self.profilesKey + ".ids")
        let savedSelection = persistence.string(forKey: Self.selectedProfileKey)
        let missingSelection = savedSelection == nil && !primaryIDs.isEmpty
        if missingSelection || backupIDs.map({ $0 != primaryIDs }) == true || savedSelection.map({ !primaryIDs.contains($0) }) == true {
            requireIndexRecovery()
            return
        }
        let initialID = savedSelection
        for index in profiles.indices {
            let id = profiles[index].id
            do {
                let manifest = try readManifest(id: id)
                if id == initialID {
                    let sources = try decodeSources(manifest)
                    profiles[index] = try loadRuleFiles(manifest)
                    nodesSources = sources.nodes
                    policy = sources.policy
                    importedSource = sources.imported
                    selectedProfileID = id
                } else {
                    profiles[index] = manifest
                }
            } catch {
                if id == initialID {
                    requireIndexRecovery()
                    return
                }
            }
        }
        if profiles.isEmpty {
            if let data = persistence.data(forKey: Self.policyKey) { policy = PolicyBook.decode(data) ?? PolicyBook() }
            if let data = Self.readDraft(Self.nodesSourcesKey, defaults: persistence) { nodesSources = NodesSourcesBook.decode(data) ?? NodesSourcesBook() }
            if let data = Self.readDraft(Self.importKey, defaults: persistence) { importedSource = ImportedSource.decode(data) }
        } else {
            migrateInstalledProfileDrafts()
        }
        if profiles.isEmpty && !content.isEmpty {
            var legacy = TunnelProfile(name: "Previous configuration", config: content)
            let bundle = sourceBundle
            if !bundle.files.isEmpty {
                legacy.sourceBundleJSON = (try? JSONEncoder().encode(bundle)).map { String(decoding: $0, as: UTF8.self) }
            }
            legacy.adoptCheckedRouteSource()
            pendingLegacyProfile = legacy
            do { try finishLegacyMigration() }
            catch { reportProfileError(error); return }
        }
        if let selected = selectedProfile {
            content = selected.config
            do {
                if initialID == nil {
                    // The legacy migration created this selection after the manifest scan.
                    let sources = try decodeSources(selected)
                    nodesSources = sources.nodes
                    policy = sources.policy
                    importedSource = sources.imported
                }
                try TunnelProfile.validateRouteSource(selected.config, version: selected.routeSourceVersion)
                if initialID == selected.id, !selected.config.isEmpty { persistence.removeObject(forKey: Self.storageKey) }
            }
            catch { reportProfileError(error) }
            if selected.sourcesChanged { configState = .needsRegenerate }
            else if selected.pendingApply != false { configState = .needsReconnect }
        }
    }

    private func requireIndexRecovery() {
        indexNeedsRecovery = true
        content = ""
        UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(true, forKey: "sakamoto.profile.requiresApply")
        reportProfileError(Self.indexError)
    }

    private static var indexError: TunnelProfile.InvalidProfile {
        TunnelProfile.InvalidProfile("The configuration list could not be read. Open Config and choose Recover configuration list.")
    }

    func recoverProfileIndex() throws {
        guard indexNeedsRecovery else { return }
        struct Reference: Decodable { let id: String }
        let saved = persistence.data(forKey: Self.profilesKey)
        let primary = saved.flatMap { try? JSONDecoder().decode([Reference].self, from: $0) }?.map(\.id) ?? []
        let backup = persistence.stringArray(forKey: Self.profilesKey + ".ids") ?? []
        let selection = persistence.string(forKey: Self.selectedProfileKey)
        var seen = Set<String>()
        let indexedIDs = (backup + primary).filter { seen.insert($0).inserted }
        var candidates = indexedIDs
        if let selection, !seen.contains(selection), UUID(uuidString: selection) != nil {
            candidates.append(selection)
        }
        var inventory: [TunnelProfile] = []
        var recovered: [TunnelProfile] = []
        var missing: [String] = []
        var unreadable: [String] = []
        for id in candidates {
            guard UUID(uuidString: id) != nil else { continue }
            do {
                let manifest = try readManifest(id: id)
                inventory.append(manifest)
                do {
                    _ = try validatedSelection(manifest)
                    recovered.append(manifest)
                } catch {
                    unreadable.append(id)
                }
            } catch let error as CocoaError where error.code == .fileReadNoSuchFile || error.code == .fileNoSuchFile {
                if seen.contains(id) { missing.append(id) }
            } catch {
                // Retain the index entry so selecting it later retries the original file.
                inventory.append(TunnelProfile(id: id, name: "Unavailable configuration (\(id))", config: ""))
                unreadable.append(id)
            }
        }
        offersRecoveryImport = recovered.isEmpty
        if offersRecoveryImport {
            let entries = Dictionary(uniqueKeysWithValues: inventory.map { ($0.id, $0) })
            recoveryInventory = candidates.filter { UUID(uuidString: $0) != nil }.map { id in
                entries[id] ?? TunnelProfile(id: id, name: "Unavailable configuration (\(id))", config: "")
            }
            recoveryNotice = "Unlock the device and retry, or import a desktop .sakamoto package as a new configuration. Existing configuration files are kept."
            return
        }
        if missing.isEmpty, unreadable.isEmpty, let selection, seen.contains(selection) {
            try commitRecoveredSelection(selection, in: recovered)
            return
        }
        var notices: [String] = []
        if !missing.isEmpty {
            notices.append("Missing configuration files (\(missing.joined(separator: ", "))). To add a missing configuration again, import its .sakamoto package.")
        }
        if !unreadable.isEmpty {
            notices.append("Could not read configuration files (\(unreadable.joined(separator: ", "))). Their entries are kept. After unlocking the device or restoring a file from backup, select its entry to retry.")
        }
        if !notices.isEmpty { notices.append("Choose an available configuration to save the recovered list.") }
        recoveryNotice = notices.isEmpty ? nil : notices.joined(separator: " ")
        profiles = recovered
        recoveryInventory = inventory
        needsRecoveryChoice = true
    }

    private func commitRecoveredSelection(_ id: String, in manifests: [TunnelProfile]) throws {
        var recovered = manifests
        guard let index = recovered.firstIndex(where: { $0.id == id }) else { throw Self.indexError }
        var (selected, sources) = try validatedSelection(readManifest(id: id))
        if needsRecoveryChoice { selected.pendingApply = true }
        recovered[index] = selected
        try persistProfilesChecked(recovered, selection: selected.id, changedID: needsRecoveryChoice ? selected.id : nil)
        profiles = recovered
        selectedProfileID = selected.id
        content = selected.config
        nodesSources = sources.nodes
        importedSource = sources.imported
        policy = sources.policy
        configState = selected.sourcesChanged ? .needsRegenerate : (selected.pendingApply != false ? .needsReconnect : .clean)
        indexNeedsRecovery = false
        needsRecoveryChoice = false
        recoveryInventory = []
        recoveryNotice = nil
        profileError = nil
        migrateInstalledProfileDrafts()
    }

    // Full manifests live in App Group files; preferences store lightweight references.
    private func persistProfilesChecked(_ candidates: [TunnelProfile], selection: String?, changedID: String?) throws {
        let root = try AppPaths.sharedDirectory()
        var profilesDir = root.appendingPathComponent("Profiles", isDirectory: true)
        try FileManager.default.createDirectory(at: profilesDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        var attributes = URLResourceValues()
        attributes.isExcludedFromBackup = true
        try profilesDir.setResourceValues(attributes)
        var references: [TunnelProfile] = []
        var writes: [(URL, Data)] = []
        for profile in candidates {
            guard UUID(uuidString: profile.id) != nil else { throw TunnelProfile.InvalidProfile("Invalid configuration identifier") }
            var manifest = profile; manifest.files = [:]
            let file = root.appendingPathComponent("Profiles/\(profile.id)/profile.json")
            if profile.id == changedID { writes.append((file, try JSONEncoder().encode(manifest))) }
            manifest.config = ""; manifest.sourceBundleJSON = nil; manifest.nodeOverrides = nil; manifest.hostGeneration = nil
            references.append(manifest)
        }
        let encodedReferences = try JSONEncoder().encode(references)
        var stagedManifest: (temp: URL, target: URL)?
        defer { if let stagedManifest { try? FileManager.default.removeItem(at: stagedManifest.temp) } }
        for (file, data) in writes {
            let fm = FileManager.default
            let directory = file.deletingLastPathComponent()
            try fm.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            let staged = directory.appendingPathComponent(UUID().uuidString + ".tmp")
            stagedManifest = (staged, file)
            try data.write(to: staged, options: .completeFileProtectionUntilFirstUserAuthentication)
            try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: staged.path)
        }
        // A new index entry must always point to a complete manifest. Existing
        // manifests stay intact until their index update succeeds.
        let newEntry = changedID.map { id in !profiles.contains(where: { $0.id == id }) } ?? false
        func commitManifest() throws {
            if let stagedManifest, rename(stagedManifest.temp.path, stagedManifest.target.path) != 0 {
                throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO)
            }
        }
        if newEntry { try commitManifest() }
        let keys = [Self.profilesKey, Self.profilesKey + ".ids", Self.selectedProfileKey]
        let previous = keys.map { persistence.object(forKey: $0) }
        persistence.set(encodedReferences, forKey: Self.profilesKey)
        persistence.set(candidates.map(\.id), forKey: Self.profilesKey + ".ids")
        persistence.set(selection, forKey: Self.selectedProfileKey)
        do {
            guard persistence.synchronize() else {
                throw TunnelProfile.InvalidProfile("Could not save the configuration list. Check available storage and retry.")
            }
            if !newEntry { try commitManifest() }
        } catch {
            for (key, value) in zip(keys, previous) {
                if let value { persistence.set(value, forKey: key) } else { persistence.removeObject(forKey: key) }
            }
            guard persistence.synchronize() else {
                throw TunnelProfile.InvalidProfile("Could not confirm the saved configuration list. Check available storage and reopen Config to retry.")
            }
            if newEntry, let stagedManifest { try FileManager.default.removeItem(at: stagedManifest.target) }
            throw error
        }
        for profile in candidates {
            UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(profile.id != selection || profile.sourcesChanged || profile.pendingApply != false, forKey: "sakamoto.experiment.pause." + profile.id)
        }
        let selectedProfile = candidates.first { $0.id == selection }
        UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(
            selectedProfile == nil || selectedProfile?.sourcesChanged == true || selectedProfile?.pendingApply != false,
            forKey: "sakamoto.profile.requiresApply"
        )
    }

    private func finishLegacyMigration() throws {
        guard let legacy = pendingLegacyProfile else { return }
        try persistProfilesChecked([legacy], selection: legacy.id, changedID: legacy.id)
        profiles = [legacy]
        selectedProfileID = legacy.id
        content = legacy.config
        pendingLegacyProfile = nil
        persistence.removeObject(forKey: Self.storageKey)
    }

    private func publishSelected(_ candidate: TunnelProfile) throws {
        guard !indexNeedsRecovery else { throw Self.indexError }
        try finishLegacyMigration()
        var staged = profiles
        if let index = staged.firstIndex(where: { $0.id == candidate.id }) { staged[index] = candidate }
        else { staged.append(candidate) }
        try persistProfilesChecked(staged, selection: candidate.id, changedID: candidate.id)
        profiles = staged
        selectedProfileID = candidate.id
        content = candidate.config
    }

    func installProfile(_ profile: TunnelProfile, fromDesktopPackage: Bool = false) throws {
        let recovering = indexNeedsRecovery && offersRecoveryImport && fromDesktopPackage
        guard !indexNeedsRecovery || recovering else { throw Self.indexError }
        guard !profiles.contains(where: { $0.id == profile.id }) else { throw TunnelProfile.InvalidProfile("Profile already installed") }
        guard UUID(uuidString: profile.id) != nil, profile.ruleRevisionID == nil else {
            throw TunnelProfile.InvalidProfile("Invalid imported configuration identifier")
        }
        let sources = try decodeSources(profile)
        let fm = FileManager.default
        let parent = try AppPaths.sharedDirectory().appendingPathComponent("Profiles", isDirectory: true)
        try fm.createDirectory(at: parent, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let directory = parent.appendingPathComponent(profile.id, isDirectory: true)
        // Exclusive creation makes the entire directory safe to roll back, including partial rule writes.
        guard mkdir(directory.path, 0o700) == 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        var profile = profile
        do {
            _ = try prepare(profile)
            if let groups = sources.groups { profile.proxyGroups = AutomaticGroupSources.adopting(groups, edits: profile.proxyGroups ?? [], content: profile.config, overrides: profile.nodeOverrides ?? [:]) }
            profile.pendingApply = true
            if recovering {
                let restored = recoveryInventory + [profile]
                try persistProfilesChecked(restored, selection: profile.id, changedID: profile.id)
                profiles = restored
                selectedProfileID = profile.id
                content = profile.config
                indexNeedsRecovery = false
                offersRecoveryImport = false
                recoveryInventory = []
                recoveryNotice = nil
            } else {
                try publishSelected(profile)
            }
        } catch {
            try fm.removeItem(at: directory)
            throw error
        }
        releaseOtherRuleFiles(keeping: profile.id)
        profileError = nil
        adoptProfileSources(sources)
        transition(.modified)
    }

    func selectProfile(_ id: String) throws {
        if indexNeedsRecovery {
            guard needsRecoveryChoice, profiles.contains(where: { $0.id == id }) else { throw Self.indexError }
            try commitRecoveredSelection(id, in: recoveryInventory)
            return
        }
        guard id != selectedProfileID else { return }
        guard profiles.contains(where: { $0.id == id }) else { return }
        var (profile, sources) = try validatedSelection(readManifest(id: id))
        profile.pendingApply = true
        try publishSelected(profile)
        profileError = nil
        releaseOtherRuleFiles(keeping: id)
        adoptProfileSources(sources)
        transition(.modified)
    }

    private func releaseOtherRuleFiles(keeping id: String) {
        for index in profiles.indices where profiles[index].id != id { profiles[index].files = [:] }
    }

    private func readManifest(id: String) throws -> TunnelProfile {
        guard UUID(uuidString: id) != nil else { throw TunnelProfile.InvalidProfile("Invalid configuration identifier") }
        let url = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(id)/profile.json")
        var manifest = try JSONDecoder().decode(TunnelProfile.self, from: Data(contentsOf: url))
        guard manifest.id == id else { throw TunnelProfile.InvalidProfile("Configuration identifier does not match its manifest") }
        manifest.adoptCheckedRouteSource()
        return manifest
    }

    private func loadRuleFiles(_ profile: TunnelProfile) throws -> TunnelProfile {
        guard profile.files.isEmpty else { return profile }
        guard UUID(uuidString: profile.id) != nil,
              profile.ruleRevisionID == nil || UUID(uuidString: profile.ruleRevisionID!) != nil else {
            throw TunnelProfile.InvalidProfile("Invalid rule snapshot identifier")
        }
        let root = try AppPaths.sharedDirectory().appendingPathComponent(profile.ruleRevisionID.map { "RuleSnapshots/\($0)/rules" } ?? "Profiles/\(profile.id)/rules", isDirectory: true)
        guard FileManager.default.fileExists(atPath: root.path) else { return profile }
        var loaded = profile
        for url in try FileManager.default.contentsOfDirectory(at: root, includingPropertiesForKeys: nil) {
            let name = "rules/" + url.lastPathComponent
            if TunnelProfile.validFileName(name) { loaded.files[name] = try Data(contentsOf: url, options: .mappedIfSafe) }
        }
        return loaded
    }

    private struct ProfileSources {
        var nodes: NodesSourcesBook
        var policy: PolicyBook
        var imported: ImportedSource?
        var groups: [String: [String]]? = nil
    }

    private func validatedSelection(_ manifest: TunnelProfile) throws -> (TunnelProfile, ProfileSources) {
        let sources = try decodeSources(manifest)
        let profile = try loadRuleFiles(manifest)
        try TunnelProfile.validateRouteSource(profile.config, version: profile.routeSourceVersion)
        if !profile.config.isEmpty { _ = try prepare(profile, installRuleFiles: false) }
        return (profile, sources)
    }

    private func decodeSources(_ profile: TunnelProfile) throws -> ProfileSources {
        guard let raw = profile.sourceBundleJSON else {
            return ProfileSources(nodes: NodesSourcesBook(), policy: PolicyBook(), imported: nil)
        }
        let bundle = try JSONDecoder().decode(SourceBundle.self, from: Data(raw.utf8))
        guard bundle.version == 1, bundle.files.count <= 128,
              bundle.files.values.reduce(0, { $0 + $1.utf8.count }) <= ICloudSyncLimits.maxSourceBytes,
              bundle.files.keys.allSatisfy(ICloudSyncPaths.isValidSourceName) else {
            throw TunnelProfile.InvalidProfile("Invalid source snapshot")
        }
        try ICloudSyncPaths.validateConfGraph(main: bundle.mainConf, files: bundle.files.mapValues { Data($0.utf8) })
        let nodeText = bundle.files["nodes.txt"] ?? ""
        let nodes = try NodeFileImport.parse(Data(nodeText.utf8), allowEmpty: true, validate: ConfigModel.validateNode)
        let subscriptions = try JSONDecoder().decode([SyncSubscription].self, from: Data((bundle.files["subscriptions.json"] ?? "[]").utf8))
        let rules = try JSONDecoder().decode([SyncPolicy].self, from: Data((bundle.files["policy.json"] ?? "[]").utf8))
        try Self.validateStructuredSource(name: "subscriptions.json", data: Data((bundle.files["subscriptions.json"] ?? "[]").utf8))
        try Self.validateStructuredSource(name: "policy.json", data: Data((bundle.files["policy.json"] ?? "[]").utf8))
        var imported: ImportedSource?
        if !bundle.mainConf.isEmpty {
            guard let content = bundle.files[bundle.mainConf] else { throw TunnelProfile.InvalidProfile("Missing main source file") }
            var error: NSError?
            _ = MobilecoreParseConfContentJSON(content, &error)
            if let error { throw error }
            imported = ImportedSource(displaySource: bundle.mainConf, content: content)
        }
        let groups = try bundle.files[AutomaticGroupSources.fileName].map { try AutomaticGroupSources.decode(Data($0.utf8)) }
        return ProfileSources(nodes: NodesSourcesBook(nodes: nodes, subscriptions: subscriptions.map { StagedSubscription(name: $0.name, url: $0.url, format: $0.format) }), policy: PolicyBook(rules: rules.map { StagedPolicyRule(match: $0.match, action: $0.action) }), imported: imported, groups: groups)
    }

    nonisolated static func validateStructuredSource(name: String, data: Data) throws {
        if name == AutomaticGroupSources.fileName {
            _ = try AutomaticGroupSources.decode(data)
        } else if name == "policy.json" {
            let rows = try JSONDecoder().decode([SyncPolicy].self, from: data)
            for row in rows {
                var error: NSError?
                guard MobilecoreNormalizePolicyRule(row.match, row.action, &error) != nil, error == nil else {
                    throw TunnelProfile.InvalidProfile("Invalid policy rule in synced source")
                }
            }
        } else if name == "subscriptions.json" {
            let rows = try JSONDecoder().decode([SyncSubscription].self, from: data)
            for row in rows {
                var error: NSError?
                _ = MobilecoreValidateSourceURL(row.url, &error)
                guard error == nil, !row.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                      ["", "auto", "singbox", "clash", "base64"].contains(row.format) else {
                    throw TunnelProfile.InvalidProfile("Invalid subscription in synced source")
                }
            }
        }
    }

    private func adoptProfileSources(_ source: ProfileSources) {
        nodesSources = source.nodes; policy = source.policy; importedSource = source.imported
        saveDraft(try? nodesSources.encoded(), key: Self.nodesSourcesKey)
        persistence.set(try? policy.encoded(), forKey: Self.policyKey)
        saveDraft(try? importedSource?.encoded(), key: Self.importKey)
    }

    private func prepare(_ profile: TunnelProfile, installRuleFiles: Bool = true) throws -> String {
        guard UUID(uuidString: profile.id) != nil else { throw TunnelProfile.InvalidProfile("Invalid configuration identifier") }
        if let revision = profile.ruleRevisionID, UUID(uuidString: revision) == nil { throw TunnelProfile.InvalidProfile("Invalid rule snapshot identifier") }
        let root = try AppPaths.sharedDirectory().appendingPathComponent(profile.ruleRevisionID.map { "RuleSnapshots/\($0)" } ?? "Profiles/\(profile.id)", isDirectory: true)
        var runtimeProfile = profile
        runtimeProfile.config = try profile.proxyChain?.applying(to: profile.config) ?? profile.config
        var config = try protectServers(in: runtimeProfile.preparedConfig(ruleDirectory: root))
        do {
            let dns = profile.regionalDNS ?? [:]
            var error: NSError?
            config = MobilecoreApplyRegionalDNS(config, dns["domains"] ?? "", dns["resolver_ip"] ?? "", dns["tls_name"] ?? "", dns["relay"] ?? "", dns["relay_fingerprint"] ?? "", &error)
            if let error { throw error }
        }
        if installRuleFiles {
            try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            for (name, data) in profile.files {
                guard TunnelProfile.validFileName(name) else { throw TunnelProfile.InvalidProfile("Invalid rule file path") }
                let target = root.appendingPathComponent(name)
                try FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
                if (try? Data(contentsOf: target, options: .mappedIfSafe)) != data {
                    try data.write(to: target, options: .atomic)
                }
                try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: target.path)
            }
        }
        var error: NSError?
        _ = MobilecoreValidateConfigJSON(config, &error)
        if let error { throw error }
        _ = LibboxCheckConfig(config, &error)
        if let error { throw error }
        return config
    }

    var experimentSettings: ExperimentSettings {
        if let settings = selectedProfile?.experiment { return settings }
        var settings = ExperimentSettings()
        settings.mode = SettingsOverrides.experimentEnabled(in: content) ? .on : .off
        return settings
    }

    func setTailscaleEnabled(_ enabled: Bool, options: TailscaleEndpointOptions) throws {
        let empty = content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        let next: String
        if enabled {
            next = empty
                ? try TailscaleEndpointProvisioning.standaloneConfiguration(options: options)
                : try TailscaleEndpointProvisioning.enable(options: options, in: content)
        } else { next = try TailscaleEndpointProvisioning.disable(in: content) }
        var candidate = selectedProfile ?? TunnelProfile(name: "Tailscale", config: next)
        candidate.config = next
        if selectedProfile == nil {
            // Keep source ownership intact; enabling is a runtime edit.
            candidate.sourceBundleJSON = String(decoding: try JSONEncoder().encode(sourceBundle), as: UTF8.self)
            try installProfile(candidate)
        } else {
            _ = try prepare(candidate)
            candidate.pendingApply = true
            if empty && sourceBundle.files.isEmpty { candidate.sourcesChanged = false }
            try publishSelected(candidate)
            transition(.modified)
        }
    }

    func setForceTailscaleDERP(_ enabled: Bool) throws {
        guard !generating && !applying else { throw TunnelProfile.InvalidProfile("Wait for generation or Apply to finish before changing Tailscale transport.") }
        guard let index = profiles.firstIndex(where: { $0.id == selectedProfileID }),
              TailscaleEndpointProvisioning.isEnabled(in: content) else { throw TunnelProfile.InvalidProfile("Enable a Tailscale endpoint first.") }
        var candidate = profiles[index]
        candidate.forceTailscaleDERP = enabled ? true : nil
        candidate.pendingApply = true
        try publishSelected(candidate)
        transition(.modified)
        lastAction = "Tailscale transport saved. Apply & reconnect to use it."
    }

    private func protectServers(in content: String) throws -> String {
        var error: NSError?
        let protected = MobilecorePrepareTailscaleConnectivityJSON(content, &error)
        if let error { throw error }
        return protected
    }

    private func recordGroupSources(in profile: inout TunnelProfile) throws {
        let bundle = try AutomaticGroupSources.updating(sourceBundle, edits: profile.proxyGroups ?? [], content: profile.config, overrides: profile.nodeOverrides ?? [:])
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        profile.sourceBundleJSON = String(decoding: try encoder.encode(bundle), as: UTF8.self)
    }

    func saveRegionalDNS(_ dns: [String: String], expected: TunnelProfile?) throws {
        guard !generating && !applying, selectedProfile == expected,
              let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else {
            throw TunnelProfile.InvalidProfile("Configuration changed. Reopen Regional DNS.")
        }
        var candidate = profiles[index]
        if dns["domains", default: ""].isEmpty { candidate.regionalDNS = nil } else {
            var approved = dns
            var error: NSError?
            approved["relay_fingerprint"] = MobilecoreRegionalRelayFingerprint(candidate.config, dns["relay"] ?? "", &error)
            if let error { throw error }
            candidate.regionalDNS = approved
        }
        _ = try prepare(candidate)
        candidate.pendingApply = true
        try publishSelected(candidate)
        transition(.modified)
        lastAction = "DNS settings saved. Apply to use them on this device."
    }

    func saveProxyGroup(_ edit: ProxyGroupEdit, expectedProfile: String?, expectedContent: String) throws {
        guard !generating && !applying, selectedProfileID == expectedProfile, content == expectedContent,
              let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else { throw TunnelProfile.InvalidProfile("Configuration changed while editing. Reopen the group and try again.") }
        var candidate = profiles[index]
        let saved = ProxyGroupEditing.recording(edit, in: content, previous: candidate.proxyGroups?.first { $0.tag == edit.tag })
        candidate.config = try ProxyGroupEditing.applying([saved], to: content)
        candidate.proxyGroups = (candidate.proxyGroups ?? []).filter { $0.tag != edit.tag } + [saved]
        try recordGroupSources(in: &candidate)
        _ = try prepare(candidate)
        candidate.pendingApply = true
        try publishSelected(candidate)
        transition(.modified)
        lastAction = "Group saved. Apply changes to use it."
    }

    func editableSourceNode(_ node: StagedNode) -> [String: Any]? {
        if let json = selectedProfile?.nodeOverrides?[node.tag], let row = (try? JSONSerialization.jsonObject(with: Data(json.utf8))) as? [String: Any] { return row }
        guard nodesSources.nodes.filter({ $0.tag == node.tag }).count == 1 else { return nil }
        return ProxyGroupEditing.outbounds(content).first { $0["tag"] as? String == node.tag }
    }

    private func renameNodeReferences(from old: String, to name: String, profile: inout TunnelProfile) throws {
        guard old != name else { return }
        profile.proxyGroups = profile.proxyGroups?.map { group in
            var next = group
            next.members = group.members.map { $0 == old ? name : $0 }
            next.excludedMembers = group.excludedMembers?.map { $0 == old ? name : $0 }
            next.additionalMembers = group.additionalMembers?.map { $0 == old ? name : $0 }
            if next.defaultMember == old { next.defaultMember = name }
            return next
        }
        if profile.beforeOnFinal == old { profile.beforeOnFinal = name }
        if profile.proxyChain?.upstream == old { profile.proxyChain?.upstream = name }
        if profile.proxyChain?.exit == old { profile.proxyChain?.exit = name }
        if let priorities = profile.experiment?.fallbacks { profile.experiment?.fallbacks = priorities.mapValues { $0.map { $0 == old ? name : $0 } } }
        if let overrides = profile.nodeOverrides {
            for (key, json) in overrides {
                let node = try JSONSerialization.jsonObject(with: Data(json.utf8))
                profile.nodeOverrides?[key] = String(decoding: try JSONSerialization.data(withJSONObject: ProxyGroupEditing.renamingReferences([old: name], in: node), options: [.sortedKeys]), as: UTF8.self)
            }
        }
    }

    func saveNodeSettings(tag: String, nodeJSON: String, expectedProfile: String?, expectedContent: String) throws {
        guard !generating && !applying, selectedProfileID == expectedProfile, content == expectedContent,
              let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else { throw TunnelProfile.InvalidProfile("Configuration changed while editing. Reopen the node and try again.") }
        var candidate = profiles[index]
        candidate.config = try protectServers(in: ProxyGroupEditing.replacingNode(tag: tag, with: nodeJSON, in: content))
        let node = try JSONSerialization.jsonObject(with: Data(nodeJSON.utf8)) as! [String: Any]
        let name = node["tag"] as! String
        let source = (candidate.nodeOverrides ?? [:]).keys.sorted().first { key in
            guard let json = candidate.nodeOverrides?[key], let row = (try? JSONSerialization.jsonObject(with: Data(json.utf8))) as? [String: Any] else { return false }
            return row["tag"] as? String == tag
        } ?? tag
        try renameNodeReferences(from: tag, to: name, profile: &candidate)
        var overrides = candidate.nodeOverrides ?? [:]; overrides[source] = nodeJSON; candidate.nodeOverrides = overrides
        try recordGroupSources(in: &candidate)
        _ = try prepare(candidate)
        candidate.pendingApply = true
        try publishSelected(candidate)
        transition(.modified)
        lastAction = "Node settings saved. Apply changes to use them."
    }

    func removeSourceNode(_ node: StagedNode) throws {
        guard !generating && !applying, nodesSources.nodes.contains(node) else { throw TunnelProfile.InvalidProfile("This node changed or was removed. Reopen the list.") }
        if let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) {
            var candidate = profiles[index]
            var groups = (candidate.proxyGroups ?? []).map { ProxyGroupEditing.resolving($0, in: content) }
            let tag = editableSourceNode(node)?["tag"] as? String ?? node.tag
            var changed = false
            for offset in groups.indices where groups[offset].members.contains(tag) {
                groups[offset].members.removeAll { $0 == tag }
                if groups[offset].excludedMembers != nil { groups[offset].excludedMembers = Array(Set(groups[offset].excludedMembers! + [tag])).sorted() }
                groups[offset].additionalMembers?.removeAll { $0 == tag }
                guard !groups[offset].members.isEmpty else { throw TunnelProfile.InvalidProfile("\(node.tag) is the last member of \(groups[offset].tag). Choose another member in Proxy groups before deleting it.") }
                if groups[offset].defaultMember == tag { groups[offset].defaultMember = "" }
                changed = true
            }
            if changed {
                candidate.config = try ProxyGroupEditing.applying(groups, to: content)
                candidate.proxyGroups = groups
                _ = try prepare(candidate)
            }
            if tag != node.tag {
                candidate.proxyGroups = candidate.proxyGroups?.map { group in
                    var next = group
                    next.excludedMembers = group.excludedMembers?.map { $0 == tag ? node.tag : $0 }
                    return next
                }
            }
            candidate.nodeOverrides?.removeValue(forKey: node.tag)
            try commitNodesSources(nodesSources.removingNode(id: node.id), profile: candidate)
        } else { try commitNodesSources(nodesSources.removingNode(id: node.id)) }
    }

    private func clearNodeOverride(_ tag: String, in candidate: inout TunnelProfile) throws {
        if let json = candidate.nodeOverrides?[tag], let row = (try? JSONSerialization.jsonObject(with: Data(json.utf8))) as? [String: Any], let name = row["tag"] as? String {
            try renameNodeReferences(from: name, to: tag, profile: &candidate)
        }
        candidate.nodeOverrides?.removeValue(forKey: tag)
    }

    func saveProxyChain(_ settings: ProxyChainSettings) throws {
        guard !generating && !applying else { throw TunnelProfile.InvalidProfile("Wait for generation or Apply to finish before saving the chain.") }
        guard let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else {
            throw TunnelProfile.InvalidProfile("Generate or import a configuration first.")
        }
        var candidate = profiles[index]
        if candidate.proxyChain == nil { candidate.config = try ProxyChainSettings.unchainedImportedConfig(candidate.config) }
        candidate.proxyChain = settings
        _ = try prepare(candidate)
        candidate.pendingApply = true
        try publishSelected(candidate)
        transition(.modified)
        lastAction = "Proxy chain saved. Apply changes to use it."
    }

    func saveExperimentSettings(_ settings: ExperimentSettings) throws {
        try settings.validate()
        guard let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else { throw TunnelProfile.InvalidProfile("Import a configuration first") }
        var candidate = profiles[index]
        try candidate.stageExperiment(settings, previous: experimentSettings.mode, content: content)
        _ = try prepare(candidate)
        try publishSelected(candidate)
        transition(.modified)
    }

    func connectionOptions() throws -> TunnelStartOptions {
        let settings = experimentSettings
        try settings.validate()
        let raw = String(decoding: try JSONEncoder().encode(settings), as: UTF8.self)
        // Preparation checks the imported rules before adding device-owned DNS rules.
        let runtime = try TailscaleConfigInjection.withoutAuthKey(connectionContent())
        if TailscaleEndpointProvisioning.isEnabled(in: runtime) {
            _ = try keyStore.readAuthKey() // Migrate legacy credentials for the selected endpoint.
        }
        return TunnelStartOptions(configContent: runtime, profileID: selectedProfileID, experimentJSON: raw,
                                  forceTailscaleDERP: selectedProfile?.forceTailscaleDERP, routeSourceVersion: 1)
    }

    func matchesConnectionOptions(_ options: TunnelStartOptions) -> Bool {
        guard selectedProfileID == options.profileID, let current = try? (applying ? connectionOptions() : systemConnectionOptions()),
              let receipt = try? TunnelConfigurationReceipt(options: options) else { return false }
        return receipt.matches(current)
    }

    func connectionContent() throws -> String {
        guard let profile = selectedProfile, !profile.config.isEmpty else {
            throw TunnelProfile.InvalidProfile("Generate the selected sources in Config before connecting.")
        }
        return try prepare(profile)
    }

    /// Compile sources through the shared Go host pipeline, then validate in
    /// Libbox. Candidate files use a new profile ID so validation failures
    /// cannot mutate the previous runtime's files.
    func generateFromSources(adding node: StagedNode? = nil) async throws {
        guard !generating else { throw TunnelProfile.InvalidProfile("Generation is already running") }
        if let generationUpgradeMessage { throw TunnelProfile.InvalidProfile(generationUpgradeMessage) }
        guard canGenerate else { throw TunnelProfile.InvalidProfile("Add a node or subscription before generating") }
        if sourceBundle.mainConf.isEmpty && !confSources.isEmpty { throw TunnelProfile.InvalidProfile("Choose a rule source in Config before generating") }
        let idBefore = selectedProfileID
        let sourceBefore = sourceBundle
        if let profile = selectedProfile, !profile.sourceDigest.isEmpty,
           sourceBefore.files[AutomaticGroupSources.fileName] == nil {
            throw TunnelProfile.InvalidProfile("Reimport a current host export to preserve automatic group exclusions before generating.")
        }
        let settingsBefore = experimentSettings
        let contentBefore = content
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        let originalSources = try encoder.encode(sourceBefore)
        var input = sourceBefore
        if let node {
            input.files["nodes.txt"] = nodesSources.adding(node: node).nodesText
        }
        let bundleJSON = String(decoding: try encoder.encode(input), as: UTF8.self)
        let regional = selectedProfile?.regionalDNS
        var settingsObject = try JSONSerialization.jsonObject(with: encoder.encode(settingsBefore)) as? [String: Any] ?? [:]
        let dnsGuard = selectedProfile?.dnsGuard
        if let dnsGuard {
            settingsObject["dnsGuard"] = try JSONSerialization.jsonObject(with: encoder.encode(dnsGuard))
        } else if let root = try? JSONSerialization.jsonObject(with: Data(contentBefore.utf8)) as? [String: Any],
                  let dns = root["dns"] as? [String: Any], let servers = dns["servers"] as? [[String: Any]],
                  servers.contains(where: { $0["tag"] as? String == "dns-bootstrap" }) {
            throw TunnelProfile.InvalidProfile("Reimport a current host export to preserve protected DNS before generating.")
        }
        var generation = selectedProfile?.hostGeneration
        if selectedProfile?.proxyChain != nil { generation?.chainExit = nil }
        if let generation {
            settingsObject["hostGeneration"] = try JSONSerialization.jsonObject(with: encoder.encode(generation))
        }
        settingsObject["regionalDNS"] = regional
        try Task.checkCancellation()
        let settingsJSON = String(decoding: try JSONSerialization.data(withJSONObject: settingsObject), as: UTF8.self)
        generating = true
        defer { generating = false }
        let raw = try await Task.detached(priority: .userInitiated) {
            var error: NSError?
            let result = MobilegenGenerateProfileWithFetcherJSON(bundleJSON, settingsJSON, NativeSourceFetcher(protectedDomains: regional?["domains"] ?? ""), &error)
            if let error { throw error }
            return result
        }.value
        try Task.checkCancellation()
        guard selectedProfileID == idBefore, try encoder.encode(sourceBundle) == originalSources, experimentSettings == settingsBefore, selectedProfile?.regionalDNS == regional, content == contentBefore else {
            throw TunnelProfile.InvalidProfile("Sources or selection changed during generation. No runtime was replaced; retry.")
        }
        var candidate = try TunnelProfile.decodePackage(Data(raw.utf8))
        let revision = candidate.id
        candidate.id = idBefore ?? UUID().uuidString
        candidate.ruleRevisionID = revision
        candidate.iCloudAdoptedBatches = selectedProfile?.iCloudAdoptedBatches
        candidate.hostGeneration = selectedProfile?.hostGeneration
        candidate.name = selectedProfile?.name ?? "Generated configuration"
        candidate.dnsGuard = dnsGuard
        candidate.regionalDNS = regional
        candidate.experiment = settingsBefore
        candidate.beforeOnFinal = selectedProfile?.beforeOnFinal
        candidate.forceTailscaleDERP = selectedProfile?.forceTailscaleDERP
        candidate.proxyChain = selectedProfile?.proxyChain
        if generation?.chainExit == nil && candidate.proxyChain == nil {
            candidate.proxyChain = ProxyChainSettings.importedChain(in: contentBefore)
        }
        var groupEdits = selectedProfile?.proxyGroups ?? []
        if let rawGroups = sourceBefore.files[AutomaticGroupSources.fileName] {
            groupEdits = AutomaticGroupSources.adopting(try AutomaticGroupSources.decode(Data(rawGroups.utf8)), edits: groupEdits, content: contentBefore, overrides: selectedProfile?.nodeOverrides ?? [:])
        }
        candidate.proxyGroups = groupEdits.map { edit in
            edit.excludedMembers == nil ? ProxyGroupEditing.recording(edit, in: contentBefore) : edit
        }
        candidate.nodeOverrides = selectedProfile?.nodeOverrides
        // Preserve device-specific Tailscale endpoint and advanced settings;
        // generated sources own rules/nodes, not endpoint identity or auth.
        if let old = try? JSONSerialization.jsonObject(with: Data(contentBefore.utf8)) as? [String: Any],
           var next = try JSONSerialization.jsonObject(with: Data(candidate.config.utf8)) as? [String: Any] {
            // Preserve the user's TUN stack/strict routing across regeneration.
            if let previous = (old["inbounds"] as? [[String: Any]])?.first(where: { $0["type"] as? String == "tun" }),
               var inbounds = next["inbounds"] as? [[String: Any]],
               let index = inbounds.firstIndex(where: { $0["type"] as? String == "tun" }) {
                for key in ["stack", "strict_route", "mtu"] { if let value = previous[key] { inbounds[index][key] = value } }
                next["inbounds"] = inbounds
            }
            if let endpoints = old["endpoints"] { next["endpoints"] = endpoints }
            if let log = old["log"] { next["log"] = log }
            candidate.config = try TailscaleEndpointProvisioning.withConnectivity(in: String(decoding: try JSONSerialization.data(withJSONObject: next, options: [.sortedKeys]), as: UTF8.self))
            if let semantics = ConfigSemanticsReader.read(contentBefore) {
                candidate.config = try SettingsOverrides.setBlockQUIC(semantics.blockQUIC, in: candidate.config)
                candidate.config = try SettingsOverrides.setBlockSTUN(semantics.blockSTUN, in: candidate.config)
            }
        }
        candidate.config = try protectServers(in: ProxyGroupEditing.applyingNodes(candidate.nodeOverrides ?? [:], to: candidate.config))
        candidate.config = try ProxyGroupEditing.applying(candidate.proxyGroups ?? [], to: candidate.config)
        candidate.proxyGroups = candidate.proxyGroups?.map { saved in
            var next = saved
            if ProxyGroupEditing.followsGeneratedNodes(saved), saved.excludedMembers != nil,
               let group = ProxyGroupEditing.groups(candidate.config).first(where: { $0.tag == saved.tag }) { next.members = group.members }
            return next
        }
        do {
            _ = try prepare(candidate)
            let sources = try decodeSources(candidate)
            try Task.checkCancellation()
            try publishSelected(candidate)
            if node != nil { adoptProfileSources(sources) }
        } catch {
            if let root = try? AppPaths.sharedDirectory() { try? FileManager.default.removeItem(at: root.appendingPathComponent("RuleSnapshots/" + revision)) }
            throw error
        }
        transition(.modified)
        transition(.regenerateSucceeded)
        lastAction = "Generated and validated on this device. Apply to use this configuration."
    }

    /// Includes and their paths belong to the selected profile, never to
    /// whichever profile happened to run the previous sync pass.
    private func includingLegacyGroupSources(_ bundle: SourceBundle) -> SourceBundle {
        guard bundle.files[AutomaticGroupSources.fileName] == nil else { return bundle }
        return (try? AutomaticGroupSources.updating(bundle, edits: selectedProfile?.proxyGroups ?? [], content: content, overrides: selectedProfile?.nodeOverrides ?? [:])) ?? bundle
    }

    var sourceBundle: SourceBundle {
        if let raw = selectedProfile?.sourceBundleJSON {
            if cachedSourceJSON == raw, let cachedSourceBundle { return includingLegacyGroupSources(cachedSourceBundle) }
            if let bundle = try? JSONDecoder().decode(SourceBundle.self, from: Data(raw.utf8)) {
                cachedSourceJSON = raw; cachedSourceBundle = bundle
                return includingLegacyGroupSources(bundle)
            }
        }
        cachedSourceJSON = nil; cachedSourceBundle = nil
        var bundle = SourceBundle()
        if !nodesSources.nodes.isEmpty { bundle.files["nodes.txt"] = nodesSources.nodesText }
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        if !policy.rules.isEmpty { bundle.files["policy.json"] = (try? encoder.encode(policy.rules.map { SyncPolicy(match: $0.match, action: $0.action) })).map { String(decoding: $0, as: UTF8.self) } }
        if !nodesSources.subscriptions.isEmpty { bundle.files["subscriptions.json"] = (try? encoder.encode(nodesSources.subscriptions.map { SyncSubscription(name: $0.name, url: $0.url, format: $0.format) })).map { String(decoding: $0, as: UTF8.self) } }
        if let source = importedSource {
            let name = ICloudSyncPaths.isValidSourceName(source.displaySource) && source.displaySource.lowercased().hasSuffix(".conf") ? source.displaySource : "conf/imported.conf"
            bundle.mainConf = name; bundle.files[name] = source.content
        }
        return includingLegacyGroupSources(bundle)
    }

    var confSources: [String] { sourceBundle.files.keys.filter { $0.lowercased().hasSuffix(".conf") }.sorted() }
    func selectConfSource(_ name: String) throws {
        var bundle = sourceBundle
        guard let body = bundle.files[name], ICloudSyncPaths.isValidSourceName(name) else { throw TunnelProfile.InvalidProfile("Unknown rule source") }
        try ICloudSyncPaths.validateConfGraph(main: name, files: bundle.files.mapValues { Data($0.utf8) })
        bundle.mainConf = name
        try publishSources(bundle)
        importedSource = ImportedSource(displaySource: name, content: body)
        saveDraft(try? importedSource?.encoded(), key: Self.importKey)
    }

    /// Adopt the whole validated snapshot together, preserving original bytes
    /// for the sync baseline instead of reconstructing unrelated source files.
    func adoptSourceBundle(_ bundle: SourceBundle, receipt: ICloudAdoptionReceipt? = nil) throws {
        let raw = String(decoding: try JSONEncoder().encode(bundle), as: UTF8.self)
        let decoded = try decodeSources(TunnelProfile(name: "Sources", config: "", sourceBundleJSON: raw))
        let original = selectedProfile ?? TunnelProfile(name: "Imported sources", config: "")
        var candidate = original
        if let receipt {
            var batches = original.iCloudAdoptedBatches ?? [:]
            batches[receipt.scope] = receipt.batch
            candidate.iCloudAdoptedBatches = batches
        }
        if let groups = decoded.groups {
            candidate.proxyGroups = AutomaticGroupSources.adopting(groups, edits: original.proxyGroups ?? [], content: content, overrides: original.nodeOverrides ?? [:])
        }
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        let canonical = try encoder.encode(bundle)
        let changed = try encoder.encode(sourceBundle) != canonical || candidate.proxyGroups != original.proxyGroups
        candidate.sourceBundleJSON = String(decoding: canonical, as: UTF8.self)
        if changed { candidate.sourcesChanged = true }
        try publishSelected(candidate)
        adoptProfileSources(decoded)
        if changed { transition(.modified) }
    }

    private func publishSources(_ bundle: SourceBundle, profile: TunnelProfile? = nil) throws {
        var candidate = profile ?? selectedProfile ?? TunnelProfile(name: "Imported sources", config: "")
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        candidate.sourceBundleJSON = String(decoding: try encoder.encode(bundle), as: UTF8.self)
        candidate.sourcesChanged = true
        try publishSelected(candidate)
        transition(.modified)
    }

    func ensureSourceProfile() throws {
        if selectedProfile == nil { try publishSources(sourceBundle) }
    }

    /// Save a new config (Config editor draft or a validated Settings/
    /// Tailscale edit). Any actual change marks it `modified` — the tunnel
    /// still runs the previous config until Regenerate+Reconnect.
    func save(_ newContent: String) throws {
        guard newContent != content else { return }
        var candidate = selectedProfile ?? TunnelProfile(name: "Imported configuration", config: newContent)
        candidate.config = newContent
        candidate.pendingApply = true
        _ = try prepare(candidate)
        try publishSelected(candidate)

        transition(.modified)
    }

    /// Stage recent failed domains as exact proxy rules. This is deliberately
    /// user-confirmed: adding a rule changes the selected profile's sources,
    /// and Apply remains the boundary that changes the running tunnel.
    func addProxyPolicyRules(_ domains: [String]) throws {
        let candidates = domains.map { $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() }
            .filter { !$0.isEmpty }
        guard !candidates.isEmpty else { throw TunnelProfile.InvalidProfile("Choose at least one failed domain.") }
        var next = policy
        var added = 0
        for domain in candidates {
            var error: NSError?
            guard let normalized = MobilecoreNormalizePolicyRule("domain:" + domain, "proxy", &error), error == nil else {
                throw error ?? TunnelProfile.InvalidProfile("Invalid failed domain.")
            }
            guard !next.rules.contains(where: { $0.match == normalized.match && $0.action == normalized.action }) else { continue }
            next = next.adding(StagedPolicyRule(match: normalized.match, action: normalized.action))
            added += 1
        }
        guard added > 0 else { throw TunnelProfile.InvalidProfile("Those domains are already in Config.") }
        try commitPolicy(next)
        let suffix = added == 1 ? "" : "s"
        lastAction = "Added \(added) failed domain rule\(suffix) to Config. Apply changes to use it."
    }

    /// Commit a staged policy book. The caller has validated every rule
    /// through mobilecore; this only persists and marks the state.
    func commitPolicy(_ book: PolicyBook) throws {
        guard policy.rules.map({ [$0.match, $0.action] }) != book.rules.map({ [$0.match, $0.action] }) else { return }
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        let policyData = try book.encoded()
        var bundle = sourceBundle
        bundle.files["policy.json"] = String(decoding: try encoder.encode(book.rules.map { SyncPolicy(match: $0.match, action: $0.action) }), as: UTF8.self)
        try publishSources(bundle)
        policy = book
        persistence.set(policyData, forKey: Self.policyKey)
        transition(.modified)
    }

    /// Commit validated nodes and subscription metadata. Generation fetches
    /// and decodes subscription bodies on this device.
    func commitNodesSources(_ book: NodesSourcesBook, profile: TunnelProfile? = nil, clearingOverride: String? = nil) throws {
        guard nodesSources.nodesText != book.nodesText ||
              nodesSources.subscriptions.map({ [$0.name, $0.url, $0.format] }) != book.subscriptions.map({ [$0.name, $0.url, $0.format] }) else { return }
        var candidate = profile ?? selectedProfile ?? TunnelProfile(name: "Imported sources", config: "")
        if let clearingOverride { try clearNodeOverride(clearingOverride, in: &candidate) }
        var bundle = try AutomaticGroupSources.updating(sourceBundle, edits: candidate.proxyGroups ?? [], content: candidate.config, overrides: candidate.nodeOverrides ?? [:])
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        if nodesSources.nodesText != book.nodesText { bundle.files["nodes.txt"] = book.nodesText }
        let subscriptions = book.subscriptions.map { SyncSubscription(name: $0.name, url: $0.url, format: $0.format) }
        bundle.files["subscriptions.json"] = String(decoding: try encoder.encode(subscriptions), as: UTF8.self)
        try publishSources(bundle, profile: candidate)
        nodesSources = book
        saveDraft(try? book.encoded(), key: Self.nodesSourcesKey)
    }

    /// Replace the last-known-good import. Call ONLY after
    /// MobilecoreParseConfContent accepted the content.
    func commitImport(_ source: ImportedSource) throws {
        guard importedSource?.content != source.content || importedSource?.displaySource != source.displaySource else { return }
        let name = ICloudSyncPaths.isValidSourceName(source.displaySource) && source.displaySource.lowercased().hasSuffix(".conf") ? source.displaySource : "conf/imported.conf"
        var bundle = sourceBundle
        bundle.mainConf = name; bundle.files[name] = source.content
        try publishSources(bundle)
        importedSource = source
        if let data = try? source.encoded() {
            saveDraft(data, key: Self.importKey)
        }
        transition(.modified)
    }

    /// Generate changed sources, validate the candidate, inject Keychain
    /// authentication and activate it. Startup needs provider confirmation;
    /// a running provider applies via reload with rollback on failure.
    func regenerateAndApply(tunnel: TunnelControlling) async {
        guard !applying else { return }
        applying = true
        defer { applying = false }
        if selectedProfile?.sourcesChanged == true || content.isEmpty {
            do { try await generateFromSources() }
            catch { transition(.regenerateFailed); lastAction = TunnelDiagnostics.sanitized(error.localizedDescription); return }
        }
        let profileBefore = selectedProfile
        // In-process structural validation before the provider sees it.
        // Top-level gomobile functions import the trailing NSError** (they
        // are C functions, not methods, so no `throws`).
        var bridgeError: NSError?
        _ = MobilecoreValidateConfigJSON(content, &bridgeError)
        if let bridgeError {
            transition(.regenerateFailed)
            lastAction = "structural check failed: \(bridgeError.localizedDescription)"
            return
        }
        do {
            let options = try connectionOptions()
            let state = try await tunnel.ping()
            try Task.checkCancellation()
            guard selectedProfile == profileBefore else {
                lastAction = "Configuration changed while checking the tunnel. Apply the selected configuration again."
                return
            }
            guard state.serviceState != .starting && state.serviceState != .stopping else {
                throw TunnelProfile.InvalidProfile("Wait for the current VPN operation to finish, then apply again.")
            }
            let transaction = UUID()
            try await tunnel.apply(options: options, starting: !state.serviceState.running, transaction: transaction) {
                guard self.selectedProfile == profileBefore else { throw CancellationError() }
                guard self.transition(.regenerateSucceeded), self.transition(.applied, transaction: transaction) else {
                    throw TunnelProfile.InvalidProfile(self.lastAction ?? "Could not save the applied configuration.")
                }
            }
            lastAction = "applied to the running provider"
        } catch {
            if selectedProfile == profileBefore { transition(.regenerateFailed) }
            lastAction = "regenerate failed: \(error.localizedDescription)"
        }
    }

    /// A system On prepares the current generated configuration just as Home
    /// Connect does. Preparing is not evidence that the provider applied it.
    func systemConnectionOptions() throws -> TunnelStartOptions {
        guard !applying && !generating else {
            throw TunnelProfile.InvalidProfile("Wait for configuration generation or Apply to finish before connecting.")
        }
        guard canConnect else {
            throw TunnelProfile.InvalidProfile("Generate the selected configuration in Config before connecting.")
        }
        if let profile = selectedProfile {
            try TunnelProfile.validateRouteSource(profile.config, version: profile.routeSourceVersion)
        }
        if let id = selectedProfileID, let confirmed = try TunnelStartOptions.confirmed(for: id) {
            try confirmed.validateRouteSource()
            return confirmed
        }
        return try connectionOptions()
    }

    /// A background intent can finish before Connected. The provider's exact
    /// applied receipt plus native Running confirms the same selected payload
    /// when the app later observes it; stale/different receipts never clear edits.
    func reconcileAppliedConnection(service: ServiceState,
                                    receipt: TunnelConfigurationReceipt? = TunnelConfigurationReceiptStore.read()) {
        guard !applying, service == .running, let id = selectedProfileID,
              (try? TunnelStartOptions.hasInterruptedApply(for: id)) == false, selectedProfile?.sourcesChanged == false, selectedProfile?.pendingApply != false,
              let receipt, let options = try? connectionOptions(), receipt.matches(options) else { return }
        transition(.regenerateSucceeded)
        transition(.applied)
    }

    func connectionConfirmed(content appliedContent: String) {
        guard !applying, let id = selectedProfileID, (try? TunnelStartOptions.hasInterruptedApply(for: id)) == false,
              selectedProfile?.sourcesChanged == false, (try? connectionContent()) == appliedContent else { return }
        transition(.regenerateSucceeded); transition(.applied)
    }

    @discardableResult private func transition(_ event: ConfigEvent, transaction: UUID? = nil) -> Bool {
        var bridgeError: NSError?
        let next = MobilecoreConfigStateTransition(configState.rawValue, event.rawValue, &bridgeError)
        if let bridgeError {
            lastAction = "state machine error: \(bridgeError.localizedDescription)"
            return false
        }
        if event == .applied, var candidate = selectedProfile {
            candidate.pendingApply = false
            if let transaction { candidate.appliedTransaction = transaction }
            do { try publishSelected(candidate) }
            catch { reportProfileError(error); lastAction = profileError; return false }
        }
        configState = ConfigState(rawValue: next) ?? configState
        return true
    }
}

enum ConfigEvent: String {
    case modified
    case regenerateSucceeded = "regenerate_succeeded"
    case regenerateFailed = "regenerate_failed"
    case applied
}
