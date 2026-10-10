import SakamotoKit

/// Intents normally execute in the containing app. A legacy widget host can
/// still use an already-applied saved profile, without reading private config.
@MainActor
enum SystemSurfaceConfiguration {
    static func prepare() throws -> TunnelStartOptions? { nil }
    static func isCurrent(_ options: TunnelStartOptions) -> Bool { false }
    static func reconcile(_ state: ServiceState) {}
}
