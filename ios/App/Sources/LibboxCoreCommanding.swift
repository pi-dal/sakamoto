import Foundation
import Libbox
import SakamotoKit

// Libbox-backed implementation of SakamotoKit.CoreCommanding.
//
// Lifecycle (the first-launch fix): the CommandServer lives inside the
// tunnel provider process, so a client created at app start has nothing to
// connect to. start()/stop() are desired-state: start() retries until the
// provider's CommandServer answers (or stop() arrives); the Home model
// calls start() when the tunnel observation reports Running and stop() when
// it reports Stopped/Stopping, so the channel follows the tunnel across
// connect/disconnect cycles and app restarts with a live tunnel. Actions
// that find the channel down THROW — callers turn every failure into a
// Notice; nothing is a silent no-op.
//
// One client carries the streams Home and Data need (Status | Group |
// Connections | Log | ClashMode). Rules carried over from docs/tui.md
// (enforced in Go, honored here):
//   - mode strings travel lowercase (RoutingMode.clashModeValue);
//   - "Testing" is a local intent flag until the group stream reports a new
//     URLTestTime (mobilecore.NodeStatus owns the delay -> status mapping);
//   - `selected` (group.selected) is an intent, never a connectivity claim;
//   - trafficAvailable=false is "unavailable", never zeros.

enum CommandChannelError: LocalizedError {
    case unavailable
    case testTimedOut

    public var errorDescription: String? {
        switch self {
        case .testTimedOut:
            return "No fresh node test result within 20 seconds"
        case .unavailable:
            return "command channel unavailable — connect the tunnel first"
        }
    }
}

final class LibboxCoreCommanding: CoreCommanding, @unchecked Sendable {
    /// Reconnect delay after a failed dial. The libbox dial itself already
    /// waits the server out (handler-bound clients retry internally ~3s);
    /// this covers "tunnel not up at all yet".
    private static let reconnectDelayNanos: UInt64 = 2 * NSEC_PER_SEC
    /// How often the parked supervisor observes the channel state while
    /// connected. handleDisconnected clears `active`; the next tick dials
    /// again — no flag race between loop teardown and a drop.
    private static let parkedPollNanos: UInt64 = 500 * NSEC_PER_MSEC

    private let lock = NSLock()
    private var client: LibboxCommandClient?
    private var clientHandler: GroupStreamHandler?
    private var desiredRunning = false
    private var loopActive = false
    private var active = false
    private var lastConnectError: String?

    private var clashModeValue: RoutingMode?
    private var clashModesSeen = false
    private var pendingTests: [String: Int64] = [:]
    private var failedTests: [String: Int64] = [:]
    private var measurementTimes: [String: Int64] = [:]
    private var connectionTable = ConnectionTable()
    private var logBuffer = LogBuffer()

    private let availabilityBroadcaster = Broadcaster<Bool>()
    private let groupBroadcaster = Broadcaster<[GroupSnapshot]>()
    private let trafficBroadcaster = Broadcaster<TrafficSnapshot>()
    private let connectionsBroadcaster = Broadcaster<[ConnectionRecord]>()
    private let logsBroadcaster = Broadcaster<[String]>()

    // MARK: CoreCommanding lifecycle

    func start() {
        lock.lock()
        desiredRunning = true
        let shouldKick = !loopActive
        loopActive = true
        lock.unlock()
        guard shouldKick else { return }
        Task { [weak self] in
            await self?.runConnectLoop()
            self?.lock.lock()
            self?.loopActive = false
            self?.lock.unlock()
        }
    }

    func stop() {
        lock.lock()
        desiredRunning = false
        let client = self.client
        self.client = nil
        let wasActive = active
        active = false
        pendingTests.removeAll()
        failedTests.removeAll()
        measurementTimes.removeAll()
        // An intentional stop is not an error; the generic "connect the
        // tunnel" hint renders until the next start.
        lastConnectError = nil
        lock.unlock()
        if wasActive {
            availabilityBroadcaster.yield(false)
        }
        guard client != nil || clientHandler != nil else { return }
        Task.detached(priority: .utility) { [weak self] in
            try? client?.disconnect()
            self?.lock.lock()
            self?.clientHandler = nil
            self?.lock.unlock()
        }
    }

