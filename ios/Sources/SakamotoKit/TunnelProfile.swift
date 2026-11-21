import Foundation

/// A complete portable runtime snapshot. Source-sync files alone are not a
/// runnable configuration; local rule sets travel with the selected profile.
public struct TunnelProfile: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var config: String
    public var files: [String: Data]
    public var sourceDigest: String
    public var routeSourceVersion: Int?
    public var importedAt: Date
    public var sourcesChanged: Bool = false
    public var sourceBundleJSON: String?
    /// Last source adoption committed on this device, keyed by sync directory scope.
    public var iCloudAdoptedBatches: [String: UUID]?
    public var pendingApply: Bool? = true
    /// Written by the app in the same atomic manifest replacement as pendingApply.
    public var appliedTransaction: UUID?
    public var experiment: ExperimentSettings?
    public var sourceFinal: String?
    public struct HostGeneration: Codable, Equatable, Sendable {
        public var nodes: [[String: String]]
        public var fingerprint: String
        /// An empty exit uses the desktop generator’s first-SOCKS convention.
        public var chainExit: String?
    }
    public var hostGeneration: HostGeneration?
    public var beforeOnFinal: String?
    public var proxyChain: ProxyChainSettings?
    public struct DNSGuard: Codable, Equatable, Sendable {
        public var local_domains: [String]
        public var bootstrap_ip: String
        public var bootstrap_server_name: String
    }
    public var dnsGuard: DNSGuard?
    public var regionalDNS: [String: String]?
    public var proxyGroups: [ProxyGroupEdit]?
    public var nodeOverrides: [String: String]?
    /// Immutable rule snapshot directory, independent of logical profile ID.
    public var ruleRevisionID: String?
    /// Device-side transport policy, kept outside the upstream sing-box JSON.
    public var forceTailscaleDERP: Bool?

    public init(id: String = UUID().uuidString, name: String, config: String, files: [String: Data] = [:], sourceDigest: String = "", importedAt: Date = Date(), sourceBundleJSON: String? = nil) {
        self.id = id; self.name = name; self.config = config; self.files = files
        self.sourceDigest = sourceDigest; self.importedAt = importedAt; self.sourceBundleJSON = sourceBundleJSON
    }

    /// An Experiment override only owns the final route while its mode is on.
    public mutating func stageExperiment(_ settings: ExperimentSettings, previous: ExperimentMode, content: String) throws {
        var next = content
        if previous != .on && settings.mode == .on {
            guard let final = ConfigSemanticsReader.read(content)?.routeFinal, !final.isEmpty else {
                throw InvalidProfile("Set an explicit FINAL route before enabling Experiment.")
            }
            next = try SettingsOverrides.setExperiment(true, in: content)
            beforeOnFinal = final
        } else if previous == .on && settings.mode != .on {
            if let beforeOnFinal {
                next = try SettingsOverrides.restoringFinal(beforeOnFinal, in: content)
                self.beforeOnFinal = nil
            } else if sourceFinal == "DIRECT" || sourceFinal == "PROXY" {
                next = try SettingsOverrides.setExperiment(sourceFinal == "PROXY", in: content)
            } else {
                throw InvalidProfile("Regenerate or reimport the sources to restore their FINAL route.")
            }
        }
        config = next
        experiment = settings
        pendingApply = true
    }

    public static func validateRouteSource(_ content: String, version: Int?) throws {
        guard version != 1, !content.isEmpty else { return }
        let root = try JSONSerialization.jsonObject(with: Data(content.utf8)) as? [String: Any]
        let route = root?["route"] as? [String: Any]
        let rules = route?["rules"] as? [[String: Any]] ?? []
        // Legacy route editors can move the surrounding rules or change the exit.
        // An inline domain route has no record of whether its domains were learned.
        func hasDomain(_ rule: [String: Any]) -> Bool {
            if let domain = rule["domain"] as? String, !domain.isEmpty { return true }
            if let domains = rule["domain"] as? [String], !domains.isEmpty { return true }
            return (rule["rules"] as? [[String: Any]])?.contains(where: hasDomain) == true
        }
        if rules.contains(where: hasDomain) {
            throw InvalidProfile("The source of domain routes in this older package is unknown. Re-export the profile with the current desktop app and import it again.")
        }
    }

    /// Adopt an installed baseline only after checking its original, unedited configuration.
    public mutating func adoptCheckedRouteSource() {
        guard routeSourceVersion != 1, !config.isEmpty,
              (try? Self.validateRouteSource(config, version: routeSourceVersion)) != nil else { return }
        routeSourceVersion = 1
    }

    public func preparedConfig(ruleDirectory: URL) throws -> String {
        try Self.validateRouteSource(config, version: routeSourceVersion)
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
        if var experimental = root["experimental"] as? [String: Any], var cache = experimental["cache_file"] as? [String: Any], cache["enabled"] as? Bool == true {
            cache["cache_id"] = id
            cache["path"] = "sakamoto-mode.db"
            experimental["cache_file"] = cache
            root["experimental"] = experimental
        }
        return String(decoding: try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys]), as: UTF8.self)
    }

    public static func validFileName(_ name: String) -> Bool {
        name.hasPrefix("rules/") && !name.contains("\\") && name.split(separator: "/", omittingEmptySubsequences: false).count == 2 &&
        !name.hasSuffix("/") && !name.split(separator: "/").contains(where: { $0 == "." || $0 == ".." || $0.hasPrefix(".") })
    }

    public static func decodePackage(_ data: Data) throws -> TunnelProfile {
        guard data.count <= 96 << 20 else { throw InvalidProfile("Package exceeds the size limit") }
        let package = try JSONDecoder().decode(Package.self, from: data)
        guard ["sakamoto-tunnel-v1", "sakamoto-tunnel-v2", "sakamoto-tunnel-v3"].contains(package.format), !package.name.isEmpty,
              package.files.count <= 128,
              package.files.keys.allSatisfy(validFileName),
              package.files.values.allSatisfy({ $0.count <= 32 << 20 }),
              package.files.values.reduce(0, { $0 + $1.count }) <= 64 << 20 else {
            throw InvalidProfile("Invalid configuration package")
        }
        try validateRouteSource(package.config, version: package.routeSourceVersion)
        let profile = TunnelProfile(name: package.name, config: package.config, files: package.files, sourceDigest: package.sourceDigest, sourceBundleJSON: package.sourceBundleJSON)
        var imported = profile
        imported.routeSourceVersion = 1 // The incoming baseline passed validateRouteSource above.
        guard package.format != "sakamoto-tunnel-v3" || package.hostGeneration != nil else {
            throw InvalidProfile("Reimport a current host export to restore the node sources.")
        }
        imported.hostGeneration = package.hostGeneration
        if let raw = package.hostMetadataJSON,
           let metadata = try JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any],
           metadata["chain_enabled"] as? Bool == true {
            let explicit = (metadata["chain_exit"] as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            imported.hostGeneration?.chainExit = explicit.isEmpty ? (metadata["socks_exit"] as? String ?? "") : explicit
        }
        imported.forceTailscaleDERP = package.forceTailscaleDERP
        imported.regionalDNS = package.regionalDNS
        imported.dnsGuard = package.dnsGuard
        imported.sourceFinal = package.sourceFinal
        imported.experiment = try ExperimentSettings.hostMetadata(package.hostMetadataJSON)
        if package.hostMetadataJSON == nil {
            imported.experiment?.mode = SettingsOverrides.experimentEnabled(in: imported.config) ? .on : .off
        }
        _ = try imported.preparedConfig(ruleDirectory: URL(fileURLWithPath: "/validation"))
        return imported
    }

    public struct Package: Codable, Sendable {
        public var routeSourceVersion: Int?
        public var format: String
        public var name: String
        public var config: String
        public var files: [String: Data]
        public var sourceDigest: String
        public var sourceBundleJSON: String?
        public var hostMetadataJSON: String?
        public var hostGeneration: HostGeneration?
        public var sourceFinal: String?
        public var dnsGuard: DNSGuard?
        public var regionalDNS: [String: String]?
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
