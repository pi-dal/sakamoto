import Foundation
import NetworkExtension
import SakamotoKit

// NetworkExtension-backed TunnelControlling.
//
// This is the App-side transport: it drives NEVPNManager (install/start/
// stop) and NETunnelProviderSession (TunnelRequest/TunnelResponse). It
// compiles against the SDK without signing; actually starting a tunnel
// requires an entitlement-bearing app target plus the PacketTunnelProvider
// (ios/Extension/), neither of which exists yet (README.md "Boundaries").
//
// Deliberately NOT implemented here: CoreCommanding. Clash mode, node
// selection and URL tests travel the Libbox command channel inside the
// provider process — an NE app↔provider message cannot reach the running
// core, and faking it would violate the docs/tui.md rule that a control
// action must never report success for work that did not happen.

public enum TunnelControllerError: Error, Equatable, Sendable {
    /// manager.connection is not a NETunnelProviderSession (misconfigured
    /// provider protocol).
    case notTunnelSession
    /// The session refused to queue the message (tunnel not up).
    case messageNotAccepted
    /// The provider replied without data. Our provider always answers with
    /// a TunnelResponse; nil means an older/misbehaving provider.
    case emptyProviderResponse
    /// The provider replied ok == false; carries its error text verbatim.
    case providerReported(String)
    /// The provider answered a ping without a state snapshot. Our provider
    /// always includes one; absence means an older/misbehaving provider.
    case responseMissingState
    case providerTimedOut
}

public final class NETunnelController: TunnelControlling, @unchecked Sendable {
    /// The PacketTunnelProvider's bundle identifier. Injected by the app
    /// target at runtime; this repository deliberately does not invent one
    /// (README.md "Boundaries": no guessed bundle IDs).
    public let providerBundleIdentifier: String
    private var manager: NEVPNManager
    private var preferencesLoaded = false
    private let requestFence = TunnelRequestFence()
    private let isCurrent: @MainActor (TunnelStartOptions) -> Bool
    @MainActor private var changingProfile = false
    @MainActor private var preferenceWaiters: [CheckedContinuation<Void, Never>] = []

    /// Hook for VPN-conflict detection (core: another VPN TUN active while
    /// ours runs). The app target wires this to real system VPN inspection.
    /// Default reports no conflict; observations say `conflict: false`
    /// because nothing was measured — never as a claim that none exists.
    public var conflictCheck: @Sendable () async -> Bool

    public init(
        providerBundleIdentifier: String,
        manager: NEVPNManager? = nil,
        isCurrent: @escaping @MainActor (TunnelStartOptions) -> Bool = { _ in true },
        conflictCheck: @escaping @Sendable () async -> Bool = { false }
    ) {
        self.providerBundleIdentifier = providerBundleIdentifier
        self.manager = manager ?? NETunnelProviderManager()
        self.preferencesLoaded = manager != nil
        self.isCurrent = isCurrent
        self.conflictCheck = conflictCheck
    }

    // MARK: TunnelControlling

    @MainActor public func connect(options: TunnelStartOptions) async throws {
        try await apply(options: options, starting: true, transaction: nil, commit: {})
    }

    @MainActor public func disconnect() async throws {
        let request = requestFence.begin()
        await acquirePreferences()
        defer { releasePreferences() }
        try requestFence.check(request)
        try await loadProviderPreferences(refresh: true)
        try requestFence.check(request)
        try await AutomaticConnectionPolicy.pause(manager) {
            try self.requestFence.perform(request) { self.manager.connection.stopVPNTunnel() }
        }
    }

    public func status() async throws -> ServiceState {
        try await loadProviderPreferences()
        return Self.serviceState(for: manager.connection.status)
    }

    public func ping() async throws -> TunnelStateSnapshot {
        try await loadProviderPreferences()
        let service = Self.serviceState(for: manager.connection.status)
        if service != .running {
            let systemError: Error? = service == .stopped || service == .unavailable ? await lastDisconnectError() : nil
            // The system error callback may arrive after a reconnect.
            let current = Self.serviceState(for: manager.connection.status)
            return TunnelStateSnapshot(serviceState: current, detail: current == service ? disconnectDetail(systemError: systemError) : nil)
        }
        let response = try await send(.ping)
        guard response.ok else {
            throw TunnelControllerError.providerReported(response.error ?? "ping failed")
        }
        guard let state = response.state else {
            throw TunnelControllerError.responseMissingState
        }
        return state
    }

