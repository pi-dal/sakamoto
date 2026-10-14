import Foundation

public struct ProxyGroupEdit: Codable, Equatable, Identifiable, Sendable {
    public var tag: String
    public var type: String
    public var members: [String]
    public var defaultMember: String
    public var url: String
    public var interval: String
    /// Non-nil marks a generated automatic group whose membership follows new nodes.
    public var excludedMembers: [String]?
    public var additionalMembers: [String]?
    public var id: String { tag }
    public init(tag: String, type: String = "selector", members: [String] = [], defaultMember: String = "", url: String = "https://www.gstatic.com/generate_204", interval: String = "3m") {
        self.tag = tag; self.type = type; self.members = members; self.defaultMember = defaultMember; self.url = url; self.interval = interval
    }
}

public enum ProxyGroupEditing {
    public static func followsGeneratedNodes(_ edit: ProxyGroupEdit) -> Bool {
        edit.type == "urltest" && ["RealityAuto", "OthersAuto"].contains(edit.tag)
    }
    public static func automaticCandidates(for tag: String, in content: String) -> [String] {
        outbounds(content).compactMap { row in
            guard let name = row["tag"] as? String, let type = row["type"] as? String,
                  !["selector", "urltest", "direct", "block", "dns", "socks"].contains(type) else { return nil }
            let tls = row["tls"] as? [String: Any]
            let reality = (tls?["reality"] as? [String: Any])?["enabled"] as? Bool == true || name.lowercased().contains("reality")
            return (tag == "RealityAuto" && reality) || (tag == "OthersAuto" && !reality) ? name : nil
        }
    }
    /// Store exclusions, including absent nodes, rather than freezing the current members.
    public static func recording(_ edit: ProxyGroupEdit, in content: String, previous: ProxyGroupEdit? = nil) -> ProxyGroupEdit {
        guard followsGeneratedNodes(edit) else { return edit }
        var saved = edit
        let candidates = Set(automaticCandidates(for: edit.tag, in: content))
        let current = groups(content).first { $0.tag == edit.tag }?.members ?? []
        let removable = candidates.union(current).union(previous?.additionalMembers ?? [])
        let excluded = Set(edit.excludedMembers ?? previous?.excludedMembers ?? [])
            .union(removable.subtracting(edit.members)).subtracting(edit.members)
        saved.excludedMembers = excluded.sorted()
        saved.additionalMembers = edit.members.filter { !candidates.contains($0) }
        return saved
    }
    public static func resolving(_ edit: ProxyGroupEdit, in content: String, knownTags: Set<String>? = nil) -> ProxyGroupEdit {
        guard followsGeneratedNodes(edit), let excluded = edit.excludedMembers else { return edit }
        var result = edit
        let candidates = automaticCandidates(for: edit.tag, in: content)
        let allowed = Set(candidates).subtracting(excluded)
        let root = (try? JSONSerialization.jsonObject(with: Data(content.utf8))) as? [String: Any]
        let endpoints = root?["endpoints"] as? [[String: Any]] ?? []
        let known = knownTags ?? Set((outbounds(content) + endpoints).compactMap { $0["tag"] as? String })
        let extras = (edit.additionalMembers ?? []).filter { known.contains($0) && !excluded.contains($0) }
        let ordered = edit.members.filter { allowed.contains($0) || extras.contains($0) }
        result.members = ordered + (candidates + extras).filter { !ordered.contains($0) && !excluded.contains($0) }
        result.members = result.members.reduce(into: []) { if !$0.contains($1) { $0.append($1) } }
        return result
    }
    public static func outbounds(_ content: String) -> [[String: Any]] {
        ((try? JSONSerialization.jsonObject(with: Data(content.utf8))) as? [String: Any])?["outbounds"] as? [[String: Any]] ?? []
    }
    public static func groups(_ content: String) -> [ProxyGroupEdit] {
        outbounds(content).filter { ["selector", "urltest"].contains($0["type"] as? String ?? "") }.compactMap { row in
            guard let tag = row["tag"] as? String else { return nil }
            return ProxyGroupEdit(tag: tag, type: row["type"] as? String ?? "selector", members: row["outbounds"] as? [String] ?? [], defaultMember: row["default"] as? String ?? "", url: row["url"] as? String ?? "https://www.gstatic.com/generate_204", interval: row["interval"] as? String ?? "3m")
        }
    }
    public static func applying(_ edits: [ProxyGroupEdit], to content: String) throws -> String {
        guard !edits.isEmpty else { return content }
        guard var root = try JSONSerialization.jsonObject(with: Data(content.utf8)) as? [String: Any], var rows = root["outbounds"] as? [[String: Any]] else { throw TunnelProfile.InvalidProfile("Generate or import a configuration first.") }
        let endpoints = root["endpoints"] as? [[String: Any]] ?? []
        let tags = rows.compactMap { $0["tag"] as? String } + endpoints.compactMap { $0["tag"] as? String }
        guard Set(tags).count == tags.count, Set(edits.map(\.tag)).count == edits.count else { throw TunnelProfile.InvalidProfile("Configuration contains duplicate names.") }
        let known = Set(tags).union(edits.map(\.tag))
        for saved in edits {
            let edit = resolving(saved, in: content, knownTags: known)
            // Keep the exclusions when the generator has no nodes for this group yet.
            if followsGeneratedNodes(edit), edit.excludedMembers != nil, edit.members.isEmpty,
               !rows.contains(where: { $0["tag"] as? String == edit.tag }) { continue }
            guard !edit.tag.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, edit.tag.count <= 128, !edit.tag.contains(where: { $0.isNewline }), ["selector", "urltest"].contains(edit.type) else { throw TunnelProfile.InvalidProfile("Enter a group name and choose Manual or Automatic.") }
            guard !edit.members.isEmpty, Set(edit.members).count == edit.members.count, !edit.members.contains(edit.tag), edit.members.allSatisfy(known.contains) else { throw TunnelProfile.InvalidProfile("Choose at least one existing member. A group cannot contain itself or missing nodes.") }
            let index = rows.firstIndex { $0["tag"] as? String == edit.tag }
            guard !endpoints.contains(where: { $0["tag"] as? String == edit.tag }), index == nil || ["selector", "urltest"].contains(rows[index!]["type"] as? String ?? "") else { throw TunnelProfile.InvalidProfile("That name belongs to a node. Choose another group name.") }
            var row = index.map { rows[$0] } ?? ["tag": edit.tag]
            row["type"] = edit.type; row["outbounds"] = edit.members
            if edit.type == "selector" {
                guard edit.defaultMember.isEmpty || edit.members.contains(edit.defaultMember) else { throw TunnelProfile.InvalidProfile("The default node must be a selected member.") }
                if edit.defaultMember.isEmpty { row.removeValue(forKey: "default") } else { row["default"] = edit.defaultMember }
                for key in ["url", "interval", "tolerance", "idle_timeout"] { row.removeValue(forKey: key) }
            } else {
                guard let url = URLComponents(string: edit.url), ["http", "https"].contains(url.scheme ?? ""), url.host?.isEmpty == false else { throw TunnelProfile.InvalidProfile("Enter an HTTP(S) latency test URL.") }
                guard edit.interval.range(of: #"^(?:[0-9]+(?:\.[0-9]+)?(?:ms|s|m|h))+$"#, options: .regularExpression) != nil,
                      edit.interval.contains(where: { $0.isNumber && $0 != "0" }) else { throw TunnelProfile.InvalidProfile("Enter a positive test interval, such as 30s, 3m or 1h.") }
                row.removeValue(forKey: "default"); row["url"] = edit.url; row["interval"] = edit.interval
            }
            if let index { rows[index] = row } else { rows.append(row) }
        }
        let all = rows + endpoints
        let graph = Dictionary(uniqueKeysWithValues: all.compactMap { row -> (String, [String])? in
            guard let tag = row["tag"] as? String else { return nil }
            var edges = row["outbounds"] as? [String] ?? []
            if let detour = row["detour"] as? String, !detour.isEmpty { edges.append(detour) }
            return (tag, edges)
        })
        var visited = Set<String>(), active = Set<String>()
        func visit(_ tag: String) throws {
            if active.contains(tag) { throw TunnelProfile.InvalidProfile("Group membership would create a proxy loop. Remove the circular member.") }
            guard visited.insert(tag).inserted else { return }
            active.insert(tag)
            for child in graph[tag] ?? [] { try visit(child) }
            active.remove(tag)
        }
        for tag in graph.keys { try visit(tag) }
        root["outbounds"] = rows
        return String(decoding: try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys]), as: UTF8.self)
    }

    /// Rewrite outbound references, never arbitrary strings such as hosts or credentials.
    public static func renamingReferences(_ names: [String: String], in value: Any) -> Any {
        if let array = value as? [Any] { return array.map { renamingReferences(names, in: $0) } }
        guard let object = value as? [String: Any] else { return value }
        var result = object.mapValues { renamingReferences(names, in: $0) }
        for key in ["outbound", "outbounds", "detour", "download_detour"] {
            if let name = object[key] as? String { result[key] = names[name] ?? name }
            else if let members = object[key] as? [String] { result[key] = members.map { names[$0] ?? $0 } }
        }
        if ["selector", "urltest"].contains(object["type"] as? String ?? ""), let name = object["default"] as? String { result["default"] = names[name] ?? name }
        if var route = result["route"] as? [String: Any], let name = route["final"] as? String { route["final"] = names[name] ?? name; result["route"] = route }
        return result
    }
    public static func replacingNode(tag: String, with nodeJSON: String, in content: String) throws -> String {
        guard var root = try JSONSerialization.jsonObject(with: Data(content.utf8)) as? [String: Any], var rows = root["outbounds"] as? [[String: Any]],
              let index = rows.firstIndex(where: { $0["tag"] as? String == tag }), let node = try JSONSerialization.jsonObject(with: Data(nodeJSON.utf8)) as? [String: Any],
              let name = node["tag"] as? String, !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, !name.contains(where: { $0.isNewline || $0 == "\0" }), node["type"] as? String == rows[index]["type"] as? String,
              !["selector", "urltest", "direct", "block", "dns"].contains(node["type"] as? String ?? "") else { throw TunnelProfile.InvalidProfile("The node changed or was removed. Reopen its editor.") }
        let endpoints = root["endpoints"] as? [[String: Any]] ?? []
        guard name == tag || !(rows + endpoints).contains(where: { $0["tag"] as? String == name }) else { throw TunnelProfile.InvalidProfile("This remark already belongs to another node or group.") }
        rows[index] = node; root["outbounds"] = rows
        root = renamingReferences([tag: name], in: root) as! [String: Any]
        return String(decoding: try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys]), as: UTF8.self)
    }
    public static func applyingNodes(_ overrides: [String: String], to content: String) throws -> String {
        guard !overrides.isEmpty else { return content }
        guard var root = try JSONSerialization.jsonObject(with: Data(content.utf8)) as? [String: Any], var rows = root["outbounds"] as? [[String: Any]] else { throw TunnelProfile.InvalidProfile("Generate or import a configuration first.") }
        var replacements: [Int: [String: Any]] = [:], names: [String: String] = [:]
        for index in rows.indices {
            guard let source = rows[index]["tag"] as? String, let json = overrides[source] else { continue }
            guard let node = try JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any], let name = node["tag"] as? String, !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, !name.contains(where: { $0.isNewline || $0 == "\0" }),
                  node["type"] as? String == rows[index]["type"] as? String else { throw TunnelProfile.InvalidProfile("The node changed. Reopen its editor.") }
            replacements[index] = node; names[source] = name
        }
        // Batch aliases before replacement so name swaps remain valid on regeneration.
        root = renamingReferences(names, in: root) as! [String: Any]
        rows = root["outbounds"] as? [[String: Any]] ?? []
        for (index, node) in replacements { rows[index] = node }
        let tags = (rows + (root["endpoints"] as? [[String: Any]] ?? [])).compactMap { $0["tag"] as? String }
        guard tags.count == Set(tags).count else { throw TunnelProfile.InvalidProfile("A saved remark conflicts with another node or group.") }
        root["outbounds"] = rows
        return String(decoding: try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys]), as: UTF8.self)
    }
}
