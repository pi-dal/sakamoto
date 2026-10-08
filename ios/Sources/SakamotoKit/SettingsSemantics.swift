import Foundation

// Settings semantics shared with the macOS TUI (internal/tui settingsRows).
//
// Two kinds of knobs exist, and the difference is structural, not cosmetic:
//
//   1. EDITABLE ON DEVICE — fields of the sing-box config JSON itself. The
//      tunnel provider applies them on Regenerate+Reconnect (the ConfigState
//      machine). Every edit is a validated, pure JSON transform here, so it
//      is unit-testable without Libbox and can never write a malformed
//      config silently (the provider reload still validates and fails loudly).
//   2. HOST-OWNED — fields of the host generator and its sidecar
//      `sakamoto.yaml` (chain SOCKS exit, fallback chain, subscriptions).
//      The generator runs on the sakamoto host; the app never regenerates
//      that output, so this file only DESCRIBES what the current config
//      contains and the UI says where to change the rest. Claiming an
//      on-device toggle for these would be a fake feature.

// MARK: - Read-only description of a generated config

/// What can be factually read back from a generated sing-box config. Values
/// are rendered verbatim; absence means the config does not have the field.
public struct ConfigSemantics: Equatable, Sendable {
    /// `log.level` as configured (TUI choice list: error/warn/info/debug).
    public var logLevel: String?
    /// The exact generated reject rules exist (internal/gen/gen.go shapes).
    public var blockSTUN: Bool
    public var blockQUIC: Bool
    /// `route.final` verbatim — the unmatched-traffic policy the generator
    /// produced ("direct" = TUI Unmatched policy off; the exit tag = on;
    /// learned-domain auto rules are indistinguishable from on here, so no
    /// "auto" is ever claimed from this field alone).
    public var routeFinal: String?
    /// `inbounds[].type == "tun"` stack and strict_route.
    public var tunStack: String?
    public var strictRoute: Bool?
    /// Number of inbounds/outbounds/endpoints — cheap sanity facts.
    public var inboundCount: Int
    public var outboundCount: Int
    public var endpointCount: Int

    public init(
        logLevel: String? = nil, blockSTUN: Bool = false, blockQUIC: Bool = false,
        routeFinal: String? = nil, tunStack: String? = nil, strictRoute: Bool? = nil,
        inboundCount: Int = 0, outboundCount: Int = 0, endpointCount: Int = 0
    ) {
        self.logLevel = logLevel
        self.blockSTUN = blockSTUN
        self.blockQUIC = blockQUIC
        self.routeFinal = routeFinal
        self.tunStack = tunStack
        self.strictRoute = strictRoute
        self.inboundCount = inboundCount
        self.outboundCount = outboundCount
        self.endpointCount = endpointCount
    }
}

public enum ConfigSemanticsReader {
    /// Parse the generator-owned facts out of a config JSON. Returns nil for
    /// structurally invalid input; callers surface that as an error, not as
    /// default values.
    public static func read(_ configJSON: String) -> ConfigSemantics? {
        guard let root = JSONValue.parse(configJSON) else { return nil }
        var semantics = ConfigSemantics()
        semantics.logLevel = root["log"]?["level"]?.stringValue
        semantics.routeFinal = root["route"]?["final"]?.stringValue
        if let rules = root["route"]?["rules"]?.arrayValue {
            semantics.blockSTUN = rules.contains { $0.isRejectRule(protocolName: "stun") }
            semantics.blockQUIC = rules.contains { $0.isRejectRule(network: "udp", port: 443) }
        }
        if let inbounds = root["inbounds"]?.arrayValue {
            semantics.inboundCount = inbounds.count
            if let tun = inbounds.first(where: { $0["type"]?.stringValue == "tun" }) {
                semantics.tunStack = tun["stack"]?.stringValue
                semantics.strictRoute = tun["strict_route"]?.boolValue
            }
        }
        semantics.outboundCount = root["outbounds"]?.arrayValue?.count ?? 0
        semantics.endpointCount = root["endpoints"]?.arrayValue?.count ?? 0
        return semantics
    }
}

