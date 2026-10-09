import XCTest
import SakamotoKit
@testable import Sakamoto

@MainActor
final class ConfigStoreTests: XCTestCase {
    final class EmptyKeys: TailscaleAuthKeyStoring, @unchecked Sendable {
        var hasAuthKey = false
        func readAuthKey() -> String? { nil }
        func storeAuthKey(_ key: String) throws {}
        func deleteAuthKey() throws {}
    }
    private var defaults: UserDefaults!
    private var suite: String!
    override func setUp() async throws {
        suite = "sakamoto-test-" + UUID().uuidString
        defaults = UserDefaults(suiteName: suite)!
        try AppServiceSetup.apply()
    }
    override func tearDown() async throws { defaults.removePersistentDomain(forName: suite) }

    private func profile(_ name: String, node: String = "") throws -> TunnelProfile {
        var bundle = SourceBundle()
        bundle.files["nodes.txt"] = node
        let raw = String(decoding: try JSONEncoder().encode(bundle), as: UTF8.self)
        return TunnelProfile(name: name, config: """
        {"log":{"level":"warn"},"dns":{"servers":[]},"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"],"auto_route":true}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","rules":[]}}
        """, sourceBundleJSON: raw)
    }
    func testProfileSelectionOwnsSourcesAndPersistsAcrossRelaunch() throws {
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        let first = try profile("First", node: "trojan://test@example.com:443#first")
        let second = try profile("Second")
        try store.installProfile(first)
        XCTAssertEqual(store.nodesSources.nodes.count, 1)
        try store.installProfile(second)
        XCTAssertTrue(store.nodesSources.nodes.isEmpty)
        try store.selectProfile(first.id)
        XCTAssertEqual(store.nodesSources.nodes.count, 1)
        XCTAssertTrue(store.canConnect)
        let restored = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        XCTAssertEqual(restored.selectedProfileID, first.id)
        XCTAssertEqual(restored.nodesSources.nodes.count, 1)
    }
    func testChangedSourcesAreKeptPerProfileAndCannotConnectUntilRegenerated() throws {
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        let first = try profile("First")
        let second = try profile("Second")
        try store.installProfile(first)
        let node = try ConfigModel.validateNode("trojan://test@example.com:443#changed")
        store.commitNodesSources(NodesSourcesBook(nodes: [node]))
        XCTAssertFalse(store.canConnect)
        try store.installProfile(second)
        XCTAssertTrue(store.canConnect)
        try store.selectProfile(first.id)
        XCTAssertFalse(store.canConnect)
        XCTAssertEqual(store.nodesSources.nodes.first?.rawLink, node.rawLink)
        XCTAssertThrowsError(try store.connectionContent())
    }
    func testIncompleteRuntimeImportKeepsSelectedProfile() throws {
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        let good = try profile("Good")
        try store.installProfile(good)
        let bad = TunnelProfile(name: "Bad", config: """
        {"inbounds":[{"type":"tun","tag":"tun-in"}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct","rules":[],"rule_set":[{"type":"local","tag":"missing","path":"/Mac/rules/missing.srs"}]}}
        """)
        XCTAssertThrowsError(try store.installProfile(bad))
        XCTAssertEqual(store.selectedProfileID, good.id)
        XCTAssertEqual(store.profiles.count, 1)
    }
    func testPendingApplySurvivesRelaunchUntilRunningConfigurationIsConfirmed() throws {
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        try store.installProfile(profile("Good"))
        let restored = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        XCTAssertNotEqual(restored.configState, .clean)
        restored.connectionConfirmed(content: try restored.connectionContent())
        let confirmed = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        XCTAssertEqual(confirmed.configState, .clean)
    }

