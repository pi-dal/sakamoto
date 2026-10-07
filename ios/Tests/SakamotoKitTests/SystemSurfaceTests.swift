import XCTest
@testable import SakamotoKit

final class SystemSurfaceTests: XCTestCase {
    func testOldReachableDoesNotOverrideDisconnectedSystemState() {
        let now = Date()
        let saved = SystemSurfaceSnapshot(serviceState: .running, phase: .reachable, selectedNode: "Node", updatedAt: now)
        XCTAssertEqual(saved.reconciled(with: .stopped, at: now).phase, .disconnected)
        let latency = SystemSurfaceSnapshot(serviceState: .running, phase: .reachable, latencyMS: 42, measuredAt: now)
        XCTAssertNil(latency.reconciled(with: .stopped, at: now).latencyMS)
        XCTAssertEqual(saved.reconciled(with: .unavailable, at: now).phase, .unavailable)
    }
    func testReachabilityAndLatencyExpireIndependently() {
        let now = Date()
        let saved = SystemSurfaceSnapshot(serviceState: .running, phase: .reachable, latencyMS: 42,
                                          measuredAt: now.addingTimeInterval(-121), updatedAt: now.addingTimeInterval(-121))
        XCTAssertFalse(saved.latencyIsFresh(at: now))
        XCTAssertEqual(saved.reconciled(with: .running, at: now).phase, .tunRunning)
        let newSession = SystemSurfaceSnapshot(serviceState: .stopped, phase: .disconnected, updatedAt: now)
        XCTAssertEqual(newSession.reconciled(with: .running, at: now).phase, .tunRunning)
        let future = SystemSurfaceSnapshot(serviceState: .running, phase: .reachable, latencyMS: 42,
                                           measuredAt: now.addingTimeInterval(1), updatedAt: now.addingTimeInterval(1))
        XCTAssertFalse(future.latencyIsFresh(at: now))
        XCTAssertEqual(future.reconciled(with: .running, at: now).phase, .tunRunning)
    }
    func testToggleUsesClickTimeStateAndDoesNotReverseAnInflightRequest() {
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .stopped), .start)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .running), .stop)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .starting), .busy)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .stopping), .busy)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .connect, state: .running), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .disconnect, state: .stopped), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .disconnect, state: .starting), .stop)
    }
    func testSnapshotPersistsOnlyDisplayFields() throws {
        let suite = "sakamoto.surface.test.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let snapshot = SystemSurfaceSnapshot(serviceState: .running, phase: .tunRunning, selectedNode: "Node", routingMode: "Rule")
        SystemSurfaceStore.write(snapshot, defaults: defaults)
        XCTAssertEqual(SystemSurfaceStore.read(defaults: defaults), snapshot)
        let json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(snapshot)) as? [String: Any])
        XCTAssertFalse(json.keys.contains("configContent"))
        XCTAssertFalse(json.keys.contains("authKey"))
    }
}
