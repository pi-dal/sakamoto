import XCTest
@testable import SakamotoKit

// Data-tab folding logic: the same semantics the TUI applies to its
// m.conns map and log tail (docs/tui.md Data page), verified against the
// daemon wire facts (NEW carries the record, UPDATE carries deltas,
// CLOSED marks without deleting).

final class ConnectionTableTests: XCTestCase {
    private func record(
        id: String, createdAt: Int64, closed: Bool = false,
        uplinkTotal: Int64 = 0, downlinkTotal: Int64 = 0
    ) -> ConnectionRecord {
        ConnectionRecord(
            id: id, network: "tcp", source: "1.2.3.4:5", destination: "example.com:443",
            domain: "example.com", outbound: "MainProxy", chain: ["MainProxy", "RealityAuto"],
            rule: "rs-proxy", createdAt: createdAt, closedAt: closed ? createdAt : 0,
            uplinkTotal: uplinkTotal, downlinkTotal: downlinkTotal, closed: closed
        )
    }

    func testNewInsertsRecord() {
        var table = ConnectionTable()
        table.apply([.init(kind: .new, id: "a", record: record(id: "a", createdAt: 1), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0)], reset: false)
        XCTAssertEqual(table.count, 1)
        XCTAssertEqual(table.openCount, 1)
        XCTAssertEqual(table.record(id: "a")?.displayName, "example.com")
    }

    func testUpdateDeltasAccumulateWithoutRecord() {
        var table = ConnectionTable()
        table.apply([.init(kind: .new, id: "a", record: record(id: "a", createdAt: 1, uplinkTotal: 100, downlinkTotal: 200), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0)], reset: false)
        table.apply([.init(kind: .update, id: "a", record: nil, uplinkDelta: 50, downlinkDelta: 70, closedAt: 0)], reset: false)
        let conn = table.record(id: "a")
        XCTAssertEqual(conn?.uplinkTotal, 150)
        XCTAssertEqual(conn?.downlinkTotal, 270)
        XCTAssertEqual(conn?.closed, false)
    }

    func testUpdateWithUnknownIDIsIgnored() {
        var table = ConnectionTable()
        table.apply([.init(kind: .update, id: "ghost", record: nil, uplinkDelta: 1, downlinkDelta: 1, closedAt: 0)], reset: false)
        XCTAssertEqual(table.count, 0)
    }

    func testClosedMarksWithoutDeleting() {
        var table = ConnectionTable()
        table.apply([.init(kind: .new, id: "a", record: record(id: "a", createdAt: 1), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0)], reset: false)
        table.apply([.init(kind: .closed, id: "a", record: nil, uplinkDelta: 10, downlinkDelta: 20, closedAt: 99)], reset: false)
        let conn = table.record(id: "a")
        XCTAssertEqual(conn?.closed, true)
        XCTAssertEqual(conn?.closedAt, 99)
        XCTAssertEqual(conn?.uplinkTotal, 10)
        XCTAssertEqual(table.openCount, 0)
        XCTAssertEqual(table.count, 1, "CLOSED rows survive so totals stay visible")
    }

    func testCompletedHistoryIsBoundedWithoutDroppingLiveConnections() {
        var table = ConnectionTable(closedCapacity: 2)
        table.apply([.init(kind: .new, id: "live", record: record(id: "live", createdAt: 0), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0)], reset: false)
        for time in 1...1000 {
            let id = String(time)
            table.apply([
                .init(kind: .new, id: id, record: record(id: id, createdAt: Int64(time)), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0),
                .init(kind: .closed, id: id, record: nil, uplinkDelta: 0, downlinkDelta: 0, closedAt: Int64(time))
            ], reset: false)
        }
        XCTAssertEqual(table.count, 3)
        XCTAssertEqual(table.openCount, 1)
        XCTAssertNotNil(table.record(id: "live"))
        XCTAssertNil(table.record(id: "1"))
        XCTAssertEqual(table.sortedByRecent().map(\.id), ["live", "1000", "999"])
    }

    func testResetClearsThenApplies() {
        var table = ConnectionTable()
        table.apply([.init(kind: .new, id: "a", record: record(id: "a", createdAt: 1), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0)], reset: false)
        table.apply([.init(kind: .new, id: "b", record: record(id: "b", createdAt: 2), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0)], reset: true)
        XCTAssertNil(table.record(id: "a"))
        XCTAssertEqual(table.record(id: "b")?.id, "b")
    }

    func testSortedByRecentOpensFirstNewestFirst() {
        var table = ConnectionTable()
        table.apply([
            .init(kind: .new, id: "old-open", record: record(id: "old-open", createdAt: 1), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0),
            .init(kind: .new, id: "new-open", record: record(id: "new-open", createdAt: 5), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0),
            .init(kind: .new, id: "closed", record: record(id: "closed", createdAt: 9), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0),
            .init(kind: .closed, id: "closed", record: nil, uplinkDelta: 0, downlinkDelta: 0, closedAt: 7),
        ], reset: false)
        XCTAssertEqual(table.sortedByRecent().map(\.id), ["new-open", "old-open", "closed"])
    }

    func testRemoveDropsLocally() {
        var table = ConnectionTable()
        table.apply([.init(kind: .new, id: "a", record: record(id: "a", createdAt: 1), uplinkDelta: 0, downlinkDelta: 0, closedAt: 0)], reset: false)
        table.remove(id: "a")
        XCTAssertEqual(table.count, 0)
    }
}

final class LogBufferTests: XCTestCase {
    func testAppendKeepsTailWithinCapacity() {
        var buffer = LogBuffer(capacity: 3)
        buffer.append(contentsOf: ["a", "b", "c", "d"])
        XCTAssertEqual(buffer.allLines, ["b", "c", "d"])
    }

    func testByteBudgetAndUnicodeTruncationBoundLargeMessages() {
        var buffer = LogBuffer(capacity: 100, byteCapacity: 12, lineByteCapacity: 8)
        buffer.append(contentsOf: ["abcde", "κόσ", "🙂🙂🙂"])
        XCTAssertEqual(buffer.allLines, ["🙂🙂"])
        XCTAssertLessThanOrEqual(buffer.allLines.reduce(0) { $0 + $1.utf8.count }, 12)
        buffer.append(contentsOf: [String(repeating: "x", count: 1 << 20)])
        XCTAssertEqual(buffer.allLines, ["xxxxxxxx"])
    }

    func testRecentReturnsOldestFirstSuffix() {
        var buffer = LogBuffer(capacity: 10)
        buffer.append(contentsOf: ["1", "2", "3"])
        XCTAssertEqual(buffer.recent(2), ["2", "3"])
        XCTAssertEqual(buffer.recent(10), ["1", "2", "3"])
        XCTAssertEqual(LogBuffer().recent(5), [])
    }

    func testEmptyAppendIsNoOp() {
        var buffer = LogBuffer(capacity: 2)
        buffer.append(contentsOf: [])
        XCTAssertEqual(buffer.allLines, [])
    }
}

final class FormatBytesTests: XCTestCase {
    // Byte-identical with the TUI's fmtB (internal/tui/view.go).
    func testTUIParity() {
        XCTAssertEqual(formatBytes(0), "0B")
        XCTAssertEqual(formatBytes(512), "512B")
        XCTAssertEqual(formatBytes(1024), "1.0K")
        XCTAssertEqual(formatBytes(2048), "2.0K")
        XCTAssertEqual(formatBytes(1 << 20), "1.0M")
        XCTAssertEqual(formatBytes(3 << 20), "3.0M")
    }
}
