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
        switch SystemTunnelPolicy.decision(for: action, state: current) {
        case .busy: throw ControlError.busy
        case .unchanged: return current
        case .start:
            guard manager.isEnabled,
                  let protocolConfiguration = manager.protocolConfiguration as? NETunnelProviderProtocol,
                  let options = TunnelStartOptions(providerConfiguration: protocolConfiguration.providerConfiguration ?? [:]),
                  !options.configContent.isEmpty else { throw ControlError.needsSetup }
            try manager.connection.startVPNTunnel()
            return .starting
        case .stop:
            manager.connection.stopVPNTunnel()
            return .stopping
        }
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
        case needsSetup, busy
        public var errorDescription: String? {
            switch self {
            case .needsSetup: return "Open sakamoto and connect once to set up and authorize the VPN."
            case .busy: return "The VPN is starting or stopping. Try again after it finishes."
            }
        }
    }
}