    var isAvailable: Bool {
        lock.lock()
        defer { lock.unlock() }
        return active
    }

    func availability() -> AsyncStream<Bool> {
        lock.lock()
        let current = active
        lock.unlock()
        // Subscribes before yielding the current value, so no change that
        // happens after this call can be missed.
        return availabilityBroadcaster.stream(startingWith: current)
    }

    /// The live client for surfaces that need a raw RPC outside this
    /// protocol (Tailscale status subscription). Nil while disconnected.
    var currentClient: LibboxCommandClient? {
        lock.lock()
        defer { lock.unlock() }
        return active ? client : nil
    }

    var lastChannelError: String? {
        lock.lock()
        defer { lock.unlock() }
        return active ? nil : lastConnectError
    }

    private func runConnectLoop() async {
        // Supervisor loop: dial until connected, then park and watch for a
        // drop; every transition is observed from shared state under the
        // lock, so stop() and handleDisconnected cannot race the loop into
        // a missed reconnect.
        while true {
            lock.lock()
            let shouldRun = desiredRunning
            let isActive = active
            lock.unlock()
            guard shouldRun else { return }
            if isActive {
                try? await Task.sleep(nanoseconds: Self.parkedPollNanos)
                continue
            }
            do {
                try await connectClient()
            } catch {
                lock.lock()
                lastConnectError = error.localizedDescription
                lock.unlock()
                try? await Task.sleep(nanoseconds: Self.reconnectDelayNanos)
            }
        }
    }

    private func connectClient() throws {
        lock.lock()
        let existing = client
        lock.unlock()
        if let existing {
            // Existing client: (re)connect it — the tunnel may have come
            // back with a fresh CommandServer socket.
            try existing.connect()
            return
        }
        let options = LibboxCommandClientOptions()
        options.addCommand(LibboxCommandStatus)
        options.addCommand(LibboxCommandGroup)
        options.addCommand(LibboxCommandConnections)
        options.addCommand(LibboxCommandLog)
        options.addCommand(LibboxCommandClashMode)
        // ObjC setter=setStatusInterval: imports as the statusInterval
        // property; the daemon reads it as time.Duration (nanoseconds).
        options.statusInterval = Int64(NSEC_PER_SEC) // 1s cadence
        let handler = GroupStreamHandler(commanding: self)
        let client = LibboxNewCommandClient(handler, options)
        lock.lock()
        if self.client == nil {
            self.client = client
            self.clientHandler = handler
        }
        let assigned = self.client
        lock.unlock()
        try assigned?.connect()
    }

    // MARK: CoreCommanding actions

    func setClashMode(_ mode: RoutingMode) async throws {
        let client = try requireClient()
        try await runOffMain {
            try client.setClashMode(mode.clashModeValue)
        }
    }

    func currentClashMode() -> RoutingMode? {
        lock.lock()
        defer { lock.unlock() }
        return clashModesSeen ? clashModeValue : nil
    }

    func selectOutbound(groupTag: String, outboundTag: String) async throws {
        let client = try requireClient()
        try await runOffMain {
            try client.selectOutbound(groupTag, outboundTag: outboundTag)
        }
    }

    private func beginNodeTest(_ tag: String) {
        lock.lock(); defer { lock.unlock() }
        pendingTests[tag] = measurementTimes[tag] ?? 0
        failedTests.removeValue(forKey: tag)
    }

