import XCTest
import Libbox
import SakamotoKit
@testable import Sakamoto

@MainActor
final class NativeLearningIntegrationTests: XCTestCase {
    func testCorrelatedFailuresVerifyCommitAndRollbackWithoutReload() async throws {
        var evidence: [[String: Any]] = []
        for outcome in ["verified", "direct_recovered", "not_improved", "rolled_back", "network_changed", "cancelled"] {
            let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
            defer { try? FileManager.default.removeItem(at: root) }
            let profile = UUID().uuidString
            var settings = ExperimentSettings(); settings.mode = .auto; settings.threshold = 3
            let prober = LearningFixtureProber(outcome: outcome)
            var updates: [String] = []
            let runner = try ProviderExperimentRunner(profileID: profile, settings: settings,
                content: #"{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"MainProxy","outbounds":["direct"]}],"route":{"final":"direct","rules":[{"rule_set":["rs-proxy"],"outbound":"MainProxy","action":"route"}]}}"#,
                root: root, prober: prober) { updates.append($0) }
            runner.availability(true); runner.clashMode("rule")
            runner.updateGroups([.init(tag: "MainProxy", selected: "exit", items: [.init(tag: "exit", delay: 10, testedAt: Int64(Date().timeIntervalSince1970))])])
            runner.observeLogs([]) // initial replay is ignored
            // A log before its matching connection must also be correlated.
            let log = "connection: open connection to origin.example:80 using outbound/direct[direct]: dial tcp 192.0.2.1:80: connection refused"
            runner.observeLogs([log])
            runner.observeConnection(id: "fresh", domain: "origin.example", destination: "192.0.2.1:80", network: "tcp", outbound: "direct", rule: "", closed: false, downlink: 0)
            if outcome == "network_changed" { runner.networkChanged() }
            if outcome == "cancelled" { await runner.quiesce() }
            let end = Date().addingTimeInterval(5)
            var state = try ExperimentFiles.read(profileID: profile, root: root)
            while state.history.isEmpty && Date() < end {
                try await Task.sleep(nanoseconds: 25_000_000)
                state = try ExperimentFiles.read(profileID: profile, root: root)
            }
            XCTAssertEqual(state.history.last?.state, ["network_changed", "cancelled"].contains(outcome) ? "skipped" : outcome)
            XCTAssertEqual(state.learned, outcome == "verified" ? ["origin.example"] : [])
            XCTAssertEqual(updates.count, outcome == "verified" ? 1 : outcome == "rolled_back" ? 2 : 0)
            if outcome == "rolled_back" { XCTAssertEqual(updates.last, "[]") }
            evidence.append(["case": outcome, "updates": updates.count, "learned": state.learned])
            await runner.quiesce()
        }
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: evidence, options: .prettyPrinted), uniformTypeIdentifier: "public.json")
        attachment.name = "mobile-learning-transactions"; attachment.lifetime = .keepAlways; add(attachment)
    }

    func testOldRuntimeStateStillDecodes() throws {
        let raw = #"{"learned":[],"status":"Stopped","updatedAt":0}"#
        XCTAssertTrue(try JSONDecoder().decode(ExperimentRuntimeState.self, from: Data(raw.utf8)).history.isEmpty)
    }
}

private final class LearningFixtureProber: NSObject, MobileexperimentLearningProberProtocol {
    let outcome: String
    init(outcome: String) { self.outcome = outcome }
    func probeOrigin(_ domain: String?, port: Int32, outbound: String?, timeoutMillis: Int32, ret0_: UnsafeMutablePointer<Int32>?) throws {
        XCTAssertEqual(domain, "origin.example"); XCTAssertEqual(port, 80)
        XCTAssertTrue(timeoutMillis > 0 && timeoutMillis <= 8000)
        ret0_?.pointee = outbound == "direct" ? (outcome == "direct_recovered" ? 200 : 502) : outbound == "" ? (outcome == "rolled_back" ? 502 : 200) : (outcome == "not_improved" ? 403 : 200)
    }
}
