import Foundation

// Tailscale vocabulary for the built-in Tailscale endpoint (sing-box
// `endpoints[].type == "tailscale"`, compiled into Libbox.xcframework via the
// upstream default build tags `with_tailscale` + `with_gvisor`).
//
// Two different things are often called "Tailscale support"; this integration
// keeps them strictly apart:
//
//   1. BUILT-IN Tailscale client (what this file models): the sing-box
//      Tailscale endpoint embeds the Tailscale client (sagernet/tailscale
//      fork, tsnet userspace networking) inside the tunnel process. Login
//      state, tailnet name, peers and exit-node selection are real tailnet
//      facts surfaced through the Libbox command channel.
//   2. EXTERNAL Tailscale detection (deliberately NOT implemented here):
//      probing a `tailscale` CLI or a system utun on macOS. That has no
//      meaning on iOS (no CLI, no system daemon), and pretending otherwise
//      would be a fake feature.
//
// The strings below are the wire facts reported by Libbox v1.14.2:
//   - BackendState values come from tailscale/ipn.State.String()
//     ("Stopped", "Starting", "NeedsLogin", "NeedsMachineAuth", "Running").
//   - Actions map 1:1 onto LibboxCommandClient methods that exist in
//     v1.14.2. Everything else is listed in `TailscaleCapabilities.
//     unsupported` with the exact reason, so the UI can say why a button is
//     missing instead of hiding the concept.

/// Normalized tailscaled backend state (ipn.State). `unrecognized` carries
/// values from a newer core verbatim — never guessed into a known bucket.
public enum TailscaleBackendState: Equatable, Sendable {
    case stopped
    case starting
    /// Login required; the login URL arrives separately (authURL).
    case needsLogin
    /// Device authorization required (tailnet policy approval).
    case needsMachineAuth
    case running
    case unrecognized(String)

    public init(backendState: String) {
        switch backendState.trimmingCharacters(in: .whitespacesAndNewlines) {
        case "Stopped": self = .stopped
        case "Starting": self = .starting
        case "NeedsLogin": self = .needsLogin
        case "NeedsMachineAuth": self = .needsMachineAuth
        case "Running": self = .running
        case let other: self = .unrecognized(other)
        }
    }

    /// Whether the tailnet session is usable (peers routable).
    public var running: Bool {
        if case .running = self { return true }
        return false
    }

    /// Whether a login flow must complete before anything else works.
    public var needsLoginFlow: Bool {
        switch self {
        case .needsLogin, .needsMachineAuth: return true
        default: return false
        }
    }

    /// Strict inverse used by round-trip tests: every case carries the exact
    /// wire string it was parsed from.
    public var wireString: String {
        switch self {
        case .stopped: return "Stopped"
        case .starting: return "Starting"
        case .needsLogin: return "NeedsLogin"
        case .needsMachineAuth: return "NeedsMachineAuth"
        case .running: return "Running"
        case .unrecognized(let raw): return raw
        }
    }
}

/// One peer on the tailnet, shaped for the UI. Selection (exit node) is a
/// separate fact and never implies reachability, mirroring the TUI rule that
/// `selected ≠ reachable`.
public struct TailscalePeerSummary: Equatable, Sendable, Identifiable {
    public var stableID: String
    public var hostName: String
    public var dnsName: String
    public var os: String
    public var tailscaleIPs: [String]
    public var online: Bool
    public var active: Bool
    public var expired: Bool
    public var exitNode: Bool
    public var exitNodeOption: Bool
    public var lastSeenUnixSeconds: Int64

    public var id: String { stableID }

    public init(
        stableID: String, hostName: String, dnsName: String, os: String,
        tailscaleIPs: [String], online: Bool, active: Bool, expired: Bool,
        exitNode: Bool, exitNodeOption: Bool, lastSeenUnixSeconds: Int64
    ) {
        self.stableID = stableID
        self.hostName = hostName
        self.dnsName = dnsName
        self.os = os
        self.tailscaleIPs = tailscaleIPs
        self.online = online
        self.active = active
        self.expired = expired
        self.exitNode = exitNode
        self.exitNodeOption = exitNodeOption
        self.lastSeenUnixSeconds = lastSeenUnixSeconds
    }

    public var displayName: String {
        let segment = dnsName.split(separator: ".").first.map(String.init) ?? ""
        return segment.isEmpty ? hostName : segment
    }
}

/// Aggregate tailnet status for one configured Tailscale endpoint.
public struct TailscaleEndpointSummary: Equatable, Sendable, Identifiable {
    public var endpointTag: String
    public var backendState: TailscaleBackendState
    public var networkName: String
    public var magicDNSSuffix: String
    /// Non-empty exactly when the core waits for the browser login flow.
    public var authURL: String
    public var selfPeer: TailscalePeerSummary?
    public var exitNodePeer: TailscalePeerSummary?
    public var peers: [TailscalePeerSummary]

    public var id: String { endpointTag }

    public init(
        endpointTag: String, backendState: TailscaleBackendState,
        networkName: String, magicDNSSuffix: String, authURL: String,
        selfPeer: TailscalePeerSummary?, exitNodePeer: TailscalePeerSummary?,
        peers: [TailscalePeerSummary]
    ) {
        self.endpointTag = endpointTag
        self.backendState = backendState
        self.networkName = networkName
        self.magicDNSSuffix = magicDNSSuffix
        self.authURL = authURL
        self.selfPeer = selfPeer
        self.exitNodePeer = exitNodePeer
        self.peers = peers
    }

