import XCTest
import SakamotoKit
@testable import Sakamoto

@MainActor
final class GenerationTests: XCTestCase {
    private var defaults: UserDefaults!
    private var suite: String!
    override func setUp() async throws {
        suite = "sakamoto-generation-test-" + UUID().uuidString
        defaults = UserDefaults(suiteName: suite)!
        try AppServiceSetup.apply()
    }
    override func tearDown() async throws { defaults.removePersistentDomain(forName: suite) }
    private func store() -> ConfigStore { ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys()) }
    func testNodesOnlyImportGeneratesValidatedRuntimeAndSurvivesRelaunch() async throws {
        let store = store()
        store.commitNodesSources(NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://test@example.com:443#node")]))
        let id = store.selectedProfileID
        XCTAssertTrue(store.canGenerate)
        XCTAssertFalse(store.canConnect)
        try await store.generateFromSources()
        XCTAssertTrue(store.canConnect)
        XCTAssertEqual(store.selectedProfileID, id)
        XCTAssertEqual(store.configState, .needsReconnect)
        XCTAssertNoThrow(try store.connectionContent())
        let restored = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        XCTAssertEqual(restored.selectedProfile?.ruleRevisionID, store.selectedProfile?.ruleRevisionID)
        XCTAssertNoThrow(try restored.connectionContent())
    }
    func testFailedGenerationRetainsOldRuleSnapshotAndConfig() async throws {
        let store = store()
        store.commitNodesSources(NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://test@example.com:443#node")]))
        try await store.generateFromSources()
        let before = store.content, revision = store.selectedProfile?.ruleRevisionID
        store.commitImport(ImportedSource(displaySource: "main.conf", content: "[General]\ninclude = missing.conf\n[Rule]\nFINAL,DIRECT"))
        do { try await store.generateFromSources(); XCTFail("missing include accepted") } catch {}
        XCTAssertEqual(store.content, before)
        XCTAssertEqual(store.selectedProfile?.ruleRevisionID, revision)
        XCTAssertFalse(store.canConnect)
    }
    private struct FolderContainer: ICloudContainer {
        let url: URL
        func containerURL() -> URL? { url }
    }
    func testRealFolderFirstSyncSourcesThenDeviceGeneration() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let cloud = root.appendingPathComponent("cloud"), local = root.appendingPathComponent("local")
        try FileManager.default.createDirectory(at: cloud.appendingPathComponent("sources/current"), withIntermediateDirectories: true)
        try Data("trojan://test@example.com:443#node".utf8).write(to: cloud.appendingPathComponent("nodes.txt"))
        try Data("[General]\ninclude = child.conf\n[Rule]\nFINAL,DIRECT".utf8).write(to: cloud.appendingPathComponent("sources/current/main.conf"))
        try Data("[Rule]\nDOMAIN,blocked.example,REJECT".utf8).write(to: cloud.appendingPathComponent("sources/current/child.conf"))
        var settings = ICloudSyncSettings(); settings.enabled = true; settings.includeConf = true
        let sync = ICloudSyncStore(settings: settings, localFilesystem: LocalFilesystem(), cloudFilesystem: CloudFilesystem(), container: FolderContainer(url: cloud), localDirectory: local.path)
        let store = store(); store.ensureSourceProfile()
        let id = store.selectedProfileID
        XCTAssertTrue(store.sourceBundle.files.isEmpty)
        let result = await sync.syncNow(staging: [], directoryURL: cloud, downloadNames: ["nodes.txt"], sourceScope: id!, discoverConf: true)
        XCTAssertEqual(result.downloads.count, 3)
        var bundle = SourceBundle(); bundle.mainConf = result.discoveredMainConf ?? ""
        for file in result.downloads { bundle.files[file.name] = String(decoding: file.data, as: UTF8.self) }
        try store.adoptSourceBundle(bundle)
        try await store.generateFromSources()
        XCTAssertEqual(store.selectedProfileID, id)
        XCTAssertTrue(store.canConnect)
        XCTAssertNoThrow(try store.connectionContent())
        let next = await sync.syncNow(staging: bundle.files.map { .init(name: $0.key, data: Data($0.value.utf8)) }, directoryURL: cloud, sourceScope: id!, mainConf: bundle.mainConf, discoverConf: true)
        XCTAssertTrue(next.downloads.isEmpty)
        XCTAssertFalse(next.status.isConflict)
    }

    func testSourceAdoptionPreservesRawFilesAndDoesNotInventEmptyMetadata() throws {
        let store = store()
        var bundle = SourceBundle()
        bundle.files["nodes.txt"] = "# owner comment\r\ntrojan://test@example.com:443#node\r\n"
        bundle.files["policy.json"] = "[ { \"match\" : \"example.org\", \"action\" : \"direct\" } ]"
        try store.adoptSourceBundle(bundle)
        let sync = ICloudSyncModel(store: store, defaults: defaults)
        let payload = Dictionary(uniqueKeysWithValues: sync.buildPayload().payload.entries.map { ($0.name, String(decoding: $0.data, as: UTF8.self)) })
        XCTAssertEqual(payload, bundle.files)
        XCTAssertNil(payload["subscriptions.json"])
        XCTAssertEqual(store.nodesSources.nodes.count, 1)
        XCTAssertEqual(store.policy.rules.count, 1)
        let restored = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        XCTAssertEqual(restored.sourceBundle.files, bundle.files)
        store.commitPolicy(PolicyBook(rules: [StagedPolicyRule(match: "changed.example", action: "reject")]))
        XCTAssertEqual(store.sourceBundle.files["nodes.txt"], bundle.files["nodes.txt"])
        XCTAssertNil(store.sourceBundle.files["subscriptions.json"])
        XCTAssertNotEqual(store.sourceBundle.files["policy.json"], bundle.files["policy.json"])
    }

    func testInvalidSyncedPolicyOrSubscriptionKeepsPreviousSources() throws {
        let store = store()
        var bundle = SourceBundle()
        bundle.files["subscriptions.json"] = #"[{"name":"Feed","url":"https://example.com/feed"}]"#
        try store.adoptSourceBundle(bundle)
        XCTAssertEqual(store.nodesSources.subscriptions.first?.format, "auto")
        let before = store.sourceBundle.files
        bundle.files["policy.json"] = #"[{"match":"example.org","action":"unknown"}]"#
        XCTAssertThrowsError(try store.adoptSourceBundle(bundle))
        XCTAssertEqual(store.sourceBundle.files, before)
        bundle.files.removeValue(forKey: "policy.json")
        bundle.files["subscriptions.json"] = #"[{"name":"Feed","url":"file:///private/feed"}]"#
        XCTAssertThrowsError(try store.adoptSourceBundle(bundle))
        XCTAssertEqual(store.sourceBundle.files, before)
    }

    func testSyncedSourceBundleCanGenerateOnFreshDevice() async throws {
        let store = store()
        var bundle = SourceBundle()
        bundle.mainConf = "sources/current/main.conf"
        bundle.files = ["nodes.txt":"trojan://test@example.com:443#node", "sources/current/main.conf":"[General]\ninclude = child.conf\n[Rule]\nFINAL,DIRECT", "sources/current/child.conf":"[Rule]\nDOMAIN,blocked.example,REJECT"]
        store.commitSourceBundle(bundle)
        // Commit snapshots through the same validated source adoption path.
        store.commitNodesSources(NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://test@example.com:443#node")]))
        try await store.generateFromSources()
        XCTAssertTrue(store.canConnect)
        XCTAssertNoThrow(try store.connectionContent())
    }
}
