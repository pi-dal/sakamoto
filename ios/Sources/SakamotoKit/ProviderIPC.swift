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
    case reloadProfile(options: TunnelStartOptions, transaction: UUID? = nil, replacing: TunnelConfigurationReceipt? = nil, requestID: UUID? = nil)
    case recoverExperiment
    case removeLearnedDomain(String)
    case location(settings: VirtualLocationSettings?)

    enum Action: String {
        case ping
        case reloadConfig, reloadProfile, reloadProvisional, restoreProfile, recoverExperiment, removeLearnedDomain, location, locationRoute
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
        case .location(let settings):
            var raw = ["action": settings?.route == nil ? Action.location.rawValue : Action.locationRoute.rawValue]
            if let settings { raw["settingsJSON"] = try settings.json() }
            return try encoder.encode(raw)
        case .ping:
            return try encoder.encode(["action": Action.ping.rawValue])
        case .recoverExperiment:
            return try encoder.encode(["action": Action.recoverExperiment.rawValue])
        case .removeLearnedDomain(let domain):
            return try encoder.encode(["action": Action.removeLearnedDomain.rawValue, "domain": domain])
        case .reloadProfile(let options, let transaction, let replacing, let requestID):
            let raw = String(decoding: try encoder.encode(options), as: UTF8.self)
            var fields = ["action": Action.reloadProfile.rawValue, "optionsJSON": raw]
            fields["requestID"] = requestID?.uuidString
            if let replacing {
                guard transaction == nil else { throw TunnelProfile.InvalidProfile("Invalid rollback transaction") }
                fields["action"] = Action.restoreProfile.rawValue
                fields["expectedReceipt"] = String(decoding: try encoder.encode(replacing), as: UTF8.self)
            } else if let transaction {
                fields["action"] = Action.reloadProvisional.rawValue
                fields["transaction"] = transaction.uuidString
            }
            return try encoder.encode(fields)
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
        let requestID = raw["requestID"].flatMap(UUID.init(uuidString:))
        switch raw[CodingKeys.action.rawValue] {
        case Action.location.rawValue, Action.locationRoute.rawValue:
            if let settings = raw["settingsJSON"] {
                guard settings.utf8.count <= 4096 else { throw TunnelProfile.InvalidProfile("Location settings too large") }
                let value = try decoder.decode(VirtualLocationSettings.self, from: Data(settings.utf8))
                try value.validate()
                if raw["action"] == Action.locationRoute.rawValue, value.route == nil || !value.enabled { throw TunnelProfile.InvalidProfile("Moving-route command requires an enabled route") }
                self = .location(settings: value)
            } else {
                guard raw["action"] != Action.locationRoute.rawValue else { throw TunnelProfile.InvalidProfile("Moving-route settings missing") }
                self = .location(settings: nil)
            }
        case Action.ping.rawValue:
            self = .ping
        case Action.recoverExperiment.rawValue:
            self = .recoverExperiment
        case Action.removeLearnedDomain.rawValue:
            guard let domain = raw["domain"], domain.count <= 253 else { throw TunnelProfile.InvalidProfile("Missing learned domain") }
            self = .removeLearnedDomain(domain)
        case Action.restoreProfile.rawValue:
            guard let json = raw["optionsJSON"], let expected = raw["expectedReceipt"] else { throw TunnelProfile.InvalidProfile("Missing rollback condition") }
            self = .reloadProfile(options: try decoder.decode(TunnelStartOptions.self, from: Data(json.utf8)), replacing: try decoder.decode(TunnelConfigurationReceipt.self, from: Data(expected.utf8)), requestID: requestID)
        case Action.reloadProvisional.rawValue:
            guard let json = raw["optionsJSON"], let transaction = raw["transaction"].flatMap(UUID.init(uuidString:)) else { throw TunnelProfile.InvalidProfile("Invalid provisional reload") }
            self = .reloadProfile(options: try decoder.decode(TunnelStartOptions.self, from: Data(json.utf8)), transaction: transaction, requestID: requestID)
        case Action.reloadProfile.rawValue:
            guard let json = raw["optionsJSON"] else { throw TunnelProfile.InvalidProfile("Missing profile reload options") }
            self = .reloadProfile(options: try decoder.decode(TunnelStartOptions.self, from: Data(json.utf8)), requestID: requestID)
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

/// Retain completion evidence across concurrent reloads for longer than the
/// transport timeout and rollback observation window. Only opaque IDs leave the provider.
public struct TunnelReloadCompletions {
    private var entries: [(id: UUID, time: TimeInterval)] = []
    public init() {}

    public mutating func record(_ id: UUID?) {
        let now = ProcessInfo.processInfo.systemUptime
        entries.removeAll { now - $0.time > 120 }
        if let id { entries.append((id, now)) }
    }

    public var recentIDs: [UUID] {
        let cutoff = ProcessInfo.processInfo.systemUptime - 120
        return entries.filter { $0.time >= cutoff }.map(\.id)
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
    public var nativeInstanceID: Int64?
    public var nativeVersion: String?
    public var recoveryProtocol: Int32?
    /// The provider has finished processing this reload, including any native rollback.
    public var completedReloadID: UUID?
    public var recentCompletedReloadIDs: [UUID]?

    public func hasCompletedReload(_ id: UUID) -> Bool {
        completedReloadID == id || recentCompletedReloadIDs?.contains(id) == true
    }
    public var appliedReceipt: TunnelConfigurationReceipt?

    public init(serviceState: ServiceState, detail: String? = nil, nativeInstanceID: Int64? = nil, nativeVersion: String? = nil, recoveryProtocol: Int32? = nil, appliedReceipt: TunnelConfigurationReceipt? = nil) {
        self.appliedReceipt = appliedReceipt
        self.nativeInstanceID = nativeInstanceID; self.nativeVersion = nativeVersion; self.recoveryProtocol = recoveryProtocol
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
    public var location: VirtualLocationSnapshot?

    enum CodingKeys: String, CodingKey {
        case ok
        case error
        case state, location
    }

    public init(ok: Bool, error: String? = nil, state: TunnelStateSnapshot? = nil, location: VirtualLocationSnapshot? = nil) {
        self.location = location
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
