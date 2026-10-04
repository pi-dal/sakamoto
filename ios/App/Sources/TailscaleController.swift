import Foundation
import Libbox
import SakamotoKit

// Libbox-backed Tailscale controller (built-in Tailscale endpoint inside the
// tunnel process, reached over the same command.sock as CoreCommanding).
//
// Lifecycle: this controller owns NO client of its own. It uses the shared
// LibboxCoreCommanding client, so the subscription follows the tunnel
// exactly like Home/Data: attach when the channel is up, detach with the
// reason when it drops, re-attach on tunnel restart. No more
// connect-at-page-open failures on first launch.
//
// Real capabilities (Libbox v1.14.2, verified against the generated
// bindings):
//   - subscribeTailscaleStatus  — live tailnet status stream
//   - setTailscaleExitNode      — pick/clear the exit node
//   - tailscaleLogout           — log this node out of the tailnet
//   - startTailscalePing        — per-peer latency probe
// Login itself is NOT an RPC: when backendState == NeedsLogin the status
// stream carries authURL and the user completes it in a browser; the
// endpoint then reports Running. Auth keys are configuration input
// (TailscaleEndpointProvisioning + Keychain injection), never a client call.
//
// Everything not in that list is surfaced through TailscaleCapabilities.
// unsupported with the exact build-tag/API reason — no fake buttons.

@MainActor
final class TailscaleController: ObservableObject {
    @Published private(set) var endpoints: [TailscaleEndpointSummary] = []
    @Published private(set) var lastError: String?
    /// Present while a peer ping is in flight (tag -> result when done).
    @Published private(set) var pingResults: [String: TailscalePingOutcome] = [:]
    /// Mirrors the command channel: false means "no tunnel / no status".
    @Published private(set) var channelActive = false

    private let commanding: LibboxCoreCommanding?
    private var client: LibboxCommandClient?
    private var subscription: LibboxTailscaleStatusSubscription?
    private var statusHandler: StatusHandler?
    private var availabilityTask: Task<Void, Never>?

    init(commanding: LibboxCoreCommanding?) {
        self.commanding = commanding
    }

    fileprivate func recordPingResult(peerID: String, outcome: TailscalePingOutcome) {
        pingResults[peerID] = outcome
    }

    struct TailscalePingOutcome: Equatable {
        var latencyMS: Int32?
        var isDirect: Bool?
        var viaRelay: String?
        var error: String?
    }

    func subscribe() {
        guard availabilityTask == nil else { return }
        guard let commanding else {
            lastError = "no command bridge in this build"
            return
        }
        availabilityTask = Task { [weak self] in
            let availability = commanding.availability()
            for await active in availability {
                guard let self else { return }
                await self.handleChannel(active)
            }
        }
    }

    func cancel() {
        availabilityTask?.cancel()
        availabilityTask = nil
        detach()
        endpoints = []
        lastError = nil
    }

    @MainActor
    private func handleChannel(_ active: Bool) async {
        channelActive = active
        if active {
            guard let commanding, subscription == nil, let client = commanding.currentClient else { return }
            statusHandler = StatusHandler(controller: self)
            do {
                let handler = statusHandler!
                let subscription = try await Task.detached(priority: .userInitiated) {
                    try client.subscribeTailscaleStatus(handler)
                }.value
                self.client = client
                self.subscription = subscription
                lastError = nil
            } catch {
                statusHandler = nil
                lastError = "tailscale status: \(error.localizedDescription)"
            }
        } else {
            detach()
            endpoints = []
            lastError = commanding?.lastChannelError ?? "command channel unavailable — connect the tunnel first"
        }
    }

    private func detach() {
        try? subscription?.close()
        subscription = nil
        statusHandler = nil
        client = nil
    }

    // MARK: Actions

    func setExitNode(endpointTag: String, peer: TailscalePeerSummary?) async {
        let stableID = peer?.stableID ?? ""
        await run { try $0.setTailscaleExitNode(endpointTag, stableID: stableID) }
    }

    func logout(endpointTag: String) async {
        await run { try $0.tailscaleLogout(endpointTag) }
    }

    func ping(endpointTag: String, peer: TailscalePeerSummary) async {
        guard let address = peer.tailscaleIPs.first else { return }
        let handler = PingHandler(controller: self, peerID: peer.stableID)
        await run { (client: LibboxCommandClient) in
            // The session reports once through the handler and is closed
            // below after a bounded wait (a one-shot probe, not a stream).
            let session = try client.startTailscalePing(endpointTag, peerIP: address, handler: handler)
            try await Task.sleep(nanoseconds: 5 * NSEC_PER_SEC)
            try? session.close()
        }
    }

    private func run(_ action: @escaping @Sendable (LibboxCommandClient) async throws -> Void) async {
        guard let client else {
            lastError = "tailscale: command channel unavailable — connect the tunnel first"
            return
        }
        do {
            try await Task.detached(priority: .userInitiated) {
                try await action(client)
            }.value
        } catch {
            lastError = error.localizedDescription
        }
    }

