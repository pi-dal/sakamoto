import Foundation

/// NEProviderStopReason raw values are stable across iOS versions. Unknown
/// reasons remain failures; only documented lifecycle events suspend recovery.
public enum TunnelStopPolicy {
    public enum Action: Sendable { case hold, suspend, failure }

    public static func action(reason: Int) -> Action {
        switch reason {
        case 1, 5, 9, 10: return .hold
        case 3, 4, 8, 11, 12, 13, 15, 16: return .suspend
        default: return .failure
        }
    }
}
