import XCTest
@testable import SakamotoKit

final class SystemControlHostTests: XCTestCase {
    func testMissingManagerDoesNotTurnAProviderConfirmedSessionOff() {
        let now = Date()
        let running = SystemSurfaceSnapshot(serviceState: .running, phase: .reachable,
            latencyMS: 42, measuredAt: now.addingTimeInterval(-600), updatedAt: now.addingTimeInterval(-600))
        XCTAssertTrue(running.controlDisplayEnabled(at: now))
        XCTAssertFalse(running.latencyIsFresh(at: now), "VPN state and probe freshness are independent")
        XCTAssertEqual(running.reconciled(with: .running, at: now).phase, .tunRunning)
        for state: ServiceState in [.stopped, .stopping, .unavailable] {
            let observed = running.reconciled(with: state, at: now)
            XCTAssertFalse(observed.controlDisplayEnabled(at: now), "A native terminal observation must win over the saved running state")
        }
    }

    func testAcceptedStartFallbackExpiresAndTerminalSnapshotsClearIt() {
        let now = Date()
        let accepted = SystemSurfaceSnapshot(serviceState: .stopped).recordingControlStart(at: now)
        XCTAssertTrue(accepted.controlDisplayEnabled(at: now))
        XCTAssertFalse(accepted.controlDisplayEnabled(at: now.addingTimeInterval(2)))
        let starting = accepted.reconciled(with: .starting, at: now)
        XCTAssertTrue(starting.controlDisplayEnabled(at: now.addingTimeInterval(29)))
        XCTAssertFalse(starting.controlDisplayEnabled(at: now.addingTimeInterval(30)))
        XCTAssertFalse(starting.controlDisplayEnabled(at: now.addingTimeInterval(-1)))
        for state: ServiceState in [.stopped, .stopping, .unavailable] {
            XCTAssertFalse(starting.reconciled(with: state, at: now).controlDisplayEnabled(at: now))
        }
    }

    func testFreshObservedStartingIsBoundedWithoutControlRequestMarker() {
        let now = Date()
        let starting = SystemSurfaceSnapshot(serviceState: .starting, updatedAt: now)
        XCTAssertTrue(starting.controlDisplayEnabled(at: now))
        XCTAssertFalse(starting.controlDisplayEnabled(at: now.addingTimeInterval(30)))
        XCTAssertFalse(SystemSurfaceSnapshot().controlDisplayEnabled(at: now))
    }

    func testProfileReadsDoNotOverwriteActionHostOrPreservedFailure() throws {
        let suite = "sakamoto.control-host.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        TunnelDiagnostics.recordControlFailure(NSError(domain: "NEVPNErrorDomain", code: 5), defaults: defaults)
        TunnelDiagnostics.recordControlExecution(action: "connect", stage: "Requested", defaults: defaults)
        TunnelDiagnostics.recordControlExecution(action: "connect", stage: "Submitted", state: .starting, defaults: defaults)
        TunnelDiagnostics.recordControlProfileRead(managerCount: 0, matched: false, nativeStatus: nil, defaults: defaults)
        let action = try XCTUnwrap(TunnelDiagnostics.latestControlExecution(defaults: defaults))
        let read = try XCTUnwrap(TunnelDiagnostics.latestControlProfileRead(defaults: defaults))
        XCTAssertEqual(action.stage, "Submitted")
        XCTAssertEqual(action.detail, "connect → Starting")
        XCTAssertEqual(action.host, Bundle.main.bundleIdentifier ?? "unknown")
        XCTAssertEqual(action.processID, ProcessInfo.processInfo.processIdentifier)
        XCTAssertTrue(read.detail.contains("0 profiles; sakamoto match: false"))
        TunnelDiagnostics.recordControlProfileRead(managerCount: 2, matched: true, nativeStatus: 3, defaults: defaults)
        XCTAssertEqual(TunnelDiagnostics.latestControlExecution(defaults: defaults)?.detail, action.detail)
        XCTAssertTrue(try XCTUnwrap(TunnelDiagnostics.latestControlFailure(defaults: defaults)).message.contains("NEVPNErrorDomain [5]"))
        let error = NSError(domain: "NEVPNErrorDomain", code: 4,
            userInfo: [NSLocalizedDescriptionKey: "Rejected https://private.test/path?token=secret"])
        TunnelDiagnostics.recordControlExecution(action: "connect", stage: "Failed", error: error, defaults: defaults)
        TunnelDiagnostics.recordControlFailure(NSError(domain: "LaterStatusRead", code: 1), stage: "Control status read", defaults: defaults)
        let failedAction = try XCTUnwrap(TunnelDiagnostics.latestControlExecution(defaults: defaults))
        XCTAssertEqual(failedAction.stage, "Failed")
        XCTAssertTrue(failedAction.detail.contains("NEVPNErrorDomain [4]"))
        XCTAssertTrue(failedAction.detail.contains("[redacted URL]"))
        XCTAssertFalse(failedAction.detail.contains("private.test"))
        XCTAssertFalse(failedAction.detail.contains("token="))
        XCTAssertFalse(failedAction.detail.contains("LaterStatusRead"))
    }
}
