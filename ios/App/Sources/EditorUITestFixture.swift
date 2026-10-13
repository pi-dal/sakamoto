#if DEBUG && targetEnvironment(simulator)
import Foundation
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
    static func makeStore() -> ConfigStore {
        let suite = "sakamoto.editor-ui-tests"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        let links = ["trojan://test@a.example:443#Review%20A", "trojan://test@b.example:443#Review%20B"]
        do {
            store.commitNodesSources(NodesSourcesBook(nodes: try links.map(ConfigModel.validateNode)))
            let config = #"{"log":{"level":"warn"},"dns":{"servers":[{"type":"udp","tag":"dns-direct","server":"1.1.1.1"}]},"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"],"auto_route":true,"stack":"mixed"}],"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":["Review A","Review B"],"default":"Review A"},{"type":"trojan","tag":"Review A","server":"a.example","server_port":443,"password":"test","tls":{"enabled":true}},{"type":"trojan","tag":"Review B","server":"b.example","server_port":443,"password":"test","tls":{"enabled":true}},{"type":"direct","tag":"direct"}],"route":{"final":"MainProxy","auto_detect_interface":true}}"#
            var fixtureConfig = config
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
