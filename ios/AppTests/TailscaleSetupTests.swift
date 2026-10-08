import XCTest
import SakamotoKit
@testable import Sakamoto

@MainActor
final class TailscaleSetupTests: XCTestCase {
    private var defaults: UserDefaults!
    private var suite: String!
    override func setUp() async throws {
        suite = "sakamoto-tailnet-" + UUID().uuidString
        defaults = UserDefaults(suiteName: suite)!
        try AppServiceSetup.apply()
    }
    override func tearDown() async throws { defaults.removePersistentDomain(forName: suite) }
    private func store() -> ConfigStore { ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys()) }

    func testFirstEnableCreatesValidatedConnectableProfileAndPersists() throws {
        let store = store()
        XCTAssertTrue(store.content.isEmpty)
        try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions())
        XCTAssertTrue(store.canConnect)
        XCTAssertTrue(TailscaleEndpointProvisioning.isEnabled(in: store.content))
        XCTAssertNoThrow(try store.connectionContent())
        let restored = self.store()
        XCTAssertTrue(restored.canConnect)
        XCTAssertTrue(TailscaleEndpointProvisioning.isEnabled(in: restored.content))
        XCTAssertNoThrow(try restored.connectionContent())
        try store.setTailscaleEnabled(false, options: TailscaleEndpointOptions())
        XCTAssertFalse(TailscaleEndpointProvisioning.isEnabled(in: store.content))
        XCTAssertNoThrow(try store.connectionContent())
    }
    func testEnableOnEmptySyncProfileKeepsIdentityAndCanConnect() throws {
        let store = store(); store.ensureSourceProfile()
        let id = store.selectedProfileID
        try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions())
        XCTAssertEqual(store.selectedProfileID, id)
        XCTAssertTrue(store.canConnect)
    }
    func testGeneratingProxySourcesPreservesEndpointAndTailnetRouting() async throws {
        let store = store()
        try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions(hostname: "my-iphone"))
        store.commitNodesSources(NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://test@example.com:443#node")]))
        try await store.generateFromSources()
        XCTAssertEqual(TailscaleEndpointProvisioning.describe(in: store.content)?.hostname, "my-iphone")
        XCTAssertTrue(store.content.contains("preferred_by"))
        XCTAssertTrue(store.content.contains("sakamoto-tailnet-dns-"))
        XCTAssertNoThrow(try store.connectionContent())
    }
    func testMalformedSavedConfigIsNotReplacedWithFirstUseDefaults() {
        defaults.set("broken-json", forKey: ConfigStore.storageKey)
        let store = store()
        XCTAssertThrowsError(try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions()))
        XCTAssertEqual(store.content, "broken-json")
    }
}
