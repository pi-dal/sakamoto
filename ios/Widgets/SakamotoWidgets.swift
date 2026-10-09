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
        let state = (try? await SystemTunnelControl.status()) ?? .unavailable
        return VPNEntry(date: now, snapshot: SystemSurfaceStore.read().reconciled(with: state, at: now))
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
            let state = try await SystemTunnelControl.status()
            return state == .running || state == .starting
        } catch {
            // A transient NE IPC error must not permanently disable the control.
            let snapshot = SystemSurfaceStore.read()
            guard (0...30).contains(Date().timeIntervalSince(snapshot.updatedAt)) else { return false }
            return snapshot.serviceState == .running || snapshot.serviceState == .starting
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
