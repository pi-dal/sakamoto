import Foundation
import NetworkExtension
import SakamotoKit

/// Operates only on this app's already-authorized provider profile. System
/// actions execute in the containing app; extension readers may use the
/// display-only App Group observation if no manager is visible to their host.
@MainActor
public enum SystemTunnelControl {
    private static let requests = SystemTunnelRequestGate()
    public static let providerIdentifier = "com.pidal.sakamoto.PacketTunnel"

    public static func status() async throws -> ServiceState {
        guard let manager = try await configuredManager() else { return .unavailable }
        return NETunnelController.serviceState(for: manager.connection.status)
    }

    /// No visible manager is an unknown observation, not proof of Off. When
    /// available, native status always overrides the shared display snapshot.
    public static func surfaceSnapshot() async throws -> SystemSurfaceSnapshot {
        let manager = try await configuredManager()
        let saved = SystemSurfaceStore.read()
        let state = manager.map { NETunnelController.serviceState(for: $0.connection.status) } ?? saved.serviceState
        return saved.reconciled(with: state, at: Date())
    }

    /// Control reads can arrive before a cold accepted Start reaches NE status.
    /// Raw status() remains an immediate observation for other callers.
    public static func controlEnabled() async throws -> Bool {
        guard let manager = try await configuredManager() else {
            return SystemSurfaceStore.read().controlDisplayEnabled(at: Date())
        }
        return try await SystemTunnelTransition.controlValue(
            state: { NETunnelController.serviceState(for: manager.connection.status) },
            snapshot: { SystemSurfaceStore.read() })
    }

    @discardableResult
    public static func perform(_ action: SystemTunnelAction,
                               prepareConfiguration: (@MainActor () throws -> TunnelStartOptions?)? = nil,
                               isPreparedConfigurationCurrent: (@MainActor (TunnelStartOptions) -> Bool)? = nil) async throws -> ServiceState {
        let request = requests.begin()
        guard let manager = try await configuredManager() else { throw ControlError.needsSetup }
        var current = requests.actionState(observed: NETunnelController.serviceState(for: manager.connection.status))
        guard requests.isCurrent(request) else { return current }
        // Turning an already-stopped VPN off must also disarm on-demand.
        if case .disconnect = action, current == .stopped {
            try await AutomaticConnectionPolicy.pause(manager) {
                guard requests.isCurrent(request) else { throw CancellationError() }
            }
        }
        current = try await SystemTunnelTransition.prepare(action,
            state: { requests.actionState(observed: NETunnelController.serviceState(for: manager.connection.status)) },
            isCurrent: { requests.isCurrent(request) })
        guard requests.isCurrent(request) else { return current }
        switch SystemTunnelPolicy.decision(for: action, state: current) {
        case .busy: throw ControlError.busy
        case .unchanged: return current
        case .start:
            // A manual On follows the same validated configuration preparation
            // as Home Connect. Pending Apply still blocks unprepared cold starts.
            let result: ServiceState
            if let prepared = try prepareConfiguration?() {
                guard !prepared.configContent.isEmpty else { throw ControlError.needsSetup }
                let controller = NETunnelController(providerBundleIdentifier: providerIdentifier, manager: manager) { candidate in
                    requests.isCurrent(request) && (isPreparedConfigurationCurrent?(candidate) ?? true)
                }
                try await controller.connect(options: prepared)
                result = .running
            } else {
                // Let the provider resolve the committed transaction at startup.
                result = try await SystemTunnelTransition.start(
                    state: { NETunnelController.serviceState(for: manager.connection.status) },
                    isCurrent: { requests.isCurrent(request) },
                    start: {
                        _ = try validatedSavedReference(manager)
                        try manager.connection.startVPNTunnel(options: nil)
                        requests.didSubmitStart()
                    },
                    reload: { try await manager.loadFromPreferences() })
            }
            if requests.isCurrent(request), result == .starting || result == .running {
                SystemSurfaceStore.write(SystemSurfaceStore.read().recordingControlStart(at: Date()))
            }
            return result
        case .stop:
            // A manual stop must survive the next network request.
            var result = current
            try await AutomaticConnectionPolicy.pause(manager) {
                guard requests.isCurrent(request) else { throw CancellationError() }
                result = try SystemTunnelTransition.submit(.disconnect, state: { requests.actionState(observed: NETunnelController.serviceState(for: manager.connection.status)) }, start: {}, stop: {
                    manager.connection.stopVPNTunnel()
                    requests.didSubmitStop()
                })
            }
            return result
        }
    }

    public static func automaticConnectionSettings() async throws -> AutomaticConnectionSettings {
        guard let manager = try await configuredManager() else { throw ControlError.needsSetup }
        return AutomaticConnectionPolicy.settings(from: manager)
    }

    public static func setAutomaticConnection(_ settings: AutomaticConnectionSettings) async throws {
        guard let manager = try await configuredManager() else { throw ControlError.needsSetup }
        if settings.mode != .off {
            let reference = try validatedSavedReference(manager)
            _ = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: reference, requiresApply: false)
        }
        try await AutomaticConnectionPolicy.updatePreferences(manager) {
            try AutomaticConnectionPolicy.apply(settings, to: manager)
            if settings.mode != .off { manager.isEnabled = true }
        }
    }

    private static func validatedSavedReference(_ manager: NETunnelProviderManager) throws -> [String: Any] {
        guard UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.bool(forKey: "sakamoto.profile.requiresApply") != true else {
            throw ControlError.needsApply
        }
        guard let proto = manager.protocolConfiguration as? NETunnelProviderProtocol,
              proto.providerBundleIdentifier == providerIdentifier,
              let saved = proto.providerConfiguration else { throw ControlError.needsSetup }
        guard saved["configContent"] == nil else { throw ControlError.needsApply }
        // This check runs in the widget host. The provider resolves the revision
        // and verifies its contents against the digest when the tunnel starts.
        guard let id = saved["profileID"] as? String, UUID(uuidString: id) != nil,
              let digest = saved["payloadDigest"] as? String, digest.utf8.count == 64,
              digest.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) })
        else { throw ControlError.needsApply }
        return saved
    }

    #if DEBUG
    @TaskLocal public static var testManager: NETunnelProviderManager?
    #endif

    private static func configuredManager() async throws -> NETunnelProviderManager? {
        #if DEBUG
        if let testManager {
            try await testManager.loadFromPreferences()
            return testManager
        }
        #endif
        let managers: [NETunnelProviderManager] = try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume(returning: managers ?? []) }
            }
        }
        guard let manager = managers.first(where: {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == providerIdentifier
        }) else {
            TunnelDiagnostics.recordControlProfileRead(managerCount: managers.count, matched: false, nativeStatus: nil)
            return nil
        }
        try await manager.loadFromPreferences()
        TunnelDiagnostics.recordControlProfileRead(managerCount: managers.count, matched: true, nativeStatus: manager.connection.status.rawValue)
        return manager
    }

    public enum ControlError: LocalizedError {
        case needsSetup, needsApply, busy
        public var errorDescription: String? {
            switch self {
            case .needsSetup: return "Open sakamoto and connect once to set up and authorize the VPN."
            case .needsApply: return "Open Config in sakamoto and apply the selected configuration before connecting."
            case .busy: return "The VPN is stopping. Try again after it finishes."
            }
        }
    }
}
