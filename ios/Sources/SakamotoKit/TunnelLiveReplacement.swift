import Foundation

/// A failed native start can already have closed the forwarding service. Restore
/// only the options witnessed by the provider before this replacement began.
public struct TunnelLiveReplacement {
    private let previous: TunnelStartOptions?

    public init(previous: TunnelStartOptions?, receipt: TunnelConfigurationReceipt?) {
        self.previous = previous.flatMap { receipt?.matches($0) == true ? $0 : nil }
    }

    @MainActor public func perform(isCurrent: () -> Bool, apply: () async throws -> Void,
                                   restore: (TunnelStartOptions) async throws -> Void) async throws {
        do {
            try await apply()
        } catch {
            if let previous, isCurrent() { try await restore(previous) }
            throw error
        }
    }
}
