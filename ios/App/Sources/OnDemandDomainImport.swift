import Foundation
import Libbox
import SakamotoKit

struct OnDemandDomainImport {
    var domains: [String] = []
    var notes: [String] = []

    static func read(bundle: SourceBundle, profile: TunnelProfile?) throws -> Self {
        var result = Self()
        var hosts = Set<String>()
        var visited = Set<String>()
        var visiting = Set<String>()
        var sourceBytes = 0
        let maxImportBytes = 2 << 20
        struct Summary: Decodable {
            var domains: [String]
            var hasNonDomainRules: Bool
            var hasRemoteRules: Bool
        }
        // sing-box Listable fields encode one entry as a scalar and many as an array.
        func strings(_ value: Any?) -> [String] {
            if let value = value as? String { return [value] }
            return value as? [String] ?? []
        }
        func add(_ values: [String]) throws {
            for value in values {
                if let normalized = try? AutomaticConnectionSettings.normalizeDomains(value), normalized.count == 1 {
                    hosts.insert(normalized[0])
                    guard hosts.count <= AutomaticConnectionSettings.maxDomains else {
                        throw AutomaticConnectionSettings.ValidationError.tooManyDomains
                    }
                }
            }
        }
        func visit(_ name: String, depth: Int = 0) throws {
            guard !visiting.contains(name), depth <= ICloudSyncLimits.maxIncludeDepth,
                  ICloudSyncPaths.isValidSourceName(name), name.lowercased().hasSuffix(".conf") else {
                throw TunnelProfile.InvalidProfile("Invalid or cyclic selected conf includes.")
            }
            if visited.contains(name) { return }
            guard visited.count < ICloudSyncLimits.maxIncludeFiles,
                  let body = bundle.files[name] else { throw TunnelProfile.InvalidProfile("Missing selected conf or include. Sync the complete sources first.") }
            sourceBytes += body.utf8.count
            guard sourceBytes <= maxImportBytes else {
                throw TunnelProfile.InvalidProfile("Configuration is too large for automatic domain import. Choose a few trigger domains manually.")
            }
            visiting.insert(name)
            defer { visiting.remove(name) }
            visited.insert(name)
            var error: NSError?
            let raw = MobilecoreOnDemandConfJSON(body, &error)
            if let error { throw error }
            let summary = try JSONDecoder().decode(Summary.self, from: Data(raw.utf8))
            try add(summary.domains)
            if summary.hasNonDomainRules { result.notes.append("IP and keyword rules cannot be used as iOS on-demand domains.") }
            if summary.hasRemoteRules {
                result.notes.append("Remote rules use the generated local snapshot. Generate current sources to include downloads.")
            }
            let parent = (name as NSString).deletingLastPathComponent
            for include in try ICloudSyncConf.localIncludes(ofConfContent: Data(body.utf8)) {
                try visit(parent.isEmpty ? include : parent + "/" + include, depth: depth + 1)
            }
        }
        if !bundle.mainConf.isEmpty {
            try visit(bundle.mainConf)
        }
        if let profile, !profile.sourcesChanged {
            guard profile.config.utf8.count <= maxImportBytes else {
                throw TunnelProfile.InvalidProfile("Generated configuration is too large for automatic domain import. Choose a few trigger domains manually.")
            }
        }
        if let profile, !profile.sourcesChanged,
           let root = try JSONSerialization.jsonObject(with: Data(profile.config.utf8)) as? [String: Any],
           let route = root["route"] as? [String: Any] {
            let outbounds = root["outbounds"] as? [[String: Any]] ?? []
            let proxyTags = Set(outbounds.filter { !["direct", "block", "dns"].contains($0["type"] as? String ?? "") }.compactMap { $0["tag"] as? String })
            let rules = route["rules"] as? [[String: Any]] ?? []
            var referenced = Set<String>()
            for rule in rules where proxyTags.contains(rule["outbound"] as? String ?? "") {
                // Negated/logical predicates cannot be flattened into a host list.
                if rule["invert"] as? Bool == true || rule["type"] as? String == "logical" { continue }
                try add(strings(rule["domain"]))
                try add(strings(rule["domain_suffix"]))
                referenced.formUnion(strings(rule["rule_set"]))
            }
            let sets = route["rule_set"] as? [[String: Any]] ?? []
            for set in sets where referenced.contains(set["tag"] as? String ?? "") {
                guard let path = set["path"] as? String,
                      let data = profile.files["rules/" + URL(fileURLWithPath: path).lastPathComponent] else {
                    result.notes.append("Some proxy rule sets have no local snapshot. Generate the configuration first.")
                    continue
                }
                var error: NSError?
                let raw = MobilegenOnDemandRuleSetJSON(data, &error)
                if let error { throw error }
                let summary = try JSONDecoder().decode(Summary.self, from: Data(raw.utf8))
                try add(summary.domains)
                if summary.hasNonDomainRules { result.notes.append("IP and keyword rules cannot be used as iOS on-demand domains.") }
            }
        }
        result.domains = hosts.sorted()
        result.notes = Array(Set(result.notes)).sorted()
        return result
    }
}