    public func reload(configContent: String) async throws {
        try await loadProviderPreferences()
        var options = TunnelStartOptions(providerConfiguration: (manager.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration ?? [:]) ?? TunnelStartOptions(configContent: configContent)
        options.configContent = configContent
        try await reload(options: options)
    }

    public func reload(options: TunnelStartOptions) async throws {
        try await apply(options: options, starting: false, transaction: nil, commit: {})
    }

    // Keep a superseded request's rollback and the next preference write in order.
    @MainActor private func acquirePreferences() async {
        if changingProfile { await withCheckedContinuation { preferenceWaiters.append($0) } }
        else { changingProfile = true }
    }

    @MainActor private func releasePreferences() {
        if preferenceWaiters.isEmpty { changingProfile = false }
        else { preferenceWaiters.removeFirst().resume() }
    }

    @MainActor public func apply(options: TunnelStartOptions, starting: Bool, transaction: UUID?, commit: @MainActor () throws -> Void) async throws {
        let ticket = requestFence.begin()
        await acquirePreferences()
        defer { releasePreferences() }
        try requestFence.check(ticket)
        func validate() throws {
            try Task.checkCancellation()
            try requestFence.check(ticket)
            guard isCurrent(options) else { throw CancellationError() }
        }
        try await loadProviderPreferences(refresh: true)
        try validate()
        let previous = try Self.rollbackProtocol(for: manager.protocolConfiguration)
        let previousName = manager.localizedDescription
        let previouslyEnabled = manager.isEnabled
        let candidate = try Self.makeProviderProtocol(bundleIdentifier: providerBundleIdentifier, options: options)
        let operationID = transaction ?? UUID()
        guard let reference = candidate.providerConfiguration else { throw TunnelStartOptions.ResolutionError.missingConfiguration }
        var staged = try TunnelStartOptions.provisionalReference(reference, transaction: operationID, previous: (previous as? NETunnelProviderProtocol)?.providerConfiguration)
        if transaction == nil { staged["controllerTransaction"] = true }
        candidate.providerConfiguration = staged
        let lease: ApplyLease = try {
            guard let root = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: SystemSurfaceStore.groupIdentifier) else { throw TunnelStartOptions.ResolutionError.missingConfiguration }
            let predecessor = candidate.providerConfiguration?["previousProfile"] as? [String: String]
            return try ApplyLease(root: root, transaction: operationID, owner: true, rollback: .init(reference: predecessor))
        }()
        defer { withExtendedLifetime(lease) {} }
        manager.protocolConfiguration = candidate
        if starting { manager.localizedDescription = "sakamoto"; manager.isEnabled = true }
        var saved = false
        var submitted = false
        let requestID = operationID
        do {
            try await savePreferences()
            saved = true
            try await reloadPreferences()
            guard let reloaded = manager.protocolConfiguration as? NETunnelProviderProtocol,
                  let reference = reloaded.providerConfiguration,
                  NSDictionary(dictionary: reference).isEqual(to: candidate.providerConfiguration ?? [:]) else {
                // Another owner replaced these preferences. Keep its newer value.
                saved = false
                throw CancellationError()
            }
            try validate()
            if starting {
                var start = options.startTunnelOptions
                start["applyTransaction"] = operationID.uuidString as NSString
                start[TunnelStartOptions.controllerConfirmsKey] = NSNumber(value: true)
                try manager.connection.startVPNTunnel(options: start)
                submitted = true
                try await confirmStart(options, validate: validate)
            } else {
                try await reloadConfirmed(options, transaction: operationID, requestID: requestID) {
                    submitted = true
                }
                try validate()
            }
            guard try await preferencesStillMatch(candidate) else { throw CancellationError() }
            try validate()
            guard try options.confirmStoredReference(reference, previous: (previous as? NETunnelProviderProtocol)?.providerConfiguration, transaction: operationID, afterWrite: commit) else {
                throw TunnelControllerError.responseMissingState
            }
        } catch {
            if saved {
                guard try await preferencesStillMatch(candidate) else { throw CancellationError() }
            }
            let previousReference = (previous as? NETunnelProviderProtocol)?.providerConfiguration
            let previousOptions = previousReference.flatMap { TunnelStartOptions(providerConfiguration: $0) }
            let restored = (error as? RunningOtherProfile)?.receipt
            var previousIsRunning = previousOptions.map { restored?.matches($0) == true } ?? false
            if submitted, !starting, let previousOptions {
                // Rollback must finish even when cancellation initiated it.
                previousIsRunning = await Task { @MainActor in
                    await self.recoverReload(previousOptions, candidate: options, failure: error, requestID: requestID)
                }.value
                guard try await preferencesStillMatch(candidate) else { throw CancellationError() }
            }
            let mustStop = submitted && !previousIsRunning
            if mustStop { manager.connection.stopVPNTunnel() }
            if !saved && error is CancellationError { throw error }
            manager.protocolConfiguration = previous
            manager.localizedDescription = previousName
            manager.isEnabled = previouslyEnabled
            if saved {
                if previous == nil { try await manager.removeFromPreferences(); preferencesLoaded = false }
                else { try await savePreferences(); try await reloadPreferences() }
            }
            if mustStop {
                // Finish observing teardown even if the task initiating Apply was cancelled.
                try await Task { @MainActor in
                    let deadline = ProcessInfo.processInfo.systemUptime + 10
                    while ![NEVPNStatus.invalid, .disconnected].contains(manager.connection.status) {
                        guard ProcessInfo.processInfo.systemUptime < deadline else { throw TunnelControllerError.providerTimedOut }
                        try await Task.sleep(nanoseconds: 100_000_000)
                    }
                }.value
            }
            throw error
        }
    }

