import Foundation
import Combine
import Libbox
import SakamotoKit

// Home model: owns the two control planes (NE transport + Libbox command
// channel) and folds the phase through the Go bridge (Mobilecore), exactly
// like the macOS TUI's Home screen semantics (docs/tui.md):
//
//   - Connect/Disconnect are requested-but-unconfirmed until the provider
//     confirms (Starting/Stopping);
//   - TUN running is not network reachable — the probe decides;
//   - one failed probe stays Unverified (retry state, never a verdict);
//   - selected ≠ reachable (the dot is intent, latency is measurement);
//   - mode changes are the Rule -> Global -> Direct cycle via the bridge.
//
// Command-channel lifecycle: the channel (Libbox CommandClient) follows the
// tunnel — observeTunnel() starts it when the provider reports Running and
// stops it otherwise, including the first launch (no tunnel yet: the model
// boots with the channel down and the UI says so) and app relaunches with a
// tunnel that was already up.

import NetworkExtension
import WidgetKit

/// Display carrier for one probe round. The classification itself runs in
/// Go (MobilecoreClassifyProbe); this struct only carries the rendered
/// strings to the view (gomobile does not export Go structs).
struct ProbeDisplay: Equatable {
    var state: String
    var path: String
    var error: String
}

@MainActor
final class HomeModel: ObservableObject {
    @Published private(set) var phase: SessionPhase = .disconnected
    @Published private(set) var serviceState: ServiceState = .stopped
    @Published private(set) var probe = ProbeDisplay(state: ProbeState.idle.rawValue, path: "", error: "")
    @Published private(set) var routingMode: RoutingMode?
    @Published private(set) var groups: [GroupSnapshot] = []
    @Published private(set) var notice: Notice?
    @Published private(set) var busy = false
    /// Live fact from the command channel; false before the first tunnel
    /// start and while it is down. Drives honest "unavailable" states.
    @Published private(set) var commandChannelActive = false

    let tunnel: TunnelControlling
    let commanding: CoreCommanding?
    let store: ConfigStore

    private var probeTask: Task<Void, Never>?
    private var channelTask: Task<Void, Never>?
    private var groupsTask: Task<Void, Never>?
    private var lifecycleTask: Task<Void, Never>?
    private var observationStarted = false
    private var lastSurface: SystemSurfaceSnapshot?
    private var measuredNode = ""
    private var measuredLatency: Int32?
    private var measuredAt: Date?
    private var verifiedAt: Date?
    private var verificationPhase: SessionPhase?
    private var storeUpdates: AnyCancellable?
    private var requestedConfig: String?
    private var requestedProfileID: String?
    private var requestedExperiment: String?

    init(
        tunnel: TunnelControlling,
        commanding: CoreCommanding?,
        store: ConfigStore
    ) {
        self.tunnel = tunnel
        self.commanding = commanding
        self.store = store
        storeUpdates = store.objectWillChange.sink { [weak self] _ in self?.objectWillChange.send() }
    }

    /// One-time activation from the view: provider snapshot, tunnel
    /// observations and command-channel availability. Idempotent.
    func activate() async {
        await refreshProviderState()
        startObservingTunnel()
        startObservingCommandChannel()
        // A tunnel that was already running (app relaunch) needs its
        // command channel started here; the observation stream yields the
        // current state first, so this also covers it.
        syncCommandChannel()
    }

    // MARK: Connect / Disconnect

    func connect() async {
        guard !busy else { return }
        guard store.canConnect else {
            notice = Notice(kind: .warning, text: "Select a complete, current configuration in Config before connecting.")
            return
        }
        busy = true
        notice = nil
        defer { busy = false }
        // Requested-but-unconfirmed: Starting until the provider confirms.
        serviceState = .starting
        refoldPhase()
        do {
            let keyStore = TailscaleKeychainStore()
            let prepared = try store.connectionContent()
            requestedConfig = prepared
            let content = try TailscaleConfigInjection.inject(
                authKey: keyStore.readAuthKey() ?? "",
                into: prepared
            )
            let options = try store.connectionOptions(content: content)
            requestedProfileID = options.profileID
            requestedExperiment = options.experimentJSON
            try await tunnel.connect(options: options)
            notice = Notice(kind: .progress, text: "Starting")
            await refreshProviderState()
            syncCommandChannel()
        } catch {
            requestedConfig = nil
            serviceState = .stopped
            notice = Notice(kind: .error, text: "connect: \(error.localizedDescription)")
            refoldPhase()
        }
    }

