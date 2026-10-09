import Foundation
import NetworkExtension
import SakamotoKit

/// Extensions operate only on this app's already-authorized provider profile.
/// They never create a profile, read private app config, or persist credentials.
@MainActor
public enum SystemTunnelControl {
    private static var actionInFlight = false
    public static let providerIdentifier = "com.pidal.sakamoto.PacketTunnel"

    public static func status() async throws -> ServiceState {
        guard let manager = try await configuredManager() else { return .unavailable }
        return NETunnelController.serviceState(for: manager.connection.status)
    }

    @discardableResult
    public static func perform(_ action: SystemTunnelAction) async throws -> ServiceState {
        guard !actionInFlight else { throw ControlError.busy }
        actionInFlight = true
        defer { actionInFlight = false }
        guard let manager = try await configuredManager() else { throw ControlError.needsSetup }
        var current = NETunnelController.serviceState(for: manager.connection.status)
        // Disconnect can cancel a connection that is still starting.
        if current == .stopping || (current == .starting && !isDisconnect(action)) {
            current = try await SystemTunnelTransition.wait(state: { NETunnelController.serviceState(for: manager.connection.status) },
                                                           accepts: { $0 != .starting && $0 != .stopping })
        }
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
            return try await SystemTunnelTransition.wait(state: { NETunnelController.serviceState(for: manager.connection.status) }, accepts: { $0 == .running })
        case .stop:
            // A manual stop must survive the next network request.
            if manager.isOnDemandEnabled {
                manager.isOnDemandEnabled = false
                try await manager.saveToPreferences()
                try await manager.loadFromPreferences()
            }
            manager.connection.stopVPNTunnel()
            return try await SystemTunnelTransition.wait(state: { NETunnelController.serviceState(for: manager.connection.status) }, accepts: { $0 == .stopped || $0 == .unavailable })
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

    private static func isDisconnect(_ action: SystemTunnelAction) -> Bool {
        if case .disconnect = action { return true }
        return false
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
        case needsSetup, needsApply, busy, timedOut
        public var errorDescription: String? {
            switch self {
            case .needsSetup: return "Open sakamoto and connect once to set up and authorize the VPN."
            case .needsApply: return "Open sakamoto and apply the selected configuration before connecting from a widget or shortcut."
            case .busy: return "The VPN is starting or stopping. Try again after it finishes."
            case .timedOut: return "The VPN has not finished changing state. Open sakamoto to check its status, or retry the control."
            }
        }
    }
}
