import Foundation

/// Shared Go repair result. Core liveness and end-to-end reachability are
/// separate facts; queued system starts always remain unverified.
public struct CoreRepairResult: Codable, Equatable, Sendable {
    public var state: String
    public var action: String
    public var verified: Bool
    public var detail: String
    public init(state: String, action: String = "none", verified: Bool = false, detail: String) {
        self.state = state; self.action = action; self.verified = verified; self.detail = detail
    }
}
