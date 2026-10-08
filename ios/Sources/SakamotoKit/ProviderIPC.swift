import Foundation

// Minimal App <-> PacketTunnelProvider message protocol, carried by
// NETunnelProviderSession.sendProviderMessage (App -> provider) and the
// provider's reply (handleAppMessage return value, provider -> App).
//
// Scope follows the sing-box-for-apple split: the provider transport only
// handles what the provider process itself owns — loading a regenerated
// config (reloadConfig, mirroring ExtensionProvider.handleAppMessage) and
// liveness/state queries. Everything that talks to the running sing-box
// core (clash mode, node selection, URL tests, traffic) belongs to the
// Libbox command channel (CoreCommanding), NOT to provider messages.
// Adding core commands here would fake a control path the NE transport
// cannot provide.

/// App -> provider request.
public enum TunnelRequest: Equatable, Sendable {
    /// Liveness + state snapshot. The provider replies without touching
    /// the core.
    case ping
    /// Replace the running configuration with a freshly generated one
    /// (ConfigState.NeedsReconnect -> applied). Carries the full generated
    /// sing-box config JSON.
    case reloadConfig(content: String)
    case reloadProfile(options: TunnelStartOptions)
    case recoverExperiment
    case removeLearnedDomain(String)

    enum Action: String {
        case ping
        case reloadConfig, reloadProfile, recoverExperiment, removeLearnedDomain
    }

    enum CodingKeys: String, CodingKey {
        case action
        case configContent, optionsJSON, domain
    }

    // MARK: Encoding

    /// Deterministic JSON (sorted keys) so the bytes are stable across
    /// processes and testable.
    public func encode() throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        switch self {
        case .ping:
            return try encoder.encode(["action": Action.ping.rawValue])
        case .recoverExperiment:
            return try encoder.encode(["action": Action.recoverExperiment.rawValue])
        case .removeLearnedDomain(let domain):
            return try encoder.encode(["action": Action.removeLearnedDomain.rawValue, "domain": domain])
        case .reloadProfile(let options):
            let raw = String(decoding: try encoder.encode(options), as: UTF8.self)
            return try encoder.encode(["action": Action.reloadProfile.rawValue, "optionsJSON": raw])
        case .reloadConfig(let content):
            return try encoder.encode([
                CodingKeys.action.rawValue: Action.reloadConfig.rawValue,
                CodingKeys.configContent.rawValue: content,
            ])
        }
    }

    // MARK: Decoding (provider side)

    /// Strict decoding: an unknown action throws. A newer app talking to an
    /// older provider must surface a loud error, never a silently ignored
    /// request.
    public init(data: Data) throws {
        let decoder = JSONDecoder()
        let raw = try decoder.decode([String: String].self, from: data)
        switch raw[CodingKeys.action.rawValue] {
        case Action.ping.rawValue:
            self = .ping
        case Action.recoverExperiment.rawValue:
            self = .recoverExperiment
        case Action.removeLearnedDomain.rawValue:
            guard let domain = raw["domain"], domain.count <= 253 else { throw TunnelProfile.InvalidProfile("Missing learned domain") }
            self = .removeLearnedDomain(domain)
        case Action.reloadProfile.rawValue:
            guard let json = raw["optionsJSON"] else { throw TunnelProfile.InvalidProfile("Missing profile reload options") }
            self = .reloadProfile(options: try decoder.decode(TunnelStartOptions.self, from: Data(json.utf8)))
        case Action.reloadConfig.rawValue:
            guard let content = raw[CodingKeys.configContent.rawValue] else {
                throw DecodingError.keyNotFound(
                    CodingKeys.configContent,
                    .init(
                        codingPath: [],
                        debugDescription: "reloadConfig request without configContent"
                    )
                )
            }
            self = .reloadConfig(content: content)
        case let action?:
            throw DecodingError.dataCorrupted(
                .init(
                    codingPath: [],
                    debugDescription: "unknown tunnel request action \(action)"
                )
            )
        case nil:
            throw DecodingError.keyNotFound(
                CodingKeys.action,
                .init(codingPath: [], debugDescription: "tunnel request without action")
            )
        }
    }
}

/// What the provider itself knows about the tunnel. Probe results are NOT
/// here: the probe is an app-side observation (docs/tui.md), and the phase
/// is folded by the gomobile bridge, not by the provider.
public struct TunnelStateSnapshot: Codable, Equatable, Sendable {
    /// The provider's view of the tunnel lifecycle (ServiceState
    /// vocabulary). Strictly decoded: an unknown value fails and forces a
    /// contract review.
    public var serviceState: ServiceState
    /// Optional human-readable detail (e.g. the last error text).
    public var detail: String?

    public init(serviceState: ServiceState, detail: String? = nil) {
        self.serviceState = serviceState
        self.detail = detail
    }
}

/// Provider -> App response.
public struct TunnelResponse: Codable, Equatable, Sendable {
    public var ok: Bool
    /// Present when ok == false; carries the failure cause verbatim.
    public var error: String?
    /// Present for queries (ping); absent for mutations that only ack.
    public var state: TunnelStateSnapshot?

    enum CodingKeys: String, CodingKey {
        case ok
        case error
        case state
    }

    public init(ok: Bool, error: String? = nil, state: TunnelStateSnapshot? = nil) {
        self.ok = ok
        self.error = error
        self.state = state
    }

    public static func failure(_ message: String) -> TunnelResponse {
        TunnelResponse(ok: false, error: message)
    }

    public func encode() throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return try encoder.encode(self)
    }

    public static func decode(_ data: Data) throws -> TunnelResponse {
        try JSONDecoder().decode(TunnelResponse.self, from: data)
    }
}
