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
    func testControlSwitchOnStartsVPNAndRepeatedOnDoesNotStopIt() {
        let action = SystemTunnelPolicy.action(enabled: true)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: .stopped), .start)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: .unavailable), .start)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: .starting), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: .running), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: .stopping), .busy)
    }
    func testControlSwitchOffStopsVPNAndDoesNotReconnectIt() {
        let action = SystemTunnelPolicy.action(enabled: false)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: .running), .stop)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: .starting), .stop)
        for state: ServiceState in [.stopped, .unavailable, .stopping] {
            XCTAssertEqual(SystemTunnelPolicy.decision(for: action, state: state), .unchanged)
        }
    }
    func testToggleUsesClickTimeStateAndCanCancelStarting() {
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .stopped), .start)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .running), .stop)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .starting), .stop)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .connect, state: .starting), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .disconnect, state: .stopping), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .toggle, state: .stopping), .busy)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .connect, state: .running), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .disconnect, state: .stopped), .unchanged)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .disconnect, state: .starting), .stop)
    }
    func testAcceptedControlRequestDoesNotRenewProbeFreshness() {
        let now = Date()
        let old = now.addingTimeInterval(-121)
        let observed = SystemSurfaceSnapshot(serviceState: .running, phase: .reachable,
            latencyMS: 42, measuredAt: old, updatedAt: old)
        let requested = observed.recordingControlStart(at: now)
        XCTAssertEqual(requested.serviceState, .running)
        XCTAssertEqual(requested.updatedAt, old)
        XCTAssertFalse(requested.latencyIsFresh(at: now))
        XCTAssertTrue(requested.hasPendingControlStart(at: now))
        let starting = requested.reconciled(with: .starting, at: now)
        XCTAssertEqual(starting.controlStartRequestedAt, now)
        XCTAssertEqual(starting.updatedAt, old)
        XCTAssertNil(starting.latencyMS)
        for state: ServiceState in [.running, .stopped, .stopping, .unavailable] {
            XCTAssertNil(starting.reconciled(with: state, at: now).controlStartRequestedAt)
        }
    }
    func testLegacySnapshotDecodesWithoutAnAcceptedControlRequest() throws {
        let original = SystemSurfaceSnapshot(serviceState: .starting)
        var json = try XCTUnwrap(JSONSerialization.jsonObject(with: JSONEncoder().encode(original)) as? [String: Any])
        json.removeValue(forKey: "controlStartRequestedAt")
        let legacy = try JSONDecoder().decode(SystemSurfaceSnapshot.self, from: JSONSerialization.data(withJSONObject: json))
        XCTAssertNil(legacy.controlStartRequestedAt)
        XCTAssertFalse(legacy.hasPendingControlStart(at: Date()))
    }
    func testAcceptedControlRequestPersistsAcrossReaders() throws {
        let suite = "sakamoto.accepted-request.\(UUID().uuidString)"
        let first = try XCTUnwrap(UserDefaults(suiteName: suite))
        let second = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { first.removePersistentDomain(forName: suite) }
        let now = Date()
        let request = SystemSurfaceSnapshot(serviceState: .stopped).recordingControlStart(at: now)
        SystemSurfaceStore.write(request, defaults: first)
        let loaded = SystemSurfaceStore.read(defaults: second)
        XCTAssertEqual(loaded, request)
        XCTAssertTrue(loaded.hasPendingControlStart(at: now))
    }
    func testControlFailureKeepsNativeCodeAndSurvivesProviderClear() throws {
        let suite = "sakamoto.control-failure.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let error = NSError(domain: "NEVPNErrorDomain", code: 4,
            userInfo: [NSLocalizedDescriptionKey: "Failed https://example.test/path?token=private"])
        TunnelDiagnostics.recordControlFailure(error, defaults: defaults)
        TunnelDiagnostics.clear(defaults: defaults)
        let saved = try XCTUnwrap(TunnelDiagnostics.latestControlFailure(defaults: defaults))
        XCTAssertEqual(saved.stage, "VPN control")
        XCTAssertTrue(saved.message.contains("NEVPNErrorDomain [4]"))
        XCTAssertTrue(saved.message.contains("[redacted URL]"))
        XCTAssertFalse(saved.message.contains("example.test"))
        XCTAssertFalse(saved.message.contains("token="))
    }
    func testControlFailureRetainsOnlyTheLatestRejection() throws {
        let suite = "sakamoto.control-latest.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        TunnelDiagnostics.recordControlFailure(NSError(domain: "First", code: 1), defaults: defaults)
        TunnelDiagnostics.recordControlFailure(NSError(domain: "Second", code: 2), stage: "Control status read", defaults: defaults)
        let saved = try XCTUnwrap(TunnelDiagnostics.latestControlFailure(defaults: defaults))
        XCTAssertEqual(saved.stage, "Control status read")
        XCTAssertTrue(saved.message.contains("Second [2]"))
        XCTAssertFalse(saved.message.contains("First [1]"))
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
