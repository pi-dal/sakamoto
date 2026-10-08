import Foundation
import Libbox
import SakamotoKit

/// Runs in the NetworkExtension, not the UI process. One main-actor owner
/// serializes observed evidence, learned-rule transactions and recovery.
@MainActor
final class ProviderExperimentRunner {
    private let root: URL
    let profileID: String
    let settings: ExperimentSettings
    private var content: String
    private let apply: (String) async throws -> Void
    private var client: LibboxCommandClient?
    private var handler: ExperimentCommandHandler?
    private var monitor: Task<Void, Never>?
    private var operation: Task<Void, Never>?
    private var removal: Task<Void, Error>?
    private var tracker: MobileexperimentExperimentTracker?
    private var groups: [ExperimentGroup] = []
    private var mode = ""
    private var connected = false
    private var stopped = false
    private var busy = false
    private var firstLogs = true
    private var roundAt = Date.distantPast
    private var recovery = ExperimentRecovery()
    private func editsPending() -> Bool {
        UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.bool(forKey: "sakamoto.experiment.pause." + profileID) == true
    }
    private var state: ExperimentRuntimeState
    struct RecoveryCommands {
        var test: (String) async throws -> Void
        var select: (String, String) async throws -> Void
        var settle: () async throws -> Void
        var advanceClock: () async throws -> Void = { try await Task.sleep(nanoseconds: 1_000_000_000) }
    }
    private var recoveryCommands: RecoveryCommands?

    init(profileID: String, settings: ExperimentSettings, content: String, root: URL, recoveryCommands: RecoveryCommands? = nil, apply: @escaping (String) async throws -> Void) throws {
        try settings.validate()
        _ = try ExperimentFiles.directory(profileID: profileID, root: root)
        self.profileID = profileID; self.settings = settings; self.content = content
        self.root = root; self.apply = apply; self.recoveryCommands = recoveryCommands
        state = try ExperimentFiles.read(profileID: profileID, root: root)
        var error: NSError?
        tracker = MobileexperimentNewExperimentTracker(Int32(settings.threshold), &error)
        if let error { throw error }
    }

    static func prepared(_ options: TunnelStartOptions, root: URL) throws -> (String, ExperimentSettings?) {
        guard let id = options.profileID, let raw = options.experimentJSON else { return (options.configContent, nil) }
        let settings = try JSONDecoder().decode(ExperimentSettings.self, from: Data(raw.utf8))
        try settings.validate()
        let state = try ExperimentFiles.read(profileID: id, root: root)
        // Only committed domains survive a cold start. An interrupted reload
        // cannot publish its candidate learned list to the next session.
        var content = options.configContent
        if settings.mode == .auto || settings.cfRegionBlock, !state.learned.isEmpty {
            var error: NSError?
            content = MobileexperimentOwnedRulesJSON(content, "[]", json(state.learned), &error)
            if let error { throw error }
        }
        return (content, settings)
    }
    private static func json(_ values: [String]) -> String { String(decoding: (try? JSONEncoder().encode(values)) ?? Data("[]".utf8), as: UTF8.self) }

