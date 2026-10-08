import XCTest
import SakamotoKit
import Libbox
@testable import Sakamoto

@MainActor
final class ProxyChainGenerationTests: XCTestCase {
    func testValidatedChainSurvivesGenerationAndRestartAndCanBeDisabled() async throws {
        let suite = "chain-test-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        try AppServiceSetup.apply()
        let store = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        store.commitNodesSources(NodesSourcesBook(nodes: [
            try ConfigModel.validateNode("trojan://test@entry.example:443#Entry"),
            try ConfigModel.validateNode("socks://@exit.example:1080#Exit")
        ]))
        var sources = store.sourceBundle
        sources.mainConf = "conf/rules.conf"
        sources.files["conf/rules.conf"] = "[General]\ndns-server = 1.1.1.1\n[Rule]\nDOMAIN,proxy.example,PROXY\nDOMAIN,direct.example,DIRECT\nFINAL,DIRECT\n"
        try store.adoptSourceBundle(sources)
        try await store.generateFromSources()
        let compiled = try XCTUnwrap(store.selectedProfile?.files["rules/proxy.srs"])
        var inspectError: NSError?
        let inspection = MobilegenInspectRuleSetJSON(compiled, &inspectError)
        XCTAssertNil(inspectError)
        XCTAssertTrue(inspection.contains("proxy.example"))
        let base = store.content
        try store.saveProxyChain(ProxyChainSettings(enabled: true, exit: "Exit"))
        XCTAssertEqual(store.content, base, "intent must not destroy the unchained snapshot")
        let runtime = try store.connectionContent()
        let runtimeRoot = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(runtime.utf8)) as? [String: Any])
        let runtimeOutbounds = try XCTUnwrap(runtimeRoot["outbounds"] as? [[String: Any]])
        XCTAssertEqual(runtimeOutbounds.first { $0["tag"] as? String == "Exit" }?["detour"] as? String, "MainProxy")
        try await store.generateFromSources()
        XCTAssertEqual(store.selectedProfile?.proxyChain?.exit, "Exit")
        XCTAssertNoThrow(try store.connectionContent())
        let restored = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        XCTAssertEqual(restored.selectedProfile?.proxyChain?.enabled, true)
        XCTAssertNoThrow(try restored.connectionContent())
        try restored.saveProxyChain(ProxyChainSettings())
        XCTAssertNoThrow(try restored.connectionContent())
        let unchained = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(restored.connectionContent().utf8)) as? [String: Any])
        let outbounds = try XCTUnwrap(unchained["outbounds"] as? [[String: Any]])
        XCTAssertNil(outbounds.first { $0["tag"] as? String == "Exit" }?["detour"])

        // A host package may already contain its chain without iOS intent.
        // Import it, disable it, relaunch, then choose a different upstream.
        var imported = try XCTUnwrap(store.selectedProfile)
        imported.id = UUID().uuidString
        imported.proxyChain = nil
        imported.config = try ProxyChainSettings(enabled: true, exit: "Exit").applying(to: imported.config)
        try restored.installProfile(imported)
        XCTAssertEqual(ProxyChainSettings.importedChain(in: restored.content)?.exit, "Exit")
        try restored.saveProxyChain(ProxyChainSettings())
        XCTAssertNil(ProxyChainSettings.importedChain(in: restored.content))
        XCTAssertNoThrow(try restored.connectionContent())
        let reloaded = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        XCTAssertNil(ProxyChainSettings.importedChain(in: reloaded.content))
        try reloaded.saveProxyChain(ProxyChainSettings(enabled: true, upstream: "Entry", exit: "Exit"))
        XCTAssertEqual(ProxyChainSettings.importedChain(in: try reloaded.connectionContent()), ProxyChainSettings(enabled: true, upstream: "Entry", exit: "Exit"))
    }
}