    @MainActor private func preferencesStillMatch(_ candidate: NETunnelProviderProtocol) async throws -> Bool {
        try await reloadPreferences()
        guard let installed = manager.protocolConfiguration as? NETunnelProviderProtocol,
              let reference = installed.providerConfiguration else { return false }
        return NSDictionary(dictionary: reference).isEqual(to: candidate.providerConfiguration ?? [:])
    }

    private static func rollbackProtocol(for saved: NEVPNProtocol?) throws -> NEVPNProtocol? {
        guard let provider = saved as? NETunnelProviderProtocol, let raw = provider.providerConfiguration else { return saved }
        guard let reference = try TunnelStartOptions.rollbackReference(for: raw) else {
            throw TunnelStartOptions.ResolutionError.unverifiedInstalledProfile
        }
        guard let snapshot = provider.copy() as? NETunnelProviderProtocol else { throw TunnelControllerError.responseMissingState }
        snapshot.providerConfiguration = reference
        return snapshot
    }

    private func reloadConfirmed(_ options: TunnelStartOptions, transaction: UUID? = nil, replacing: TunnelConfigurationReceipt? = nil, requestID: UUID, onSubmission: () -> Void = {}) async throws {
        let response = try await send(.reloadProfile(options: options, transaction: transaction, replacing: replacing, requestID: requestID), onSubmission: onSubmission)
        guard response.ok else { throw TunnelControllerError.providerReported(response.error ?? "reloadConfig failed") }
        guard response.state?.serviceState == .running, response.state?.appliedReceipt?.matches(options) == true else {
            throw TunnelControllerError.responseMissingState
        }
    }

    private func recoverReload(_ previous: TunnelStartOptions, candidate: TunnelStartOptions, failure: Error, requestID: UUID) async -> Bool {
        var deadline = ProcessInfo.processInfo.systemUptime + 5
        var pendingRestore: UUID?
        var restoreAttempts = 0
        repeat {
            if let state = try? await ping() {
                if let restoreID = pendingRestore {
                    if state.serviceState == .running, state.hasCompletedReload(restoreID) {
                        if state.appliedReceipt?.matches(previous) == true { return true }
                        guard state.appliedReceipt?.matches(candidate) == true else { return false }
                        // The provider finished this request with B still running. A fresh
                        // observation can authorize another conditional restore.
                        pendingRestore = nil
                    }
                    // While restoring, a ping can still report the candidate or a starting service.
                    if state.serviceState != .running && state.serviceState != .starting { return false }
                } else if state.serviceState == .running, let receipt = state.appliedReceipt {
                    if receipt.matches(previous) {
                        if state.hasCompletedReload(requestID) { return true }
                        if case TunnelControllerError.providerReported = failure { return true }
                    } else if receipt.matches(candidate) {
                        guard restoreAttempts < 3 else { return false }
                        restoreAttempts += 1
                        let restoreID = UUID()
                        do {
                            try await reloadConfirmed(previous, replacing: receipt, requestID: restoreID) {
                                pendingRestore = restoreID
                            }
                            return true
                        } catch {
                            if pendingRestore != nil {
                                deadline = ProcessInfo.processInfo.systemUptime + 5
                                continue
                            }
                            // Synchronous rejection: recheck the live receipt before retrying
                            // within the original deadline.
                        }
                    } else { return false }
                } else if state.serviceState != .starting { return false }
            }
            do { try await Task.sleep(nanoseconds: 100_000_000) } catch { return false }
        } while ProcessInfo.processInfo.systemUptime < deadline
        return false
    }

