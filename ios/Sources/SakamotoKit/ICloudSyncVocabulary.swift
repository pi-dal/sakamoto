import Foundation

// iCloud source-sync vocabulary for the iOS app — the Swift counterpart of
// internal/icloud (sources.go + the settings half of internal/config) and
// docs/icloud.md. Semantics pinned here:
//
//   * enabled is FALSE by default. Enabling requires an explicit user
//     confirmation that names what will be uploaded (node links, conf with
//     possible proxy credentials, subscription feed URLs).
//   * includeConf is Optional-on-purpose: nil means "never confirmed" — an
//     upgrade must not silently expand an old nodes-only consent into conf
//     upload (docs/icloud.md: "an upgrade never expands old consent
//     silently"). Only an explicit true (fresh config default) or a user
//     confirmation stores true.
//   * Never syncable, enforced by name at ANY depth: generated sing-box
//     config.json, .srs rule sets, databases, sockets/locks, logs, key
//     material, API rotation state, the sync's own baseline state file, and
//     hidden/traversal components. This is the port of
//     internal/icloud.ValidSourceName.

// MARK: - Settings

public struct ICloudSyncSettings: Codable, Equatable, Sendable {
    /// Master switch. Never defaulted on; the UI toggles it only through a
    /// confirmation dialog.
    public var enabled: Bool = false

    /// nil = not yet confirmed (treated as false everywhere). true = the
    /// user consented to uploading the rule conf. A fresh configuration may
    /// set true directly; an upgraded one stays nil until confirmed.
    public var includeConf: Bool?

    /// Additional named source paths (safe relative names only, validated
    /// with `ICloudSyncPaths.isValidSourceName`). Each name must map to a
    /// payload entry the app actually maintains; unknown names are reported
    /// as skipped by the sync pass, never invented.
    public var additionalSourcePaths: [String] = []

    /// Subdirectory inside the iCloud Drive container (single path
    /// component, no separators — cloud layout parity with the macOS
    /// runtime: sakamoto/...).
    public var directoryName: String = "sakamoto"

    public init() {}

    /// Conf upload consent as the pass consumes it.
    public var includesConf: Bool { includeConf == true }

    public static func decode(_ data: Data) -> ICloudSyncSettings? {
        try? JSONDecoder().decode(ICloudSyncSettings.self, from: data)
    }

    public func encoded() throws -> Data {
        try JSONEncoder().encode(self)
    }
}

// MARK: - Payload

/// The entries this device offers to the sync pass. The app builds this from
/// its ConfigStore staging state; the store validates and syncs it. Entries
/// are pure data — nothing here is read from disk at sync time, so the pass
/// is unit-testable and cannot accidentally widen its scope.
public struct ICloudSyncPayload: Equatable, Sendable {
    public struct Entry: Equatable, Sendable {
        /// Safe relative cloud name ("nodes.txt", "policy.json",
        /// "subscriptions.json", "conf/<file>.conf").
        public var name: String
        public var data: Data

        public init(name: String, data: Data) {
            self.name = name
            self.data = data
        }
    }

    public var entries: [Entry] = []

    public init(entries: [Entry] = []) {
        self.entries = entries
    }

    /// The generated sing-box config content — NEVER a payload entry. This
    /// static name exists so call sites can express the boundary in code and
    /// the denylist rejects it at any depth.
    public static let forbiddenGeneratedConfigName = "config.json"
}

// MARK: - Status

/// Terminal states of one sync pass (docs/icloud.md): disabled → nothing
/// happens; unavailable → no iCloud account/container, local features
/// untouched; synced/upToDate → completed pass; conflict → the pass stopped
/// BEFORE overwriting either copy; failed → I/O problem after some copies
/// may have completed (each completed copy keeps its baseline).
public enum ICloudSyncStatus: Equatable, Sendable {
    case disabled
    /// Enabled but no pass has run yet in this install.
    case waitingFirstRun
    case unavailable(String)
    case upToDate(Date)
    case synced(Date, [String])
    case conflict(Date, [String])
    case failed(Date, String)

    public var isConflict: Bool {
        if case .conflict = self { return true }
        return false
    }

    public var timestamp: Date? {
        switch self {
        case .disabled, .unavailable, .waitingFirstRun: return nil
        case .upToDate(let d), .synced(let d, _), .conflict(let d, _), .failed(let d, _): return d
        }
    }

    /// Single-line rendering for the Settings page.
    public var summary: String {
        switch self {
        case .disabled:
            return "Sync disabled — everything stays on this device."
        case .waitingFirstRun:
            return "Enabled — no sync run yet."
        case .unavailable(let reason):
            return "iCloud unavailable: \(reason)"
        case .upToDate(let date):
            return "Up to date (last sync \(ICloudSyncFormatting.timestamp(date)))"
        case .synced(let date, let updates):
            return "Synced \(updates.count) file(s) at \(ICloudSyncFormatting.timestamp(date))"
        case .conflict(let date, let names):
            return "Conflict — not overwritten: \(names.joined(separator: ", ")) " +
                "(at \(ICloudSyncFormatting.timestamp(date)))"
        case .failed(let date, let message):
            return "Failed at \(ICloudSyncFormatting.timestamp(date)): \(message)"
        }
    }
}

public enum ICloudSyncFormatting {
    public static func timestamp(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.dateStyle = .short
        formatter.timeStyle = .short
        return formatter.string(from: date)
    }
}

// MARK: - Path policy (port of internal/icloud/sources.go)

