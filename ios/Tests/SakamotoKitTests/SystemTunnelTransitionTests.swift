import XCTest
import SakamotoKit
@testable import SakamotoNE

@MainActor
final class SystemTunnelTransitionTests: XCTestCase {
    func testTenConnectDisconnectCyclesUseSettledObservations() async throws {
        var current: ServiceState = .stopped
        for _ in 0..<10 {
            XCTAssertEqual(SystemTunnelPolicy.decision(for: .connect, state: current), .start)
            var connecting = [ServiceState.stopped, .starting, .running]
            current = try await SystemTunnelTransition.wait(intervalNanoseconds: 0, state: { connecting.removeFirst() }, accepts: { $0 == .running })
            XCTAssertEqual(SystemTunnelPolicy.decision(for: .connect, state: current), .unchanged)
            XCTAssertEqual(SystemTunnelPolicy.decision(for: .disconnect, state: current), .stop)
            var stopping = [ServiceState.running, .stopping, .stopped]
            current = try await SystemTunnelTransition.wait(intervalNanoseconds: 0, state: { stopping.removeFirst() }, accepts: { $0 == .stopped })
        }
        XCTAssertEqual(current, .stopped)
    }

    func testTimeoutCanBeFollowedBySuccessfulNewObservation() async throws {
        do {
            _ = try await SystemTunnelTransition.wait(timeout: 0, state: { .starting }, accepts: { $0 == .running })
            XCTFail("A stuck transition must time out")
        } catch SystemTunnelControl.ControlError.timedOut {}
        let next = try await SystemTunnelTransition.wait(state: { .running }, accepts: { $0 == .running })
        XCTAssertEqual(next, .running)
    }

    func testCancelledWaitDoesNotSpinUntilDeadline() async throws {
        let task = Task { try await SystemTunnelTransition.wait(timeout: 60, state: { .starting }, accepts: { $0 == .running }) }
        task.cancel()
        do {
            _ = try await task.value
            XCTFail("Cancelled wait succeeded")
        } catch is CancellationError {}
    }
}
