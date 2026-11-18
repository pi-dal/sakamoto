import Foundation

/// The caller serializes network-settings operations and captures one runtime.
/// Revalidate after every asynchronous system call, including failed restores.
public enum TunnelNetworkSettingsTransaction {
    public enum Result: String, Sendable { case restored, recovered }

    public static func refresh(
        isCurrent: () -> Bool,
        reapply: () async throws -> Void
    ) async throws -> Result {
        guard isCurrent() else { throw CancellationError() }
        do {
            try await reapply()
            guard isCurrent() else { throw CancellationError() }
            return .restored
        } catch {
            guard isCurrent() else { throw CancellationError() }
            do {
                try await reapply()
                guard isCurrent() else { throw CancellationError() }
                return .recovered
            } catch {
                guard isCurrent() else { throw CancellationError() }
                throw error
            }
        }
    }
}