    private func nodeTestPending(_ tag: String) throws -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard active else { throw CommandChannelError.unavailable }
        return pendingTests[tag] != nil
    }

    private func failNodeTest(_ tag: String) {
        lock.lock(); defer { lock.unlock() }
        failedTests[tag] = pendingTests.removeValue(forKey: tag) ?? measurementTimes[tag] ?? 0
    }

    private func freshTestDelay(_ tag: String) -> UInt64 {
        lock.lock(); defer { lock.unlock() }
        let now = Date().timeIntervalSince1970
        guard let last = measurementTimes[tag], last >= Int64(now) else { return 0 }
        // The core reports second-resolution timestamps. Start in the next
        // second so a quick repeated test cannot reuse the previous stamp.
        return UInt64(max(0, floor(now) + 1.05 - now) * 1_000_000_000)
    }

    func urlTest(outboundTag: String) async throws {
        let client = try requireClient()
        let delay = freshTestDelay(outboundTag)
        if delay > 0 { try await Task.sleep(nanoseconds: delay) }
        beginNodeTest(outboundTag)
        do {
            try await runOffMain { try client.urlTest(outboundTag) }
            // The RPC only queues a measurement. Hold the batch slot until
            // the authoritative group stream reports a newer measurement.
            for _ in 0..<80 {
                try await Task.sleep(nanoseconds: 250_000_000)
                if try !nodeTestPending(outboundTag) { return }
            }
            throw CommandChannelError.testTimedOut
        } catch {
            failNodeTest(outboundTag)
            throw error
        }
    }

    func closeConnection(id: String) async throws {
        let client = try requireClient()
        try await runOffMain {
            try client.closeConnection(id)
        }
        lock.lock()
        connectionTable.remove(id: id)
        let records = connectionTable.sortedByRecent()
        lock.unlock()
        connectionsBroadcaster.yield(records)
    }

    // MARK: CoreCommanding streams

    func groups() -> AsyncStream<[GroupSnapshot]> {
        groupBroadcaster.stream()
    }

    func traffic() -> AsyncStream<TrafficSnapshot> {
        trafficBroadcaster.stream()
    }

    func connections() -> AsyncStream<[ConnectionRecord]> {
        connectionsBroadcaster.stream()
    }

    func logs() -> AsyncStream<[String]> {
        logsBroadcaster.stream()
    }

    // MARK: Stream intake (called from the Libbox handler thread)

    fileprivate func handleConnected() {
        lock.lock()
        guard desiredRunning else {
            // stop() won the race with the dial; drop the connection.
            let client = self.client
            lock.unlock()
            guard let client else { return }
            Task.detached(priority: .utility) { try? client.disconnect() }
            return
        }
        active = true
        lastConnectError = nil
        lock.unlock()
        availabilityBroadcaster.yield(true)
    }

    fileprivate func handleDisconnected(message: String?) {
        lock.lock()
        let wasActive = active
        active = false
        // A drop while the tunnel should be running is an error worth
        // showing; an intentional-stop drop is not.
        if desiredRunning {
            lastConnectError = message
        }
        lock.unlock()
        if wasActive {
            // "No groups/traffic" is not "no nodes/zero traffic"; surfaces
            // subscribe to availability to render a real unavailable state.
            // The supervisor loop observes `active == false` and dials again.
            availabilityBroadcaster.yield(false)
        }
    }

    fileprivate func handleStatus(_ message: LibboxStatusMessage?) {
        guard let message else { return }
        let snapshot = TrafficSnapshot(
            uplink: message.uplink,
            downlink: message.downlink,
            uplinkTotal: message.uplinkTotal,
            downlinkTotal: message.downlinkTotal,
            connectionsIn: message.connectionsIn,
            connectionsOut: message.connectionsOut,
            trafficAvailable: message.trafficAvailable
        )
        trafficBroadcaster.yield(snapshot)
    }

    fileprivate func handleConnectionEvents(_ events: LibboxConnectionEvents?) {
        guard let events else { return }
        var mapped: [ConnectionEvent] = []
        if let iterator = events.iterator() {
            while iterator.hasNext() {
                guard let event = iterator.next() else { break }
                mapped.append(ConnectionEvent(
                    kind: ConnectionEventKind(rawValue: event.type) ?? .update,
                    id: event.id_,
                    record: event.connection.map(Self.mapRecord),
                    uplinkDelta: event.uplinkDelta,
                    downlinkDelta: event.downlinkDelta,
                    closedAt: event.closedAt
                ))
            }
        }
        guard !mapped.isEmpty || events.reset else { return }
        lock.lock()
        connectionTable.apply(mapped, reset: events.reset)
        let records = connectionTable.sortedByRecent()
        lock.unlock()
        connectionsBroadcaster.yield(records)
    }

    fileprivate func handleLogs(_ messageList: LibboxLogIteratorProtocol?) {
        guard let messageList else { return }
        var lines: [String] = []
        while messageList.hasNext() {
            guard let entry = messageList.next() else { break }
            lines.append(entry.message)
        }
        guard !lines.isEmpty else { return }
        lock.lock()
        logBuffer.append(contentsOf: lines)
        let snapshot = logBuffer.allLines
        lock.unlock()
        logsBroadcaster.yield(snapshot)
    }

    fileprivate func handleGroups(_ groups: LibboxOutboundGroupIteratorProtocol?) {
        var snapshots: [GroupSnapshot] = []
        if let groups {
            while groups.hasNext() {
                guard let group = groups.next() else { break }
                var items: [NodeSnapshot] = []
                if let itemIterator = group.getItems() {
                    while itemIterator.hasNext() {
                        guard let item = itemIterator.next() else { break }
                        lock.lock()
                        // A fresh delay result retires the pending test: the
                        // stream is authoritative once it reports.
                        if let previous = pendingTests[item.tag], item.urlTestTime > previous {
                            pendingTests.removeValue(forKey: item.tag)
                        }
                        if let failedAt = failedTests[item.tag], item.urlTestTime > failedAt { failedTests.removeValue(forKey: item.tag) }
                        measurementTimes[item.tag] = max(measurementTimes[item.tag] ?? 0, item.urlTestTime)
                        let isPending = pendingTests[item.tag] != nil
                        let delay: Int32 = failedTests[item.tag] != nil ? -1 : item.urlTestDelay
                        lock.unlock()
                        let statusString = MobilecoreNodeStatus(delay, isPending)
                        let status = NodeStatus(
                            rawValue: statusString ?? NodeStatus.untested.rawValue
                        ) ?? .untested
                        items.append(NodeSnapshot(
                            tag: item.tag,
                            status: status,
                            latencyMS: delay,
                            selected: group.selected == item.tag,
                            kind: item.type
                        ))
                    }
                }
                snapshots.append(GroupSnapshot(
                    tag: group.tag,
                    selectable: group.selectable,
                    selectedTag: group.selected.isEmpty ? nil : group.selected,
                    items: items
                ))
            }
        }
        groupBroadcaster.yield(snapshots)
    }

    fileprivate func handleClashModeInitialize(_ modeList: LibboxStringIteratorProtocol?, currentMode: String?) {
        lock.lock()
        clashModeValue = RoutingMode(rawValue: currentMode ?? "")
        clashModesSeen = true
        lock.unlock()
    }

    fileprivate func handleClashModeUpdate(_ newMode: String?) {
        lock.lock()
        clashModeValue = RoutingMode(rawValue: newMode ?? "")
        lock.unlock()
    }

    // MARK: Mapping (pure)

    private static func mapRecord(_ connection: LibboxConnection) -> ConnectionRecord {
        var chain: [String] = []
        if let chainIterator = connection.chain() {
            while chainIterator.hasNext() {
                chain.append(chainIterator.next())
            }
        }
        return ConnectionRecord(
            id: connection.id_,
            network: connection.network,
            source: connection.source,
            destination: connection.destination,
            domain: connection.domain,
            outbound: connection.outbound,
            chain: chain,
            rule: connection.rule,
            createdAt: connection.createdAt,
            closedAt: connection.closedAt,
            uplinkTotal: connection.uplinkTotal,
            downlinkTotal: connection.downlinkTotal,
            closed: connection.closedAt > 0
        )
    }

    private func requireClient() throws -> LibboxCommandClient {
        lock.lock()
        defer { lock.unlock() }
        guard active, let client else {
            throw CommandChannelError.unavailable
        }
        return client
    }

    private func runOffMain<T>(_ block: @escaping () throws -> T) async throws -> T {
        try await Task.detached(priority: .userInitiated) {
            try block()
        }.value
    }
}

