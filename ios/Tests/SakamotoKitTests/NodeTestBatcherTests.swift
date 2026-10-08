import XCTest
@testable import SakamotoKit

final class NodeTestBatcherTests: XCTestCase {
    actor Recorder {
        var active = 0, peak = 0
        var calls: [String] = []
        func test(_ tag: String) async throws {
            active += 1; peak = max(peak, active); calls.append(tag)
            defer { active -= 1 }
            try await Task.sleep(nanoseconds: 1_000_000)
            if tag == "bad" { throw NSError(domain: "test", code: 1) }
        }
        func snapshot() -> (Int, [String]) { (peak, calls) }
    }
    func testDeduplicationBoundedRequestsAndPartialFailure() async {
        func node(_ tag: String, _ kind: String = "trojan") -> NodeSnapshot { NodeSnapshot(tag: tag, status: .untested, latencyMS: 0, selected: false, kind: kind) }
        let shared = [node("a"), node("bad"), node("DIRECT", "direct"), node("MainProxy", "selector")]
        let groups = [GroupSnapshot(tag: "MainProxy", selectable: true, selectedTag: nil, items: shared), GroupSnapshot(tag: "ManualPick", selectable: true, selectedTag: nil, items: shared + (0..<8).map { node("n\($0)") })]
        let targets = NodeTestBatcher.targets(groups)
        XCTAssertEqual(targets.count, 10)
        let recorder = Recorder()
        let report = await NodeTestBatcher.run(targets) { try await recorder.test($0) }
        let snapshot = await recorder.snapshot()
        XCTAssertEqual(report.requested, 10); XCTAssertEqual(report.failed, 1)
        XCTAssertEqual(Set(snapshot.1).count, 10)
        XCTAssertLessThanOrEqual(snapshot.0, 4)
        XCTAssertGreaterThan(snapshot.0, 1)
    }
}
