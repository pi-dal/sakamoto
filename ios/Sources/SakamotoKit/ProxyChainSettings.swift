import Foundation

/// Profile-owned intent applied to a runtime copy. The unchained generated
/// snapshot stays intact, so disabling a chain restores all memberships/rules.
public struct ProxyChainSettings: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var upstream: String
    public var exit: String
    public init(enabled: Bool = false, upstream: String = "MainProxy", exit: String = "") {
        self.enabled = enabled; self.upstream = upstream; self.exit = exit
    }

    public static func isChainable(_ row: [String: Any]) -> Bool {
        guard let type = row["type"] as? String else { return false }
        return !["selector", "urltest", "direct", "block", "dns"].contains(type)
    }

    public static func importedChain(in config: String) -> ProxyChainSettings? {
        guard let root = try? JSONSerialization.jsonObject(with: Data(config.utf8)) as? [String: Any],
              let outbounds = root["outbounds"] as? [[String: Any]] else { return nil }
        let route = root["route"] as? [String: Any] ?? [:]
        let proxy = (route["rules"] as? [[String: Any]])?.first { ($0["rule_set"] as? [String]) == ["rs-proxy"] || ($0["clash_mode"] as? String)?.lowercased() == "global" }?["outbound"] as? String ?? route["final"] as? String
        guard let exit = outbounds.first(where: { $0["tag"] as? String == proxy && Self.isChainable($0) }),
              let upstream = exit["detour"] as? String, let tag = exit["tag"] as? String else { return nil }
        return ProxyChainSettings(enabled: true, upstream: upstream, exit: tag)
    }

    public static func unchainedImportedConfig(_ config: String) throws -> String {
        guard let chain = importedChain(in: config), var root = try JSONSerialization.jsonObject(with: Data(config.utf8)) as? [String: Any] else { return config }
        var outbounds = root["outbounds"] as? [[String: Any]] ?? []
        for index in outbounds.indices where outbounds[index]["tag"] as? String == chain.exit { outbounds[index].removeValue(forKey: "detour") }
        root["outbounds"] = outbounds
        if let route = root["route"] as? [String: Any] {
            root["route"] = Self.rewriteRoutes(route, from: [chain.exit], to: chain.upstream)
        }
        if var dns = root["dns"] as? [String: Any], var servers = dns["servers"] as? [[String: Any]] {
            for index in servers.indices where servers[index]["detour"] as? String == chain.exit { servers[index]["detour"] = chain.upstream }
            dns["servers"] = servers; root["dns"] = dns
        }
        return String(decoding: try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys]), as: UTF8.self)
    }

    public func applying(to config: String) throws -> String {
        guard enabled else { return config }
        guard var root = try JSONSerialization.jsonObject(with: Data(config.utf8)) as? [String: Any],
              var outbounds = root["outbounds"] as? [[String: Any]],
              let exitIndex = outbounds.firstIndex(where: { $0["tag"] as? String == exit && Self.isChainable($0) }),
              upstream != exit, outbounds.contains(where: { $0["tag"] as? String == upstream && !["direct", "block", "dns"].contains($0["type"] as? String ?? "") }) else {
            throw TunnelProfile.InvalidProfile("Choose an existing upstream and leaf proxy exit for the proxy chain.")
        }
        outbounds[exitIndex]["detour"] = upstream
        var removed: Set<String> = [exit]
        var changed = true
        while changed {
            changed = false
            for index in outbounds.indices {
                guard let members = outbounds[index]["outbounds"] as? [String] else { continue }
                let remaining = members.filter { !removed.contains($0) }
                outbounds[index]["outbounds"] = remaining
                if remaining.isEmpty, let tag = outbounds[index]["tag"] as? String {
                    if removed.insert(tag).inserted { changed = true }
                } else if let selected = outbounds[index]["default"] as? String, !remaining.contains(selected) {
                    outbounds[index]["default"] = remaining.first
                }
            }
        }
        guard !removed.contains(upstream) else { throw TunnelProfile.InvalidProfile("The chain needs an upstream node other than its exit.") }
        outbounds.removeAll { row in
            guard let tag = row["tag"] as? String else { return false }
            return tag != exit && removed.contains(tag)
        }
        var graph: [String: [String]] = [:]
        for row in outbounds + (root["endpoints"] as? [[String: Any]] ?? []) {
            guard let tag = row["tag"] as? String, graph[tag] == nil else { throw TunnelProfile.InvalidProfile("Proxy chain requires unique outbound tags.") }
            graph[tag] = (row["outbounds"] as? [String] ?? []) + ((row["detour"] as? String).map { [$0] } ?? [])
        }
        var visited = Set<String>(), visiting = Set<String>()
        func visit(_ tag: String) throws {
            if visiting.contains(tag) { throw TunnelProfile.InvalidProfile("This proxy chain creates a routing cycle.") }
            if visited.contains(tag) { return }
            guard let next = graph[tag] else { throw TunnelProfile.InvalidProfile("Proxy chain references a missing outbound: \(tag)") }
            visiting.insert(tag)
            for dependency in next { try visit(dependency) }
            visiting.remove(tag); visited.insert(tag)
        }
        for tag in graph.keys { try visit(tag) }
        root["outbounds"] = outbounds
        // PROXY and Global use the exit. DIRECT/REJECT and tailnet-specific
        // routes keep their original policy. The exit dials through upstream.
        if var route = root["route"] as? [String: Any] {
            route = Self.rewriteRoutes(route, from: ["MainProxy", upstream], to: exit)
            root["route"] = route
        }
        if var dns = root["dns"] as? [String: Any], var servers = dns["servers"] as? [[String: Any]] {
            for index in servers.indices where servers[index]["detour"] as? String == "MainProxy" || servers[index]["detour"] as? String == upstream { servers[index]["detour"] = exit }
            dns["servers"] = servers; root["dns"] = dns
        }
        return String(decoding: try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys]), as: UTF8.self)
    }

    private static func rewriteRoutes(_ value: [String: Any], from: Set<String>, to: String) -> [String: Any] {
        var result = value
        if let tag = value["outbound"] as? String, from.contains(tag) { result["outbound"] = to }
        if let tag = value["final"] as? String, from.contains(tag) { result["final"] = to }
        if let rules = value["rules"] as? [[String: Any]] { result["rules"] = rules.map { rewriteRoutes($0, from: from, to: to) } }
        return result
    }
}
