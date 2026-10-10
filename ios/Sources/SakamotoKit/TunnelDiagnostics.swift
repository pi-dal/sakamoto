import Foundation

public struct TunnelFailure: Codable, Sendable {
    public var stage: String
    public var message: String
    public var date: Date
}

public struct TunnelControlObservation: Codable, Sendable {
    public var stage: String
    public var host: String
    public var processID: Int32
    public var detail: String
    public var date: Date
}

/// Shared between the app and extension, including failed cold starts. Never
/// records config contents; URL authorities/query strings are redacted.
public enum TunnelDiagnostics {
    private static let key = "sakamoto.tunnel.lastFailure"
    private static let controlKey = "sakamoto.tunnel.lastControlFailure"
    private static let executionKey = "sakamoto.tunnel.lastControlExecution"
    private static let profileReadKey = "sakamoto.tunnel.lastControlProfileRead"
    private static var defaults: UserDefaults? {
        guard let group = Bundle.main.object(forInfoDictionaryKey: "SakamotoAppGroupIdentifier") as? String else { return nil }
        return UserDefaults(suiteName: group)
    }
    public static func sanitized(_ message: String) -> String {
        String(message.replacingOccurrences(of: #"[a-zA-Z][a-zA-Z0-9+.-]*://[^\s\"']+"#, with: "[redacted URL]", options: .regularExpression).prefix(600))
    }
    public static func record(stage: String, error: Error) {
        let failure = TunnelFailure(stage: stage, message: sanitized(error.localizedDescription), date: Date())
        if let data = try? JSONEncoder().encode(failure) { defaults?.set(data, forKey: key) }
    }
    /// Preserve a rejected system action across a later successful provider
    /// start. Widget callers use the shared suite, without app Info.plist keys.
    public static func recordControlFailure(_ error: Error, stage: String = "VPN control", defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) {
        let native = error as NSError
        let failure = TunnelFailure(stage: stage,
            message: sanitized("\(native.domain) [\(native.code)]: \(native.localizedDescription)"), date: Date())
        if let data = try? JSONEncoder().encode(failure) { defaults?.set(data, forKey: controlKey) }
    }
    public static func latestControlFailure(defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) -> TunnelFailure? {
        defaults?.data(forKey: controlKey).flatMap { try? JSONDecoder().decode(TunnelFailure.self, from: $0) }
    }

    /// Separate slots keep value-provider reads from overwriting the last
    /// action dispatch. Store only process identity, counts and native state.
    public static func recordControlExecution(action: String, stage: String, state: ServiceState? = nil, error: Error? = nil,
                                              defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) {
        var detail = action + (state.map { " → \($0.rawValue)" } ?? "")
        if let error {
            let native = error as NSError
            detail += "; \(native.domain) [\(native.code)]: \(native.localizedDescription)"
        }
        recordObservation(key: executionKey, stage: stage, detail: detail, defaults: defaults)
    }
    public static func recordControlProfileRead(managerCount: Int, matched: Bool, nativeStatus: Int?,
                                                defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) {
        recordObservation(key: profileReadKey, stage: "Profile read",
            detail: "\(managerCount) profiles; sakamoto match: \(matched); NE status: \(nativeStatus.map(String.init) ?? "unavailable")",
            defaults: defaults)
    }
    public static func latestControlExecution(defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) -> TunnelControlObservation? {
        observation(key: executionKey, defaults: defaults)
    }
    public static func latestControlProfileRead(defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) -> TunnelControlObservation? {
        observation(key: profileReadKey, defaults: defaults)
    }
    private static func recordObservation(key: String, stage: String, detail: String, defaults: UserDefaults?) {
        let value = TunnelControlObservation(stage: stage, host: Bundle.main.bundleIdentifier ?? "unknown",
            processID: ProcessInfo.processInfo.processIdentifier, detail: sanitized(detail), date: Date())
        if let data = try? JSONEncoder().encode(value) { defaults?.set(data, forKey: key) }
    }
    private static func observation(key: String, defaults: UserDefaults?) -> TunnelControlObservation? {
        defaults?.data(forKey: key).flatMap { try? JSONDecoder().decode(TunnelControlObservation.self, from: $0) }
    }

    public static func clear(defaults override: UserDefaults? = nil) { (override ?? defaults)?.removeObject(forKey: key) }
    public static func latest() -> TunnelFailure? {
        defaults?.data(forKey: key).flatMap { try? JSONDecoder().decode(TunnelFailure.self, from: $0) }
    }
}
