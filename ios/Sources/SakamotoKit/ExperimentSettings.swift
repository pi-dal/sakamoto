import Foundation

public enum ExperimentMode: String, Codable, CaseIterable, Sendable { case off, on, auto }

/// Policy applies when the profile is connected/applied, not while editing.
public struct ExperimentSettings: Codable, Equatable, Sendable {
    public var mode: ExperimentMode = .off
    public var threshold: Int = 3
    public var cfRegionBlock = false
    public var fallbackEnabled = false
    public var recoverAfter = 2
    public var fallbacks: [String: [String]] = [:]
    public init() {}
    public func validate() throws {
        guard (1...20).contains(threshold), (1...20).contains(recoverAfter), fallbacks.count <= 32,
              fallbacks.allSatisfy({ !$0.key.isEmpty && !$0.key.contains("\0") && (1...32).contains($0.value.count) && Set($0.value).count == $0.value.count && $0.value.allSatisfy { !$0.isEmpty && !$0.contains("\0") } }) else {
            throw TunnelProfile.InvalidProfile("Invalid Experiment threshold or fallback priorities")
        }
    }
    public static func hostMetadata(_ raw: String?) throws -> ExperimentSettings {
        var settings = ExperimentSettings()
        guard let raw else { return settings }
        guard let root = try JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any] else { return settings }
        let experiment = root["experiment"] as? [String: Any] ?? [:]
        if let mode = experiment["mode"] as? String {
            guard let parsed = ExperimentMode(rawValue: mode) else { throw TunnelProfile.InvalidProfile("Unknown Experiment mode") }
            settings.mode = parsed
        }
        settings.threshold = experiment["threshold"] as? Int ?? 3
        settings.cfRegionBlock = experiment["cf_region_block"] as? Bool ?? false
        settings.fallbackEnabled = root["fallback_enabled"] as? Bool ?? false
        settings.recoverAfter = root["recover_after"] as? Int ?? 2
        settings.fallbacks = root["fallbacks"] as? [String: [String]] ?? [:]
        try settings.validate()
        return settings
    }
}

public struct ExperimentRuntimeState: Codable, Equatable, Sendable {
    public var learned: [String] = []
    public var status = "Stopped"
    public var updatedAt = Date()
    public init() {}
}

/// Timestamped latency facts, not the UI's selected/reachable shorthand.
public struct ExperimentGroup: Equatable, Sendable {
    public var tag: String
    public var selected: String
    public var items: [Item]
    public struct Item: Equatable, Sendable {
        public var tag: String
        public var delay: Int32
        public var testedAt: Int64
        public init(tag: String, delay: Int32, testedAt: Int64) { self.tag = tag; self.delay = delay; self.testedAt = testedAt }
    }
    public init(tag: String, selected: String, items: [Item]) { self.tag = tag; self.selected = selected; self.items = items }
    public static func healthy(_ tag: String, groups: [ExperimentGroup], since: Int64) -> Bool {
        let candidates = groups.first(where: { $0.tag == tag })?.items ?? groups.flatMap(\.items).filter { $0.tag == tag }
        return candidates.contains { $0.delay > 0 && $0.testedAt >= since }
    }
    public static func selectedProxyHealthy(groups: [ExperimentGroup], since: Int64) -> Bool {
        guard let main = groups.first(where: { $0.tag == "MainProxy" }), !main.selected.isEmpty else { return false }
        if let selected = groups.first(where: { $0.tag == main.selected }) {
            return selected.items.contains { $0.tag == selected.selected && $0.delay > 0 && $0.testedAt >= since }
        }
        return main.items.contains { $0.tag == main.selected && $0.delay > 0 && $0.testedAt >= since }
    }
    public static func candidate(selector: String, chain: [String], groups: [ExperimentGroup], since: Int64) -> String? {
        guard let selected = groups.first(where: { $0.tag == selector })?.selected, chain.contains(selected) else { return nil }
        return chain.first { healthy($0, groups: groups, since: since) }
    }
}

/// Consecutive, candidate-specific recovery streaks. No healthy result or
/// manual selection resets the streak instead of accumulating old rounds.
public struct ExperimentRecovery: Sendable {
    private var candidate: [String: String] = [:]
    private var counts: [String: Int] = [:]
    public init() {}
    public mutating func decision(selector: String, chain: [String], selected: String, healthy: String?, required: Int) -> String? {
        guard let current = chain.firstIndex(of: selected), let healthy, let desired = chain.firstIndex(of: healthy), desired != current else {
            candidate[selector] = nil; counts[selector] = nil; return nil
        }
        if desired > current { candidate[selector] = nil; counts[selector] = nil; return healthy }
        let count = candidate[selector] == healthy ? (counts[selector] ?? 0) + 1 : 1
        candidate[selector] = healthy; counts[selector] = count
        guard count >= required else { return nil }
        candidate[selector] = nil; counts[selector] = nil; return healthy
    }
    public mutating func reset() { candidate = [:]; counts = [:] }
}

/// Private, profile-scoped operational data. Never part of source sync.
public enum ExperimentFiles {
    public static func directory(profileID: String, root: URL) throws -> URL {
        guard UUID(uuidString: profileID) != nil else { throw TunnelProfile.InvalidProfile("Invalid profile identifier") }
        return root.appendingPathComponent("Profiles/" + profileID)
    }
    public static func read(profileID: String, root: URL) throws -> ExperimentRuntimeState {
        let file = try directory(profileID: profileID, root: root).appendingPathComponent("experiment-runtime.json")
        guard FileManager.default.fileExists(atPath: file.path) else { return ExperimentRuntimeState() }
        return try JSONDecoder().decode(ExperimentRuntimeState.self, from: Data(contentsOf: file))
    }
    public static func write(_ state: ExperimentRuntimeState, profileID: String, root: URL) throws {
        let directory = try directory(profileID: profileID, root: root)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let file = directory.appendingPathComponent("experiment-runtime.json")
        try JSONEncoder().encode(state).write(to: file, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
    }
}
