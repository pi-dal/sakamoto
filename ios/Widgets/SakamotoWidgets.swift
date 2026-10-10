import SwiftUI
import WidgetKit
import SakamotoKit
import SakamotoNE

private struct VPNEntry: TimelineEntry {
    let date: Date
    let snapshot: SystemSurfaceSnapshot
}

private struct VPNProvider: TimelineProvider {
    func placeholder(in context: Context) -> VPNEntry { VPNEntry(date: Date(), snapshot: SystemSurfaceSnapshot()) }
    func getSnapshot(in context: Context, completion: @escaping (VPNEntry) -> Void) {
        Task { completion(await entry()) }
    }
    func getTimeline(in context: Context, completion: @escaping (Timeline<VPNEntry>) -> Void) {
        Task {
            let current = await entry()
            completion(Timeline(entries: [current], policy: .after(Date().addingTimeInterval(60))))
        }
    }
    private func entry() async -> VPNEntry {
        let now = Date()
        let snapshot = (try? await SystemTunnelControl.surfaceSnapshot()) ?? SystemSurfaceStore.read()
        return VPNEntry(date: now, snapshot: snapshot)
    }
}

private struct VPNWidgetView: View {
    let entry: VPNEntry
    @Environment(\.widgetFamily) private var family

    var body: some View {
        if #available(iOS 17.0, *) {
            VPNWidgetContent(snapshot: entry.snapshot, date: entry.date, family: family)
                .containerBackground(.fill.tertiary, for: .widget)
        } else {
            VPNWidgetContent(snapshot: entry.snapshot, date: entry.date, family: family)
                .padding()
                .background(Color(uiColor: .secondarySystemBackground))
        }
    }
}

struct VPNWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "com.pidal.sakamoto.vpn-widget", provider: VPNProvider()) { entry in VPNWidgetView(entry: entry) }
            .configurationDisplayName("VPN connection")
            .description("View the VPN state and connect or disconnect.")
            .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}

@available(iOS 18.0, *)
private struct VPNControlProvider: ControlValueProvider {
    var previewValue: Bool { false }
    func currentValue() async throws -> Bool {
        do {
            return try await SystemTunnelControl.controlEnabled()
        } catch {
            TunnelDiagnostics.recordControlFailure(error, stage: "Control status read")
            // A transient NE IPC error must not permanently disable the control.
            return SystemSurfaceStore.read().controlDisplayEnabled(at: Date())
        }
    }
}

@available(iOS 18.0, *)
struct VPNControl: ControlWidget {
    var body: some ControlWidgetConfiguration {
        StaticControlConfiguration(kind: "com.pidal.sakamoto.vpn-control", provider: VPNControlProvider()) { enabled in
            ControlWidgetToggle(isOn: enabled, action: SetTunnelEnabledIntent()) {
                Label("sakamoto VPN", systemImage: "network")
            }
        }
        .displayName("VPN connection")
        .description("Connect or disconnect your saved sakamoto VPN.")
    }
}

@main
struct SakamotoWidgets: WidgetBundle {
    var body: some Widget {
        VPNWidget()
        if #available(iOS 18.0, *) { VPNControl() }
    }
}
