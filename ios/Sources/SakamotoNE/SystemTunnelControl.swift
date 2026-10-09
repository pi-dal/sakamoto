import Foundation
import NetworkExtension
import SakamotoKit

/// Extensions operate only on this app's already-authorized provider profile.
/// They never create a profile, read private app config, or persist credentials.
public enum SystemTunnelControl {
    public static let providerIdentifier = "com.pidal.sakamoto.PacketTunnel"

    public static func status() async throws -> ServiceState {
        guard let manager = try await configuredManager() else { return .unavailable }
        return NETunnelController.serviceState(for: manager.connection.status)
    }

    @discardableResult
    public static func perform(_ action: SystemTunnelAction) async throws -> ServiceState {
        guard let manager = try await configuredManager() else { throw ControlError.needsSetup }
        let current = NETunnelController.serviceState(for: manager.connection.status)
        // Turning an already-stopped VPN off must also disarm on-demand.
        if case .disconnect = action, manager.isOnDemandEnabled {
            manager.isOnDemandEnabled = false
            try await manager.saveToPreferences()
            try await manager.loadFromPreferences()
        }
        switch SystemTunnelPolicy.decision(for: action, state: current) {
        case .busy: throw ControlError.busy
        case .unchanged: return current
        case .start:
            guard UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier)?.bool(forKey: "sakamoto.profile.requiresApply") != true else {
                throw ControlError.needsApply
            }
            guard let protocolConfiguration = manager.protocolConfiguration as? NETunnelProviderProtocol,
                  let options = TunnelStartOptions(providerConfiguration: protocolConfiguration.providerConfiguration ?? [:]),
                  !options.configContent.isEmpty else { throw ControlError.needsSetup }
            if !manager.isEnabled {
                manager.isEnabled = true
                try await manager.saveToPreferences()
                try await manager.loadFromPreferences()
            }
            try manager.connection.startVPNTunnel()
            return .starting
        case .stop:
            // A manual stop must survive the next network request.
            if manager.isOnDemandEnabled {
                manager.isOnDemandEnabled = false
                try await manager.saveToPreferences()
                try await manager.loadFromPreferences()
            }
            manager.connection.stopVPNTunnel()
            return .stopping
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

    private static func configuredManager() async throws -> NETunnelProviderManager? {
        let managers: [NETunnelProviderManager] = try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume(returning: managers ?? []) }
            }
        }
        return managers.first {
            ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == providerIdentifier
        }
    }

    public enum ControlError: LocalizedError {
        case needsSetup, needsApply, busy
        public var errorDescription: String? {
            switch self {
            case .needsSetup: return "Open sakamoto and connect once to set up and authorize the VPN."
            case .needsApply: return "Open sakamoto and apply the selected configuration before connecting from a widget or shortcut."
            case .busy: return "The VPN is starting or stopping. Try again after it finishes."
            }
        }
    }
}
