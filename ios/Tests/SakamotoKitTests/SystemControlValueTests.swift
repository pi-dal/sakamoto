import XCTest
import SakamotoKit
@testable import SakamotoNE

@MainActor
final class SystemControlValueTests: XCTestCase {
    func testIndependentControlReadWaitsForAcceptedFirstStart() async throws {
        let acceptedAt = Date()
        var snapshot = SystemSurfaceSnapshot(serviceState: .stopped, phase: .disconnected)
            .recordingControlStart(at: acceptedAt)
        var native: ServiceState = .stopped
        var reads = 0
        let enabled = try await SystemTunnelTransition.controlValue(state: { native }, snapshot: { snapshot }, date: { acceptedAt }, pause: {
            reads += 1
            if reads == 3 {
                native = .starting
                snapshot = .init(serviceState: .starting, phase: .starting)
            }
        })
        XCTAssertTrue(enabled, "An old Disconnected read must not immediately undo the first On")
        XCTAssertEqual(reads, 3)
    }

    func testActualOffWithNoAcceptedRequestDoesNotWait() async throws {
        let enabled = try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { .init(serviceState: .stopped) }, pause: {
            XCTFail("A normal Off observation must return immediately")
        })
        XCTAssertFalse(enabled)
    }

    func testLegacyStartingDisplayCannotKeepTheControlOn() async throws {
        let enabled = try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { .init(serviceState: .starting, phase: .starting) }, pause: {
            XCTFail("Starting without an accepted-request timestamp is not a pending control action")
        })
        XCTAssertFalse(enabled)
    }

    func testExpiredOrFutureRequestDoesNotHoldOffRead() async throws {
        let now = Date()
        for age in [2.0, 30.0, -1.0] {
            let snapshot = SystemSurfaceSnapshot().recordingControlStart(at: now.addingTimeInterval(-age))
            let enabled = try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { snapshot }, date: { now }, pause: {
                XCTFail("Only a recent accepted Start may delay a control read")
            })
            XCTAssertFalse(enabled)
        }
    }

    func testAnUnacknowledgedStartExpiresWithoutPretendingToBeOn() async throws {
        var now = Date()
        let snapshot = SystemSurfaceSnapshot().recordingControlStart(at: now)
        var waits = 0
        let enabled = try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { snapshot }, date: { now }, pause: {
            waits += 1
            now = now.addingTimeInterval(2)
        })
        XCTAssertFalse(enabled)
        XCTAssertEqual(waits, 1)
    }

    func testReadHasItsOwnBoundEvenIfPendingRequestsKeepChanging() async throws {
        let snapshot = SystemSurfaceSnapshot().recordingControlStart(at: Date())
        let enabled = try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { snapshot }, timeout: 0, pause: {
            XCTFail("An expired read deadline must return")
        })
        XCTAssertFalse(enabled)
    }

    func testProviderFailureDuringWaitReturnsOff() async throws {
        let now = Date()
        var snapshot = SystemSurfaceSnapshot().recordingControlStart(at: now)
        var waits = 0
        let enabled = try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { snapshot }, date: { now }, pause: {
            waits += 1
            snapshot = .init(serviceState: .unavailable, phase: .unavailable)
        })
        XCTAssertFalse(enabled)
        XCTAssertEqual(waits, 1)
    }

    func testNewOffClearsPendingReadImmediately() async throws {
        let now = Date()
        var snapshot = SystemSurfaceSnapshot().recordingControlStart(at: now)
        var waits = 0
        let enabled = try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { snapshot }, date: { now }, pause: {
            waits += 1
            snapshot = snapshot.reconciled(with: .stopping, at: now)
        })
        XCTAssertFalse(enabled)
        XCTAssertEqual(waits, 1)
    }

    func testNativeOnWinsEvenAfterRequestWindowExpired() async throws {
        let snapshot = SystemSurfaceSnapshot().recordingControlStart(at: Date().addingTimeInterval(-30))
        for state: ServiceState in [.starting, .running] {
            let enabled = try await SystemTunnelTransition.controlValue(state: { state }, snapshot: { snapshot }, pause: {
                XCTFail("Native On must return immediately")
            })
            XCTAssertTrue(enabled)
        }
    }

    func testInvalidAndStoppingDoNotWaitOnARequestMarker() async throws {
        let snapshot = SystemSurfaceSnapshot().recordingControlStart(at: Date())
        for state: ServiceState in [.unavailable, .stopping] {
            let enabled = try await SystemTunnelTransition.controlValue(state: { state }, snapshot: { snapshot }, pause: {
                XCTFail("Invalid or stopping profiles must not delay an Off observation")
            })
            XCTAssertFalse(enabled)
        }
    }

    func testControlReadCanBeCancelled() async throws {
        let snapshot = SystemSurfaceSnapshot().recordingControlStart(at: Date())
        var paused: CheckedContinuation<Void, Never>?
        let waiting = Task {
            try await SystemTunnelTransition.controlValue(state: { .stopped }, snapshot: { snapshot }, pause: {
                await withCheckedContinuation { paused = $0 }
            })
        }
        for _ in 0..<100 {
            if paused != nil { break }
            await Task.yield()
        }
        XCTAssertNotNil(paused)
        waiting.cancel()
        paused?.resume()
        do {
            _ = try await waiting.value
            XCTFail("Expected cancellation")
        } catch is CancellationError {}
    }
}
