import XCTest
import SakamotoKit
@testable import Sakamoto

@MainActor
final class NodeGroupEditingTests: XCTestCase {
    private func setup() async throws -> (ConfigStore, UserDefaults, String) {
        let suite = "node-group-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        let store = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        try AppServiceSetup.apply()
        store.commitNodesSources(NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://test@a.example:443#A"), try ConfigModel.validateNode("trojan://test@b.example:443#B")]))
        var sources = store.sourceBundle
        sources.mainConf = "conf/rules.conf"
        sources.files["conf/rules.conf"] = "[General]\ndns-server = 1.1.1.1\n[Rule]\nFINAL,PROXY\n"
        try store.adoptSourceBundle(sources)
        try await store.generateFromSources()
        return (store, defaults, suite)
    }
    func testNodeAndGroupOverridesSurviveGenerationAndRestart() async throws {
        let (store, defaults, suite) = try await setup()
        defer { defaults.removePersistentDomain(forName: suite) }
        var node = try XCTUnwrap(ProxyGroupEditing.outbounds(store.content).first { $0["tag"] as? String == "A" })
        node["server"] = "edited.example"; node["server_port"] = 8443
        let json = String(decoding: try JSONSerialization.data(withJSONObject: node), as: UTF8.self)
        try store.saveNodeSettings(tag: "A", nodeJSON: json, expectedProfile: store.selectedProfileID, expectedContent: store.content)
        try store.saveProxyGroup(ProxyGroupEdit(tag: "Custom", members: ["B", "A"], defaultMember: "B"), expectedProfile: store.selectedProfileID, expectedContent: store.content)
        try store.saveProxyGroup(ProxyGroupEdit(tag: "MainProxy", members: ["Custom"]), expectedProfile: store.selectedProfileID, expectedContent: store.content)
        try await store.generateFromSources()
        let restored = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        XCTAssertEqual(ProxyGroupEditing.outbounds(restored.content).first { $0["tag"] as? String == "A" }?["server"] as? String, "edited.example")
        XCTAssertEqual(ProxyGroupEditing.groups(restored.content).first { $0.tag == "MainProxy" }?.members, ["Custom"])
        XCTAssertEqual(ProxyGroupEditing.groups(restored.content).first { $0.tag == "Custom" }?.members, ["B", "A"])
        XCTAssertNoThrow(try restored.connectionContent())
        try await restored.generateFromSources()
        XCTAssertEqual(restored.selectedProfile?.nodeOverrides?.count, 1)
        XCTAssertEqual(restored.selectedProfile?.proxyGroups?.count, 2)
    }
    func testInvalidAndStaleEditsLeaveValidatedSnapshotUntouched() async throws {
        let (store, defaults, suite) = try await setup()
        defer { defaults.removePersistentDomain(forName: suite) }
        let oldContent = store.content, oldProfile = store.selectedProfileID
        XCTAssertThrowsError(try store.saveProxyGroup(ProxyGroupEdit(tag: "MainProxy", members: ["missing"]), expectedProfile: oldProfile, expectedContent: oldContent))
        XCTAssertEqual(store.content, oldContent)
        var node = try XCTUnwrap(ProxyGroupEditing.outbounds(oldContent).first { $0["tag"] as? String == "A" })
        node["server_port"] = -1
        let json = String(decoding: try JSONSerialization.data(withJSONObject: node), as: UTF8.self)
        XCTAssertThrowsError(try store.saveNodeSettings(tag: "A", nodeJSON: json, expectedProfile: oldProfile, expectedContent: oldContent))
        XCTAssertEqual(store.content, oldContent)
        try store.saveProxyGroup(ProxyGroupEdit(tag: "MainProxy", members: ["B"]), expectedProfile: oldProfile, expectedContent: oldContent)
        let current = store.content
        XCTAssertThrowsError(try store.saveProxyGroup(ProxyGroupEdit(tag: "MainProxy", members: ["A"]), expectedProfile: oldProfile, expectedContent: oldContent))
        XCTAssertEqual(store.content, current)
        XCTAssertThrowsError(try store.saveProxyGroup(ProxyGroupEdit(tag: "MainProxy", members: ["A"]), expectedProfile: UUID().uuidString, expectedContent: current))
    }
    func testSourceEditPreservesIdentityAndClearsPriorOverride() async throws {
        let (store, defaults, suite) = try await setup()
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = ConfigModel(tunnel: HomeLifecycleTests.Tunnel(), store: store)
        let node = try XCTUnwrap(store.nodesSources.nodes.first { $0.tag == "A" })
        let json = String(decoding: try JSONSerialization.data(withJSONObject: XCTUnwrap(ProxyGroupEditing.outbounds(store.content).first { $0["tag"] as? String == "A" })), as: UTF8.self)
        try store.saveNodeSettings(tag: "A", nodeJSON: json, expectedProfile: store.selectedProfileID, expectedContent: store.content)
        try model.saveSourceNode("trojan://test@new.example:8443#A", original: node, profileID: store.selectedProfileID)
        XCTAssertEqual(store.nodesSources.nodes.first?.id, node.id)
        XCTAssertEqual(store.nodesSources.nodes.first?.addedAt, node.addedAt)
        XCTAssertNil(store.selectedProfile?.nodeOverrides?["A"])
        XCTAssertTrue(store.selectedProfile?.sourcesChanged == true)
        let updated = store.nodesSources.nodes
        XCTAssertThrowsError(try model.saveSourceNode(node.rawLink, original: node, profileID: store.selectedProfileID))
        XCTAssertEqual(store.nodesSources.nodes, updated)
        try await store.generateFromSources()
        XCTAssertEqual(ProxyGroupEditing.outbounds(store.content).first { $0["tag"] as? String == "A" }?["server"] as? String, "new.example")
        let current = try XCTUnwrap(store.nodesSources.nodes.first)
        try model.removeNode(current)
        XCTAssertFalse(store.nodesSources.nodes.contains { $0.id == node.id })
        XCTAssertEqual(store.nodesSources.nodes.count, 1)
    }
    func testDeletingNodeRepairsSavedGroupsAndRejectsEmptyGroupsBeforeMutation() async throws {
        let (store, defaults, suite) = try await setup()
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = ConfigModel(tunnel: HomeLifecycleTests.Tunnel(), store: store)
        try store.saveProxyGroup(ProxyGroupEdit(tag: "MainProxy", members: ["A", "B"], defaultMember: "A"), expectedProfile: store.selectedProfileID, expectedContent: store.content)
        let a = try XCTUnwrap(store.nodesSources.nodes.first { $0.tag == "A" })
        XCTAssertThrowsError(try model.saveSourceNode("trojan://test@a.example:443#Renamed", original: a, profileID: store.selectedProfileID))
        try model.removeNode(a)
        XCTAssertEqual(store.selectedProfile?.proxyGroups?.first?.members, ["B"])
        try await store.generateFromSources()
        XCTAssertEqual(ProxyGroupEditing.groups(store.content).first { $0.tag == "MainProxy" }?.members, ["B"])
        let before = store.content, book = store.nodesSources
        XCTAssertThrowsError(try model.removeNode(XCTUnwrap(book.nodes.first)))
        XCTAssertEqual(store.nodesSources, book)
        XCTAssertEqual(store.content, before)
    }
    func testGroupCycleThroughNodeDetourAndEndpointIsRejected() throws {
        let content = #"{"outbounds":[{"tag":"Node","type":"socks","server":"x","server_port":1080,"detour":"Main"},{"tag":"Main","type":"selector","outbounds":["direct"]},{"tag":"direct","type":"direct"}],"endpoints":[{"tag":"tail","type":"tailscale"}],"route":{"final":"Main"},"future":{"keep":true}}"#
        XCTAssertThrowsError(try ProxyGroupEditing.applying([ProxyGroupEdit(tag: "Main", members: ["Node"])], to: content))
        XCTAssertThrowsError(try ProxyGroupEditing.applying([ProxyGroupEdit(tag: "tail", members: ["direct"])], to: content))
        XCTAssertThrowsError(try ProxyGroupEditing.applying([ProxyGroupEdit(tag: "Main", members: ["Main"])], to: content))
        XCTAssertThrowsError(try ProxyGroupEditing.applying([ProxyGroupEdit(tag: "Main", members: ["direct", "direct"])], to: content))
        let saved = try ProxyGroupEditing.applying([ProxyGroupEdit(tag: "Main", type: "urltest", members: ["tail", "direct"], interval: "30s")], to: content)
        let root = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(saved.utf8)) as? [String: Any])
        XCTAssertEqual((root["future"] as? [String: Bool])?["keep"], true)
        XCTAssertEqual(ProxyGroupEditing.groups(saved).first?.type, "urltest")
        XCTAssertEqual(ProxyGroupEditing.groups(saved).first?.interval, "30s")
        XCTAssertThrowsError(try ProxyGroupEditing.applying([ProxyGroupEdit(tag: "Main", type: "urltest", members: ["direct"], interval: "0s")], to: content))
    }
}
