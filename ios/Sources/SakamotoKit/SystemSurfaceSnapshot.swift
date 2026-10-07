import Foundation

/// Public display data shared with WidgetKit. Never includes config content,
/// node URLs, server addresses, auth keys or diagnostic logs.
public struct SystemSurfaceSnapshot: Codable, Equatable, Sendable {
    public var serviceState: ServiceState
    public var phase: SessionPhase
    public var selectedNode: String
    public var routingMode: String
    public var latencyMS: Int32?
    public var measuredAt: Date?
    public var updatedAt: Date

    public init(serviceState: ServiceState = .unavailable, phase: SessionPhase = .unavailable,
                selectedNode: String = "", routingMode: String = "", latencyMS: Int32? = nil,
                measuredAt: Date? = nil, updatedAt: Date = Date()) {
        self.serviceState = serviceState
        self.phase = phase
        self.selectedNode = selectedNode
        self.routingMode = routingMode
        self.latencyMS = latencyMS
        self.measuredAt = measuredAt
        self.updatedAt = updatedAt
    }

    public func latencyIsFresh(at date: Date) -> Bool {
        guard let measuredAt, let latencyMS, latencyMS > 0 else { return false }
        return (0...120).contains(date.timeIntervalSince(measuredAt))
    }

    /// An old app/provider observation must not override current system VPN
    /// state. Reachability expires independently of the VPN being connected.
    public func reconciled(with service: ServiceState, at date: Date) -> Self {
        var result = self
        result.serviceState = service
        if serviceState != service || service != .running {
            result.latencyMS = nil
            result.measuredAt = nil
        }
        if service != .running {
            switch service {
            case .starting: result.phase = .starting
            case .stopping: result.phase = .stopping
            case .stopped: result.phase = .disconnected
            case .unavailable: result.phase = .unavailable
            case .running: break
            }
        } else if serviceState != .running || !(0...120).contains(date.timeIntervalSince(updatedAt)) {
            result.phase = .tunRunning
        }
        return result
    }
}

public enum SystemSurfaceStore {
    public static let groupIdentifier = "group.com.pidal.sakamoto"
    private static let key = "sakamoto.system-surface.v1"

    public static func read(defaults: UserDefaults? = UserDefaults(suiteName: groupIdentifier)) -> SystemSurfaceSnapshot {
        guard let data = defaults?.data(forKey: key), let snapshot = try? JSONDecoder().decode(SystemSurfaceSnapshot.self, from: data) else {
            return SystemSurfaceSnapshot()
        }
        return snapshot
    }

    public static func write(_ snapshot: SystemSurfaceSnapshot, defaults: UserDefaults? = UserDefaults(suiteName: groupIdentifier)) {
        guard let data = try? JSONEncoder().encode(snapshot) else { return }
        defaults?.set(data, forKey: key)
    }
}

public enum SystemTunnelAction: Sendable { case connect, disconnect, toggle }
public enum SystemTunnelDecision: Equatable, Sendable { case start, stop, unchanged, busy }

public enum SystemTunnelPolicy {
    public static func decision(for action: SystemTunnelAction, state: ServiceState) -> SystemTunnelDecision {
        switch action {
        case .connect:
            if state == .running { return .unchanged }
            if state == .starting || state == .stopping { return .busy }
            return .start
        case .disconnect:
            if state == .stopped || state == .unavailable { return .unchanged }
            if state == .stopping { return .busy }
            return .stop
        case .toggle:
            if state == .starting || state == .stopping { return .busy }
            return state == .running ? .stop : .start
        }
    }
}
