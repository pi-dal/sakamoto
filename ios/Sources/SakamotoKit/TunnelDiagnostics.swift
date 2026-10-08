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
    public static func clear() { defaults?.removeObject(forKey: key) }
    public static func latest() -> TunnelFailure? {
        defaults?.data(forKey: key).flatMap { try? JSONDecoder().decode(TunnelFailure.self, from: $0) }
    }
}