    func disconnect() async {
        guard !busy else { return }
        busy = true
        defer { busy = false }
        // The tunnel is treated as up until the stop is confirmed.
        serviceState = .stopping
        refoldPhase()
        do {
            try await tunnel.disconnect()
        } catch {
            notice = Notice(kind: .error, text: "disconnect: \(error.localizedDescription)")
        }
        // The provider stops asynchronously; poll the snapshot once after a
        // beat instead of claiming success immediately.
        try? await Task.sleep(nanoseconds: 600 * NSEC_PER_MSEC)
        await refreshProviderState()
        syncCommandChannel()
        if !serviceState.running {
            phase = .disconnected
        }
    }

    func refreshProviderState() async {
        do {
            let snapshot = try await tunnel.ping()
            serviceState = snapshot.serviceState
            confirmRequestedConfiguration()
            if let detail = snapshot.detail, !detail.isEmpty {
                notice = Notice(kind: .warning, text: detail)
            } else if notice?.kind == .progress && serviceState != .starting && serviceState != .stopping {
                notice = nil
            }
        } catch {
            // The provider handle cannot be reached at all.
            serviceState = .unavailable
            notice = Notice(kind: .error, text: "provider unreachable: \(error.localizedDescription)")
        }
        refoldPhase()
    }

    // MARK: Command-channel lifecycle (tunnel-following)

    private func startObservingTunnel() {
        guard !observationStarted else { return }
        observationStarted = true
        lifecycleTask = Task { [weak self] in
            let observations = self?.tunnel.observations()
            for await observation in observations ?? AsyncStream { $0.finish() } {
                guard let self else { return }
                self.serviceState = observation.serviceState
                self.confirmRequestedConfiguration()
                if let detail = observation.detail, !detail.isEmpty {
                    self.notice = Notice(kind: .warning, text: detail)
                } else if self.notice?.kind == .progress && self.serviceState != .starting && self.serviceState != .stopping {
                    self.notice = nil
                }
                self.syncCommandChannel()
                self.refoldPhase(conflict: observation.conflict)
            }
        }
    }

    private func startObservingCommandChannel() {
        guard let commanding else { return }
        channelTask = Task { [weak self] in
            let availability = commanding.availability()
            for await active in availability {
                guard let self else { return }
                self.commandChannelActive = active
                if active {
                    // The stream re-reports the core's mode on (re)connect;
                    // pick it up so the Mode row is a fact, not a memory.
                    self.routingMode = commanding.currentClashMode()
                    self.startConsumingGroups()
                } else {
                    self.groups = []
                }
            }
        }
    }

    private func confirmRequestedConfiguration() {
        if serviceState.running, let config = requestedConfig {
            if store.selectedProfileID == requestedProfileID,
               (try? store.connectionOptions().experimentJSON) == requestedExperiment {
                store.connectionConfirmed(content: config)
            }
            requestedConfig = nil; requestedProfileID = nil; requestedExperiment = nil
        }
    }

    private func syncCommandChannel() {
        guard let commanding else { return }
        if serviceState.running {
            commanding.start()
        } else {
            commanding.stop()
        }
    }

    private func startConsumingGroups() {
        guard let commanding else { return }
        groupsTask?.cancel()
        let stream = commanding.groups()
        groupsTask = Task { [weak self] in
            for await snapshots in stream {
                self?.groups = snapshots
                self?.publishSurface()
            }
        }
    }

    // MARK: Mode cycle ([ Mode ] button / `m` equivalent)

    func setRoutingMode(_ mode: RoutingMode) async {
        guard let commanding, commandChannelActive else {
            notice = Notice(kind: .warning, text: "mode: command channel unavailable — connect the tunnel first")
            return
        }
        do {
            try await commanding.setClashMode(mode)
            routingMode = mode
            publishSurface()
            notice = Notice(kind: .info, text: "Mode: \(mode.rawValue)")
        } catch {
            notice = Notice(kind: .error, text: "mode \(mode.rawValue) unavailable — regenerate the config and reconnect")
        }
    }