public enum ICloudSyncLimits {
    /// Per-file upload cap (internal/icloud maxSourceBytes).
    public static let maxSourceBytes = 32 << 20
    /// Conf-parse cap (localRuleIncludes).
    public static let maxConfParseBytes = 16 << 20
    /// Include-graph depth/file limits (discover()).
    public static let maxIncludeDepth = 8
    public static let maxIncludeFiles = 128
}

public enum ICloudSyncPaths {

    /// Port of internal/icloud.ValidSourceName: safe relative source names
    /// only. Rejects absolute paths, backslashes, anything not already in
    /// cleaned form, traversal/hidden components, the `logs/` top directory,
    /// known generated-state and credential filenames, and dangerous
    /// suffixes — at ANY depth. iOS adds its own local staging filenames to
    /// the denylist (same class of local-only state).
    public static func isValidSourceName(_ name: String) -> Bool {
        if name.isEmpty { return false }
        if name.hasPrefix("/") { return false }
        if name.contains("\\") || name.contains("\0") { return false }
        guard cleaned(name) == name else { return false }

        let components = name.split(separator: "/", omittingEmptySubsequences: false).map(String.init)
        if components.first?.lowercased() == "logs" { return false }

        for part in components {
            if part.isEmpty || part == "." || part == ".." || part.hasPrefix(".") { return false }
            let lower = part.lowercased()
            switch lower {
            case "config.json", "sakamoto.yaml", "proxy-restore.json", "auto-proxy.json",
                 "api-rotation.pending.json", "watch.sock", "watch.lock", "svc.sock",
                 "dns-restore.json", "icloud-state.json", "auth.json", "secrets.zsh",
                 // iOS-local staging state (never a cloud source).
                 "sakamoto-config.json", "icloud-sync-settings.json":
                return false
            default:
                break
            }
            for suffix in [".srs", ".log", ".sock", ".lock", ".db", ".db.rule", ".pem", ".key"] {
                if lower.hasSuffix(suffix) { return false }
            }
        }
        return true
    }

    /// Minimal port of filepath.Clean for relative slash paths (the only form
    /// this module accepts).
    static func cleaned(_ path: String) -> String {
        var result: [String] = []
        for part in path.split(separator: "/", omittingEmptySubsequences: true) {
            switch part {
            case ".":
                continue
            case "..":
                if let last = result.last, last != ".." {
                    result.removeLast()
                } else {
                    result.append("..")
                }
            default:
                result.append(String(part))
            }
        }
        return result.joined(separator: "/")
    }

    /// Components of a valid source name, for path walking.
    static func components(of name: String) -> [String] {
        name.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
    }
}

// MARK: - Conf include discovery (port of localRuleIncludes)

public enum ICloudSyncConf {

    /// Local relative include= references found in a Shadowrocket conf's
    /// [General] section. Remote http(s) includes are importer-managed and
    /// deliberately NOT returned (docs/icloud.md).
    public static func localIncludes(ofConfContent data: Data) throws -> [String] {
        guard data.count <= ICloudSyncLimits.maxConfParseBytes else {
            throw ICloudSyncError.confTooLarge
        }
        var text = String(decoding: data, as: UTF8.self)
        if text.hasPrefix("\u{FEFF}") {
            text.removeFirst()
        }
        guard hasRuleOrGeneralSection(text) else {
            throw ICloudSyncError.confLacksSections
        }

        var includes: [String] = []
        var section = ""
        for rawLine in text.split(separator: "\n", omittingEmptySubsequences: false) {
            // Port of inlineConfComment (`\s//`): strip from the first
            // whitespace-prefixed // comment onward, then trim.
            var line = String(rawLine)
            if let spaceSlash = line.range(of: " //") {
                line = String(line[..<spaceSlash.lowerBound])
            }
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("[") && trimmed.hasSuffix("]") {
                section = trimmed.lowercased()
                continue
            }
            guard section == "[general]" else { continue }
            guard let equals = trimmed.firstIndex(of: "=") else { continue }
            let key = trimmed[..<equals].trimmingCharacters(in: .whitespaces)
            guard key.lowercased() == "include" else { continue }
            let value = trimmed[trimmed.index(after: equals)...]
            for piece in value.split(separator: ",") {
                let include = piece.trimmingCharacters(in: .whitespaces)
                if include.isEmpty { continue }
                if include.hasPrefix("https://") || include.hasPrefix("http://") { continue }
                guard ICloudSyncPaths.isValidSourceName("conf/" + include) else {
                    throw ICloudSyncError.unsafeInclude(include)
                }
                includes.append(include)
            }
        }
        return includes
    }

    /// Port of ruleConfSection: a real [General] or [Rule] section header
    /// line (optionally followed by a // comment).
    static func hasRuleOrGeneralSection(_ text: String) -> Bool {
        for rawLine in text.split(separator: "\n", omittingEmptySubsequences: false) {
            var line = String(rawLine)
            if let spaceSlash = line.range(of: " //") {
                line = String(line[..<spaceSlash.lowerBound])
            }
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            let lower = trimmed.lowercased()
            if lower == "[general]" || lower == "[rule]" {
                return true
            }
        }
        return false
    }
}

// MARK: - Errors

public enum ICloudSyncError: Error, Equatable, Sendable {
    case forbiddenSourceName(String)
    case confTooLarge
    case confLacksSections
    case unsafeInclude(String)
    case noSourcesConfigured
    case containerUnavailable
    case sourceTooLarge(String)
    case pathEscapesRoot(String)
    case symlinkRefused(String)
    case deletedLocally(String)
    case deletedInCloud(String)
    case conflict(String)
    case filesystem(String)
}
