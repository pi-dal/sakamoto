import Foundation

// The two control planes between an iOS app and the running tunnel,
// mirroring how the macOS TUI talks to its supervisor and how
// sing-box-for-apple splits its transports:
//
//   TunnelControlling  — the NE transport (NEVPNManager /
//                        NETunnelProviderSession): Connect, Disconnect,
//                        config reload, liveness. Implemented today by
//                        SakamotoNE.NETunnelController.
//
//   CoreCommanding     — the Libbox command channel (CommandClient ->
//                        CommandServer inside the tunnel provider):
//                        clash mode, node selection, URL tests, group
//                        observation. Implemented by the future Libbox
//                        bridge; deliberately NOT implementable over NE
//                        provider messages, so no type here may pretend
//                        otherwise.
//
// Product semantics follow the macOS TUI exactly (docs/tui.md):
// Connect/Disconnect are the [ Connect ] / [ Disconnect ] actions, the
// phase is folded by mobilecore.SessionPhase, mode changes are the
// Rule -> Global -> Direct cycle, and a config change needs
// Regenerate -> reconnect before the core uses it (ConfigState).

/// What the NE transport reports about the tunnel itself. The status-bar
/// phase is NOT computed here: feed these observations through the gomobile
/// bridge (mobilecore.SessionPhase) and render its result verbatim.
public struct TunnelObservation: Equatable, Sendable {
    public var serviceState: ServiceState
    /// Another VPN TUN is active while ours runs (core: conflict only
    /// counts while running; otherwise it surfaces as a Notice).
    public var conflict: Bool
    /// Optional detail text (e.g. the last provider error).
    public var detail: String?

    public init(serviceState: ServiceState, conflict: Bool = false, detail: String? = nil) {
        self.serviceState = serviceState
        self.conflict = conflict
        self.detail = detail
    }
}

/// NE transport boundary. Implementations must keep the mobilecore rule:
/// Starting/Stopping are requested-but-unconfirmed; only provider-confirmed
/// reports may move the observation to Running/Stopped.
public protocol TunnelControlling: AnyObject, Sendable {
    /// [ Connect ]: install/start the tunnel with the given generated
    /// config. Throws on NE errors; it never reports success for a request
    /// that was only queued.
    func connect(options: TunnelStartOptions) async throws

    /// [ Disconnect ]: stop the tunnel.
    func disconnect() async throws

    /// Liveness + provider state snapshot (TunnelRequest.ping).
    func ping() async throws -> TunnelStateSnapshot

    /// Apply a regenerated config to a running tunnel
    /// (TunnelRequest.reloadConfig). The caller updates ConfigState through
    /// the bridge (mobilecore.ConfigStateTransition with .applied) only
    /// after this returns successfully.
    func reload(configContent: String) async throws

    /// Monotonic stream of tunnel observations. The stream ends when the
    /// controller is deinitialized.
    func observations() -> AsyncStream<TunnelObservation>
    func reload(options: TunnelStartOptions) async throws
    func recoverExperiment() async throws
    func removeLearnedDomain(_ domain: String) async throws
}

public extension TunnelControlling {
    func reload(options: TunnelStartOptions) async throws { try await reload(configContent: options.configContent) }
    func recoverExperiment() async throws { throw TunnelProfile.InvalidProfile("Experiment recovery is unavailable on this transport") }
    func removeLearnedDomain(_ domain: String) async throws { throw TunnelProfile.InvalidProfile("Learned routes are unavailable on this transport") }
}

/// One proxy group as seen through the Libbox command channel, already
/// shaped to the core.GroupState semantics.
public struct GroupSnapshot: Equatable, Sendable {
    public var tag: String
    /// selector groups accept a direct user choice; URL-test groups choose
    /// automatically (core.GroupKind).
    public var selectable: Bool
    /// The group's currently active outbound; nil until the core reports
    /// a selection.
    public var selectedTag: String?
    public var items: [NodeSnapshot]

