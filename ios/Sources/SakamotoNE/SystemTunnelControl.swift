import Foundation
import NetworkExtension
import SakamotoKit

/// Extensions operate only on this app's already-authorized provider profile.
/// They never create a profile, read private app config, or persist credentials.
@MainActor
public enum SystemTunnelControl {
    private static let requests = SystemTunnelRequestGate()
    public static let providerIdentifier = "com.pidal.sakamoto.PacketTunnel"

    public static func status() async throws -> ServiceState {
        guard let manager = try await configuredManager() else { return .unavailable }
        return NETunnelController.serviceState(for: manager.connection.status)
    }

    /// Control reads can arrive before a cold accepted Start reaches NE status.
    /// Raw status() remains an immediate observation for other callers.
    public static func controlEnabled() async throws -> Bool {
        guard let manager = try await configuredManager() else { return false }
        return try await SystemTunnelTransition.controlValue(
            state: { NETunnelController.serviceState(for: manager.connection.status) },
            snapshot: { SystemSurfaceStore.read() })
    }

    @discardableResult
    public static func perform(_ action: SystemTunnelAction) async throws -> ServiceState {
        let request = requests.begin()
        guard let manager = try await configuredManager() else { throw ControlError.needsSetup }
        var current = requests.actionState(observed: NETunnelController.serviceState(for: manager.connection.status))
        guard requests.isCurrent(request) else { return current }
        // Turning an already-stopped VPN off must also disarm on-demand.
        if case .disconnect = action, manager.isOnDemandEnabled {
            manager.isOnDemandEnabled = false
            try await manager.saveToPreferences()
            try await manager.loadFromPreferences()
        }
        current = try await SystemTunnelTransition.prepare(action,
            state: { requests.actionState(observed: NETunnelController.serviceState(for: manager.connection.status)) },
            isCurrent: { requests.isCurrent(request) })
        guard requests.isCurrent(request) else { return current }
        switch SystemTunnelPolicy.decision(for: action, state: current) {
        case .busy: throw ControlError.busy
        case .unchanged: return current
        case .start:
            try validateStart(manager)
            if !manager.isEnabled {
                manager.isEnabled = true
                try await manager.saveToPreferences()
                try await manager.loadFromPreferences()
            }
            let result = try await SystemTunnelTransition.start(
                state: { NETunnelController.serviceState(for: manager.connection.status) },
                isCurrent: { requests.isCurrent(request) },
                start: {
                    try validateStart(manager)
                    let requestedAt = Date()
                    try manager.connection.startVPNTunnel()
                    requests.didSubmitStart()
                    SystemSurfaceStore.write(SystemSurfaceStore.read().recordingControlStart(at: requestedAt))
                },
                reload: { try await manager.loadFromPreferences() })
            guard requests.isCurrent(request) else {
                return requests.actionState(observed: NETunnelController.serviceState(for: manager.connection.status))
            }
            return result
        case .stop:
            // A manual stop must survive the next network request.
            if manager.isOnDemandEnabled {
                manager.isOnDemandEnabled = false
                try await manager.saveToPreferences()
                try await manager.loadFromPreferences()
            }
            return try requests.submit(request, state: { NETunnelController.serviceState(for: manager.connection.status) }) {
                try SystemTunnelTransition.submit(.disconnect, state: { requests.actionState(observed: NETunnelController.serviceState(for: manager.connection.status)) }, start: {}, stop: {
                    manager.connection.stopVPNTunnel()
                    requests.didSubmitStop()
                })
            }
        }
    }

    public static func automaticConnectionSettings() async throws -> AutomaticConnectionSettings {
        guard let manager = try await configuredManager() else { return .init() }
        return AutomaticConnectionPolicy.settings(from: manager)
    }

    public static func setAutomaticConnection(_ settings: AutomaticConnectionSettings) async throws {
        guard let manager = try await configuredManager() else { throw ControlError.needsSetup }
        if settings.mode != .off {
            guard UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.bool(forKey: "sakamoto.profile.requiresApply") != true else {
                throw ControlError.needsApply
            }
            guard let proto = manager.protocolConfiguration as? NETunnelProviderProtocol,
                  let options = TunnelStartOptions(providerConfiguration: proto.providerConfiguration ?? [:]),
                  !options.configContent.isEmpty else { throw ControlError.needsSetup }
            manager.isEnabled = true
        }
        try AutomaticConnectionPolicy.apply(settings, to: manager)
        try await manager.saveToPreferences()
        try await manager.loadFromPreferences()
    }

    private static func validateStart(_ manager: NETunnelProviderManager) throws {
        guard UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.bool(forKey: "sakamoto.profile.requiresApply") != true else {
            throw ControlError.needsApply
        }
        guard let proto = manager.protocolConfiguration as? NETunnelProviderProtocol,
              let options = TunnelStartOptions(providerConfiguration: proto.providerConfiguration ?? [:]),
              !options.configContent.isEmpty else { throw ControlError.needsSetup }
    }

    private static func configuredManager() async throws -> NETunnelProviderManager? {
        let managers: [NETunnelProviderManager] = try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume(returning: managers ?? []) }
            }
        }
        guard let manager = managers.first(where: {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == providerIdentifier
        }) else { return nil }
        try await manager.loadFromPreferences()
        return manager
    }

    public enum ControlError: LocalizedError {
        case needsSetup, needsApply, busy
        public var errorDescription: String? {
            switch self {
            case .needsSetup: return "Open sakamoto and connect once to set up and authorize the VPN."
            case .needsApply: return "Open sakamoto and apply the selected configuration before connecting from a widget or shortcut."
            case .busy: return "The VPN is stopping. Try again after it finishes."
            }
        }
    }
}
