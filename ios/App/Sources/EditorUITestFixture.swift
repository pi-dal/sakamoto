#if DEBUG && targetEnvironment(simulator)
import Foundation
import Security
import SakamotoKit

/// Isolated simulator-only fixture for end-to-end editor regression tests.
/// Never modifies normal preferences and never starts a tunnel.
@MainActor
enum EditorUITestFixture {
    private final class EmptyKeys: TailscaleAuthKeyStoring, @unchecked Sendable {
        var hasAuthKey = false
        func readAuthKey() -> String? { nil }
        func storeAuthKey(_ key: String) throws {}
        func deleteAuthKey() throws {}
    }
    // Keys live under a per-test service. The UI fixture never opens the device's shared item.
    static func credentialStores(id: UUID, cold: Bool) throws -> (ConfigStore, S3SyncModel) {
        let suite = "fixture.credential-ui." + id.uuidString
        let defaults = UserDefaults(suiteName: suite)!
        let shared = TailscaleKeychainStore(service: suite, account: "shared")
        let s3 = TailscaleKeychainStore(service: suite, account: "s3")
        if let phase = ProcessInfo.processInfo.environment["SAKAMOTO_KEY_DELETE_PHASE"] {
            let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                        kSecAttrService as String: suite, kSecAttrAccount as String: "tailscale-auth-key"]
            let retryFault = FileManager.default.temporaryDirectory.appendingPathComponent(suite + ".delete-once")
            if phase == "retry" { try Data().write(to: retryFault) }
            let vault = TailscaleKeychainStore(service: suite, loadShared: { try shared.readAuthKey() },
                saveShared: { try shared.storeAuthKey($0) }, removeShared: {
                    if phase == "fail" { throw AppSetupError("Fixture shared keychain unavailable") }
                    if FileManager.default.fileExists(atPath: retryFault.path) {
                        try FileManager.default.removeItem(at: retryFault)
                        throw AppSetupError("Fixture shared keychain unavailable")
                    }
                    try shared.deleteAuthKey()
                })
            let store = ConfigStore(persistence: defaults, keyStore: vault)
            if phase == "fail" {
                try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions())
                let item = query.merging([kSecValueData as String: Data("fixture-original".utf8)]) { _, new in new }
                guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess, try shared.readAuthKey() == nil else {
                    throw AppSetupError("Could not create legacy-only fixture")
                }
            } else {
                let legacyStatus = SecItemCopyMatching(query as CFDictionary, nil)
                guard legacyStatus == (phase == "retry" ? errSecSuccess : errSecItemNotFound) else {
                    throw AppSetupError("Unexpected legacy item after relaunch")
                }
                let options = try store.connectionOptions()
                let runtime = try TailscaleConfigInjection.forTunnel(options.configContent) { try shared.readAuthKey() }
                let document = try JSONSerialization.jsonObject(with: Data(runtime.utf8)) as? [String: Any]
                guard let endpoint = (document?["endpoints"] as? [[String: Any]])?.first(where: { $0["type"] as? String == "tailscale" }),
                      endpoint["auth_key"] as? String == (phase == "retry" ? "fixture-original" : nil) else {
                    throw AppSetupError("Unexpected endpoint credential after relaunch")
                }
            }
            return (store, S3SyncModel(store: store, defaults: defaults, vault: s3))
        }
        if cold {
            let runtime = try TailscaleConfigInjection.forTunnel(#"{"endpoints":[{"type":"tailscale"}]}"#) { try shared.readAuthKey() }
            let document = try JSONSerialization.jsonObject(with: Data(runtime.utf8)) as? [String: Any]
            let endpoints = document?["endpoints"] as? [[String: Any]]
            guard endpoints?.first?["auth_key"] as? String == "fixture-original" else { throw AppSetupError("Fixture key changed") }
        } else {
            try shared.storeAuthKey("fixture-original")
            let raw = try JSONEncoder().encode(S3SyncCredentials(accessKey: "fixture", secretKey: "fixture"))
            try s3.storeAuthKey(String(decoding: raw, as: UTF8.self))
        }
        let vault = TailscaleKeychainStore(service: suite, loadShared: { try shared.readAuthKey() },
            saveShared: { try shared.storeAuthKey($0) }, removeLegacy: { _ in errSecInteractionNotAllowed })
        let store = ConfigStore(persistence: defaults, keyStore: vault)
        let model = S3SyncModel(store: store, defaults: defaults, vault: FirstReadUnavailable(s3))
        return (store, model)
    }

    private final class FirstReadUnavailable: TailscaleAuthKeyStoring, @unchecked Sendable {
        let vault: TailscaleKeychainStore
        var first = true
        init(_ vault: TailscaleKeychainStore) { self.vault = vault }
        var hasAuthKey: Bool { vault.hasAuthKey }
        func readAuthKey() throws -> String? {
            if first { first = false; throw AppSetupError("Fixture keychain unavailable") }
            return try vault.readAuthKey()
        }
        func storeAuthKey(_ key: String) throws { try vault.storeAuthKey(key) }
        func deleteAuthKey() throws { try vault.deleteAuthKey() }
    }

    static func tailnetProfiles(id: UUID, cold: Bool) throws -> ConfigStore {
        let defaults = UserDefaults(suiteName: "fixture.tailnet-profiles." + id.uuidString)!
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        if ProcessInfo.processInfo.arguments.contains("--empty-profile-fixture") { return store }
        if ProcessInfo.processInfo.arguments.contains("--source-form-cold") {
            guard store.selectedProfile?.name == "Fixture B", let expected = defaults.string(forKey: "sourceBundleBefore"),
                  store.selectedProfile?.sourceBundleJSON == expected else { throw AppSetupError("B source bundle changed after cancel") }
        } else if ProcessInfo.processInfo.arguments.contains("--experiment-profile-cold") {
            guard store.selectedProfile?.name == "Fixture B",
                  try JSONDecoder().decode(ExperimentSettings.self, from: defaults.data(forKey: "experimentBefore") ?? Data()) == store.experimentSettings,
                  try JSONDecoder().decode(PolicyBook.self, from: defaults.data(forKey: "policyBefore") ?? Data()) == store.policy,
                  !(store.selectedProfile?.sourceBundleJSON ?? "").contains("only-a.invalid") else {
                throw AppSetupError("Experiment switch changed persisted settings or rules")
            }
        } else if ProcessInfo.processInfo.arguments.contains("--chain-profile-cold") {
            guard store.selectedProfile?.name == "Fixture B", let nodes = store.sourceBundle.files["nodes.txt"] else {
                throw AppSetupError("Cold B sources missing")
            }
            for content in [nodes, try store.connectionOptions().configContent] {
                guard ["fixture-b-secret", "b-exit.invalid"].allSatisfy(content.contains),
                      !["fixture-a-secret", "a-exit.invalid"].contains(where: content.contains) else {
                    throw AppSetupError("Cold B chain exit did not match B's edit")
                }
            }
        } else if cold {
            guard store.selectedProfile?.name == "Fixture B", store.selectedProfile?.pendingApply == false,
                  TailscaleEndpointProvisioning.describe(in: try store.systemConnectionOptions().configContent) == TailscaleEndpointOptions() else {
                throw AppSetupError("Cold B profile has unexpected endpoint options")
            }
        } else {
            let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
            for (file, name) in [("mixed.sakamoto", "Fixture B"), ("groups.sakamoto", "Fixture A")] {
                var profile = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent(file)))
                profile.name = name
                try store.installProfile(profile)
                if name == "Fixture B", ProcessInfo.processInfo.arguments.contains("--source-form-profile") {
                    defaults.set(store.selectedProfile?.sourceBundleJSON, forKey: "sourceBundleBefore")
                }
                if ProcessInfo.processInfo.arguments.contains("--experiment-profile") {
                    let directory = try ExperimentFiles.directory(profileID: profile.id, root: AppPaths.sharedDirectory())
                    let history = #"{"history":[{"domain":"only-a.invalid","state":"failed","time":"test","detail":"fixture","direct":{"reachable":false}}]}"#
                    let isB = name == "Fixture B"
                    try Data((isB ? "{" : history).utf8).write(to: directory.appendingPathComponent("experiment-runtime.json"), options: .atomic)
                    if isB {
                        defaults.set(try JSONEncoder().encode(store.experimentSettings), forKey: "experimentBefore")
                        defaults.set(try JSONEncoder().encode(store.policy), forKey: "policyBefore")
                    }
                }
            }
            try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions(hostname: "phone-a", acceptRoutes: true, exitNode: "exit-a", exitNodeAllowLANAccess: true))
        }
        return store
    }

    final class TailnetApplyReceiver: TunnelControlling {
        func ping() async throws -> TunnelStateSnapshot { TunnelStateSnapshot(serviceState: .running) }
        func reload(configContent: String) async throws {
            guard TailscaleEndpointProvisioning.describe(in: configContent) == TailscaleEndpointOptions() else {
                throw AppSetupError("B received unexpected endpoint options")
            }
        }
        func connect(options: TunnelStartOptions) async throws { try await reload(configContent: options.configContent) }
        func disconnect() async throws {}
        func observations() -> AsyncStream<TunnelObservation> { AsyncStream { $0.finish() } }
    }

    static func makeStore() -> ConfigStore {
        let suite = "sakamoto.editor-ui-tests"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        let links = ["trojan://test@a.example:443#Review%20A", "trojan://test@b.example:443#Review%20B"]
        do {
            try store.commitNodesSources(NodesSourcesBook(nodes: try links.map(ConfigModel.validateNode)))
            let config = #"{"log":{"level":"warn"},"dns":{"servers":[{"type":"udp","tag":"dns-direct","server":"1.1.1.1"}]},"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"],"auto_route":true,"stack":"mixed"}],"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":["Review A","Review B"],"default":"Review A"},{"type":"trojan","tag":"Review A","server":"a.example","server_port":443,"password":"test","tls":{"enabled":true}},{"type":"trojan","tag":"Review B","server":"b.example","server_port":443,"password":"test","tls":{"enabled":true}},{"type":"direct","tag":"direct"}],"route":{"final":"MainProxy","auto_detect_interface":true}}"#
            var fixtureConfig = config
            if ProcessInfo.processInfo.arguments.contains("--chain-ui-test"),
               var root = try JSONSerialization.jsonObject(with: Data(config.utf8)) as? [String: Any],
               var rows = root["outbounds"] as? [[String: Any]],
               let index = rows.firstIndex(where: { $0["tag"] as? String == "Review B" }) {
                rows[index]["type"] = "vless"
                rows[index].removeValue(forKey: "password")
                rows[index]["uuid"] = "00000000-0000-4000-8000-000000000001"
                root["outbounds"] = rows
                fixtureConfig = String(decoding: try JSONSerialization.data(withJSONObject: root), as: UTF8.self)
            }
            if ProcessInfo.processInfo.arguments.contains("--reality-group-ui-test") {
                fixtureConfig = try ProxyGroupEditing.applying([
                    ProxyGroupEdit(tag: "RealityAuto", type: "urltest", members: ["Review A", "Review B"]),
                    ProxyGroupEdit(tag: "ManualPick", members: ["Review A", "Review B"])
                ], to: config)
            }
            try store.installProfile(TunnelProfile(name: "Editor review", config: fixtureConfig, sourceBundleJSON: String(decoding: try JSONEncoder().encode(store.sourceBundle), as: UTF8.self)))
        } catch { assertionFailure("Editor fixture could not be prepared: \(error.localizedDescription)") }
        return store
    }
}
#endif
