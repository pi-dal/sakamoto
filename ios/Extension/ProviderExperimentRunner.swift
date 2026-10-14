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
    private let apply: (String) async throws -> Void
    private var client: LibboxCommandClient?
    private var handler: ExperimentCommandHandler?
    private var monitor: Task<Void, Never>?
    private var monitorSignal: AsyncStream<Void>.Continuation?
    private var probeWake: Task<Void, Never>?
    private var schedule = ExperimentProbeSchedule()
    private var healthSince: Int64 = 0
    private var recovering = false
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
        var settleFor: ((Int) async throws -> Void)? = nil
        var advanceClock: () async throws -> Void = { try await Task.sleep(nanoseconds: 1_000_000_000) }
    }
    private var recoveryCommands: RecoveryCommands?

    init(profileID: String, settings: ExperimentSettings, content: String, root: URL, recoveryCommands: RecoveryCommands? = nil, apply: @escaping (String) async throws -> Void) throws {
        try settings.validate()
        _ = try ExperimentFiles.directory(profileID: profileID, root: root)
        self.profileID = profileID; self.settings = settings
        self.root = root; self.apply = apply; self.recoveryCommands = recoveryCommands
        state = try ExperimentFiles.read(profileID: profileID, root: root)
        if settings.learningEnabled {
            var error: NSError?
            tracker = MobileexperimentNewExperimentTracker(Int32(settings.threshold), &error)
            if let error { throw error }
        }
    }

    static func prepared(_ options: TunnelStartOptions, root: URL) throws -> (String, ExperimentSettings?) {
        var preparationError: NSError?
        let connected = MobilecorePrepareTailscaleConnectivityJSON(options.configContent, &preparationError)
        if let preparationError { throw preparationError }
        guard let id = options.profileID, let raw = options.experimentJSON else { return (connected, nil) }
        let settings = try JSONDecoder().decode(ExperimentSettings.self, from: Data(raw.utf8))
        try settings.validate()
        let state = try ExperimentFiles.read(profileID: id, root: root)
        // Reserve one live slot even when the committed list is empty.
        // Later learning changes matching for new flows without restarting
        // Tailscale or discarding an existing SSH/PTY session.
        var content = connected
        if settings.mode == .auto || settings.cfRegionBlock {
            var error: NSError?
            content = MobileexperimentPrepareLiveRulesJSON(content, "[]", json(state.learned), &error)
            if let error { throw error }
        }
        return (content, settings)
    }
    private static func json(_ values: [String]) -> String { String(decoding: (try? JSONEncoder().encode(values)) ?? Data("[]".utf8), as: UTF8.self) }

    func start() {
        guard monitor == nil, !stopped else { return }
        guard settings.requiresMonitoring else { status("Monitoring disabled"); return }
        healthSince = Int64(Date().timeIntervalSince1970)
        let options = LibboxCommandClientOptions()
        if settings.learningEnabled {
            options.addCommand(LibboxCommandConnections); options.addCommand(LibboxCommandLog)
        }
        options.addCommand(LibboxCommandGroup); options.addCommand(LibboxCommandClashMode)
        options.statusInterval = 5_000_000_000
        let handler = ExperimentCommandHandler(owner: self)
        self.handler = handler
        client = LibboxNewCommandClient(handler, options)
        if let client {
            recoveryCommands = RecoveryCommands(
                test: { tag in try await Task.detached { try client.urlTest(tag) }.value },
                select: { selector, tag in try await Task.detached { try client.selectOutbound(selector, outboundTag: tag) }.value },
                settle: {},
                settleFor: { seconds in try await Task.sleep(nanoseconds: UInt64(seconds) * NSEC_PER_SEC) }
            )
        }
        status("Monitoring while VPN runs")
        let signals = AsyncStream<Void>(bufferingPolicy: .bufferingNewest(1)) { continuation in
            monitorSignal = continuation
            continuation.yield(())
        }
        monitor = Task { [weak self] in
            for await _ in signals {
                guard let self, !Task.isCancelled, !self.stopped else { return }
                let fallbackEligible = self.settings.fallbacks.contains { selector, chain in self.groups.first(where: { $0.tag == selector }).map { chain.contains($0.selected) } ?? false }
                if !self.connected, let client = self.client {
                    _ = try? await Task.detached { try client.connect() }.value
                } else if self.settings.fallbackEnabled && fallbackEligible && !self.busy {
                    try? self.beginRecovery(manual: false)
                } else if !self.busy, !self.editsPending(), self.mode == "rule",
                          self.settings.learningEnabled,
                          let selected = self.groups.first(where: { $0.tag == "MainProxy" })?.selected,
                          let client = self.client {
                    let wait = max(0, Double(self.healthSince) + 0.05 - Date().timeIntervalSince1970)
                    if wait > 0 { try? await Task.sleep(nanoseconds: UInt64(wait * 1_000_000_000)) }
                    guard !Task.isCancelled, !self.stopped else { return }
                    _ = try? await Task.detached { try client.urlTest(selected) }.value
                }
                guard !Task.isCancelled, !self.stopped else { return }
                self.scheduleWake()
            }
        }
    }

    private func scheduleWake() {
        probeWake?.cancel()
        guard monitorSignal != nil else { return }
        if connected {
            guard mode == "rule" else { return }
            let eligible = settings.fallbacks.contains { selector, chain in groups.first(where: { $0.tag == selector }).map { chain.contains($0.selected) } ?? false }
            guard settings.learningEnabled || (settings.fallbackEnabled && eligible) else { return }
        }
        let delay = UInt64(schedule.delaySeconds(learning: settings.learningEnabled)) * NSEC_PER_SEC
        probeWake = Task { [weak self] in
            do { try await Task.sleep(nanoseconds: delay) } catch { return }
            self?.monitorSignal?.yield(())
        }
    }

    private func wakeMonitor() {
        probeWake?.cancel()
        monitorSignal?.yield(())
    }

    func networkChanged() {
        guard !stopped, settings.requiresMonitoring else { return }
        healthSince = Int64(Date().timeIntervalSince1970) + 1
        schedule.reset(); recovery.reset()
        if recovering {
            operation?.cancel()
            let pending = operation
            Task { [weak self] in
                await pending?.value
                guard let self, !self.stopped else { return }
                self.wakeMonitor()
            }
        } else { wakeMonitor() }
    }

    func quiesce() async {
        stopped = true; monitorSignal?.finish(); probeWake?.cancel(); monitor?.cancel(); operation?.cancel()
        removal?.cancel()
        await operation?.value
        _ = try? await removal?.value
        stop()
    }
    func stop() {
        stopped = true; monitorSignal?.finish(); probeWake?.cancel(); monitor?.cancel(); operation?.cancel(); removal?.cancel()
        let client = client; self.client = nil
        Task.detached { try? client?.disconnect() }
        connected = false; tracker = nil; groups = []; recovery.reset()
        status("Stopped")
    }
    func availability(_ value: Bool) {
        guard !stopped else { return }
        let changed = connected != value
        connected = value; firstLogs = true
        if !value { groups = []; mode = ""; recovery.reset(); schedule.reset(); status("Waiting for the core command channel") }
        if changed { wakeMonitor() }
    }
    func clashMode(_ value: String?) {
        let next = value?.lowercased() ?? ""
        if mode != next { mode = next; wakeMonitor() }
    }
    func updateGroups(_ values: [ExperimentGroup]) {
        let previous = ExperimentGroup.selectedObservation(groups: groups)
        let current = ExperimentGroup.selectedObservation(groups: values)
        let selectors = Set(settings.fallbacks.keys).union(["MainProxy"])
        let selectionChanged = values.contains(where: { group in selectors.contains(group.tag) && (groups.first(where: { $0.tag == group.tag }).map { $0.selected != group.selected } ?? !group.selected.isEmpty) })
        if selectionChanged { recovery.reset() }
        groups = values
        if !busy && (selectionChanged || ((previous?.delay ?? 0) > 0 && (current?.delay ?? 0) <= 0)) {
            schedule.reset(); wakeMonitor()
        } else if !busy, !settings.fallbackEnabled, let current, current.testedAt > (previous?.testedAt ?? 0) {
            schedule.record(healthy: current.delay > 0 && current.testedAt >= healthSince && Int64(Date().timeIntervalSince1970) - current.testedAt <= 120)
        }
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
        guard ExperimentGroup.selectedProxyHealthy(groups: groups, since: max(healthSince, Int64(now.timeIntervalSince1970) - 120)) else { return }
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
        let previous = Self.json(state.learned)
        let candidate = Self.json(next)
        guard connected, !stopped, !editsPending(), mode == "rule" else { throw TunnelProfile.InvalidProfile("Automatic changes require Rule mode and a live tunnel") }
        status("Updating learned routes")
        do {
            try await apply(candidate)
            try Task.checkCancellation()
            guard !stopped else { throw CancellationError() }
            guard !editsPending() else { throw TunnelProfile.InvalidProfile("Profile edits arrived during learning; previous runtime restored") }
            var updated = state; updated.learned = next; updated.status = "Learned routes applied"; updated.updatedAt = Date()
            try ExperimentFiles.write(updated, profileID: profileID, root: root)
            state = updated
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
        let targets = Set(eligible.values.flatMap { $0 })
        let waitSeconds = ExperimentGroup.resultWaitSeconds(tags: Array(targets), groups: groups)
        busy = true; recovering = true; roundAt = Date(); status("Recovery tests running")
        operation = Task { [weak self] in
            guard let self else { return }
            defer {
                self.busy = false; self.recovering = false
                if !self.stopped { self.scheduleWake() }
            }
            do {
                // Libbox timestamps tests in whole seconds. Move into a new
                // second before testing so an earlier result cannot pass the
                // round's freshness boundary, even for very fast tests.
                try await commands.advanceClock()
                guard !self.stopped, self.connected, !self.editsPending() else { return }
                for tag in targets {
                    try Task.checkCancellation()
                    try await commands.test(tag)
                }
                if let settleFor = commands.settleFor { try await settleFor(waitSeconds) }
                else { try await commands.settle() }
                try Task.checkCancellation()
                guard !self.stopped, self.connected, self.mode == "rule", !self.editsPending() else { return }
                var healthyRound = true
                for (selector, chain) in eligible {
                    guard let current = self.groups.first(where: { $0.tag == selector })?.selected,
                          current == selectedBefore[selector] else { self.recovery.reset(); healthyRound = false; continue }
                    let healthy = ExperimentGroup.candidate(selector: selector, chain: chain, groups: self.groups, since: started)
                    if healthy != current { healthyRound = false }
                    if let candidate = self.recovery.decision(selector: selector, chain: chain, selected: current, healthy: healthy, required: self.settings.recoverAfter) {
                        try await commands.select(selector, candidate)
                        self.status("Fallback selection requested")
                    }
                }
                self.schedule.record(healthy: healthyRound)
                self.status("Recovery round completed; fresh results only")
            } catch {
                self.schedule.reset()
                if !self.stopped && !Task.isCancelled { self.status("Recovery failed; selection retained") }
            }
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
