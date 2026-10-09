import XCTest
import SakamotoKit
@testable import Sakamoto

@MainActor
final class OnDemandDomainImportTests: XCTestCase {
    func testSelectedConfAndIncludesUseProxyTargetsOnly() throws {
        var bundle = SourceBundle()
        bundle.mainConf = "conf/main.conf"
        bundle.files = [
            "conf/main.conf": "[General]\ninclude=extra.conf\n[Rule]\nDOMAIN,main.example,PROXY\nDOMAIN,direct.example,DIRECT\nFINAL,DIRECT",
            "conf/extra.conf": "[Rule]\nDOMAIN-SUFFIX,extra.example,CustomGroup\nIP-CIDR,192.0.2.0/24,PROXY",
            "conf/unselected.conf": "[Rule]\nDOMAIN,unrelated.example,PROXY"
        ]
        let imported = try OnDemandDomainImport.read(bundle: bundle, profile: nil)
        XCTAssertEqual(imported.domains, ["extra.example", "main.example"])
        XCTAssertEqual(imported.notes.count, 1)
    }

    func testMissingIncludeFailsWithoutInventingPartialImport() {
        var bundle = SourceBundle()
        bundle.mainConf = "main.conf"
        bundle.files = ["main.conf": "[General]\ninclude=missing.conf\n[Rule]\nDOMAIN,a.example,PROXY"]
        XCTAssertThrowsError(try OnDemandDomainImport.read(bundle: bundle, profile: nil))
    }

    func testGeneratedScalarAndArrayFieldsAndDirtySnapshot() throws {
        let profile = TunnelProfile(name: "Snapshot", config: #"{"outbounds":[{"type":"selector","tag":"PROXY"},{"type":"direct","tag":"direct"}],"route":{"rules":[{"outbound":"PROXY","domain":"one.example"},{"outbound":"PROXY","domain_suffix":["two.example","three.example"]},{"outbound":"direct","domain":"direct.example"},{"outbound":"PROXY","invert":true,"domain":"inverted.example"}]}}"#)
        XCTAssertEqual(try OnDemandDomainImport.read(bundle: SourceBundle(), profile: profile).domains, ["one.example", "three.example", "two.example"])
        var dirty = profile
        dirty.sourcesChanged = true
        XCTAssertTrue(try OnDemandDomainImport.read(bundle: SourceBundle(), profile: dirty).domains.isEmpty)
    }

    func testGeneratedCompiledRuleSetContributesProxyDomains() async throws {
        try AppServiceSetup.apply()
        let suite = "ondemand-import-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        store.commitNodesSources(NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://test@example.com:443#node")]))
        var bundle = store.sourceBundle
        bundle.mainConf = "conf/main.conf"
        bundle.files["conf/main.conf"] = "[Rule]\nDOMAIN-SUFFIX,compiled.example,PROXY\nDOMAIN,direct.example,DIRECT\nFINAL,DIRECT"
        try store.adoptSourceBundle(bundle)
        try await store.generateFromSources()
        // No conf body here: verifies actual compiled snapshot extraction.
        let imported = try OnDemandDomainImport.read(bundle: SourceBundle(), profile: store.selectedProfile)
        XCTAssertTrue(imported.domains.contains("compiled.example"))
        XCTAssertFalse(imported.domains.contains("direct.example"))
    }
}