    private struct RunningOtherProfile: LocalizedError {
        let receipt: TunnelConfigurationReceipt?
        var errorDescription: String? { "The VPN is running a different configuration. The selected changes still need applying." }
    }

    @MainActor private func confirmStart(_ options: TunnelStartOptions, validate: () throws -> Void) async throws {
        let deadline = ProcessInfo.processInfo.systemUptime + 20
        while ProcessInfo.processInfo.systemUptime < deadline {
            try await Task.sleep(nanoseconds: 250_000_000)
            try validate()
            let snapshot = try await ping()
            try validate()
            if snapshot.serviceState.running {
                guard snapshot.appliedReceipt?.matches(options) == true else {
                    throw RunningOtherProfile(receipt: snapshot.appliedReceipt)
                }
                return
            }
            if let detail = snapshot.detail { throw TunnelControllerError.providerReported(detail) }
        }
        throw TunnelControllerError.providerTimedOut
    }

    public func location(settings: VirtualLocationSettings?) async throws -> VirtualLocationSnapshot {
        let response = try await send(.location(settings: settings))
        guard response.ok, let snapshot = response.location else { throw TunnelControllerError.providerReported(response.error ?? "Location response unavailable") }
        return snapshot
    }

    public func recoverExperiment() async throws {
        let response = try await send(.recoverExperiment)
        guard response.ok else { throw TunnelControllerError.providerReported(response.error ?? "Recovery failed") }
    }

    public func removeLearnedDomain(_ domain: String) async throws {
        let response = try await send(.removeLearnedDomain(domain))
        guard response.ok else { throw TunnelControllerError.providerReported(response.error ?? "Removal failed") }
    }

    public func observations() -> AsyncStream<TunnelObservation> {
        AsyncStream { continuation in
            // NEVPNStatusDidChange is the unified cross-platform status
            // notification (iOS 9+ / macOS 10.11+).
            let sequence = TunnelRequestFence()
            let emit: @Sendable () -> Void = { [weak self] in
                guard let self else { return }
                let ticket = sequence.begin()
                let observed = self.manager.connection.status
                continuation.yield(self.currentObservation())
                guard observed == .disconnected || observed == .invalid else { return }
                Task { [weak self] in
                    guard let self else { return }
                    let error = await self.lastDisconnectError()
                    guard self.manager.connection.status == observed, (try? sequence.check(ticket)) != nil else { return }
                    if let error {
                        let native = error as NSError
                        TunnelDiagnostics.recordEvent(stage: "System VPN", message: "\(native.domain) [\(native.code)]: \(native.localizedDescription)")
                        continuation.yield(self.currentObservation(systemError: error))
                    }
                }
            }
            let observer = NotificationCenter.default.addObserver(
                forName: .NEVPNStatusDidChange,
                object: nil,
                queue: nil
            ) { [weak self] notification in
                guard let self else { return }
                if let connection = notification.object as? NEVPNConnection, connection !== self.manager.connection { return }
                emit()
            }
            emit()
            continuation.onTermination = { _ in
                _ = sequence.begin()
                NotificationCenter.default.removeObserver(observer)
            }
        }
    }

    // MARK: Provider protocol construction

    /// Builds the NETunnelProviderProtocol for the provider target:
    /// provider bundle identifier plus the persisted start options
    /// (configContent) so a cold provider launch can resolve them without
    /// the app running.
    public static func makeProviderProtocol(
        bundleIdentifier: String,
        options: TunnelStartOptions
    ) throws -> NETunnelProviderProtocol {
        let protocolConfiguration = NETunnelProviderProtocol()
        protocolConfiguration.providerBundleIdentifier = bundleIdentifier
        protocolConfiguration.providerConfiguration = try options.storedReference()
        // NEVPNProtocol requires a non-empty serverAddress to save; the
        // value is cosmetic for a local packet-tunnel provider.
        protocolConfiguration.serverAddress = "sakamoto"
        return protocolConfiguration
    }

    /// NEVPNStatus -> ServiceState. `connected` is the system-confirmed
    /// fact; connecting/reasserting stay in-flight guesses (ServiceStarting)
    /// exactly like the macOS TUI treats supervisor transitions.
    public static func serviceState(for status: NEVPNStatus) -> ServiceState {
        switch status {
        case .connected:
            return .running
        case .connecting, .reasserting:
            return .starting
        case .disconnecting:
            return .stopping
        case .disconnected:
            return .stopped
        case .invalid:
            return .unavailable
        @unknown default:
            return .stopped
        }
    }

    // MARK: Internals