    public init(
        tag: String, selectable: Bool, selectedTag: String?, items: [NodeSnapshot]
    ) {
        self.tag = tag
        self.selectable = selectable
        self.selectedTag = selectedTag
        self.items = items
    }
}

/// One node in a group. `status` is computed on the Go side
/// (mobilecore.NodeStatus(delayMS, testing) -> core.NodeStatus) so iOS
/// never re-derives latency semantics. `selected` is the filled dot: an
/// intent, never a connectivity claim.
public struct NodeSnapshot: Equatable, Sendable {
    public var tag: String
    public var status: NodeStatus
    public var latencyMS: Int32
    public var selected: Bool

    public init(tag: String, status: NodeStatus, latencyMS: Int32, selected: Bool) {
        self.tag = tag
        self.status = status
        self.latencyMS = latencyMS
        self.selected = selected
    }
}

/// Libbox command-channel boundary (CommandClient over the CommandServer
/// inside the tunnel). Requires Libbox.xcframework; the bridge that
/// implements this lives outside the SPM package until the framework
/// exists (see README.md "Boundaries").
///
/// Lifecycle: the CommandServer only exists while the tunnel provider runs,
/// so implementations must treat start/stop as desired-state with retries —
/// `start()` before the tunnel is up must keep trying (and eventually
/// succeed when the provider appears), not fail permanently. Availability
/// is observable so pages can render a real "unavailable" instead of
/// guessing.
public protocol CoreCommanding: AnyObject, Sendable {
    /// Begin trying to reach the CommandServer (idempotent). Called when the
    /// tunnel observation reports Running; implementations retry until the
    /// server answers or `stop()` arrives.
    func start()

    /// Give up and tear the client down (idempotent). Called when the
    /// tunnel is no longer running.
    func stop()

    /// Whether the command channel is connected right now. False is a fact
    /// (server not reachable yet/gone), never a guess about the tunnel.
    var isAvailable: Bool { get }

    /// The last reason the channel failed to connect or dropped, for an
    /// honest "unavailable" row. Nil while connected / before attempts.
    var lastChannelError: String? { get }

    /// Availability stream; yields the current value first, then every
    /// change. Multiple subscribers are supported (Home, Data, Tailscale).
    func availability() -> AsyncStream<Bool>

    /// [ Mode: X ] button / `m` key equivalent. Send the lowercase clash
    /// string (RoutingMode.clashModeValue); the core validates against its
    /// available modes — an unavailable mode must surface
    /// "regenerate the config and reconnect" (core.ErrRoutingModeUnavailable
    /// wording), never a silent no-op.
    func setClashMode(_ mode: RoutingMode) async throws

    /// The mode the core reports (nil before the first clash-mode message).
    func currentClashMode() -> RoutingMode?

    /// Pick an outbound in a selector group (or delegate through the
    /// manual-pick chain for URL-test members).
    func selectOutbound(groupTag: String, outboundTag: String) async throws

    /// Trigger a URL test for one outbound ("Testing…" until the stream
    /// reports the new status).
    func urlTest(outboundTag: String) async throws

    /// Group/node stream (CommandClient groups stream) mapped to
    /// GroupSnapshot with Go-derived NodeStatus values.
    func groups() -> AsyncStream<[GroupSnapshot]>

    /// Status stream (CommandStatus): traffic rates/totals. Nil snapshot
    /// values in the stream mean trafficAvailable=false — unavailable, not
    /// zero.
    func traffic() -> AsyncStream<TrafficSnapshot>

    /// Connection stream (CommandConnections) folded into a recent-first
    /// list, mirroring the TUI Data page's m.conns map.
    func connections() -> AsyncStream<[ConnectionRecord]>

    /// Core log tail (CommandLog), oldest first, bounded.
    func logs() -> AsyncStream<[String]>

    /// Close one connection (CommandClient CloseConnection). Throws on
    /// failure — callers surface the error as a Notice, never silently.
    func closeConnection(id: String) async throws
}
