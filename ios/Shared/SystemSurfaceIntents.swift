import AppIntents
import WidgetKit
import SakamotoKit
import SakamotoNE

/// Operations run in the containing app so the VPN capability belongs to the
/// same process as the main Connect button. No shared config or keys are needed.
struct ConnectTunnelIntent: AppIntent {
    static var title: LocalizedStringResource = "Connect VPN"
    static var description = IntentDescription("Connect the saved sakamoto VPN profile.")
    static var openAppWhenRun = true
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(.connect)
        return .result()
    }
}

struct DisconnectTunnelIntent: AppIntent {
    static var title: LocalizedStringResource = "Disconnect VPN"
    static var openAppWhenRun = true
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(.disconnect)
        return .result()
    }
}

struct ToggleTunnelIntent: AppIntent {
    static var title: LocalizedStringResource = "Toggle VPN"
    static var openAppWhenRun = true
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(.toggle)
        return .result()
    }
}

struct TunnelStatusIntent: AppIntent {
    static var title: LocalizedStringResource = "Get VPN status"
    static var openAppWhenRun = true
    func perform() async throws -> some IntentResult & ReturnsValue<String> & ProvidesDialog {
        let state = try await SystemTunnelControl.status()
        return .result(value: state.rawValue, dialog: "VPN: \(state.rawValue)")
    }
}

@available(iOS 18.0, *)
struct SetTunnelEnabledIntent: SetValueIntent {
    static var title: LocalizedStringResource = "Set VPN connection"
    static var openAppWhenRun = true
    @Parameter(title: "Connected") var value: Bool
    func perform() async throws -> some IntentResult {
        try await SystemSurfaceActions.perform(value ? .connect : .disconnect)
        return .result()
    }
}

enum SystemSurfaceActions {
    static func perform(_ action: SystemTunnelAction) async throws {
        let state = try await SystemTunnelControl.perform(action)
        let snapshot = SystemSurfaceStore.read().reconciled(with: state, at: Date())
        // An idempotent Connect must not renew an older network probe.
        SystemSurfaceStore.write(snapshot)
        SystemSurfaceReload.reload()
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
