import XCTest
import SakamotoKit
import Libbox
@testable import Sakamoto

@MainActor
final class ProviderExperimentTests: XCTestCase {
    private let config = """
    {"log":{"level":"warn"},"dns":{"servers":[]},"inbounds":[],"outbounds":[{"type":"direct","tag":"direct"},{"type":"socks","tag":"Exit","server":"127.0.0.1","server_port":1080}],"route":{"rules":[{"action":"reject","domain":["blocked.example"]},{"action":"route","outbound":"direct","domain":["explicit.example"]},{"action":"route","outbound":"Exit","rule_set":["rs-proxy"]}],"rule_set":[{"type":"local","tag":"rs-proxy","format":"source","path":"RULEPATH"}],"final":"direct"}}
    """
    private var root: URL!
    private func runtime() throws -> String {
        let rule = root.appendingPathComponent("proxy.json")
        try Data("{\"version\":3,\"rules\":[]}".utf8).write(to: rule)
        return config.replacingOccurrences(of: "RULEPATH", with: rule.path)
    }
    override func setUp() async throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try AppServiceSetup.apply()
    }
    override func tearDown() async throws { try? FileManager.default.removeItem(at: root) }

    func testCorrelatedFailureUpdatesActualReloadAndPrivateLearnedState() async throws {
        var settings = ExperimentSettings(); settings.mode = .auto; settings.threshold = 1
        var applied: [String] = []
        let id = UUID().uuidString
        let runner = try ProviderExperimentRunner(profileID: id, settings: settings, content: runtime(), root: root) { applied.append($0) }
        runner.availability(true); runner.clashMode("rule")
        runner.updateGroups([ExperimentGroup(tag: "MainProxy", selected: "Exit", items: [.init(tag: "Exit", delay: 10, testedAt: Int64(Date().timeIntervalSince1970))])])
        runner.observeLogs([]) // historical first batch is ignored
        runner.observeConnection(id: "1", domain: "learned.example", destination: "93.184.215.14:443", network: "tcp", outbound: "direct", rule: "", closed: false, downlink: 0)
        runner.observeLogs(["connection: open connection to learned.example:443 using outbound/direct[direct]: dial tcp 93.184.215.14:443: i/o timeout"])
        for _ in 0..<20 {
            if (try ExperimentFiles.read(profileID: id, root: root)).learned == ["learned.example"] { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(try ExperimentFiles.read(profileID: id, root: root).learned, ["learned.example"])
        XCTAssertEqual(applied.count, 1)
        XCTAssertTrue(applied[0].contains("learned.example"))
        XCTAssertLessThan(try XCTUnwrap(applied[0].range(of: "explicit.example")?.lowerBound), try XCTUnwrap(applied[0].range(of: "learned.example")?.lowerBound))
        try await runner.remove("learned.example")
        XCTAssertTrue(try ExperimentFiles.read(profileID: id, root: root).learned.isEmpty)
        XCTAssertFalse(applied.last!.contains("learned.example"))
        await runner.quiesce()
    }
    func testReloadFailureDoesNotPublishLearnedDomainAndRestoresOldConfig() async throws {
        var settings = ExperimentSettings(); settings.mode = .auto; settings.threshold = 1
        var attempts: [String] = []
        let id = UUID().uuidString, before = try runtime()
        let runner = try ProviderExperimentRunner(profileID: id, settings: settings, content: before, root: root) { candidate in
            attempts.append(candidate)
            if candidate.contains("learned.example") { throw TunnelProfile.InvalidProfile("Synthetic provider rejection") }
        }
        runner.availability(true); runner.clashMode("rule")
        runner.updateGroups([ExperimentGroup(tag: "MainProxy", selected: "Exit", items: [.init(tag: "Exit", delay: 10, testedAt: Int64(Date().timeIntervalSince1970))])])
        runner.observeLogs([])
        runner.observeConnection(id: "1", domain: "learned.example", destination: "93.184.215.14:443", network: "tcp", outbound: "direct", rule: "", closed: false, downlink: 0)
        runner.observeLogs(["connection: open connection to learned.example:443 using outbound/direct[direct]: dial tcp 93.184.215.14:443: i/o timeout"])
        for _ in 0..<20 {
            if attempts.count >= 2 { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(attempts.count, 2)
        XCTAssertEqual(attempts.last, before)
        XCTAssertTrue(try ExperimentFiles.read(profileID: id, root: root).learned.isEmpty)
        await runner.quiesce()
    }
    func testCFRegionRequiresExplicitCorrelatedEvidenceAndHealthySelectedProxy() async throws {
        var settings = ExperimentSettings(); settings.cfRegionBlock = true
        let id = UUID().uuidString
        var applied: [String] = []
        let runner = try ProviderExperimentRunner(profileID: id, settings: settings, content: runtime(), root: root) { applied.append($0) }
        runner.availability(true); runner.clashMode("rule"); runner.observeLogs([])
        let log = "connection: open connection to region.example:443 using outbound/direct[direct]: HTTP 403 Cloudflare error 1009 country blocked"
        runner.observeConnection(id: "explicit", domain: "region.example", destination: "93.184.215.14:443", network: "tcp", outbound: "direct", rule: "rs-direct", closed: false, downlink: 0)
        runner.observeLogs([log])
        XCTAssertTrue(applied.isEmpty)
        runner.updateGroups([ExperimentGroup(tag: "MainProxy", selected: "Exit", items: [.init(tag: "Exit", delay: 10, testedAt: Int64(Date().timeIntervalSince1970))])])
        runner.observeConnection(id: "allowed", domain: "region.example", destination: "93.184.215.14:443", network: "tcp", outbound: "direct", rule: "", closed: false, downlink: 0)
        runner.observeLogs(["HTTP 403 forbidden"])
        XCTAssertTrue(applied.isEmpty)
        runner.observeLogs([log])
        for _ in 0..<20 {
            if (try ExperimentFiles.read(profileID: id, root: root)).learned == ["region.example"] { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(try ExperimentFiles.read(profileID: id, root: root).learned, ["region.example"])
        XCTAssertEqual(applied.count, 1)
        await runner.quiesce()
    }

    func testRecoveryTestsPriorityCandidatesAndRequestsFreshFallback() async throws {
        var settings = ExperimentSettings(); settings.fallbackEnabled = true; settings.fallbacks = ["MainProxy": ["primary", "backup"]]
        var tested: [String] = [], selected: [String] = []
        var runner: ProviderExperimentRunner!
        let commands = ProviderExperimentRunner.RecoveryCommands(test: { tested.append($0) }, select: { group, tag in selected.append(group + ":" + tag) }, settle: {
            runner.updateGroups([
                .init(tag: "MainProxy", selected: "primary", items: []),
                .init(tag: "primary", selected: "p", items: [.init(tag: "p", delay: 0, testedAt: Int64(Date().timeIntervalSince1970) + 2)]),
                .init(tag: "backup", selected: "b", items: [.init(tag: "b", delay: 30, testedAt: Int64(Date().timeIntervalSince1970) + 2)])
            ])
        })
        var testCommands = commands; testCommands.advanceClock = {}
        runner = try ProviderExperimentRunner(profileID: UUID().uuidString, settings: settings, content: runtime(), root: root, recoveryCommands: testCommands) { _ in }
        runner.availability(true); runner.clashMode("rule")
        runner.updateGroups([.init(tag: "MainProxy", selected: "primary", items: [])])
        try runner.beginRecovery(manual: true)
        for _ in 0..<20 {
            if !selected.isEmpty { break }
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(Set(tested), Set(["primary", "backup"]))
        XCTAssertEqual(selected, ["MainProxy:backup"])
        await runner.quiesce()
    }
    func testManualSelectionDuringRecoveryIsNeverOverridden() async throws {
        var settings = ExperimentSettings(); settings.fallbackEnabled = true; settings.fallbacks = ["MainProxy": ["primary", "backup"]]
        var selections = 0
        var runner: ProviderExperimentRunner!
        let commands = ProviderExperimentRunner.RecoveryCommands(test: { _ in }, select: { _, _ in selections += 1 }, settle: {
            runner.updateGroups([.init(tag: "MainProxy", selected: "manual", items: []), .init(tag: "backup", selected: "b", items: [.init(tag: "b", delay: 10, testedAt: Int64(Date().timeIntervalSince1970) + 2)])])
        })
        var testCommands = commands; testCommands.advanceClock = {}
        runner = try ProviderExperimentRunner(profileID: UUID().uuidString, settings: settings, content: runtime(), root: root, recoveryCommands: testCommands) { _ in }
        runner.availability(true); runner.clashMode("rule")
        runner.updateGroups([.init(tag: "MainProxy", selected: "primary", items: [])])
        try runner.beginRecovery(manual: true)
        try await Task.sleep(nanoseconds: 30_000_000)
        XCTAssertEqual(selections, 0)
        await runner.quiesce()
    }

    func testColdStartRestoresOnlyCommittedLearnedRules() throws {
        var settings = ExperimentSettings(); settings.mode = .auto
        let id = UUID().uuidString
        var state = ExperimentRuntimeState(); state.learned = ["committed.example"]
        try ExperimentFiles.write(state, profileID: id, root: root)
        let options = TunnelStartOptions(configContent: try runtime(), profileID: id, experimentJSON: String(decoding: try JSONEncoder().encode(settings), as: UTF8.self))
        let prepared = try ProviderExperimentRunner.prepared(options, root: root)
        XCTAssertTrue(prepared.0.contains("committed.example"))
        var error: NSError?
        _ = LibboxCheckConfig(prepared.0, &error)
        XCTAssertNil(error)
    }
}