    // MARK: Status intake (MainActor)

    fileprivate func applySummaries(_ summaries: [TailscaleEndpointSummary]) {
        endpoints = summaries
        lastError = nil
    }

    fileprivate func applyError(_ message: String?) {
        // Stream-level errors while attached (endpoint stopped, core
        // reload); show the cause, the channel watcher re-attaches.
        lastError = message
    }

    // MARK: Mapping (pure, unit-testable)

    nonisolated static func map(_ update: LibboxTailscaleStatusUpdate?) -> [TailscaleEndpointSummary] {
        guard let update else { return [] }
        var summaries: [TailscaleEndpointSummary] = []
        if let iterator = update.endpoints() {
            while iterator.hasNext() {
                guard let endpoint = iterator.next() else { break }
                summaries.append(mapEndpoint(endpoint))
            }
        }
        return summaries
    }

    private nonisolated static func mapEndpoint(_ endpoint: LibboxTailscaleEndpointStatus) -> TailscaleEndpointSummary {
        var peers: [TailscalePeerSummary] = []
        if let groupIterator = endpoint.userGroups() {
            while groupIterator.hasNext() {
                guard let group = groupIterator.next() else { break }
                if let peerIterator = group.peers() {
                    while peerIterator.hasNext() {
                        guard let peer = peerIterator.next() else { break }
                        peers.append(mapPeer(peer))
                    }
                }
            }
        }
        // Deduplicate peers that appear in several user groups (same tailnet
        // mesh, multiple group rows) — identity is the stable ID.
        var seen = Set<String>()
        peers = peers.filter { seen.insert($0.stableID).inserted }
        return TailscaleEndpointSummary(
            endpointTag: endpoint.endpointTag,
            backendState: TailscaleBackendState(backendState: endpoint.backendState),
            networkName: endpoint.networkName,
            magicDNSSuffix: endpoint.magicDNSSuffix,
            authURL: endpoint.authURL,
            selfPeer: endpoint.self_.map(mapPeer),
            exitNodePeer: endpoint.exitNode.map(mapPeer),
            peers: peers
        )
    }

    private nonisolated static func mapPeer(_ peer: LibboxTailscalePeer) -> TailscalePeerSummary {
        var ips: [String] = []
        if let ipIterator = peer.tailscaleIPs() {
            while ipIterator.hasNext() {
                ips.append(ipIterator.next())
            }
        }
        return TailscalePeerSummary(
            stableID: peer.stableID,
            hostName: peer.hostName,
            dnsName: peer.dnsName,
            os: peer.os,
            tailscaleIPs: ips,
            online: peer.online,
            active: peer.active,
            expired: peer.expired,
            exitNode: peer.exitNode,
            exitNodeOption: peer.exitNodeOption,
            lastSeenUnixSeconds: peer.lastSeen
        )
    }
}

private final class StatusHandler: NSObject, LibboxTailscaleStatusHandlerProtocol {
    private weak var controller: TailscaleController?

    init(controller: TailscaleController) {
        self.controller = controller
    }

    func onStatusUpdate(_ status: LibboxTailscaleStatusUpdate?) {
        // Libbox calls on its own thread; the mapping is pure, the publish
        // hop is MainActor.
        let summaries = TailscaleController.map(status)
        Task { @MainActor [weak self] in
            self?.controller?.applySummaries(summaries)
        }
    }

    func onError(_ message: String?) {
        Task { @MainActor [weak self] in
            self?.controller?.applyError(message)
        }
    }
}

private final class PingHandler: NSObject, LibboxTailscalePingHandlerProtocol {
    private weak var controller: TailscaleController?
    private let peerID: String

    init(controller: TailscaleController, peerID: String) {
        self.controller = controller
        self.peerID = peerID
    }

    func onPingResult(_ result: LibboxTailscalePingResult?) {
        guard let result else { return }
        let outcome = TailscaleController.TailscalePingOutcome(
            latencyMS: result.error.isEmpty ? Int32(result.latencyMs) : nil,
            isDirect: result.error.isEmpty ? result.isDirect : nil,
            viaRelay: result.peerRelay.isEmpty ? nil : result.peerRelay,
            error: result.error.isEmpty ? nil : result.error
        )
        Task { @MainActor [weak self] in
            self?.controller?.recordPingResult(peerID: self?.peerID ?? "", outcome: outcome)
        }
    }

    func onError(_ message: String?) {
        let outcome = TailscaleController.TailscalePingOutcome(
            latencyMS: nil, isDirect: nil, viaRelay: nil, error: message
        )
        Task { @MainActor [weak self] in
            self?.controller?.recordPingResult(peerID: self?.peerID ?? "", outcome: outcome)
        }
    }
}