    func cycleRoutingMode() async {
        guard let commanding else {
            notice = Notice(kind: .warning, text: "mode: command channel unavailable")
            return
        }
        guard commandChannelActive else {
            notice = Notice(kind: .warning, text: "mode: command channel unavailable — connect the tunnel first")
            return
        }
        let next = MobilecoreNextRoutingMode(routingMode?.rawValue ?? commanding.currentClashMode()?.rawValue ?? "")
        guard let nextMode = RoutingMode(rawValue: next) else { return }
        do {
            // The core validates against available modes; an unavailable
            // mode must surface loudly, never a silent no-op.
            try await commanding.setClashMode(nextMode)
            routingMode = nextMode
            publishSurface()
            notice = Notice(kind: .info, text: "Mode: \(nextMode.rawValue)")
        } catch {
            notice = Notice(
                kind: .error,
                text: "mode \(nextMode.rawValue) unavailable — regenerate the config and reconnect"
            )
        }
    }

    func selectNode(groupTag: String, node: NodeSnapshot) async {
        guard let commanding, commandChannelActive else {
            notice = Notice(kind: .warning, text: "selection: command channel unavailable")
            return
        }
        do {
            try await commanding.selectOutbound(groupTag: groupTag, outboundTag: node.tag)
            notice = Notice(kind: .info, text: "selected \(node.tag) — selected is not reachable; run a test")
        } catch {
            notice = Notice(kind: .error, text: "select: \(error.localizedDescription)")
        }
    }

    func testNode(_ node: NodeSnapshot) async {
        guard let commanding, commandChannelActive else {
            notice = Notice(kind: .warning, text: "test: command channel unavailable")
            return
        }
        do {
            try await commanding.urlTest(outboundTag: node.tag)
        } catch {
            notice = Notice(kind: .error, text: "test: \(error.localizedDescription)")
        }
    }

    // MARK: Probe (single path on iOS: system routing through the TUN)

    func runProbe() async {
        probe = ProbeDisplay(state: ProbeState.checking.rawValue, path: "", error: "")
        refoldPhase()
        probeTask?.cancel()
        probeTask = Task { [weak self] in
            guard let self else { return }
            var request = URLRequest(url: URL(string: "https://www.gstatic.com/generate_204")!)
            request.timeoutInterval = 8
            let routePassed: Bool
            do {
                let (_, response) = try await URLSession.shared.data(for: request)
                routePassed = (response as? HTTPURLResponse)?.statusCode == 204
            } catch {
                routePassed = false
            }
            // No separate browser-proxy path on iOS yet: proxyEnabled=false.
            // Classification runs in Go through the bindable flattenings.
            self.probe = ProbeDisplay(
                state: MobilecoreProbeStateOf(routePassed, false, false),
                path: MobilecoreProbePathOf(routePassed, false, false),
                error: MobilecoreProbeErrorOf(routePassed, false, false)
            )
            self.verifiedAt = Date()
            self.refoldPhase()
        }
    }

    // MARK: Phase folding (Go bridge, never re-derived in Swift)

    private func refoldPhase(conflict: Bool = false) {
        phase = SessionPhase(
            raw: MobilecoreSessionPhase(serviceState.rawValue, probe.state, conflict)
        ) ?? (serviceState.running ? .tunRunning : .disconnected)
        publishSurface()
    }

    private func publishSurface() {
        let selection = groups.lazy.compactMap { group -> NodeSnapshot? in
            guard let tag = group.selectedTag else { return nil }
            return group.items.first { $0.tag == tag }
        }.first
        let node = selection?.tag ?? ""
        let latency = selection?.status == .reachable ? selection?.latencyMS : nil
        if node != measuredNode || latency != measuredLatency {
            measuredNode = node; measuredLatency = latency
            measuredAt = latency == nil ? nil : Date()
        }
        // A mode/group repaint must not renew the age of a network probe.
        if verificationPhase != phase || verifiedAt == nil {
            verificationPhase = phase
            verifiedAt = Date()
        }
        let snapshot = SystemSurfaceSnapshot(serviceState: serviceState, phase: phase,
            selectedNode: node, routingMode: routingMode?.rawValue ?? "", latencyMS: latency,
            measuredAt: measuredAt, updatedAt: verifiedAt ?? Date())
        if let previous = lastSurface,
           previous.serviceState == snapshot.serviceState && previous.phase == snapshot.phase &&
           previous.selectedNode == snapshot.selectedNode && previous.routingMode == snapshot.routingMode &&
           previous.latencyMS == snapshot.latencyMS && previous.measuredAt == snapshot.measuredAt &&
           previous.updatedAt == snapshot.updatedAt { return }
        lastSurface = snapshot
        SystemSurfaceStore.write(snapshot)
        SystemSurfaceReload.reload()
    }

    deinit {
        probeTask?.cancel()
        channelTask?.cancel()
        groupsTask?.cancel()
        lifecycleTask?.cancel()
    }
}
