import Foundation

/// The caller serializes network-settings operations and captures one runtime.
/// Revalidate after every asynchronous system call, including failed restores.
public enum TunnelNetworkSettingsTransaction {
    public enum Result: String, Sendable { case restored, recovered }

    public static func flush(
        isCurrent: () -> Bool,
        withdraw: () async throws -> Void,
        restore: () async throws -> Void,
        cancel: (Error) -> Void
    ) async throws -> Result {
        guard isCurrent() else { throw CancellationError() }
        try await withdraw()
        guard isCurrent() else { throw CancellationError() }
        do {
            try await restore()
            guard isCurrent() else { throw CancellationError() }
            return .restored
        } catch {
            guard isCurrent() else { throw CancellationError() }
            do {
                try await restore()
                guard isCurrent() else { throw CancellationError() }
                return .recovered
            } catch {
                guard isCurrent() else { throw CancellationError() }
                cancel(error)
                throw error
            }
        }
    }
}
