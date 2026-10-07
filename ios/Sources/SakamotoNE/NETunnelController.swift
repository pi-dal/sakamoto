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
}

public final class NETunnelController: TunnelControlling, @unchecked Sendable {
    /// The PacketTunnelProvider's bundle identifier. Injected by the app
    /// target at runtime; this repository deliberately does not invent one
    /// (README.md "Boundaries": no guessed bundle IDs).
    public let providerBundleIdentifier: String
    private var manager: NEVPNManager
    private var preferencesLoaded = false

    /// Hook for VPN-conflict detection (core: another VPN TUN active while
    /// ours runs). The app target wires this to real system VPN inspection.
    /// Default reports no conflict; observations say `conflict: false`
    /// because nothing was measured — never as a claim that none exists.
    public var conflictCheck: @Sendable () async -> Bool

    public init(
        providerBundleIdentifier: String,
        manager: NEVPNManager = NETunnelProviderManager(),
        conflictCheck: @escaping @Sendable () async -> Bool = { false }
    ) {
        self.providerBundleIdentifier = providerBundleIdentifier
        self.manager = manager
        self.conflictCheck = conflictCheck
    }

    // MARK: TunnelControlling

    public func connect(options: TunnelStartOptions) async throws {
        try await loadProviderPreferences()
        manager.protocolConfiguration = Self.makeProviderProtocol(
            bundleIdentifier: providerBundleIdentifier,
            options: options
        )
        manager.localizedDescription = "sakamoto"
        manager.isEnabled = true
        try await savePreferences()
        try await reloadPreferences()
        try manager.connection.startVPNTunnel(options: options.startTunnelOptions)
    }

    public func disconnect() async throws {
        try await loadProviderPreferences()
        manager.connection.stopVPNTunnel()
    }

    public func ping() async throws -> TunnelStateSnapshot {
        try await loadProviderPreferences()
        let service = Self.serviceState(for: manager.connection.status)
        if service != .running { return TunnelStateSnapshot(serviceState: service, detail: nil) }
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
        let response = try await send(.reloadConfig(content: configContent))
        guard response.ok else {
            throw TunnelControllerError.providerReported(response.error ?? "reloadConfig failed")
        }
    }

    public func observations() -> AsyncStream<TunnelObservation> {
        AsyncStream { continuation in
            // NEVPNStatusDidChange is the unified cross-platform status
            // notification (iOS 9+ / macOS 10.11+).
            let observer = NotificationCenter.default.addObserver(
                forName: .NEVPNStatusDidChange,
                object: nil,
                queue: nil
            ) { [weak self] _ in
                guard let self else { return }
                continuation.yield(self.currentObservation())
            }
            continuation.yield(currentObservation())
            continuation.onTermination = { _ in
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
    ) -> NETunnelProviderProtocol {
        let protocolConfiguration = NETunnelProviderProtocol()
        protocolConfiguration.providerBundleIdentifier = bundleIdentifier
        protocolConfiguration.providerConfiguration = options.providerConfiguration
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

    private func currentObservation() -> TunnelObservation {
        let state = Self.serviceState(for: manager.connection.status)
        return TunnelObservation(serviceState: state, conflict: false, detail: nil)
    }

    private func loadProviderPreferences() async throws {
        guard !preferencesLoaded else { return }
        if manager is NETunnelProviderManager {
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

    private func send(_ request: TunnelRequest) async throws -> TunnelResponse {
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw TunnelControllerError.notTunnelSession
        }
        let data = try request.encode()
        return try await withCheckedThrowingContinuation { continuation in
            let resumeWithData = { (responseData: Data?) in
                guard let responseData = responseData else {
                    continuation.resume(throwing: TunnelControllerError.emptyProviderResponse)
                    return
                }
                do {
                    continuation.resume(returning: try TunnelResponse.decode(responseData))
                } catch {
                    continuation.resume(throwing: error)
                }
            }
            // Swift import of sendProviderMessage(_:returnError:responseHandler:)
            // is `throws` (the ObjC BOOL return becomes an error); queue
            // rejection and transport failures both throw.
            do {
                try session.sendProviderMessage(data) { responseData in
                    resumeWithData(responseData)
                }
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }
}
