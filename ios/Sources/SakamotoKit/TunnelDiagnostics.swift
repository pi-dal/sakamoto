import Foundation

public struct TunnelFailure: Codable, Sendable {
    public var stage: String
    public var message: String
    public var date: Date
}

/// Shared between the app and extension, including failed cold starts. Never
/// records config contents; URL authorities/query strings are redacted.
public enum TunnelDiagnostics {
    private static let key = "sakamoto.tunnel.lastFailure"
    private static let controlKey = "sakamoto.tunnel.lastControlFailure"
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

    public static func clear(defaults override: UserDefaults? = nil) { (override ?? defaults)?.removeObject(forKey: key) }
    public static func latest() -> TunnelFailure? {
        defaults?.data(forKey: key).flatMap { try? JSONDecoder().decode(TunnelFailure.self, from: $0) }
    }
}
