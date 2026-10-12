import XCTest
@testable import SakamotoKit

final class LocationRouteTests: XCTestCase {
    private func route() -> VirtualLocationRoute { VirtualLocationRoute(points: [.init(latitude: 0, longitude: 0), .init(latitude: 0, longitude: 1), .init(latitude: 0, longitude: 3)], durationSeconds: 30) }
    func testDistanceWeightedLegsAndExactEndpoint() throws {
        let route = route(); try route.validate()
        XCTAssertEqual(route.position(elapsed: 10).longitude, 1, accuracy: 0.000001)
        XCTAssertEqual(route.position(elapsed: 20).longitude, 2, accuracy: 0.000001)
        XCTAssertEqual(route.position(elapsed: 99), route.points.last)
        XCTAssertEqual(route.position(elapsed: -10), route.points.first)
    }
    func testCrossesDateLineByShortArc() throws {
        let route = VirtualLocationRoute(points: [.init(latitude: 0, longitude: 179), .init(latitude: 0, longitude: -179)], durationSeconds: 10)
        try route.validate(); XCTAssertLessThan(route.distance, 230000)
        XCTAssertEqual(abs(route.position(elapsed: 5).longitude), 180, accuracy: 0.000001)
    }
    func testPauseResumeUsesMonotonicElapsedAndNeverRestartsAtEnd() throws {
        var player = try LocationRoutePlayback(route: route(), now: 100)
        player.setPaused(true, now: 110)
        XCTAssertEqual(player.elapsed(now: 999), 10)
        XCTAssertEqual(player.position(now: 999).longitude, 1, accuracy: 0.000001)
        player.setPaused(false, now: 1000)
        XCTAssertEqual(player.elapsed(now: 1010), 20)
        XCTAssertEqual(player.state(now: 1020), "Completed")
        player.setPaused(true, now: 1040); player.setPaused(false, now: 1050)
        XCTAssertEqual(player.elapsed(now: 1060), 30)
    }
    func testDelayedTicksCatchUpAndBackwardsClockDoesNotReverse() throws {
        let player = try LocationRoutePlayback(route: route(), now: 100)
        XCTAssertEqual(player.elapsed(now: 99), 0)
        XCTAssertEqual(player.position(now: 125).longitude, 2.5, accuracy: 0.000001)
    }
    func testRejectsInvalidPlansBeforeIPC() throws {
        for route in [VirtualLocationRoute(points: [], durationSeconds: 10), VirtualLocationRoute(points: [.init(latitude: 0, longitude: 0), .init(latitude: 0, longitude: 0)], durationSeconds: 10), VirtualLocationRoute(points: [.init(latitude: 0, longitude: 0), .init(latitude: 0, longitude: 180)], durationSeconds: 10), VirtualLocationRoute(points: [.init(latitude: .nan, longitude: 0), .init(latitude: 0, longitude: 1)], durationSeconds: 10), VirtualLocationRoute(points: route().points, durationSeconds: 9), VirtualLocationRoute(points: route().points, durationSeconds: 30, intervalSeconds: 0)] {
            XCTAssertThrowsError(try TunnelRequest.location(settings: VirtualLocationSettings(route: route)).encode())
        }
        XCTAssertThrowsError(try VirtualLocationRoute.parse("0, 0\n1, 2, 3", duration: "30", interval: "1"))
    }
    func testRouteIPCAndOldSnapshotCompatibility() throws {
        let request = TunnelRequest.location(settings: VirtualLocationSettings(enabled: true, route: route()))
        XCTAssertEqual(try TunnelRequest(data: request.encode()), request)
        let old = Data(#"{"enabled":false,"connections":0,"requests":0,"patched":0,"skipped":0,"tlsFailures":0,"lastRequest":0,"lastPatch":0,"error":""}"#.utf8)
        XCTAssertNil(try JSONDecoder().decode(VirtualLocationSnapshot.self, from: old).route)
    }
    func testDraftPersistsButRouteNeverAutoStarts() throws {
        let name = "route-test-\(UUID())"; let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let route = route()
        try VirtualLocationStore.save(VirtualLocationSettings(enabled: true, latitude: 12, longitude: 34, route: route), defaults: defaults)
        try VirtualLocationStore.saveRouteDraft(route, defaults: defaults)
        XCTAssertNil(VirtualLocationStore.load(defaults: defaults).route)
        XCTAssertEqual(VirtualLocationStore.load(defaults: defaults).latitude, 12)
        XCTAssertEqual(VirtualLocationStore.routeDraft(defaults: defaults), route)
    }
}
