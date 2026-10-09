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
        struct Summary: Decodable {
            var proxyDomains: [String]
            var proxyNonDomainRules: Int
            var includesPending: [String]
            var ruleSetsPending: [String]
        }
        // sing-box Listable fields encode one entry as a scalar and many as an array.
        func strings(_ value: Any?) -> [String] {
            if let value = value as? String { return [value] }
            return value as? [String] ?? []
        }
        func add(_ values: [String]) {
            for value in values {
                if let normalized = try? AutomaticConnectionSettings.normalizeDomains(value), normalized.count == 1 {
                    hosts.insert(normalized[0])
                }
            }
        }
        func visit(_ name: String) throws {
            if visited.contains(name) { return }
            guard visited.count < ICloudSyncLimits.maxIncludeFiles,
                  let body = bundle.files[name] else { throw TunnelProfile.InvalidProfile("Missing selected conf or include. Sync the complete sources first.") }
            visited.insert(name)
            var error: NSError?
            let raw = MobilecoreParseConfContentJSON(body, &error)
            if let error { throw error }
            let summary = try JSONDecoder().decode(Summary.self, from: Data(raw.utf8))
            add(summary.proxyDomains)
            if summary.proxyNonDomainRules > 0 { result.notes.append("IP and keyword rules cannot be used as iOS on-demand domains.") }
            if !summary.ruleSetsPending.isEmpty || summary.includesPending.contains(where: { $0.hasPrefix("http") }) {
                result.notes.append("Remote rules use the generated local snapshot. Generate current sources to include downloads.")
            }
            let parent = (name as NSString).deletingLastPathComponent
            for include in try ICloudSyncConf.localIncludes(ofConfContent: Data(body.utf8)) {
                try visit(parent.isEmpty ? include : parent + "/" + include)
            }
        }
        if !bundle.mainConf.isEmpty {
            try ICloudSyncPaths.validateConfGraph(main: bundle.mainConf, files: bundle.files.mapValues { Data($0.utf8) })
            try visit(bundle.mainConf)
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
                add(strings(rule["domain"]))
                add(strings(rule["domain_suffix"]))
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
                let raw = MobilegenInspectRuleSetJSON(data, &error)
                if let error { throw error }
                let object = try JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any] ?? [:]
                for rule in object["rules"] as? [[String: Any]] ?? [] {
                    if rule["invert"] as? Bool == true || rule["type"] as? String == "logical" { continue }
                    add(strings(rule["domain"]))
                    add(strings(rule["domain_suffix"]))
                    if rule["ip_cidr"] != nil || rule["domain_keyword"] != nil { result.notes.append("IP and keyword rules cannot be used as iOS on-demand domains.") }
                }
            }
        }
        result.domains = hosts.sorted()
        result.notes = Array(Set(result.notes)).sorted()
        return result
    }
}
