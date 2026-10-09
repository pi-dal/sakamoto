import NetworkExtension
import XCTest
import SakamotoKit
@testable import SakamotoNE

@MainActor
final class SystemTunnelColdStartTests: XCTestCase {
    private func stale() -> NSError {
        NSError(domain: NEVPNErrorDomain, code: NEVPNError.configurationStale.rawValue)
    }

    func testFirstRejectedStaleStartReloadsAndSucceedsWithoutAnotherTap() async throws {
        var native: ServiceState = .stopped
        var starts = 0, reloads = 0
        let result = try await SystemTunnelTransition.start(state: { native }, isCurrent: { true }, start: {
            starts += 1
            if starts == 1 { throw self.stale() }
            native = .starting
        }, reload: { reloads += 1 }, pause: { XCTFail("Connecting is already acknowledged") })
        XCTAssertEqual(result, .starting)
        XCTAssertEqual(starts, 2)
        XCTAssertEqual(reloads, 1)
    }

    func testReloadFindingRunningDoesNotSubmitAnotherStart() async throws {
        var native: ServiceState = .stopped
        var starts = 0
        let result = try await SystemTunnelTransition.start(state: { native }, isCurrent: { true }, start: {
            starts += 1
            throw self.stale()
        }, reload: { native = .running }, pause: { XCTFail("An existing connection must not wait") })
        XCTAssertEqual(result, .running)
        XCTAssertEqual(starts, 1)
    }

    func testASecondStaleRejectionIsPropagatedWithoutLooping() async throws {
        var starts = 0, reloads = 0
        do {
            _ = try await SystemTunnelTransition.start(state: { .stopped }, isCurrent: { true }, start: {
                starts += 1
                throw self.stale()
            }, reload: { reloads += 1 })
            XCTFail("Expected the second rejection")
        } catch {
            XCTAssertEqual((error as NSError).domain, NEVPNErrorDomain)
            XCTAssertEqual((error as NSError).code, NEVPNError.configurationStale.rawValue)
        }
        XCTAssertEqual(starts, 2)
        XCTAssertEqual(reloads, 1)
    }

    func testOtherFailuresAreNotAutomaticallyRetried() async throws {
        let errors = [
            NEVPNError.configurationInvalid, .configurationDisabled, .connectionFailed,
            .configurationReadWriteFailed, .configurationUnknown,
        ].map { NSError(domain: NEVPNErrorDomain, code: $0.rawValue) } + [
            NSError(domain: "Other", code: NEVPNError.configurationStale.rawValue),
        ]
        for expected in errors {
            var starts = 0, reloads = 0
            do {
                _ = try await SystemTunnelTransition.start(state: { .stopped }, isCurrent: { true }, start: {
                    starts += 1
                    throw expected
                }, reload: { reloads += 1 })
                XCTFail("Expected a non-retryable error")
            } catch {
                XCTAssertEqual((error as NSError).domain, expected.domain)
                XCTAssertEqual((error as NSError).code, expected.code)
            }
            XCTAssertEqual(starts, 1)
            XCTAssertEqual(reloads, 0)
        }
    }

    func testReloadFailureDoesNotSubmitRetry() async throws {
        var starts = 0
        do {
            _ = try await SystemTunnelTransition.start(state: { .stopped }, isCurrent: { true }, start: {
                starts += 1
                throw self.stale()
            }, reload: { throw SystemTunnelControl.ControlError.needsApply })
            XCTFail("Expected the reload validation error")
        } catch SystemTunnelControl.ControlError.needsApply {}
        XCTAssertEqual(starts, 1)
    }

    func testNewOffDuringReloadPreventsRetry() async throws {
        let gate = SystemTunnelRequestGate()
        let on = gate.begin()
        var native: ServiceState = .stopped
        var starts = 0
        let result = try await SystemTunnelTransition.start(state: { native }, isCurrent: { gate.isCurrent(on) }, start: {
            starts += 1
            throw self.stale()
        }, reload: {
            _ = gate.begin()
            native = .stopping
        })
        XCTAssertEqual(result, .stopping)
        XCTAssertEqual(starts, 1)
    }