/// LibboxCommandClientHandler funneling stream callbacks into the commanding
/// object. Libbox invokes these on its own threads; snapshot mapping is pure
/// and lock-guarded, SwiftUI consumes the yielded values on the main actor.
private final class GroupStreamHandler: NSObject, LibboxCommandClientHandlerProtocol, @unchecked Sendable {
    private weak var commanding: LibboxCoreCommanding?

    init(commanding: LibboxCoreCommanding) {
        self.commanding = commanding
    }

    func connected() {
        commanding?.handleConnected()
    }

    func disconnected(_ message: String?) {
        commanding?.handleDisconnected(message: message)
    }

    func setDefaultLogLevel(_ level: Int32) {}

    func clearLogs() {
        // The core cleared its log buffer (operator action elsewhere); ours
        // keeps the tail until the next write so the Data page never
        // silently rewrites history mid-scroll.
    }

    func writeLogs(_ messageList: LibboxLogIteratorProtocol?) {
        commanding?.handleLogs(messageList)
    }

    func writeStatus(_ message: LibboxStatusMessage?) {
        commanding?.handleStatus(message)
    }

    func writeGroups(_ message: LibboxOutboundGroupIteratorProtocol?) {
        commanding?.handleGroups(message)
    }

    func writeOutbounds(_ message: LibboxOutboundGroupItemIteratorProtocol?) {}

