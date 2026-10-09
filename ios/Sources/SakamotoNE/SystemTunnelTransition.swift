import Foundation
import SakamotoKit

/// Await live NetworkExtension observations rather than persisting a requested
/// Starting/Stopping state as the final Control Center value.
@MainActor
enum SystemTunnelTransition {
    static func wait(timeout: TimeInterval = 12, intervalNanoseconds: UInt64 = 200_000_000,
                     state: () -> ServiceState, accepts: (ServiceState) -> Bool) async throws -> ServiceState {
        let deadline = DispatchTime.now().uptimeNanoseconds + UInt64(max(0, timeout) * 1_000_000_000)
        while true {
            try Task.checkCancellation()
            let current = state()
            if accepts(current) { return current }
            let now = DispatchTime.now().uptimeNanoseconds
            if now >= deadline { throw SystemTunnelControl.ControlError.timedOut }
            try await Task.sleep(nanoseconds: min(intervalNanoseconds, deadline - now))
        }
    }
}