    func start() {
        let options = LibboxCommandClientOptions()
        options.addCommand(LibboxCommandConnections); options.addCommand(LibboxCommandLog)
        options.addCommand(LibboxCommandGroup); options.addCommand(LibboxCommandClashMode)
        options.statusInterval = 1_000_000_000
        let handler = ExperimentCommandHandler(owner: self)
        self.handler = handler
        client = LibboxNewCommandClient(handler, options)
        if let client {
            recoveryCommands = RecoveryCommands(
                test: { tag in try await Task.detached { try client.urlTest(tag) }.value },
                select: { selector, tag in try await Task.detached { try client.selectOutbound(selector, outboundTag: tag) }.value },
                settle: { try await Task.sleep(nanoseconds: 10_000_000_000) }
            )
        }
        status("Monitoring while VPN runs")
        monitor = Task { [weak self] in
            while !Task.isCancelled {
                guard let self, !self.stopped else { return }
                if !self.connected, let client = self.client {
                    _ = try? await Task.detached { try client.connect() }.value
                } else if self.settings.fallbackEnabled && !self.busy {
                    try? self.beginRecovery(manual: false)
                } else if !self.busy, !self.editsPending(), self.mode == "rule",
                          self.settings.mode == .auto || self.settings.cfRegionBlock,
                          let selected = self.groups.first(where: { $0.tag == "MainProxy" })?.selected,
                          let client = self.client {
                    // Refresh proxy-health evidence even without fallback;
                    // never assume selected means reachable.
                    _ = try? await Task.detached { try client.urlTest(selected) }.value
                }
                try? await Task.sleep(nanoseconds: 30_000_000_000)
            }
        }
    }
    func quiesce() async {
        stopped = true; monitor?.cancel(); operation?.cancel()
        removal?.cancel()
        await operation?.value
        _ = try? await removal?.value
        stop()
    }
    func stop() {
        stopped = true; monitor?.cancel(); operation?.cancel(); removal?.cancel()
        let client = client; self.client = nil
        Task.detached { try? client?.disconnect() }
        connected = false; tracker = nil; groups = []; recovery.reset()
        status("Stopped")
    }
    func availability(_ value: Bool) {
        connected = value; firstLogs = true
        if !value { groups = []; mode = ""; recovery.reset(); status("Waiting for the core command channel") }
    }
    func clashMode(_ value: String?) { mode = value?.lowercased() ?? "" }
    func updateGroups(_ values: [ExperimentGroup]) {
        if values.contains(where: { group in groups.first(where: { $0.tag == group.tag }).map { $0.selected != group.selected } ?? false }) { recovery.reset() }
        groups = values
    }
    func observeConnection(id: String, domain: String, destination: String, network: String, outbound: String, rule: String, closed: Bool, downlink: Int64) {
        guard connected, !busy, !editsPending(), settings.mode == .auto || settings.cfRegionBlock else { return }
        tracker?.connection(id, domain: domain, destination: destination, network: network, outbound: outbound, rule: rule, closed: closed, downlink: downlink, nowMillis: Int64(Date().timeIntervalSince1970 * 1000))
    }
    func observeLogs(_ lines: [String]) {
        // The first batch after subscription is historical, not evidence for
        // this session. Later errors still need a matching NEW connection.
        if firstLogs { firstLogs = false; return }
        guard connected, !busy, !stopped, !editsPending(), mode == "rule", settings.mode == .auto || settings.cfRegionBlock else { return }
        let now = Date()
        guard ExperimentGroup.selectedProxyHealthy(groups: groups, since: Int64(now.timeIntervalSince1970) - 120) else { return }
        for line in lines {
            let domain = tracker?.observeLog(line, nowMillis: Int64(now.timeIntervalSince1970 * 1000), proxyHealthy: true, autoMode: settings.mode == .auto, cfRegion: settings.cfRegionBlock) ?? ""
            if !domain.isEmpty && !state.learned.contains(domain) && state.learned.count < 1000 {
                busy = true
                operation = Task { [weak self] in
                    guard let self else { return }
                    defer { self.busy = false }
                    do { try await self.replaceLearned((self.state.learned + [domain]).sorted()) }
                    catch {
                        if self.state.status != "Reload rollback failed; disconnect and reconnect" {
                            self.status("Learning failed; previous rules retained")
                        }
                    }
                }
                return
            }
        }
    }
    func remove(_ domain: String) async throws {
        guard !busy, connected, !stopped, !editsPending() else { throw TunnelProfile.InvalidProfile("Experiment is busy or the VPN is disconnected") }
        guard state.learned.contains(domain) else { throw TunnelProfile.InvalidProfile("No such learned rule in this running profile") }
        busy = true; defer { busy = false; removal = nil }
        let next = state.learned.filter { $0 != domain }
        let task = Task { try await self.replaceLearned(next) }
        removal = task
        try await task.value
    }
    private func replaceLearned(_ next: [String]) async throws {
        let previous = content
        var error: NSError?
        let active = settings.mode == .auto || settings.cfRegionBlock
        let candidate = MobileexperimentOwnedRulesJSON(content, Self.json(active ? state.learned : []), Self.json(active ? next : []), &error)
        if let error { throw error }
        _ = LibboxCheckConfig(candidate, &error)
        if let error { throw error }
        guard connected, !stopped, !editsPending(), mode == "rule" else { throw TunnelProfile.InvalidProfile("Automatic changes require Rule mode and a live tunnel") }
        status("Validating learned route")
        do {
            try await apply(candidate)
            try Task.checkCancellation()
            guard !stopped else { throw CancellationError() }
            guard !editsPending() else { throw TunnelProfile.InvalidProfile("Profile edits arrived during learning; previous runtime restored") }
            var updated = state; updated.learned = next; updated.status = "Learned routes applied"; updated.updatedAt = Date()
            try ExperimentFiles.write(updated, profileID: profileID, root: root)
            state = updated; content = candidate
        } catch {
            // Roll back the actual service as well as its display state. If
            // rollback fails, keep a loud failure, not a false success.
            if !stopped {
                do { try await apply(previous) }
                catch { status("Reload rollback failed; disconnect and reconnect"); throw error }
            }
            throw error
        }
    }
    func beginRecovery(manual: Bool) throws {
        guard settings.fallbackEnabled, !settings.fallbacks.isEmpty else { throw TunnelProfile.InvalidProfile("Configure fallback priorities and enable recovery first") }
        guard connected, mode == "rule", !busy, !stopped, !editsPending() else { throw TunnelProfile.InvalidProfile("Recovery requires an idle running tunnel in Rule mode") }
        if manual && Date().timeIntervalSince(roundAt) < 90 { throw TunnelProfile.InvalidProfile("Recovery cooldown: wait 90 seconds between manual rounds") }
        guard let commands = recoveryCommands else { throw TunnelProfile.InvalidProfile("Core command channel unavailable") }
        let eligible = settings.fallbacks.filter { selector, chain in groups.first(where: { $0.tag == selector }).map { chain.contains($0.selected) } ?? false }
        guard !eligible.isEmpty else { throw TunnelProfile.InvalidProfile("Manual selection preserved; no priority chain is selected") }
        let selectedBefore = Dictionary(uniqueKeysWithValues: groups.map { ($0.tag, $0.selected) })
        let started = Int64(Date().timeIntervalSince1970) + 1
        busy = true; roundAt = Date(); status("Recovery tests running")
        operation = Task { [weak self] in
            guard let self else { return }
            defer { self.busy = false }
            do {
                // Libbox timestamps tests in whole seconds. Move into a new
                // second before testing so an earlier result cannot pass the
                // round's freshness boundary, even for very fast tests.
                try await commands.advanceClock()
                guard !self.stopped, self.connected, !self.editsPending() else { return }
                for tag in Set(eligible.values.flatMap { $0 }) {
                    try await commands.test(tag)
                }
                try await commands.settle()
                guard !self.stopped, self.connected, self.mode == "rule", !self.editsPending() else { return }
                for (selector, chain) in eligible {
                    guard let current = self.groups.first(where: { $0.tag == selector })?.selected,
                          current == selectedBefore[selector] else { self.recovery.reset(); continue }
                    let healthy = ExperimentGroup.candidate(selector: selector, chain: chain, groups: self.groups, since: started)
                    if let candidate = self.recovery.decision(selector: selector, chain: chain, selected: current, healthy: healthy, required: self.settings.recoverAfter) {
                        try await commands.select(selector, candidate)
                        self.status("Fallback selection requested")
                    }
                }
                self.status("Recovery round completed; fresh results only")
            } catch { if !self.stopped { self.status("Recovery failed; selection retained") } }
        }
    }
    private func status(_ message: String) {
        state.status = message; state.updatedAt = Date()
        do { try ExperimentFiles.write(state, profileID: profileID, root: root) }
        catch { TunnelDiagnostics.record(stage: "Experiment persistence", error: error) }
    }
}