    func initializeClashMode(_ modeList: LibboxStringIteratorProtocol?, currentMode: String?) {
        commanding?.handleClashModeInitialize(modeList, currentMode: currentMode)
    }

    func updateClashMode(_ newMode: String?) {
        commanding?.handleClashModeUpdate(newMode)
    }

    // The Swift importer renames writeConnectionEvents: to write(_:) in this
    // gomobile generation (same generation that suffixes protocols with
    // `Protocol`; see NOTICE.md).
    func write(_ events: LibboxConnectionEvents?) {
        commanding?.handleConnectionEvents(events)
    }
}

/// Small fan-out for stream subscribers: every `stream()` call returns an
/// independent AsyncStream; yields fan out to all live subscribers. Yields
/// happen on Libbox handler threads; AsyncStream continuations are Sendable.
private final class Broadcaster<T>: @unchecked Sendable {
    private let lock = NSLock()
    private var continuations: [UUID: AsyncStream<T>.Continuation] = [:]

    /// - Parameter startingWith: yielded first (after registration, so no
    ///   subsequent change is missed); nil starts the stream empty.
    func stream(startingWith initial: T? = nil) -> AsyncStream<T> {
        let broadcaster = self
        return AsyncStream { continuation in
            let id = UUID()
            broadcaster.lock.lock()
            broadcaster.continuations[id] = continuation
            broadcaster.lock.unlock()
            if let initial {
                continuation.yield(initial)
            }
            continuation.onTermination = { _ in
                broadcaster.lock.lock()
                broadcaster.continuations[id] = nil
                broadcaster.lock.unlock()
            }
        }
    }

    func yield(_ value: T) {
        lock.lock()
        let targets = Array(continuations.values)
        lock.unlock()
        for continuation in targets {
            continuation.yield(value)
        }
    }
}