// MARK: - Validated on-device edits

public enum SettingsOverrideError: Error, Equatable, LocalizedError {
    case notUTF8
    case invalidConfig(String)
    case rootNotObject
    case unknownLogLevel(String)
    case missingRouteRules

    public var errorDescription: String? {
        switch self {
        case .notUTF8: return "config is not UTF-8 text"
        case .invalidConfig(let detail): return "invalid config: \(detail)"
        case .rootNotObject: return "config root must be a JSON object"
        case .unknownLogLevel(let level): return "log level must be error, warn, info, or debug (got \(level))"
        case .missingRouteRules: return "config has no route.rules array; regenerate on the host first"
        }
    }
}

/// Validated transforms over the saved config JSON for the knobs the TUI
/// also edits and whose storage is the sing-box config itself. Each function
/// returns a NEW string or throws; the input is never mutated in place.
public enum SettingsOverrides {
    /// TUI choice list (internal/tui settingsRows "Log level"). The generator
    /// writes `log.level`; sing-box accepts trace/debug/info/warn/error/fatal/
    /// panic, but sakamoto exposes the TUI's four — staying inside the same
    /// vocabulary both ends render.
    public static let allowedLogLevels = ["error", "warn", "info", "debug"]

    /// Matches the host experiment.ProxyOutbound contract: use the explicit
    /// rs-proxy rule, including a configured chain exit. Never guess a tag.
    public static func experimentProxy(in configJSON: String) -> String? {
        guard let root = try? parseRoot(configJSON),
              let route = root["route"] as? [String: Any],
              let rules = route["rules"] as? [[String: Any]] else { return nil }
        return rules.first(where: {
            ($0["rule_set"] as? [String]) == ["rs-proxy"] && !($0["outbound"] as? String ?? "").isEmpty
        })?["outbound"] as? String
    }

    public static func experimentEnabled(in configJSON: String) -> Bool {
        guard let proxy = experimentProxy(in: configJSON) else { return false }
        return ConfigSemanticsReader.read(configJSON)?.routeFinal == proxy
    }

    public static func setExperiment(_ enabled: Bool, in configJSON: String) throws -> String {
        var root = try parseRoot(configJSON)
        guard let proxy = experimentProxy(in: configJSON),
              let outbounds = root["outbounds"] as? [[String: Any]],
              outbounds.contains(where: { ($0["tag"] as? String) == proxy }),
              outbounds.contains(where: { ($0["tag"] as? String) == "direct" && ($0["type"] as? String) == "direct" }),
              var route = root["route"] as? [String: Any] else {
            throw SettingsOverrideError.invalidConfig("import a configuration with explicit proxy and direct routes first")
        }
        route["final"] = enabled ? proxy : "direct"
        root["route"] = route
        return try serialize(root)
    }

    public static func setLogLevel(_ level: String, in configJSON: String) throws -> String {
        let normalized = level.lowercased()
        guard allowedLogLevels.contains(normalized) else {
            throw SettingsOverrideError.unknownLogLevel(level)
        }
        var root = try parseRoot(configJSON)
        var log = root["log"] as? [String: Any] ?? [:]
        log["level"] = normalized
        root["log"] = log
        return try serialize(root)
    }

    /// Insert/remove the exact generated rule (internal/gen/gen.go:
    /// `{"protocol": "stun", "action": "reject"}`). Insertion position
    /// mirrors the generator: right after the sniff + DNS-hijack rules, so
    /// reject/security precedence matches a freshly generated config.
    public static func setBlockSTUN(_ enabled: Bool, in configJSON: String) throws -> String {
        try setRejectRule(
            enabled, in: configJSON,
            rule: ["protocol": "stun", "action": "reject"],
            matches: { $0.isRejectRule(protocolName: "stun") }
        )
    }

    /// `{"network": "udp", "port": 443, "action": "reject"}` — same contract.
    public static func setBlockQUIC(_ enabled: Bool, in configJSON: String) throws -> String {
        try setRejectRule(
            enabled, in: configJSON,
            rule: ["network": "udp", "port": 443, "action": "reject"],
            matches: { $0.isRejectRule(network: "udp", port: 443) }
        )
    }

