import Foundation

/// Shared groups.json contains exclusions in source-node names. Aliases and
/// custom runtime groups stay local; {} explicitly restores default membership.
public enum AutomaticGroupSources {
    public static let fileName = "groups.json"
    public static let tags: Set<String> = ["RealityAuto", "OthersAuto"]

    public static func decode(_ data: Data) throws -> [String: [String]] {
        let groups = try JSONDecoder().decode([String: [String]].self, from: data)
        for (group, excluded) in groups {
            guard tags.contains(group), Set(excluded).count == excluded.count,
                  excluded.allSatisfy({ !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !$0.contains(where: { $0 == "\0" || $0 == "\r" || $0 == "\n" }) }) else {
                throw TunnelProfile.InvalidProfile("Invalid automatic group exclusions in synced source.")
            }
        }
        return groups
    }
    public static func encode(_ groups: [String: [String]]) throws -> String {
        let encoder = JSONEncoder(); encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        let data = try encoder.encode(groups.filter { !$0.value.isEmpty }.mapValues { $0.sorted() })
        _ = try decode(data)
        return String(decoding: data, as: UTF8.self)
    }
    private static func aliases(_ overrides: [String: String]) -> [String: String] {
        overrides.reduce(into: [:]) { names, pair in
            if let row = (try? JSONSerialization.jsonObject(with: Data(pair.value.utf8))) as? [String: Any], let tag = row["tag"] as? String { names[pair.key] = tag }
        }
    }
    public static func exclusions(edits: [ProxyGroupEdit], content: String, overrides: [String: String] = [:]) -> [String: [String]]? {
        let names = aliases(overrides)
        let sources = names.reduce(into: [String: String]()) { $0[$1.value] = $1.key }
        let automatic = edits.filter(ProxyGroupEditing.followsGeneratedNodes)
        guard !automatic.isEmpty else { return nil }
        return Dictionary(uniqueKeysWithValues: automatic.map { saved in
            let edit = saved.excludedMembers == nil ? ProxyGroupEditing.recording(saved, in: content) : saved
            return (edit.tag, Array(Set((edit.excludedMembers ?? []).map { sources[$0] ?? $0 })).sorted())
        })
    }
    public static func adopting(_ groups: [String: [String]], edits: [ProxyGroupEdit], content: String, overrides: [String: String] = [:]) -> [ProxyGroupEdit] {
        let names = aliases(overrides)
        let current = ProxyGroupEditing.groups(content)
        let namesToUpdate = Set(groups.keys).union(edits.filter(ProxyGroupEditing.followsGeneratedNodes).map(\.tag)).union(current.filter(ProxyGroupEditing.followsGeneratedNodes).map(\.tag))
        return edits.filter { !namesToUpdate.contains($0.tag) } + namesToUpdate.sorted().map { tag in
            var edit = edits.first { $0.tag == tag } ?? current.first { $0.tag == tag } ?? ProxyGroupEdit(tag: tag, type: "urltest")
            edit.type = "urltest"
            edit.excludedMembers = (groups[tag] ?? []).map { names[$0] ?? $0 }.sorted()
            return ProxyGroupEditing.resolving(edit, in: content)
        }
    }
    public static func updating(_ bundle: SourceBundle, edits: [ProxyGroupEdit], content: String, overrides: [String: String] = [:]) throws -> SourceBundle {
        var bundle = bundle
        let groups = exclusions(edits: edits, content: content, overrides: overrides)
        guard groups != nil || bundle.files[fileName] != nil else { return bundle }
        if let raw = bundle.files[fileName], let existing = try? decode(Data(raw.utf8)),
           existing.filter({ !$0.value.isEmpty }).mapValues({ $0.sorted() }) == (groups ?? [:]).filter({ !$0.value.isEmpty }).mapValues({ $0.sorted() }) { return bundle }
        bundle.files[fileName] = try encode(groups ?? [:])
        return bundle
    }
}
