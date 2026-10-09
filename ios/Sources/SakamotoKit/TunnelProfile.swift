import Foundation

/// A complete portable runtime snapshot. Source-sync files alone are not a
/// runnable configuration; local rule sets travel with the selected profile.
public struct TunnelProfile: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var config: String
    public var files: [String: Data]
    public var sourceDigest: String
    public var importedAt: Date
    public var sourcesChanged: Bool = false
    public var sourceBundleJSON: String?
    public var pendingApply: Bool? = true
    public var experiment: ExperimentSettings?
    public var proxyChain: ProxyChainSettings?
    /// Immutable rule snapshot directory, independent of logical profile ID.
    public var ruleRevisionID: String?
    /// Device-side transport policy, kept outside the upstream sing-box JSON.
    public var forceTailscaleDERP: Bool?

    public init(id: String = UUID().uuidString, name: String, config: String, files: [String: Data] = [:], sourceDigest: String = "", importedAt: Date = Date(), sourceBundleJSON: String? = nil) {
        self.id = id; self.name = name; self.config = config; self.files = files
        self.sourceDigest = sourceDigest; self.importedAt = importedAt; self.sourceBundleJSON = sourceBundleJSON
    }

    public func preparedConfig(ruleDirectory: URL) throws -> String {
        guard var root = try JSONSerialization.jsonObject(with: Data(config.utf8)) as? [String: Any],
              var route = root["route"] as? [String: Any],
              let inbounds = root["inbounds"] as? [[String: Any]],
              inbounds.contains(where: { $0["type"] as? String == "tun" }) else {
            throw InvalidProfile("Configuration requires a TUN inbound")
        }
        var sets = route["rule_set"] as? [[String: Any]] ?? []
        for index in sets.indices {
            guard let old = sets[index]["path"] as? String else { continue }
            let key = "rules/" + URL(fileURLWithPath: old).lastPathComponent
            guard let bytes = files[key], !bytes.isEmpty, Self.validFileName(key) else {
                throw InvalidProfile("Configuration is missing a local rule set. Import a complete .sakamoto package from the Mac.")
            }
            sets[index]["path"] = ruleDirectory.appendingPathComponent(key).path
        }
        route["rule_set"] = sets
        root["route"] = route
        return String(decoding: try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys]), as: UTF8.self)
    }

    public static func validFileName(_ name: String) -> Bool {
        name.hasPrefix("rules/") && !name.contains("\\") && name.split(separator: "/", omittingEmptySubsequences: false).count == 2 &&
        !name.hasSuffix("/") && !name.split(separator: "/").contains(where: { $0 == "." || $0 == ".." || $0.hasPrefix(".") })
    }

    public static func decodePackage(_ data: Data) throws -> TunnelProfile {
        guard data.count <= 96 << 20 else { throw InvalidProfile("Package exceeds the size limit") }
        let package = try JSONDecoder().decode(Package.self, from: data)
        guard package.format == "sakamoto-tunnel-v1", !package.name.isEmpty,
              package.files.count <= 128,
              package.files.keys.allSatisfy(validFileName),
              package.files.values.allSatisfy({ $0.count <= 32 << 20 }),
              package.files.values.reduce(0, { $0 + $1.count }) <= 64 << 20 else {
            throw InvalidProfile("Invalid configuration package")
        }
        let profile = TunnelProfile(name: package.name, config: package.config, files: package.files, sourceDigest: package.sourceDigest, sourceBundleJSON: package.sourceBundleJSON)
        var imported = profile
        imported.forceTailscaleDERP = package.forceTailscaleDERP
        imported.experiment = try ExperimentSettings.hostMetadata(package.hostMetadataJSON)
        if package.hostMetadataJSON == nil {
            imported.experiment?.mode = SettingsOverrides.experimentEnabled(in: imported.config) ? .on : .off
        }
        _ = try imported.preparedConfig(ruleDirectory: URL(fileURLWithPath: "/validation"))
        return imported
    }

    public struct Package: Codable, Sendable {
        public var format: String
        public var name: String
        public var config: String
        public var files: [String: Data]
        public var sourceDigest: String
        public var sourceBundleJSON: String?
        public var hostMetadataJSON: String?
        public var forceTailscaleDERP: Bool?
        public init(name: String, config: String, files: [String: Data], sourceDigest: String = "", sourceBundleJSON: String? = nil) {
            self.format = "sakamoto-tunnel-v1"; self.name = name; self.config = config
            self.files = files; self.sourceDigest = sourceDigest; self.sourceBundleJSON = sourceBundleJSON
        }
    }
    public struct InvalidProfile: LocalizedError {
        public var message: String
        public init(_ message: String) { self.message = message }
        public var errorDescription: String? { message }
    }
}
