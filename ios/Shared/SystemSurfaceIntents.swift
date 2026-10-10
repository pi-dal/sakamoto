import AppIntents
import WidgetKit
import SakamotoKit
import SakamotoNE

/// Mutating intents run in the containing app, including a cold background
/// launch. NetworkExtension entitlement alone does not select the intent host.
struct ConnectTunnelIntent: AppIntent {
    static var title: LocalizedStringResource = "Connect VPN"
    static var description = IntentDescription("Connect the saved sakamoto VPN without opening the app. Use with an App → Is Opened automation; an already-connected VPN stays connected.", categoryName: "VPN connection", searchKeywords: ["App automation", "Connect", "VPN"])
    static var openAppWhenRun = false
    @available(iOS 26.0, *)
    static var supportedModes: IntentModes { .foreground(.dynamic) }
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(.connect)
        return .result()
    }
}

struct DisconnectTunnelIntent: AppIntent {
    static var title: LocalizedStringResource = "Disconnect VPN"
    static var description = IntentDescription("Disconnect the device VPN and pause on-demand connection. Use with an optional App → Is Closed automation; switching away from the app also triggers it.", categoryName: "VPN connection", searchKeywords: ["App automation", "Disconnect", "VPN"])
    static var openAppWhenRun = false
    @available(iOS 26.0, *)
    static var supportedModes: IntentModes { .foreground(.dynamic) }
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(.disconnect)
        return .result()
    }
}

struct ToggleTunnelIntent: AppIntent {
    static var title: LocalizedStringResource = "Toggle VPN"
    static var description = IntentDescription("Switch the saved VPN on or off. For App automations, use Connect VPN or Disconnect VPN to keep the requested state explicit.", categoryName: "VPN connection")
    static var openAppWhenRun = false
    @available(iOS 26.0, *)
    static var supportedModes: IntentModes { .foreground(.dynamic) }
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(.toggle)
        return .result()
    }
}

struct TunnelStatusIntent: AppIntent {
    static var title: LocalizedStringResource = "Get VPN status"
    static var openAppWhenRun = false
    @available(iOS 26.0, *)
    static var supportedModes: IntentModes { .foreground(.dynamic) }
    func perform() async throws -> some IntentResult & ReturnsValue<String> & ProvidesDialog {
        let state = try await SystemTunnelControl.status()
        return .result(value: state.rawValue, dialog: "VPN: \(state.rawValue)")
    }
}

@available(iOS 18.0, *)
struct SetTunnelEnabledIntent: SetValueIntent {
    static var title: LocalizedStringResource = "Set VPN connection"
    static var openAppWhenRun = false
    @available(iOS 26.0, *)
    static var supportedModes: IntentModes { .foreground(.dynamic) }
    @Parameter(title: "Connected") var value: Bool
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(SystemTunnelPolicy.action(enabled: value))
        return .result()
    }
}

enum SystemSurfaceActions {
    static func perform(_ action: SystemTunnelAction) async throws {
        defer { SystemSurfaceReload.reload() }
        let name = String(describing: action)
        TunnelDiagnostics.recordControlExecution(action: name, stage: "Requested")
        do {
            let state = try await SystemTunnelControl.perform(action)
            TunnelDiagnostics.recordControlExecution(action: name, stage: "Submitted", state: state)
            let snapshot = SystemSurfaceStore.read().reconciled(with: state, at: Date())
            // An idempotent Connect must not renew an older network probe.
            SystemSurfaceStore.write(snapshot)
        } catch {
            TunnelDiagnostics.recordControlExecution(action: name, stage: "Failed", error: error)
            TunnelDiagnostics.recordControlFailure(error)
            // Clear stale requested values even when a transition times out.
            if let state = try? await SystemTunnelControl.status() {
                SystemSurfaceStore.write(SystemSurfaceStore.read().reconciled(with: state, at: Date()))
            }
            throw error
        }
    }
}

enum SystemSurfaceReload {
    static func reload() {
        WidgetCenter.shared.reloadTimelines(ofKind: "com.pidal.sakamoto.vpn-widget")
        if #available(iOS 18.0, *) {
            ControlCenter.shared.reloadControls(ofKind: "com.pidal.sakamoto.vpn-control")
        }
    }
}

struct SakamotoShortcuts: AppShortcutsProvider {
    static var appShortcuts: [AppShortcut] {
        AppShortcut(intent: ConnectTunnelIntent(), phrases: ["Connect VPN with \(.applicationName)"], shortTitle: "Connect VPN", systemImageName: "network")
        AppShortcut(intent: DisconnectTunnelIntent(), phrases: ["Disconnect VPN with \(.applicationName)"], shortTitle: "Disconnect VPN", systemImageName: "power")
        AppShortcut(intent: ToggleTunnelIntent(), phrases: ["Toggle VPN with \(.applicationName)"], shortTitle: "Toggle VPN", systemImageName: "switch.2")
        AppShortcut(intent: TunnelStatusIntent(), phrases: ["Get VPN status with \(.applicationName)"], shortTitle: "VPN Status", systemImageName: "info.circle")
    }
}
