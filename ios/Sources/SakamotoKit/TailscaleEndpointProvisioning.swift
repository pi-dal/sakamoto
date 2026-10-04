import Foundation

// Enable/config of the BUILT-IN Tailscale ENDPOINT inside a generated
// sing-box config.
//
// Semantics verified against sing-box v1.14.2
// (docs/configuration/endpoint/tailscale.md + protocol/tailscale/endpoint.go):
//
//   - Tailscale is an ENDPOINT (`endpoints[].type == "tailscale"`): "a
//     protocol with inbound and outbound behavior" in one process. It is
//     NOT an outbound-group node; sakamoto never references it as a proxy
//     outbound and the UI must not present it as one.
//   - All endpoint fields are optional: `{"type": "tailscale", "tag": …}`
//     is a complete, legal endpoint. Login then flows through the auth URL
//     (or an injected auth_key); state persists under the libbox base path.
//   - `auth_key` is configuration input. sakamoto keeps it in the Keychain
//     and injects it ONLY into the start-time config content
//     (TailscaleConfigInjection). It must never enter UserDefaults, logs,
//     or this file's output beyond what the operator's own config carries.
//
// The host generator (internal/gen) does not emit `endpoints[]`, so before
// this provisioning runs, a saved config has no Tailscale endpoint and
// TailscaleConfigInjection fails with `noTailscaleEndpoint` — a blocking
// error by design. Enable writes the endpoint into the SAVED config draft
// (ConfigState → NeedsRegenerate), keeping the Regenerate+Reconnect order
// that every other config change follows.

/// Operator-settable subset of the endpoint fields (docs tailscale.md).
/// Everything else stays at core defaults — a minimal legal endpoint.
public struct TailscaleEndpointOptions: Equatable, Sendable {
    /// Endpoint tag. Must not collide with other endpoint/outbound tags.
    public static let defaultTag = "tailscale-in"

    public var tag: String
    /// Tailscale hostname of this node (letters/digits/hyphens, <= 63).
    /// Unset = platform default (on iOS the device name).
    public var hostname: String?
    /// Accept subnet routes advertised by other tailnet nodes.
    public var acceptRoutes: Bool
    /// Initial exit node (name or stable ID/IP). Runtime selection via the
    /// command channel (SetTailscaleExitNode) overrides this at runtime.
    public var exitNode: String?
    /// Route locally reachable subnets via the exit node instead of direct.
    public var exitNodeAllowLANAccess: Bool

    public init(
        tag: String = TailscaleEndpointOptions.defaultTag,
        hostname: String? = nil,
        acceptRoutes: Bool = false,
        exitNode: String? = nil,
        exitNodeAllowLANAccess: Bool = false
    ) {
        self.tag = tag
        self.hostname = hostname
        self.acceptRoutes = acceptRoutes
        self.exitNode = exitNode
        self.exitNodeAllowLANAccess = exitNodeAllowLANAccess
    }
}

public enum TailscaleEndpointError: Error, Equatable, LocalizedError {
    case notUTF8
    case invalidConfig(String)
    case rootNotObject
    case invalidTag(String)
    case tagCollision(String)
    case invalidHostname(String)
    case invalidExitNode(String)

    public var errorDescription: String? {
        switch self {
        case .notUTF8: return "config is not UTF-8 text"
        case .invalidConfig(let detail): return "invalid config: \(detail)"
        case .rootNotObject: return "config root must be a JSON object"
        case .invalidTag(let tag): return "endpoint tag must be a non-empty tag without whitespace (got \(tag))"
        case .tagCollision(let tag): return "tag \(tag) is already used by an outbound or endpoint"
        case .invalidHostname(let name): return "hostname may contain letters, digits and hyphens only, up to 63 characters (got \(name))"
        case .invalidExitNode(let node): return "exit node must be a non-empty name or IP without control characters (got \(node))"
        }
    }
}

public enum TailscaleEndpointProvisioning {
    /// Whether at least one tailscale endpoint is configured.
    public static func isEnabled(in configJSON: String) -> Bool {
        guard let root = JSONValueFactory.parse(configJSON),
              let endpoints = root["endpoints"]?.arrayValue else {
            return false
        }
        return endpoints.contains { $0["type"]?.stringValue == "tailscale" }
    }

    /// Current operator-settable options of the FIRST tailscale endpoint,
    /// or nil when none is configured (enabled == false). Fields the
    /// operator never set read back as nil/false (core defaults).
    public static func describe(in configJSON: String) -> TailscaleEndpointOptions? {
        guard let root = JSONValueFactory.parse(configJSON),
              let endpoints = root["endpoints"]?.arrayValue,
              let endpoint = endpoints.first(where: { $0["type"]?.stringValue == "tailscale" }) else {
            return nil
        }
        return TailscaleEndpointOptions(
            tag: endpoint["tag"]?.stringValue ?? TailscaleEndpointOptions.defaultTag,
            hostname: endpoint["hostname"]?.stringValue,
            acceptRoutes: endpoint["accept_routes"]?.boolValue ?? false,
            exitNode: endpoint["exit_node"]?.stringValue,
            exitNodeAllowLANAccess: endpoint["exit_node_allow_lan_access"]?.boolValue ?? false
        )
    }