    /// Optional owner-provided fixture, staged privately into the simulator
    /// Documents directory. It is never committed or printed by the test.
    func testProvisionedCompleteHostPackageUsesAppGroupRuleFiles() throws {
        let file = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0].appendingPathComponent("integration.sakamoto")
        guard FileManager.default.fileExists(atPath: file.path) else { throw XCTSkip("No private host package provisioned") }
        let profile = try TunnelProfile.decodePackage(Data(contentsOf: file))
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        try store.installProfile(profile)
        let content = try store.connectionContent()
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(content.utf8)) as? [String: Any])
        let route = try XCTUnwrap(json["route"] as? [String: Any])
        let sets = try XCTUnwrap(route["rule_set"] as? [[String: Any]])
        let paths = sets.compactMap { $0["path"] as? String }
        XCTAssertEqual(paths.count, profile.files.count)
        let group = try AppPaths.sharedDirectory().path
        XCTAssertTrue(paths.allSatisfy { $0.hasPrefix(group + "/Profiles/") && FileManager.default.fileExists(atPath: $0) })
        XCTAssertTrue(store.canConnect)
        XCTAssertLessThan(defaults.data(forKey: ConfigStore.profilesKey)?.count ?? 0, 1 << 20)
        let restored = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        XCTAssertEqual(restored.selectedProfile?.files.count, profile.files.count)
        XCTAssertNoThrow(try restored.connectionContent())
    }

    func testExperimentSettingsBelongToProfileAndTravelInConnectionOptions() throws {
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        var first = try profile("Experiment")
        first.config = first.config.replacingOccurrences(of: "\"rules\":[]", with: "\"rules\":[{\"rule_set\":[\"rs-proxy\"],\"action\":\"route\",\"outbound\":\"direct\"}],\"rule_set\":[{\"type\":\"inline\",\"tag\":\"rs-proxy\",\"rules\":[{\"domain\":[\"proxy.example\"]}]}]" )
        try store.installProfile(first)
        var settings = ExperimentSettings(); settings.mode = .auto; settings.threshold = 5; settings.cfRegionBlock = true
        try store.saveExperimentSettings(settings)
        let options = try store.connectionOptions()
        XCTAssertEqual(options.profileID, first.id)
        XCTAssertEqual(try JSONDecoder().decode(ExperimentSettings.self, from: Data(try XCTUnwrap(options.experimentJSON).utf8)), settings)
        let second = try profile("Other")
        try store.installProfile(second)
        XCTAssertFalse(store.experimentSettings.cfRegionBlock)
        try store.selectProfile(first.id)
        XCTAssertEqual(store.experimentSettings.threshold, 5)
        let restored = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        XCTAssertEqual(restored.experimentSettings, settings)
    }

    func testDERPPolicyBelongsToProfileSurvivesGenerationAndRequiresApply() async throws {
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        try store.installProfile(profile("Tailnet", node: "trojan://test@example.com:443#node"))
        try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions())
        try store.setForceTailscaleDERP(true)
        XCTAssertTrue(store.selectedProfile?.pendingApply == true)
        XCTAssertEqual(try store.connectionOptions().forceTailscaleDERP, true)
        try await store.generateFromSources()
        XCTAssertEqual(try store.connectionOptions().forceTailscaleDERP, true)
        let firstID = try XCTUnwrap(store.selectedProfileID)
        try store.installProfile(profile("Other"))
        XCTAssertNil(try store.connectionOptions().forceTailscaleDERP)
        try store.selectProfile(firstID)
        let restored = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        XCTAssertEqual(try restored.connectionOptions().forceTailscaleDERP, true)
        try restored.setForceTailscaleDERP(false)
        XCTAssertNil(try restored.connectionOptions().forceTailscaleDERP)
    }

    func testIdenticalSyncDoesNotInvalidateRunnableProfile() throws {
        let store = ConfigStore(persistence: defaults, keyStore: EmptyKeys())
        try store.installProfile(profile("Good", node: "trojan://test@example.com:443#node"))
        store.commitNodesSources(NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://test@example.com:443#node")]))
        XCTAssertTrue(store.canConnect)
        store.commitSourceBundle(store.sourceBundle)
        XCTAssertTrue(store.canConnect)
    }
}
