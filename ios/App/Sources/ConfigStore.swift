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
    @Published private(set) var profileError: String?
    func reportProfileError(_ error: Error) { profileError = TunnelDiagnostics.sanitized(error.localizedDescription) }
    var selectedProfile: TunnelProfile? { profiles.first { $0.id == selectedProfileID } }
    @Published private(set) var applying = false
    @Published private(set) var generating = false
    var canConnect: Bool { !applying && !generating && selectedProfile != nil && selectedProfile?.sourcesChanged == false && !content.isEmpty }
    var canGenerate: Bool { !generating && (!nodesSources.nodes.isEmpty || !nodesSources.subscriptions.isEmpty) }

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

    private let persistence: UserDefaults
    private let keyStore: TailscaleAuthKeyStoring
    private var cachedSourceJSON: String?
    private var cachedSourceBundle: SourceBundle?

    init(persistence: UserDefaults = .standard, keyStore: TailscaleAuthKeyStoring) {
        self.persistence = persistence
        self.keyStore = keyStore
        self.content = persistence.string(forKey: Self.storageKey) ?? ""
        if let data = persistence.data(forKey: Self.policyKey) {
            self.policy = PolicyBook.decode(data) ?? PolicyBook()
        } else {
            self.policy = PolicyBook()
        }
        if let data = persistence.data(forKey: Self.nodesSourcesKey) {
            self.nodesSources = NodesSourcesBook.decode(data) ?? NodesSourcesBook()
        } else {
            self.nodesSources = NodesSourcesBook()
        }
        if let data = persistence.data(forKey: Self.importKey) {
            self.importedSource = ImportedSource.decode(data)
        } else {
            self.importedSource = nil
        }
        self.profiles = persistence.data(forKey: Self.profilesKey).flatMap { try? JSONDecoder().decode([TunnelProfile].self, from: $0) } ?? []
        for index in profiles.indices {
            if UUID(uuidString: profiles[index].id) != nil,
               let url = try? AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(profiles[index].id)/profile.json"),
               let data = try? Data(contentsOf: url),
               let manifest = try? JSONDecoder().decode(TunnelProfile.self, from: data) { profiles[index] = manifest }
        }
        let initialID = profiles.first { $0.id == persistence.string(forKey: Self.selectedProfileKey) }?.id ?? profiles.first?.id
        if let index = profiles.firstIndex(where: { $0.id == initialID }),
           let loaded = try? loadRuleFiles(profiles[index]) { profiles[index] = loaded }
        self.selectedProfileID = persistence.string(forKey: Self.selectedProfileKey)
        if profiles.isEmpty && !content.isEmpty {
            let legacy = TunnelProfile(name: "Previous configuration", config: content)
            profiles = [legacy]; selectedProfileID = legacy.id
            persistProfiles()
        }
        if let selected = selectedProfile {
            content = selected.config
            if selected.sourcesChanged { configState = .needsRegenerate }
            else if selected.pendingApply != false { configState = .needsReconnect }
        }
        else if let first = profiles.first { selectedProfileID = first.id; content = first.config; persistProfiles() }
    }

    private func persistProfiles() {
        // Complete manifests (including large conf includes) and binaries
        // live in App Group files, not CFPreferences' limited value store.
        do {
            let root = try AppPaths.sharedDirectory()
            var references: [TunnelProfile] = []
            for profile in profiles {
                guard UUID(uuidString: profile.id) != nil else { throw TunnelProfile.InvalidProfile("Invalid configuration identifier") }
                var manifest = profile; manifest.files = [:]
                let file = root.appendingPathComponent("Profiles/\(profile.id)/profile.json")
                try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
                try JSONEncoder().encode(manifest).write(to: file, options: .atomic)
                try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
                UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(profile.id != selectedProfileID || profile.sourcesChanged || profile.pendingApply != false, forKey: "sakamoto.experiment.pause." + profile.id)
                manifest.config = ""; manifest.sourceBundleJSON = nil
                references.append(manifest)
            }
            persistence.set(try JSONEncoder().encode(references), forKey: Self.profilesKey)
            persistence.set(selectedProfileID, forKey: Self.selectedProfileKey)
            UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(
                selectedProfile == nil || selectedProfile?.sourcesChanged == true || selectedProfile?.pendingApply != false,
                forKey: "sakamoto.profile.requiresApply"
            )
        } catch { reportProfileError(error) }
    }

    func installProfile(_ profile: TunnelProfile) throws {
        _ = try prepare(profile)
        let sources = try decodeSources(profile)
        if let old = selectedProfileID { UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(true, forKey: "sakamoto.experiment.pause." + old) }
        var profile = profile
        profile.pendingApply = true
        profiles.append(profile)
        releaseOtherRuleFiles(keeping: profile.id)
        profileError = nil
        selectedProfileID = profile.id
        content = profile.config
        adoptProfileSources(sources)
        persistence.set(content, forKey: Self.storageKey)
        persistProfiles()
        transition(.modified)
    }

    func selectProfile(_ id: String) throws {
        if let old = selectedProfileID, old != id { UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(true, forKey: "sakamoto.experiment.pause." + old) }
        guard let index = profiles.firstIndex(where: { $0.id == id }) else { return }
        let profile = try loadRuleFiles(profiles[index])
        if !profile.sourcesChanged && !profile.config.isEmpty { _ = try prepare(profile) }
        let sources = try decodeSources(profile)
        profileError = nil
        selectedProfileID = id; content = profile.config
        profiles[index] = profile
        profiles[index].pendingApply = true
        releaseOtherRuleFiles(keeping: id)
        adoptProfileSources(sources)
        persistence.set(content, forKey: Self.storageKey)
        persistProfiles(); transition(.modified)
    }

    private func releaseOtherRuleFiles(keeping id: String) {
        for index in profiles.indices where profiles[index].id != id { profiles[index].files = [:] }
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
        let nodes = nodeText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? [] : try NodeFileImport.parse(Data(nodeText.utf8), validate: ConfigModel.validateNode)
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
        return ProfileSources(nodes: NodesSourcesBook(nodes: nodes, subscriptions: subscriptions.map { StagedSubscription(name: $0.name, url: $0.url, format: $0.format) }), policy: PolicyBook(rules: rules.map { StagedPolicyRule(match: $0.match, action: $0.action) }), imported: imported)
    }

    nonisolated static func validateStructuredSource(name: String, data: Data) throws {
        if name == "policy.json" {
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
        persistence.set(try? nodesSources.encoded(), forKey: Self.nodesSourcesKey)
        persistence.set(try? policy.encoded(), forKey: Self.policyKey)
        persistence.set(try? importedSource?.encoded(), forKey: Self.importKey)
    }

    private func prepare(_ profile: TunnelProfile) throws -> String {
        guard UUID(uuidString: profile.id) != nil else { throw TunnelProfile.InvalidProfile("Invalid configuration identifier") }
        if let revision = profile.ruleRevisionID, UUID(uuidString: revision) == nil { throw TunnelProfile.InvalidProfile("Invalid rule snapshot identifier") }
        let root = try AppPaths.sharedDirectory().appendingPathComponent(profile.ruleRevisionID.map { "RuleSnapshots/\($0)" } ?? "Profiles/\(profile.id)", isDirectory: true)
        var runtimeProfile = profile
        runtimeProfile.config = try profile.proxyChain?.applying(to: profile.config) ?? profile.config
        let config = try runtimeProfile.preparedConfig(ruleDirectory: root)
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
        _ = try prepare(candidate)
        if selectedProfile == nil {
            // Keep source ownership intact; enabling is a runtime edit.
            candidate.sourceBundleJSON = String(decoding: try JSONEncoder().encode(sourceBundle), as: UTF8.self)
            try installProfile(candidate)
        } else {
            save(next)
            if empty && sourceBundle.files.isEmpty, let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) {
                profiles[index].sourcesChanged = false
                persistProfiles()
            }
        }
    }

    func setForceTailscaleDERP(_ enabled: Bool) throws {
        guard !generating && !applying else { throw TunnelProfile.InvalidProfile("Wait for generation or Apply to finish before changing Tailscale transport.") }
        guard let index = profiles.firstIndex(where: { $0.id == selectedProfileID }),
              TailscaleEndpointProvisioning.isEnabled(in: content) else { throw TunnelProfile.InvalidProfile("Enable a Tailscale endpoint first.") }
        profiles[index].forceTailscaleDERP = enabled ? true : nil
        profiles[index].pendingApply = true
        persistProfiles()
        transition(.modified)
        lastAction = "Tailscale transport saved. Apply & reconnect to use it."
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
        content = candidate.config
        persistence.set(content, forKey: Self.storageKey)
        profiles[index] = candidate
        profiles[index].pendingApply = true
        UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(true, forKey: "sakamoto.experiment.pause." + candidate.id)
        persistProfiles()
        transition(.modified)
        lastAction = "Proxy chain saved. Apply changes to use it."
    }

    func saveExperimentSettings(_ settings: ExperimentSettings) throws {
        try settings.validate()
        guard let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else { throw TunnelProfile.InvalidProfile("Import a configuration first") }
        let adjusted = try SettingsOverrides.setExperiment(settings.mode == .on, in: content)
        var candidate = profiles[index]; candidate.config = adjusted
        _ = try prepare(candidate)
        profiles[index].experiment = settings
        profiles[index].pendingApply = true
        content = adjusted; profiles[index].config = adjusted
        persistProfiles(); persistence.set(content, forKey: Self.storageKey)
        transition(.modified)
    }

    func connectionOptions(content prepared: String? = nil) throws -> TunnelStartOptions {
        let settings = experimentSettings
        try settings.validate()
        let raw = String(decoding: try JSONEncoder().encode(settings), as: UTF8.self)
        return TunnelStartOptions(configContent: try prepared ?? connectionContent(), profileID: selectedProfileID, experimentJSON: raw,
                                  forceTailscaleDERP: selectedProfile?.forceTailscaleDERP)
    }

    func connectionContent() throws -> String {
        guard let profile = selectedProfile, !profile.sourcesChanged else {
            throw TunnelProfile.InvalidProfile("Generate the selected sources in Config before connecting.")
        }
        return try prepare(profile)
    }

    /// Compile sources through the shared Go host pipeline, then validate in
    /// Libbox. Candidate files use a new profile ID so validation failures
    /// cannot mutate the previous runtime's files.
    func generateFromSources() async throws {
        guard !generating else { throw TunnelProfile.InvalidProfile("Generation is already running") }
        guard canGenerate else { throw TunnelProfile.InvalidProfile("Add a node or subscription before generating") }
        if sourceBundle.mainConf.isEmpty && !confSources.isEmpty { throw TunnelProfile.InvalidProfile("Choose a rule source in Config before generating") }
        let idBefore = selectedProfileID
        let sourceBefore = sourceBundle
        let settingsBefore = experimentSettings
        let contentBefore = content
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        let bundleJSON = String(decoding: try encoder.encode(sourceBefore), as: UTF8.self)
        let settingsJSON = String(decoding: try encoder.encode(settingsBefore), as: UTF8.self)
        generating = true
        defer { generating = false }
        let raw = try await Task.detached(priority: .userInitiated) {
            var error: NSError?
            let result = MobilegenGenerateProfileWithFetcherJSON(bundleJSON, settingsJSON, NativeSourceFetcher(), &error)
            if let error { throw error }
            return result
        }.value
        guard selectedProfileID == idBefore, try encoder.encode(sourceBundle) == Data(bundleJSON.utf8), experimentSettings == settingsBefore, content == contentBefore else {
            throw TunnelProfile.InvalidProfile("Sources or selection changed during generation. No runtime was replaced; retry.")
        }
        var candidate = try TunnelProfile.decodePackage(Data(raw.utf8))
        let revision = candidate.id
        candidate.id = idBefore ?? UUID().uuidString
        candidate.ruleRevisionID = revision
        candidate.name = selectedProfile?.name ?? "Generated configuration"
        candidate.experiment = settingsBefore
        candidate.forceTailscaleDERP = selectedProfile?.forceTailscaleDERP
        candidate.proxyChain = selectedProfile?.proxyChain ?? ProxyChainSettings.importedChain(in: contentBefore)
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
        do {
            _ = try prepare(candidate)
            _ = try decodeSources(candidate)
        } catch {
            if let root = try? AppPaths.sharedDirectory() { try? FileManager.default.removeItem(at: root.appendingPathComponent("RuleSnapshots/" + revision)) }
            throw error
        }
        if let old = idBefore { UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.set(true, forKey: "sakamoto.experiment.pause." + old) }
        if let index = profiles.firstIndex(where: { $0.id == idBefore }) { profiles[index] = candidate }
        else { profiles.append(candidate) }
        selectedProfileID = candidate.id; content = candidate.config
        persistence.set(content, forKey: Self.storageKey); persistProfiles()
        transition(.modified)
        transition(.regenerateSucceeded)
        lastAction = "Generated and validated on this device. Apply to use this configuration."
    }

    /// Includes and their paths belong to the selected profile, never to
    /// whichever profile happened to run the previous sync pass.
    var sourceBundle: SourceBundle {
        if let raw = selectedProfile?.sourceBundleJSON {
            if cachedSourceJSON == raw, let cachedSourceBundle { return cachedSourceBundle }
            if let bundle = try? JSONDecoder().decode(SourceBundle.self, from: Data(raw.utf8)) {
                cachedSourceJSON = raw; cachedSourceBundle = bundle
                return bundle
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
        return bundle
    }

    var confSources: [String] { sourceBundle.files.keys.filter { $0.lowercased().hasSuffix(".conf") }.sorted() }
    func selectConfSource(_ name: String) throws {
        var bundle = sourceBundle
        guard let body = bundle.files[name], ICloudSyncPaths.isValidSourceName(name) else { throw TunnelProfile.InvalidProfile("Unknown rule source") }
        try ICloudSyncPaths.validateConfGraph(main: name, files: bundle.files.mapValues { Data($0.utf8) })
        bundle.mainConf = name
        commitSourceBundle(bundle)
        commitImport(ImportedSource(displaySource: name, content: body))
    }

    /// Adopt the whole validated snapshot together, preserving original bytes
    /// for the sync baseline instead of reconstructing unrelated source files.
    func adoptSourceBundle(_ bundle: SourceBundle) throws {
        let raw = String(decoding: try JSONEncoder().encode(bundle), as: UTF8.self)
        let decoded = try decodeSources(TunnelProfile(name: "Sources", config: "", sourceBundleJSON: raw))
        commitSourceBundle(bundle)
        adoptProfileSources(decoded)
    }

    func commitSourceBundle(_ bundle: SourceBundle) {
        ensureSourceProfile()
        guard let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else { return }
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        guard let data = try? encoder.encode(bundle) else { return }
        let raw = String(decoding: data, as: UTF8.self)
        let existing = try? encoder.encode(sourceBundle)
        if existing != data {
            profiles[index].sourceBundleJSON = raw
            profiles[index].sourcesChanged = true
            persistProfiles(); transition(.modified)
        }
    }

    func ensureSourceProfile() {
        guard selectedProfile == nil else { return }
        let bundle = sourceBundle
        var profile = TunnelProfile(name: "Imported sources", config: "")
        profile.sourceBundleJSON = (try? JSONEncoder().encode(bundle)).map { String(decoding: $0, as: UTF8.self) }
        profile.sourcesChanged = true
        profiles.append(profile); selectedProfileID = profile.id
        persistProfiles()
    }

    private func invalidateProfileSources(changedFiles: Set<String>) {
        ensureSourceProfile()
        guard let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) else { return }
        var bundle = sourceBundle
        let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
        if changedFiles.contains("nodes.txt") { bundle.files["nodes.txt"] = nodesSources.nodesText }
        if changedFiles.contains("policy.json") { bundle.files["policy.json"] = (try? encoder.encode(policy.rules.map { SyncPolicy(match: $0.match, action: $0.action) })).map { String(decoding: $0, as: UTF8.self) } }
        if changedFiles.contains("subscriptions.json") { bundle.files["subscriptions.json"] = (try? encoder.encode(nodesSources.subscriptions.map { SyncSubscription(name: $0.name, url: $0.url, format: $0.format) })).map { String(decoding: $0, as: UTF8.self) } }
        if changedFiles.contains("conf"), let source = importedSource {
            let name = ICloudSyncPaths.isValidSourceName(source.displaySource) && source.displaySource.lowercased().hasSuffix(".conf") ? source.displaySource : "conf/imported.conf"
            bundle.mainConf = name; bundle.files[name] = source.content
        }
        if let data = try? encoder.encode(bundle) { profiles[index].sourceBundleJSON = String(decoding: data, as: UTF8.self) }
        profiles[index].sourcesChanged = true
        persistProfiles()
    }

    /// Save a new config (Config editor draft or a validated Settings/
    /// Tailscale edit). Any actual change marks it `modified` — the tunnel
    /// still runs the previous config until Regenerate+Reconnect.
    func save(_ newContent: String) {
        guard newContent != content else { return }
        content = newContent
        if let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) {
            profiles[index].config = newContent
            profiles[index].pendingApply = true
        } else {
            let profile = TunnelProfile(name: "Imported configuration", config: newContent)
            profiles.append(profile); selectedProfileID = profile.id
        }
        persistProfiles()
        persistence.set(newContent, forKey: Self.storageKey)
        transition(.modified)
    }

    /// Commit a staged policy book. The caller has validated every rule
    /// through mobilecore; this only persists and marks the state.
    func commitPolicy(_ book: PolicyBook) {
        guard policy.rules.map({ [$0.match, $0.action] }) != book.rules.map({ [$0.match, $0.action] }) else { return }
        policy = book
        invalidateProfileSources(changedFiles: ["policy.json"])
        if let data = try? book.encoded() {
            persistence.set(data, forKey: Self.policyKey)
        }
        transition(.modified)
    }

    /// Commit validated nodes and subscription metadata. Generation fetches
    /// and decodes subscription bodies on this device.
    func commitNodesSources(_ book: NodesSourcesBook) {
        guard nodesSources.nodesText != book.nodesText ||
              nodesSources.subscriptions.map({ [$0.name, $0.url, $0.format] }) != book.subscriptions.map({ [$0.name, $0.url, $0.format] }) else { return }
        var changed: Set<String> = []
        if nodesSources.nodesText != book.nodesText { changed.insert("nodes.txt") }
        if nodesSources.subscriptions.map({ [$0.name, $0.url, $0.format] }) != book.subscriptions.map({ [$0.name, $0.url, $0.format] }) { changed.insert("subscriptions.json") }
        nodesSources = book
        invalidateProfileSources(changedFiles: changed)
        if let data = try? book.encoded() {
            persistence.set(data, forKey: Self.nodesSourcesKey)
        }
        transition(.modified)
    }

    /// Replace the last-known-good import. Call ONLY after
    /// MobilecoreParseConfContent accepted the content.
    func commitImport(_ source: ImportedSource) {
        guard importedSource?.content != source.content || importedSource?.displaySource != source.displaySource else { return }
        importedSource = source
        invalidateProfileSources(changedFiles: ["conf"])
        if let data = try? source.encoded() {
            persistence.set(data, forKey: Self.importKey)
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
        let profileID = selectedProfileID
        let configBefore = content
        let experimentBefore = experimentSettings
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
            let content = try TailscaleConfigInjection.inject(
                authKey: keyStore.readAuthKey() ?? "",
                into: try connectionContent()
            )
            let state = try await tunnel.ping()
            let options = try connectionOptions(content: content)
            if state.serviceState.running { try await tunnel.reload(options: options) }
            else {
                guard state.serviceState != .starting && state.serviceState != .stopping else {
                    throw TunnelProfile.InvalidProfile("Wait for the current VPN operation to finish, then apply again.")
                }
                try await tunnel.connect(options: options)
                // startVPNTunnel queues a request; only a provider-confirmed
                // Running snapshot can make the configuration Clean.
                var running = false
                for _ in 0..<30 {
                    try await Task.sleep(nanoseconds: 500_000_000)
                    let snapshot = try await tunnel.ping()
                    if snapshot.serviceState.running { running = true; break }
                    if let detail = snapshot.detail { throw TunnelProfile.InvalidProfile(detail) }
                }
                guard running else { throw TunnelProfile.InvalidProfile("VPN startup was not confirmed. Check the startup diagnostic and retry.") }
            }
            guard selectedProfileID == profileID, self.content == configBefore, experimentSettings == experimentBefore, selectedProfile?.sourcesChanged == false else {
                lastAction = "Selection or sources changed during Apply; the new selection still needs applying."
                return
            }
            transition(.regenerateSucceeded)
            transition(.applied)
            lastAction = "applied to the running provider"
        } catch {
            transition(.regenerateFailed)
            lastAction = "regenerate failed: \(error.localizedDescription)"
        }
    }

    func connectionConfirmed(content appliedContent: String) {
        guard (try? connectionContent()) == appliedContent else { return }
        transition(.regenerateSucceeded); transition(.applied)
    }

    private func transition(_ event: ConfigEvent) {
        var bridgeError: NSError?
        let next = MobilecoreConfigStateTransition(configState.rawValue, event.rawValue, &bridgeError)
        if let bridgeError {
            lastAction = "state machine error: \(bridgeError.localizedDescription)"
            return
        }
        configState = ConfigState(rawValue: next) ?? configState
        if event == .applied, let index = profiles.firstIndex(where: { $0.id == selectedProfileID }) {
            profiles[index].pendingApply = false
            persistProfiles()
        }
    }
}

enum ConfigEvent: String {
    case modified
    case regenerateSucceeded = "regenerate_succeeded"
    case regenerateFailed = "regenerate_failed"
    case applied
}
