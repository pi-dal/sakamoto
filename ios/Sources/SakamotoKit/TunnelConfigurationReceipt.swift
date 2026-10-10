import Foundation
import CryptoKit

/// Evidence published only after the provider applies a configuration. The
/// shared receipt contains a digest, never configuration or credentials.
public struct TunnelConfigurationReceipt: Codable, Equatable, Sendable {
    public var profileID: String?
    public var fingerprint: String
    public var appliedAt: Date

    public init(options: TunnelStartOptions, appliedAt: Date = Date()) throws {
        profileID = options.profileID
        fingerprint = try Self.fingerprint(options)
        self.appliedAt = appliedAt
    }

    public func matches(_ options: TunnelStartOptions) -> Bool {
        profileID == options.profileID && (try? Self.fingerprint(options)) == fingerprint
    }

    private static func fingerprint(_ options: TunnelStartOptions) throws -> String {
        func canonical(_ value: String) throws -> String {
            let object = try JSONSerialization.jsonObject(with: Data(value.utf8))
            return String(decoding: try JSONSerialization.data(withJSONObject: object, options: [.sortedKeys]), as: UTF8.self)
        }
        var normalized = options
        normalized.configContent = try canonical(options.configContent)
        normalized.experimentJSON = try options.experimentJSON.map(canonical)
        normalized.forceTailscaleDERP = options.forceTailscaleDERP == true
        normalized.locale = nil
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        return SHA256.hash(data: try encoder.encode(normalized)).map { String(format: "%02x", $0) }.joined()
    }
}

public enum TunnelConfigurationReceiptStore {
    private static let key = "sakamoto.tunnel.appliedConfiguration"
    public static func record(_ options: TunnelStartOptions, defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) {
        guard let receipt = try? TunnelConfigurationReceipt(options: options),
              let data = try? JSONEncoder().encode(receipt) else { return }
        defaults?.set(data, forKey: key)
    }
    public static func read(defaults: UserDefaults? = UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)) -> TunnelConfigurationReceipt? {
        defaults?.data(forKey: key).flatMap { try? JSONDecoder().decode(TunnelConfigurationReceipt.self, from: $0) }
    }
}
