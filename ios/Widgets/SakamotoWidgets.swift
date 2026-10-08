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
    private var snapshot: SystemSurfaceSnapshot { entry.snapshot }
    private var busy: Bool { snapshot.serviceState == .starting || snapshot.serviceState == .stopping }

    var body: some View {
        if #available(iOS 17.0, *) {
            content.containerBackground(.fill.tertiary, for: .widget)
        } else { content.padding().background(Color(uiColor: .secondarySystemBackground)) }
    }

    @ViewBuilder private var content: some View {
        if family == .systemSmall {
            compactContent
        } else {
            expandedContent
        }
    }

    /// A small widget has about 120pt of usable width. Keep the name clear
    /// of the symbol and 44pt control rather than letting the header wrap.
    private var compactContent: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("sakamoto")
                .font(.headline)
                .lineLimit(1)
                .minimumScaleFactor(0.85)
                .frame(maxWidth: .infinity, alignment: .leading)
            HStack {
                Image(systemName: "network")
                    .font(.title2)
                    .foregroundStyle(.secondary)
                    .accessibilityHidden(true)
                Spacer(minLength: 8)
                action
            }
            Text(statusTitle)
                .font(.subheadline.weight(.semibold))
                .lineLimit(2)
                .minimumScaleFactor(0.85)
        }
        .widgetURL(URL(string: "sakamoto://home"))
    }

    private var expandedContent: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label("sakamoto", systemImage: "network").font(.headline)
                Spacer()
                action
            }
            Text(statusTitle).font(.title3.weight(.semibold))
            if family != .systemSmall {
                HStack(alignment: .firstTextBaseline) {
                    Text(snapshot.selectedNode.isEmpty ? "No selected node" : snapshot.selectedNode).lineLimit(1)
                    Spacer()
                    latency
                }
                .font(.subheadline)
                if !snapshot.routingMode.isEmpty { Text(snapshot.routingMode).font(.caption).foregroundStyle(.secondary) }
            }
            if family == .systemLarge {
                Spacer(minLength: 0)
                Text(snapshot.phase == .reachable ? "Last network check passed" : "Open Home to check the network")
                    .font(.subheadline).foregroundStyle(.secondary)
                Link("Open sakamoto", destination: URL(string: "sakamoto://home")!)
                    .font(.body)
            }
        }
        .widgetURL(URL(string: "sakamoto://home"))
    }

    private var statusTitle: String {
        switch snapshot.phase {
        case .tunRunning: return "VPN running"
        case .reachable: return "Network verified"
        case .unverified: return "Network unverified"
        default: return snapshot.phase.rawValue
        }
    }

    @ViewBuilder private var latency: some View {
        if let value = snapshot.latencyMS, value > 0 {
            VStack(alignment: .trailing, spacing: 2) {
                Text("\(value) ms").monospacedDigit()
                if !snapshot.latencyIsFresh(at: entry.date) { Text("Last test").font(.caption2) }
            }
            .foregroundStyle(snapshot.latencyIsFresh(at: entry.date) ? .primary : .secondary)
        } else { Text("Not tested").foregroundStyle(.secondary) }
    }

    @ViewBuilder private var action: some View {
        if #available(iOS 17.0, *) {
            Button(intent: ToggleTunnelIntent()) {
                Image(systemName: "power").frame(minWidth: 44, minHeight: 44)
            }
            .buttonStyle(.bordered)
            .tint(.primary)
            .disabled(busy)
            .accessibilityLabel(snapshot.serviceState == .running ? "Disconnect VPN" : "Connect VPN")
        } else {
            Link(destination: URL(string: "sakamoto://home")!) {
                Image(systemName: "power").frame(minWidth: 44, minHeight: 44)
            }
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
    func currentValue() async throws -> Bool { try await SystemTunnelControl.status() == .running }
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
