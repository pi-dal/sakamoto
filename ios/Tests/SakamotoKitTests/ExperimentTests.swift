import XCTest
@testable import SakamotoKit

final class ExperimentTests: XCTestCase {
    func testHostMetadataAndThresholdValidation() throws {
        let settings = try ExperimentSettings.hostMetadata("""
        {"experiment":{"mode":"auto","threshold":4,"cf_region_block":true},"fallback_enabled":true,"recover_after":3,"fallbacks":{"MainProxy":["RealityAuto","OthersAuto"]}}
        """)
        XCTAssertEqual(settings.mode, .auto)
        XCTAssertEqual(settings.threshold, 4)
        XCTAssertTrue(settings.cfRegionBlock)
        XCTAssertTrue(settings.fallbackEnabled)
        XCTAssertEqual(settings.recoverAfter, 3)
        var bad = settings; bad.threshold = 0
        XCTAssertThrowsError(try bad.validate())
        bad = settings; bad.fallbacks["MainProxy"] = ["duplicate", "duplicate"]
        XCTAssertThrowsError(try bad.validate())
    }
    func testProxyEvidenceRequiresActuallySelectedHealthyMember() {
        let main = ExperimentGroup(tag: "MainProxy", selected: "failed", items: [.init(tag: "failed", delay: 0, testedAt: 100), .init(tag: "other", delay: 20, testedAt: 100)])
        XCTAssertFalse(ExperimentGroup.selectedProxyHealthy(groups: [main], since: 90))
        let good = ExperimentGroup(tag: "MainProxy", selected: "good", items: [.init(tag: "good", delay: 20, testedAt: 100)])
        XCTAssertTrue(ExperimentGroup.selectedProxyHealthy(groups: [good], since: 90))
        XCTAssertFalse(ExperimentGroup.selectedProxyHealthy(groups: [good], since: 101))
    }
    func testFallbackFreshnessAndManualSelection() {
        let selector = ExperimentGroup(tag: "MainProxy", selected: "primary", items: [])
        let primary = ExperimentGroup(tag: "primary", selected: "p", items: [.init(tag: "p", delay: 0, testedAt: 100)])
        let backup = ExperimentGroup(tag: "backup", selected: "b", items: [.init(tag: "b", delay: 30, testedAt: 100)])
        XCTAssertEqual(ExperimentGroup.candidate(selector: "MainProxy", chain: ["primary", "backup"], groups: [selector, primary, backup], since: 100), "backup")
        XCTAssertNil(ExperimentGroup.candidate(selector: "MainProxy", chain: ["primary", "backup"], groups: [selector, primary, backup], since: 101))
        let manual = ExperimentGroup(tag: "MainProxy", selected: "manual", items: [])
        XCTAssertNil(ExperimentGroup.candidate(selector: "MainProxy", chain: ["primary", "backup"], groups: [manual, primary, backup], since: 100))
    }
    func testRecoveryRequiresConsecutiveHealthyRoundsButFallsBackImmediately() {
        var recovery = ExperimentRecovery()
        XCTAssertNil(recovery.decision(selector: "Main", chain: ["primary", "backup"], selected: "backup", healthy: "primary", required: 2))
        XCTAssertNil(recovery.decision(selector: "Main", chain: ["primary", "backup"], selected: "backup", healthy: nil, required: 2))
        XCTAssertNil(recovery.decision(selector: "Main", chain: ["primary", "backup"], selected: "backup", healthy: "primary", required: 2))
        XCTAssertEqual(recovery.decision(selector: "Main", chain: ["primary", "backup"], selected: "backup", healthy: "primary", required: 2), "primary")
        XCTAssertEqual(recovery.decision(selector: "Main", chain: ["primary", "backup"], selected: "primary", healthy: "backup", required: 2), "backup")
        XCTAssertNil(recovery.decision(selector: "Main", chain: ["primary", "backup"], selected: "manual", healthy: "primary", required: 2))
    }

    func testOptionsAndReloadWireCarryTheSelectedProfileSettings() throws {
        var settings = ExperimentSettings(); settings.mode = .auto
        let raw = String(decoding: try JSONEncoder().encode(settings), as: UTF8.self)
        let options = TunnelStartOptions(configContent: "{}", profileID: UUID().uuidString, experimentJSON: raw)
        XCTAssertEqual(TunnelStartOptions(providerConfiguration: options.providerConfiguration), options)
        XCTAssertEqual(TunnelStartOptions(startTunnelOptions: options.startTunnelOptions), options)
        XCTAssertEqual(try TunnelRequest(data: TunnelRequest.reloadProfile(options: options).encode()), .reloadProfile(options: options))
        XCTAssertEqual(try TunnelRequest(data: TunnelRequest.recoverExperiment.encode()), .recoverExperiment)
        XCTAssertEqual(try TunnelRequest(data: TunnelRequest.removeLearnedDomain("example.com").encode()), .removeLearnedDomain("example.com"))
    }
    func testRuntimeFilesStayProfileScopedAndPrivate() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        let first = UUID().uuidString, second = UUID().uuidString
        var state = ExperimentRuntimeState(); state.learned = ["example.com"]
        try ExperimentFiles.write(state, profileID: first, root: root)
        XCTAssertEqual(try ExperimentFiles.read(profileID: first, root: root).learned, ["example.com"])
        XCTAssertTrue(try ExperimentFiles.read(profileID: second, root: root).learned.isEmpty)
        XCTAssertThrowsError(try ExperimentFiles.directory(profileID: "../../escape", root: root))
    }
}