private final class ExperimentCommandHandler: NSObject, LibboxCommandClientHandlerProtocol, @unchecked Sendable {
    private weak var owner: ProviderExperimentRunner?
    init(owner: ProviderExperimentRunner) { self.owner = owner }
    func connected() { Task { @MainActor [weak self] in self?.owner?.availability(true) } }
    func disconnected(_ message: String?) { Task { @MainActor [weak self] in self?.owner?.availability(false) } }
    func setDefaultLogLevel(_ level: Int32) {}
    func clearLogs() {}
    func writeStatus(_ message: LibboxStatusMessage?) {}
    func writeOutbounds(_ message: LibboxOutboundGroupItemIteratorProtocol?) {}
    func initializeClashMode(_ list: LibboxStringIteratorProtocol?, currentMode: String?) { Task { @MainActor [weak self] in self?.owner?.clashMode(currentMode) } }
    func updateClashMode(_ value: String?) { Task { @MainActor [weak self] in self?.owner?.clashMode(value) } }
    func writeLogs(_ iterator: LibboxLogIteratorProtocol?) {
        var lines: [String] = []
        while iterator?.hasNext() == true { if let entry = iterator?.next() { lines.append(entry.message) } }
        let batch = lines
        Task { @MainActor [weak self] in self?.owner?.observeLogs(batch) }
    }
    func writeGroups(_ iterator: LibboxOutboundGroupIteratorProtocol?) {
        var values: [ExperimentGroup] = []
        while iterator?.hasNext() == true {
            guard let group = iterator?.next() else { break }
            var items: [ExperimentGroup.Item] = []
            let entries = group.getItems()
            while entries?.hasNext() == true {
                guard let entry = entries?.next() else { break }
                items.append(.init(tag: entry.tag, delay: entry.urlTestDelay, testedAt: entry.urlTestTime))
            }
            values.append(.init(tag: group.tag, selected: group.selected, items: items))
        }
        let batch = values
        Task { @MainActor [weak self] in self?.owner?.updateGroups(batch) }
    }
    func write(_ events: LibboxConnectionEvents?) {
        guard events?.reset == false else { return }
        let iterator = events?.iterator()
        while iterator?.hasNext() == true {
            guard let event = iterator?.next(), let c = event.connection,
                  event.type == LibboxConnectionEventNew || event.type == LibboxConnectionEventClosed else { continue }
            let id = event.id_, domain = c.domain, destination = c.destination, network = c.network, outbound = c.outbound, rule = c.rule
            let closed = event.type == LibboxConnectionEventClosed, downlink = c.downlinkTotal
            Task { @MainActor [weak self] in self?.owner?.observeConnection(id: id, domain: domain, destination: destination, network: network, outbound: outbound, rule: rule, closed: closed, downlink: downlink) }
        }
    }
}
