import XCTest
import SakamotoKit
@testable import SakamotoNE

@MainActor
final class SystemTunnelTransitionTests: XCTestCase {
    func testStartReturnsBeforeRunningAndCanImmediatelyBeCancelled() async throws {
        var native: ServiceState = .stopped
        let result = try SystemTunnelTransition.submit(.connect, state: { native }, start: { native = .starting }, stop: {})
        XCTAssertEqual(result, .starting)
        XCTAssertEqual(native, .starting)
        let stopped = try SystemTunnelTransition.submit(.toggle, state: { native }, start: {}, stop: { native = .stopping })
        XCTAssertEqual(stopped, .stopping)
        XCTAssertEqual(native, .stopping)
        XCTAssertEqual(SystemTunnelPolicy.decision(for: .disconnect, state: native), .unchanged)
    }

    func testOlderPreferencePreparationCannotReconnectAfterNewStop() async throws {
        let gate = SystemTunnelRequestGate()
        let connectRequest = gate.begin()
        let disconnectRequest = gate.begin()
        var native: ServiceState = .starting
        var commands: [String] = []
        _ = try gate.submit(disconnectRequest, state: { native }) {
            try SystemTunnelTransition.submit(.disconnect, state: { native }, start: {}, stop: { commands.append("stop"); native = .stopping })
        }
        let superseded = try gate.submit(connectRequest, state: { native }) {
            try SystemTunnelTransition.submit(.connect, state: { native }, start: { commands.append("start") }, stop: {})
        }
        XCTAssertEqual(superseded, .stopping)
        XCTAssertEqual(commands, ["stop"])
    }

    func testStateChangedDuringPreparationIsReadBeforeSubmitting() async throws {
        var starts = 0, stops = 0
        let connected = try SystemTunnelTransition.submit(.connect, state: { .running }, start: { starts += 1 }, stop: {})
        let disconnected = try SystemTunnelTransition.submit(.disconnect, state: { .stopped }, start: {}, stop: { stops += 1 })
        XCTAssertEqual(connected, .running)
        XCTAssertEqual(disconnected, .stopped)
        XCTAssertEqual(starts + stops, 0)
    }

    func testSwitchOnDuringStoppingStartsOnceAfterTeardown() async throws {
        for action in [SystemTunnelPolicy.action(enabled: true), .toggle] {
            var native: ServiceState = .stopping
            var starts = 0, stops = 0, waits = 0
            let prepared = try await SystemTunnelTransition.prepare(action, state: { native }, isCurrent: { true }, pause: {
                XCTAssertEqual(starts, 0)
                waits += 1
                native = .stopped
            })
            XCTAssertEqual(prepared, .stopped)
            let result = try SystemTunnelTransition.submit(action, state: { native },
                start: { starts += 1; native = .starting }, stop: { stops += 1 })
            XCTAssertEqual(result, .starting)
            XCTAssertEqual(waits, 1)
            XCTAssertEqual(starts, 1)
            XCTAssertEqual(stops, 0)
            _ = try SystemTunnelTransition.submit(.connect, state: { native },
                start: { starts += 1 }, stop: { stops += 1 })
            XCTAssertEqual(starts, 1)
            XCTAssertEqual(stops, 0)
        }
    }

    func testNewOffSupersedesOnWaitingForTeardown() async throws {
        let gate = SystemTunnelRequestGate()
        let on = gate.begin()
        var native: ServiceState = .stopping
        var starts = 0
        _ = try await SystemTunnelTransition.prepare(.connect, state: { native }, isCurrent: { gate.isCurrent(on) }, pause: {
            let off = gate.begin()
            _ = try gate.submit(off, state: { native }) {
                try SystemTunnelTransition.submit(.disconnect, state: { native }, start: { starts += 1 }, stop: {})
            }
            native = .stopped
        })
        let result = try gate.submit(on, state: { native }) {
            try SystemTunnelTransition.submit(.connect, state: { native }, start: { starts += 1 }, stop: {})
        }
        XCTAssertEqual(result, .stopped)
        XCTAssertEqual(starts, 0)
    }

    func testDisconnectDoesNotWaitForTeardown() async throws {
        let result = try await SystemTunnelTransition.prepare(.disconnect, state: { .stopping }, isCurrent: { true }, pause: {
            XCTFail("Off must not wait for the VPN to finish stopping")
        })
        XCTAssertEqual(result, .stopping)
    }

    func testConnectedOrStartingOnDoesNotWaitOrRestart() async throws {
        for native: ServiceState in [.running, .starting] {
            let current = try await SystemTunnelTransition.prepare(.connect, state: { native }, isCurrent: { true }, pause: {
                XCTFail("On must not wait for an already requested connection")
            })
            XCTAssertEqual(current, native)
            var starts = 0, stops = 0
            _ = try SystemTunnelTransition.submit(.connect, state: { current }, start: { starts += 1 }, stop: { stops += 1 })
            XCTAssertEqual(starts + stops, 0)
        }
    }

    func testTeardownTimeoutIsBoundedAndCanBeRetried() async throws {
        do {
            _ = try await SystemTunnelTransition.prepare(.connect, state: { .stopping }, isCurrent: { true }, timeout: 0, pause: {
                XCTFail("An expired teardown wait must return immediately")
            })
            XCTFail("Expected teardown timeout")
        } catch SystemTunnelControl.ControlError.busy {}
        let ready = try await SystemTunnelTransition.prepare(.connect, state: { .stopped }, isCurrent: { true })
        XCTAssertEqual(ready, .stopped)
    }

    func testCancelledTeardownWaitDoesNotStartVPN() async throws {
        var entered = false, starts = 0
        let waiting = Task {
            _ = try await SystemTunnelTransition.prepare(.connect, state: { .stopping }, isCurrent: { true }, pause: {
                entered = true
                try await Task.sleep(nanoseconds: 10_000_000_000)
            })
            _ = try SystemTunnelTransition.submit(.connect, state: { .stopped }, start: { starts += 1 }, stop: {})
        }
        for _ in 0..<100 {
            if entered { break }
            await Task.yield()
        }
        XCTAssertTrue(entered)
        waiting.cancel()
        do {
            try await waiting.value
            XCTFail("Expected cancellation")
        } catch is CancellationError {}
        XCTAssertEqual(starts, 0)
    }

    func testTenCyclesWithRepeatedIdempotentRequests() async throws {
        var native: ServiceState = .stopped
        var starts = 0, stops = 0
        for _ in 0..<10 {
            _ = try SystemTunnelTransition.submit(.connect, state: { native },
                start: { starts += 1; native = .starting }, stop: {})
            _ = try SystemTunnelTransition.submit(.connect, state: { native },
                start: { starts += 1 }, stop: {})
            native = .running
            _ = try SystemTunnelTransition.submit(.toggle, state: { native },
                start: {}, stop: { stops += 1; native = .stopping })
            _ = try SystemTunnelTransition.submit(.disconnect, state: { native },
                start: {}, stop: { stops += 1 })
            native = .stopped
        }
        XCTAssertEqual(starts, 10)
        XCTAssertEqual(stops, 10)
    }
}