    func testColdStartWaitsForDelayedConnectingWithoutSubmittingAgain() async throws {
        var native: ServiceState = .stopped
        var starts = 0, reads = 0
        let result = try await SystemTunnelTransition.start(state: { native }, isCurrent: { true }, start: {
            starts += 1
            // NE has accepted Start, but its status has not changed yet.
        }, reload: { XCTFail("An accepted Start must not reload or retry") }, pause: {
            XCTAssertEqual(starts, 1)
            reads += 1
            if reads == 3 { native = .starting }
        })
        XCTAssertEqual(result, .starting)
        XCTAssertEqual(reads, 3)
        XCTAssertEqual(starts, 1)
    }

    func testFastCompletionReturnsRunningRatherThanOverwritingItWithStarting() async throws {
        let result = try await SystemTunnelTransition.start(state: { .running }, isCurrent: { true }, start: {
            XCTFail("An existing connection must not be restarted")
        }, reload: { XCTFail("An existing connection must not reload") })
        XCTAssertEqual(result, .running)
        var native: ServiceState = .stopped
        let connected = try await SystemTunnelTransition.start(state: { native }, isCurrent: { true }, start: {
            native = .running
        }, reload: { XCTFail("An accepted Start must not retry") })
        XCTAssertEqual(connected, .running)
    }

    func testSlowAcknowledgementRemainsUnconfirmedWithoutRetry() async throws {
        var starts = 0
        let result = try await SystemTunnelTransition.start(state: { .stopped }, isCurrent: { true }, start: {
            starts += 1
        }, reload: { XCTFail("An accepted Start must not retry") }, acknowledgementTimeout: 0,
        pause: { XCTFail("An expired acknowledgement wait must return") })
        XCTAssertEqual(result, .starting)
        XCTAssertNotEqual(result, .running)
        XCTAssertEqual(starts, 1)
    }

    func testOffCancelsAcceptedStartBeforeNativeConnectingAppears() async throws {
        let gate = SystemTunnelRequestGate()
        let on = gate.begin()
        var native: ServiceState = .stopped
        var starts = 0, stops = 0
        let result = try await SystemTunnelTransition.start(state: { native }, isCurrent: { gate.isCurrent(on) }, start: {
            starts += 1
            gate.didSubmitStart()
        }, reload: { XCTFail("An accepted Start must not retry") }, pause: {
            let off = gate.begin()
            _ = try gate.submit(off, state: { native }) {
                try SystemTunnelTransition.submit(.disconnect, state: { gate.actionState(observed: native) }, start: {}, stop: {
                    stops += 1
                    native = .stopping
                    gate.didSubmitStop()
                })
            }
        })
        XCTAssertEqual(result, .stopping)
        XCTAssertEqual(starts, 1)
        XCTAssertEqual(stops, 1)
    }

    func testRepeatedOnWhileNativeStatusLagsDoesNotSubmitAgain() async throws {
        let gate = SystemTunnelRequestGate()
        var native: ServiceState = .stopped
        var starts = 0
        gate.didSubmitStart()
        let pending = gate.actionState(observed: native)
        let result = try SystemTunnelTransition.submit(.connect, state: { pending }, start: {
            starts += 1
        }, stop: { XCTFail("Repeated On must not disconnect") })
        XCTAssertEqual(result, .starting)
        XCTAssertEqual(starts, 0)
        native = .starting
        XCTAssertEqual(gate.actionState(observed: native), .starting)
        // Once NE has acknowledged the request, a real startup failure wins.
        native = .stopped
        XCTAssertEqual(gate.actionState(observed: native), .stopped)
    }

    func testCancellationDuringAcknowledgementDoesNotResubmit() async throws {
        var paused: CheckedContinuation<Void, Never>?
        var starts = 0
        let waiting = Task {
            try await SystemTunnelTransition.start(state: { .stopped }, isCurrent: { true }, start: {
                starts += 1
            }, reload: { XCTFail("An accepted Start must not retry") }, pause: {
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
        XCTAssertEqual(starts, 1)
    }
}
