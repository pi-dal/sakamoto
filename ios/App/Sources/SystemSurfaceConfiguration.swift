import SakamotoKit
import SakamotoNE

/// Shares the app's existing configuration owner with background App Intents.
/// No second store is created, and no private configuration reaches WidgetKit.
@MainActor
enum SystemSurfaceConfiguration {
    static weak var store: ConfigStore?

    static func prepare() throws -> TunnelStartOptions? {
        guard let store else { throw SystemTunnelControl.ControlError.needsApply }
        return try store.systemConnectionOptions()
    }

    static func isCurrent(_ options: TunnelStartOptions) -> Bool {
        guard let store, let current = try? store.systemConnectionOptions(),
              let identity = try? TunnelConfigurationReceipt(options: options) else { return false }
        return identity.matches(current)
    }

    static func reconcile(_ state: ServiceState) {
        store?.reconcileAppliedConnection(service: state)
    }
}