    private func lastDisconnectError() async -> Error? {
        if #available(iOS 16.0, macOS 13.0, *) {
            return await withCheckedContinuation { continuation in
                let reply = DisconnectErrorReply(continuation)
                DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 3) { reply.finish(nil) }
                manager.connection.fetchLastDisconnectError { reply.finish($0) }
            }
        }
        return nil
    }

    private func disconnectDetail(systemError: Error?) -> String? {
        var details = TunnelDiagnostics.latest().map { ["\($0.stage): \($0.message)"] } ?? []
        if let systemError {
            let native = systemError as NSError
            details.append("System VPN: \(native.domain) [\(native.code)]: \(TunnelDiagnostics.sanitized(native.localizedDescription))")
        }
        return details.isEmpty ? nil : details.joined(separator: "\n")
    }

    private func currentObservation(systemError: Error? = nil) -> TunnelObservation {
        let state = Self.serviceState(for: manager.connection.status)
        let detail = state == .stopped || state == .unavailable ? disconnectDetail(systemError: systemError) : nil
        return TunnelObservation(serviceState: state, conflict: false, detail: detail)
    }

    private func loadProviderPreferences(refresh: Bool = false) async throws {
        if preferencesLoaded {
            // Refresh mutations after Settings/Control Center changed preferences.
            // Status polling reads the existing session without a disk reload.
            if refresh { try await reloadPreferences() }
        } else if manager is NETunnelProviderManager {
            let managers: [NETunnelProviderManager] = try await withCheckedThrowingContinuation { continuation in
                NETunnelProviderManager.loadAllFromPreferences { managers, error in
                    if let error { continuation.resume(throwing: error) }
                    else { continuation.resume(returning: managers ?? []) }
                }
            }
            if let saved = managers.first(where: {
                ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == providerBundleIdentifier
            }) { manager = saved }
        }
        if let proto = manager.protocolConfiguration as? NETunnelProviderProtocol,
           let legacy = proto.providerConfiguration, legacy["configContent"] != nil {
            if let options = TunnelStartOptions(providerConfiguration: legacy), options.profileID != nil {
                proto.providerConfiguration = try options.storedReference()
            } else {
                proto.providerConfiguration = [:]
                manager.isOnDemandEnabled = false
            }
            manager.protocolConfiguration = proto
            try await savePreferences()
            try await reloadPreferences()
        }
        preferencesLoaded = true
    }

    private func reloadPreferences() async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.loadFromPreferences { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
    }

    private func savePreferences() async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.saveToPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private func send(_ request: TunnelRequest, onSubmission: () -> Void = {}) async throws -> TunnelResponse {
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw TunnelControllerError.notTunnelSession
        }
        let data = try request.encode()
        return try await withCheckedThrowingContinuation { continuation in
            let reply = ProviderMessageReply(continuation)
            // A provider may be alive but blocked in native code. Keep the
            // app's independent system Disconnect path usable in that case.
            let timeout: TimeInterval = request == .ping ? 3 : 60
            DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + timeout) {
                reply.finish(.failure(TunnelControllerError.providerTimedOut))
            }
            do {
                try session.sendProviderMessage(data) { responseData in
                    guard let responseData else { reply.finish(.failure(TunnelControllerError.emptyProviderResponse)); return }
                    do { reply.finish(.success(try TunnelResponse.decode(responseData))) }
                    catch { reply.finish(.failure(error)) }
                }
                onSubmission()
            } catch { reply.finish(.failure(error)) }
        }
    }
}

private final class DisconnectErrorReply: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Error?, Never>?
    init(_ continuation: CheckedContinuation<Error?, Never>) { self.continuation = continuation }
    func finish(_ error: Error?) {
        lock.lock(); let pending = continuation; continuation = nil; lock.unlock()
        pending?.resume(returning: error)
    }
}

private final class ProviderMessageReply: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<TunnelResponse, Error>?
    init(_ continuation: CheckedContinuation<TunnelResponse, Error>) { self.continuation = continuation }
    func finish(_ result: Result<TunnelResponse, Error>) {
        lock.lock(); let pending = continuation; continuation = nil; lock.unlock()
        pending?.resume(with: result)
    }
}

private final class TunnelRequestFence: @unchecked Sendable {
    private let lock = NSRecursiveLock()
    private var generation: UInt64 = 0
    func begin() -> UInt64 { lock.lock(); defer { lock.unlock() }; generation &+= 1; return generation }
    func check(_ ticket: UInt64) throws {
        lock.lock(); defer { lock.unlock() }
        guard ticket == generation else { throw CancellationError() }
    }
    func perform(_ ticket: UInt64, operation: () throws -> Void) throws {
        lock.lock(); defer { lock.unlock() }; try check(ticket); try operation()
    }
}
