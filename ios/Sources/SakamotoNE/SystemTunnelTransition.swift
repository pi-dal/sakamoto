import Foundation
import NetworkExtension
import SakamotoKit

/// Submit a request without keeping the system intent alive until the VPN
/// finishes connecting. NetworkExtension/provider observations confirm it.
@MainActor
enum SystemTunnelTransition {
    /// A quick Off → On may arrive while NE is still stopping. Wait only for
    /// teardown, never for connection establishment; a newer Off cancels it.
    static func prepare(_ action: SystemTunnelAction, state: () -> ServiceState,
                        isCurrent: () -> Bool, timeout: TimeInterval = 2,
                        pause: () async throws -> Void = { try await Task.sleep(nanoseconds: 50_000_000) }) async throws -> ServiceState {
        let deadline = ProcessInfo.processInfo.systemUptime + timeout
        while true {
            try Task.checkCancellation()
            let current = state()
            guard isCurrent() else { return current }
            guard SystemTunnelPolicy.decision(for: action, state: current) == .busy else { return current }
            guard ProcessInfo.processInfo.systemUptime < deadline else { throw SystemTunnelControl.ControlError.busy }
            try await pause()
        }
    }

    /// Configuration-stale is a rejected request, so reloading and retrying
    /// once is safe. Never retry an accepted start or a provider failure.
    static func start(state: () -> ServiceState, isCurrent: () -> Bool,
                      start: () throws -> Void, reload: () async throws -> Void,
                      acknowledgementTimeout: TimeInterval = 1,
                      pause: () async throws -> Void = { try await Task.sleep(nanoseconds: 50_000_000) }) async throws -> ServiceState {
        try Task.checkCancellation()
        guard isCurrent() else { return state() }
        var submitted = false
        func submitStart() throws -> ServiceState {
            try submit(.connect, state: state, start: {
                try start()
                submitted = true
            }, stop: {})
        }
        let result: ServiceState
        do {
            result = try submitStart()
        } catch {
            let vpnError = error as NSError
            guard vpnError.domain == NEVPNErrorDomain,
                  vpnError.code == NEVPNError.configurationStale.rawValue else { throw error }
            try await reload()
            try Task.checkCancellation()
            guard isCurrent() else { return state() }
            result = try submitStart()
        }
        guard submitted else { return result }
        return try await acknowledgeStart(state: state, isCurrent: isCurrent,
                                          timeout: acknowledgementTimeout, pause: pause)
    }

    /// Give NE a brief chance to publish Connecting after a cold submission.
    /// This never waits for Connected and never resubmits a successful call.
    /// On expiry the result remains an unconfirmed request, not a failure.
    static func acknowledgeStart(state: () -> ServiceState,
                                 isCurrent: () -> Bool, timeout: TimeInterval = 1,
                                 pause: () async throws -> Void = { try await Task.sleep(nanoseconds: 50_000_000) }) async throws -> ServiceState {
        let deadline = ProcessInfo.processInfo.systemUptime + timeout
        while true {
            try Task.checkCancellation()
            let current = state()
            guard isCurrent() else { return current }
            if current == .starting || current == .running || current == .stopping { return current }
            guard ProcessInfo.processInfo.systemUptime < deadline else { return .starting }
            try await pause()
        }
    }

    /// A fresh, accepted Start may reach this independent control read before
    /// NE publishes Connecting. Wait within its two-second acknowledgement
    /// window; the result is always native state, never a synthetic On value.
    static func controlValue(state: () -> ServiceState, snapshot: () -> SystemSurfaceSnapshot,
                             date: () -> Date = Date.init, timeout: TimeInterval = 2,
                             pause: () async throws -> Void = { try await Task.sleep(nanoseconds: 50_000_000) }) async throws -> Bool {
        let deadline = ProcessInfo.processInfo.systemUptime + timeout
        while true {
            try Task.checkCancellation()
            let current = state()
            if current == .starting || current == .running { return true }
            guard current == .stopped, snapshot().hasPendingControlStart(at: date()),
                  ProcessInfo.processInfo.systemUptime < deadline else { return false }
            try await pause()
        }
    }

    static func submit(_ action: SystemTunnelAction, state: () -> ServiceState,
                       start: () throws -> Void, stop: () -> Void) throws -> ServiceState {
        let current = state()
        switch SystemTunnelPolicy.decision(for: action, state: current) {
        case .start:
            try start()
            return .starting
        case .stop:
            stop()
            return .stopping
        case .unchanged: return current
        case .busy: throw SystemTunnelControl.ControlError.busy
        }
    }
}

/// A newer intent supersedes older preference-loading work, without locking
/// out Disconnect while a Connect request is being prepared.
@MainActor
final class SystemTunnelRequestGate {
    private var latest: UInt64 = 0
    private var pendingStartDeadline: TimeInterval?
    func begin() -> UInt64 { latest &+= 1; return latest }
    func didSubmitStart() { pendingStartDeadline = ProcessInfo.processInfo.systemUptime + 2 }
    func didSubmitStop() { pendingStartDeadline = nil }
    /// NE may still report Disconnected immediately after accepting Start.
    /// A following Off must cancel that request instead of becoming a no-op.
    func actionState(observed: ServiceState) -> ServiceState {
        if observed == .starting || observed == .running || observed == .stopping {
            pendingStartDeadline = nil
            return observed
        }
        if let deadline = pendingStartDeadline, ProcessInfo.processInfo.systemUptime < deadline {
            return .starting
        }
        pendingStartDeadline = nil
        return observed
    }
    func isCurrent(_ request: UInt64) -> Bool { request == latest }
    func submit(_ request: UInt64, state: () -> ServiceState,
                operation: () throws -> ServiceState) rethrows -> ServiceState {
        guard isCurrent(request) else { return state() }
        return try operation()
    }
}