    /// Enable (append a minimal legal endpoint) or update the existing one
    /// with `options`. Idempotent for an already-identical endpoint. Never
    /// touches `auth_key`: that stays Keychain-only until the start-time
    /// injection (TailscaleConfigInjection).
    @discardableResult
    public static func enable(
        options: TailscaleEndpointOptions, in configJSON: String
    ) throws -> String {
        try validate(options)
        let data = try configData(configJSON)
        let raw: Any
        do {
            raw = try JSONSerialization.jsonObject(with: data, options: [])
        } catch {
            throw TailscaleEndpointError.invalidConfig(error.localizedDescription)
        }
        guard var root = raw as? [String: Any] else {
            throw TailscaleEndpointError.rootNotObject
        }

        var endpoints = (root["endpoints"] as? [[String: Any]]) ?? []
        // Tag uniqueness: endpoints and outbounds share one namespace at the
        // core (rule actions route to both), so collide loudly here.
        let outboundTags = Set(
            ((root["outbounds"] as? [[String: Any]]) ?? []).compactMap { $0["tag"] as? String }
        )
        let otherEndpointTags = Set(
            endpoints
                .filter { ($0["type"] as? String) != "tailscale" || ($0["tag"] as? String) != options.tag }
                .compactMap { $0["tag"] as? String }
        )
        if outboundTags.contains(options.tag) || otherEndpointTags.contains(options.tag) {
            throw TailscaleEndpointError.tagCollision(options.tag)
        }

        var endpoint = endpoints.first {
            ($0["type"] as? String) == "tailscale" && ($0["tag"] as? String) == options.tag
        } ?? endpoints.first {
            ($0["type"] as? String) == "tailscale"
        } ?? ["type": "tailscale", "tag": options.tag]

        endpoint["type"] = "tailscale"
        endpoint["tag"] = options.tag
        // Optional fields: write when set, remove when cleared — absence is
        // the core default, and stale values must not survive an edit.
        setOptional(&endpoint, key: "hostname", value: normalizedOptional(options.hostname))
        endpoint["accept_routes"] = options.acceptRoutes
        setOptional(&endpoint, key: "exit_node", value: normalizedOptional(options.exitNode))
        endpoint["exit_node_allow_lan_access"] = options.exitNodeAllowLANAccess

        if let index = endpoints.firstIndex(where: { ($0["type"] as? String) == "tailscale" }) {
            endpoints[index] = endpoint
        } else {
            endpoints.append(endpoint)
        }
        root["endpoints"] = endpoints
        return try serialize(root)
    }

    /// Remove all tailscale endpoints. Unchanged input returns as-is
    /// (idempotent); a config without `endpoints` is also a no-op.
    @discardableResult
    public static func disable(in configJSON: String) throws -> String {
        let data = try configData(configJSON)
        let raw: Any
        do {
            raw = try JSONSerialization.jsonObject(with: data, options: [])
        } catch {
            throw TailscaleEndpointError.invalidConfig(error.localizedDescription)
        }
        guard var root = raw as? [String: Any] else {
            throw TailscaleEndpointError.rootNotObject
        }
        guard var endpoints = root["endpoints"] as? [[String: Any]] else {
            return configJSON
        }
        let remaining = endpoints.filter { ($0["type"] as? String) != "tailscale" }
        if remaining.count == endpoints.count {
            return configJSON
        }
        if remaining.isEmpty {
            root.removeValue(forKey: "endpoints")
        } else {
            root["endpoints"] = remaining
        }
        return try serialize(root)
    }

    // MARK: Validation / plumbing

    private static func validate(_ options: TailscaleEndpointOptions) throws {
        let tag = options.tag
        if tag.isEmpty || tag.contains(where: { $0.isWhitespace || $0.isNewline }) {
            throw TailscaleEndpointError.invalidTag(tag)
        }
        if let hostname = options.hostname {
            let asciiHostname = hostname.allSatisfy { char in
                (char.isASCII && char.isLetter) || (char.isASCII && char.isNumber) || char == "-"
            }
            let valid = !hostname.isEmpty
                && hostname.count <= 63
                && asciiHostname
                && hostname.first != "-" && hostname.last != "-"
            if !valid {
                throw TailscaleEndpointError.invalidHostname(hostname)
            }
        }
        if let exitNode = options.exitNode {
            // A name or IP; whitespace/control characters are never valid.
            let hasControlChar = exitNode.contains { char in
                guard let ascii = char.asciiValue else { return false }
                return ascii < 32 || ascii == 127
            }
            let valid = !exitNode.isEmpty
                && !exitNode.contains(where: { $0.isWhitespace || $0.isNewline })
                && !hasControlChar
            if !valid {
                throw TailscaleEndpointError.invalidExitNode(exitNode)
            }
        }
    }

    private static func normalizedOptional(_ value: String?) -> String? {
        guard let value, !value.isEmpty else { return nil }
        return value
    }

    private static func setOptional(_ endpoint: inout [String: Any], key: String, value: String?) {
        if let value {
            endpoint[key] = value
        } else {
            endpoint.removeValue(forKey: key)
        }
    }

    private static func configData(_ configJSON: String) throws -> Data {
        guard let data = configJSON.data(using: .utf8) else {
            throw TailscaleEndpointError.notUTF8
        }
        return data
    }

    private static func serialize(_ root: [String: Any]) throws -> String {
        let data: Data
        do {
            data = try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys, .prettyPrinted])
        } catch {
            throw TailscaleEndpointError.invalidConfig(error.localizedDescription)
        }
        return String(data: data, encoding: .utf8) ?? ""
    }
}
