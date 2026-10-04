import SwiftUI
import SakamotoKit

// Home tab: phase banner, Connect/Disconnect, mode cycle, groups. The phase
// word is rendered verbatim from the Go bridge — no iOS-side folding.

struct HomeView: View {
    @ObservedObject var model: HomeModel

    var body: some View {
        List {
            Section {
                phaseBanner
                connectButton
                modeButton
                if let notice = model.notice {
                    Text(notice.text)
                        .font(.footnote)
                        .foregroundStyle(noticeColor(notice.kind))
                }
            } header: {
                Text("Tunnel")
            }

            Section {
                HStack {
                    Text("Probe")
                    Spacer()
                    Text(model.probe.state)
                        .foregroundStyle(probeColor)
                }
                if !model.probe.path.isEmpty {
                    Text("Network reachable: \(model.probe.path)")
                        .font(.footnote)
                }
                if !model.probe.error.isEmpty {
                    Text(model.probe.error)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                Button("Run probe") {
                    Task { await model.runProbe() }
                }
                .disabled(model.phase != .tunRunning && model.phase != .reachable && model.phase != .unverified)
            } header: {
                Text("Network")
            } footer: {
                Text("TUN running is not network reachable; the probe verifies. One failed probe stays Unverified — a retry state, not a verdict.")
            }

            commandChannelSection

            groupsSection
        }
        .navigationTitle("Home")
        .task { await model.activate() }
    }

    private var phaseBanner: some View {
        HStack {
            Text(model.phase.rawValue)
                .font(.title3.bold())
                .foregroundStyle(phaseColor)
            Spacer()
        }
    }

    private var connectButton: some View {
        Button {
            Task {
                if model.phase == .disconnected || model.phase == .unavailable {
                    await model.connect()
                } else {
                    await model.disconnect()
                }
            }
        } label: {
            Text(model.phase == .disconnected || model.phase == .unavailable ? "Connect" : "Disconnect")
                .frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
        .disabled(model.busy)
    }

    private var modeButton: some View {
        Button {
            Task { await model.cycleRoutingMode() }
        } label: {
            HStack {
                Text("Mode")
                Spacer()
                Text(model.routingMode?.rawValue ?? "—")
                    .foregroundStyle(.secondary)
            }
        }
        .disabled(!model.commandChannelActive)
    }

    /// The command channel is a fact, not a guess: it starts with the
    /// tunnel and says so while it is down (docs/tui.md: an unavailable
    /// control action is surfaced, never a silent no-op).
    @ViewBuilder
    private var commandChannelSection: some View {
        Section {
            HStack {
                Text("Command channel")
                Spacer()
                Text(model.commandChannelActive ? "Connected" : "Unavailable")
                    .foregroundStyle(model.commandChannelActive ? Color.green : Color.secondary)
            }
            if !model.commandChannelActive {
                Text("The libbox command server runs inside the tunnel process. Connect the tunnel; the channel follows it with retries — mode, selection, tests and the Data page wait for it.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        } header: {
            Text("Control plane")
        }
    }

    @ViewBuilder
    private var groupsSection: some View {
        Section {
            if model.groups.isEmpty {
                Text("No group stream. Connect first; the stream arrives through the libbox command channel.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            ForEach(model.groups, id: \.tag) { group in
                DisclosureGroup {
                    ForEach(group.items, id: \.tag) { node in
                        nodeRow(group: group, node: node)
                    }
                } label: {
                    HStack {
                        Text(group.tag)
                        Spacer()
                        // `selected` is the filled dot: an intent, never a
                        // connectivity claim.
                        if let selected = group.selectedTag {
                            Circle()
                                .fill(Color.accentColor)
                                .frame(width: 8, height: 8)
                            Text(selected)
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        } header: {
            Text("Groups")
        }
    }

    private func nodeRow(group: GroupSnapshot, node: NodeSnapshot) -> some View {
        HStack {
            Text(node.tag)
            Spacer()
            // Latency is the measurement; "Reachable" here is this node's
            // URL-test result, independent of selection.
            Text(node.status.rawValue)
                .font(.footnote)
                .foregroundStyle(node.status == .reachable ? Color.green : Color.secondary)
            if node.status == .reachable {
                Text("\(node.latencyMS) ms")
                    .font(.footnote.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            if group.selectable {
                Button("Pick") {
                    Task { await model.selectNode(groupTag: group.tag, node: node) }
                }
                .font(.footnote)
                .buttonStyle(.bordered)
            }
            Button("Test") {
                Task { await model.testNode(node) }
            }
            .font(.footnote)
            .buttonStyle(.bordered)
        }
    }

    private var phaseColor: Color {
        switch model.phase {
        case .reachable: return .green
        case .tunRunning: return .yellow
        case .unverified: return .orange
        case .conflict, .unavailable: return .red
        case .starting, .stopping: return .secondary
        case .disconnected: return .secondary
        }
    }

    private var probeColor: Color {
        switch model.probe.state {
        case ProbeState.reachable.rawValue: return Color.green
        case ProbeState.checking.rawValue: return Color.secondary
        case ProbeState.unverified.rawValue: return Color.orange
        default: return Color.secondary
        }
    }

    private func noticeColor(_ kind: NoticeKind) -> Color {
        switch kind {
        case .error: return .red
        case .warning: return .orange
        case .success: return .green
        case .progress, .info: return .secondary
        }
    }
}