    private static func setRejectRule(
        _ enabled: Bool,
        in configJSON: String,
        rule: [String: Any],
        matches: (JSONValue) -> Bool
    ) throws -> String {
        var root = try parseRoot(configJSON)
        guard var route = root["route"] as? [String: Any] else {
            throw SettingsOverrideError.missingRouteRules
        }
        guard var rules = route["rules"] as? [[String: Any]] else {
            throw SettingsOverrideError.missingRouteRules
        }
        let existing = rules.enumerated().filter { matches(JSONValue(dict: $0.element)) }
        if enabled {
            guard existing.isEmpty else { return configJSON } // idempotent
            // Generator order: protections sit at index 2 (after sniff and
            // DNS hijack). Any position is valid sing-box, but matching the
            // generator keeps diffs against a host regeneration minimal.
            rules.insert(rule, at: min(2, rules.count))
        } else {
            guard !existing.isEmpty else { return configJSON } // idempotent
            for (offset, _) in existing.reversed() {
                rules.remove(at: offset)
            }
        }
        route["rules"] = rules
        root["route"] = route
        return try serialize(root)
    }

    private static func parseRoot(_ configJSON: String) throws -> [String: Any] {
        guard let data = configJSON.data(using: .utf8) else {
            throw SettingsOverrideError.notUTF8
        }
        let raw: Any
        do {
            raw = try JSONSerialization.jsonObject(with: data, options: [])
        } catch {
            throw SettingsOverrideError.invalidConfig(error.localizedDescription)
        }
        guard let root = raw as? [String: Any] else {
            throw SettingsOverrideError.rootNotObject
        }
        return root
    }

    private static func serialize(_ root: [String: Any]) throws -> String {
        let data: Data
        do {
            data = try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys, .prettyPrinted])
        } catch {
            throw SettingsOverrideError.invalidConfig(error.localizedDescription)
        }
        return String(data: data, encoding: .utf8) ?? ""
    }
}

// MARK: - Minimal JSON access helpers (Foundation-only, no third party)

/// A tiny read-only JSON value wrapper so the matching helpers above stay
/// readable. Values come from JSONSerialization; numbers are NSNumber.
public struct JSONValue {
    public let raw: Any?

    public init(_ raw: Any?) { self.raw = raw }
    public init(dict: [String: Any]) { self.raw = dict }

    public subscript(key: String) -> JSONValue? {
        guard let dict = raw as? [String: Any], let value = dict[key] else { return nil }
        return JSONValue(value)
    }

    public var stringValue: String? { raw as? String }
    public var boolValue: Bool? {
        (raw as? NSNumber)?.boolValue
    }
    public var intValue: Int? { (raw as? NSNumber)?.intValue }
    public var arrayValue: [JSONValue]? {
        (raw as? [Any])?.map(JSONValue.init)
    }

    /// Matches the generator's STUN reject rule shape exactly:
    /// action=reject AND protocol=stun (extra keys do not disqualify a
    /// user-added variant of the same rule).
    public func isRejectRule(protocolName: String) -> Bool {
        guard self["action"]?.stringValue == "reject" else { return false }
        return self["protocol"]?.stringValue == protocolName
    }

    /// Matches the generator's QUIC reject rule shape: action=reject AND
    /// network=udp AND port=443.
    public func isRejectRule(network: String, port: Int) -> Bool {
        guard self["action"]?.stringValue == "reject" else { return false }
        guard self["network"]?.stringValue == network else { return false }
        return self["port"]?.intValue == port
    }
}

public enum JSONValueFactory {
    public static func parse(_ text: String) -> JSONValue? {
        guard let data = text.data(using: .utf8),
              let raw = try? JSONSerialization.jsonObject(with: data, options: []) else {
            return nil
        }
        return JSONValue(raw)
    }
}

extension JSONValue {
    /// Convenience used by ConfigSemanticsReader and tests.
    fileprivate static func parse(_ text: String) -> JSONValue? {
        JSONValueFactory.parse(text)
    }
}
