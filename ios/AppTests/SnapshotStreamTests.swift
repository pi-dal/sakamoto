import XCTest
@testable import Sakamoto

@MainActor
final class SnapshotStreamTests: XCTestCase {
    func testSlowConsumerGetsOnlyLatestFullSnapshot() async {
        let broadcaster = Broadcaster<[Int]>()
        let stream = broadcaster.stream(startingWith: [0])
        for value in 1...10_000 { broadcaster.yield([value]) }
        var iterator = stream.makeAsyncIterator()
        let latest = await iterator.next()
        XCTAssertEqual(latest, [10_000])
    }
}