    /// Exit-node candidates: peers advertising the option, excluding self and
    /// the currently selected exit node. Empty = no candidates (UI shows the
    /// fact, it does not invent options).
    public var exitNodeCandidates: [TailscalePeerSummary] {
        let selfID = selfPeer?.stableID
        let currentExitID = exitNodePeer?.stableID
        return peers.filter { peer in
            peer.exitNodeOption && peer.stableID != selfID && peer.stableID != currentExitID
        }
    }
}

/// What the built-in integration can and cannot do, with reasons. Data, not
/// UI copy: the UI renders these verbatim so a missing feature is always
/// explainable.
public enum TailscaleCapabilities {
    /// Actions actually wired to Libbox v1.14.2 CommandClient methods.
    public static let supportedActions: [TailscaleAction] = [
        .observeStatus, .setExitNode, .clearExitNodeSelection, .logout, .pingPeer,
    ]

    /// Reasons a capability is absent, kept next to the code that would
    /// otherwise be tempted to fake it.
    public static let unsupported: [TailscaleUnsupportedCapability] = [
        .init(
            capability: .taildrop,
            reason: "libbox v1.14.2 Apple builds set ts_omit_taildrop; file sharing RPCs are absent on iOS"
        ),
        .init(
            capability: .tailscaleSSH,
            reason: "libbox v1.14.2 Apple builds set ts_omit_ssh; Tailscale SSH sessions are absent on iOS"
        ),
        .init(
            capability: .serve,
            reason: "no libbox API exposes tailscale serve/funnel on iOS"
        ),
        .init(
            capability: .loginWithAuthKeyUI,
            reason: "auth keys are configuration input (Keychain -> endpoint config), not a client RPC; the login flow is the auth URL"
        ),
        .init(
            capability: .externalCLIProbe,
            reason: "iOS has no tailscale CLI or system daemon; probing one would be fake support"
        ),
    ]
}

public enum TailscaleCapability: String, Sendable, CaseIterable {
    case taildrop
    case tailscaleSSH = "tailscale-ssh"
    case serve
    case loginWithAuthKeyUI = "login-with-auth-key-ui"
    case externalCLIProbe = "external-cli-probe"
}

public struct TailscaleUnsupportedCapability: Equatable, Sendable, Identifiable {
    public var capability: TailscaleCapability
    public var reason: String

    public var id: String { capability.rawValue }

    public init(capability: TailscaleCapability, reason: String) {
        self.capability = capability
        self.reason = reason
    }
}

/// User-invocable actions (the UI can offer exactly these).
public enum TailscaleAction: String, Sendable, CaseIterable {
    /// Subscribe to the tailnet status stream.
    case observeStatus = "observe-status"
    /// Pick a peer as exit node (Libbox SetTailscaleExitNode).
    case setExitNode = "set-exit-node"
    /// Deselect the exit node (SetTailscaleExitNode with an empty stable ID
    /// clears the selection in the core).
    case clearExitNodeSelection = "clear-exit-node-selection"
    /// Log this node out of the tailnet (Libbox TailscaleLogout).
    case logout
    /// Latency probe of one peer (Libbox StartTailscalePing).
    case pingPeer = "ping-peer"
}

/// Storage contract for the Tailscale auth key. The key is operator input;
/// it must live in the Keychain and must never appear in source, logs, or the
/// repository. Implementations must not return the key from a description or
/// debug printer.
public protocol TailscaleAuthKeyStoring: AnyObject, Sendable {
    func readAuthKey() -> String?
    func storeAuthKey(_ key: String) throws
    func deleteAuthKey() throws
    var hasAuthKey: Bool { get }
}

/// Pure helper that injects the stored auth key into a generated sing-box
/// config's `endpoints[]` entry of type "tailscale". Kept pure (string in,
/// string out) so it is unit-testable without Libbox and without Keychain.
///
/// The key travels to the tunnel process inside configContent at start time
/// (same trust boundary as the upstream client passing profile configs into
/// the extension); it is never written to the repository or to logs.
public enum TailscaleConfigInjection {
    public static func inject(authKey: String, into configJSON: String) throws -> String {
        guard !authKey.isEmpty else { return configJSON }
        guard let data = configJSON.data(using: .utf8) else {
            throw TailscaleConfigError.notUTF8
        }
        let raw: Any
        do {
            raw = try JSONSerialization.jsonObject(with: data, options: [])
        } catch let error as TailscaleConfigError {
            throw error
        } catch {
            throw TailscaleConfigError.invalidConfig(error.localizedDescription)
        }
        guard var root = raw as? [String: Any] else {
            throw TailscaleConfigError.rootNotObject
        }
        guard var endpoints = root["endpoints"] as? [[String: Any]], !endpoints.isEmpty else {
            throw TailscaleConfigError.noTailscaleEndpoint
        }
        var found = false
        endpoints = endpoints.map { item in
            guard let type = item["type"] as? String, type == "tailscale" else {
                return item
            }
            var item = item
            // Keychain wins over any key already in the config text.
            item["auth_key"] = authKey
            found = true
            return item
        }
        guard found else {
            throw TailscaleConfigError.noTailscaleEndpoint
        }
        root["endpoints"] = endpoints
        let serialized = try JSONSerialization.data(withJSONObject: root, options: [.sortedKeys])
        return String(data: serialized, encoding: .utf8) ?? configJSON
    }
}

public enum TailscaleConfigError: Error, Equatable {
    case notUTF8
    case invalidConfig(String)
    case rootNotObject
    case noTailscaleEndpoint
}
