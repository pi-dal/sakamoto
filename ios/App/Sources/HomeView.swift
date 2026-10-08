import SwiftUI
import SakamotoKit

// Home tab: phase banner, Connect/Disconnect, mode cycle, groups. The phase
// word is rendered verbatim from the Go bridge — no iOS-side folding.

struct HomeView: View {
    @ObservedObject var model: HomeModel
    @State private var showRoutingModes = false
    @State private var showDisconnectConfirm = false

    var body: some View {
        List {
            Section {
                phaseBanner
                Picker("Configuration", selection: Binding(
                    get: { model.store.selectedProfileID ?? "" },
                    set: { id in
                        do { try model.store.selectProfile(id) }
                        catch { model.store.reportProfileError(error) }
                    }
                )) {
                    Text("Choose configuration").tag("")
                    ForEach(model.store.profiles) { Text($0.name).tag($0.id) }
                }
                if let error = model.store.profileError { Text(error).font(.footnote).foregroundStyle(.primary) }
                if model.store.configState != .clean && model.store.canConnect && model.serviceState.running {
                    Button("Apply selected configuration") { Task { await model.store.regenerateAndApply(tunnel: model.tunnel) } }
                }
                if model.store.selectedProfile?.sourcesChanged == true {
                    Label("Sources changed — generate in Config",  systemImage: "exclamationmark.circle").font(.footnote).foregroundStyle(.secondary)
                }
                connectButton.sakamotoInspectTag("TunnelConnect")
                modeButton.sakamotoInspectTag("RoutingMode")
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
                    Text("Connection check")
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
                Button("Check connection") {
                    Task { await model.runProbe() }
                }
                .buttonStyle(.borderless)
                .disabled(model.phase != .tunRunning && model.phase != .reachable && model.phase != .unverified)
            } header: {
                Text("Network")

            }

            groupsSection
            Section {
                DisclosureGroup("Connection details") {
                    LabeledRow("Command channel", model.commandChannelActive ? "Connected" : "Unavailable")
                    LabeledRow("Service", model.serviceState.rawValue)
                }
            }
        }
        .navigationTitle("Home")
        .sakamotoRootPage()
        .confirmationDialog("Disconnect tunnel?", isPresented: $showDisconnectConfirm, titleVisibility: .visible) {
            Button("Disconnect", role: .destructive) { Task { await model.disconnect() } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("The active tunnel and its network routing will stop.")
        }
        .confirmationDialog("Routing mode", isPresented: $showRoutingModes, titleVisibility: .visible) {
            ForEach([RoutingMode.rule, .global, .direct], id: \.self) { mode in
                Button(mode.rawValue) { Task { await model.setRoutingMode(mode) } }
            }
            Button("Cancel", role: .cancel) {}
        }
        .task { await model.activate() }
    }

    private var phaseBanner: some View {
        HStack {
            Text(model.phase.rawValue)
                .font(.title3.bold())
                .foregroundStyle(phaseColor)
            Spacer()
        }
        .accessibilityLabel("VPN status: \(model.phase.rawValue)")
    }

    private var connectButton: some View {
        Button {
            Task {
                if model.phase == .disconnected || model.phase == .unavailable {
                    await model.connect()
                } else {
                    showDisconnectConfirm = true
                }
            }
        } label: {
            Text(model.phase == .disconnected || model.phase == .unavailable ? "Connect" : "Disconnect")
                .frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
        .disabled(model.busy || ((model.phase == .disconnected || model.phase == .unavailable) && !model.store.canConnect))
    }

    private var modeButton: some View {
        Button {
            showRoutingModes = true
        } label: {
            HStack {
                Text("Mode")
                Spacer()
                Text(model.routingMode?.rawValue ?? "—")
                    .foregroundStyle(.secondary)
            }
        }
        .buttonStyle(.plain)
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
                    .foregroundStyle(model.commandChannelActive ? Color.primary : Color.secondary)
            }
        } header: {
            Text("Control plane")
        }
    }

    @ViewBuilder
    private var groupsSection: some View {
        Section {
            if model.groups.isEmpty {
                Text(model.store.content.isEmpty ? "Import a VPN configuration in Config to get started." : "Connect to view nodes and groups")
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
            Text("Nodes & groups")
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
                .foregroundStyle(node.status == .reachable ? Color.primary : Color.secondary)
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
                .sakamotoGlassButton()
            }
            Button("Test") {
                Task { await model.testNode(node) }
            }
            .font(.footnote)
            .sakamotoGlassButton()
        }
    }

    private var phaseColor: Color {
        switch model.phase {
        case .reachable: return .primary
        case .tunRunning: return .secondary
        case .unverified: return .secondary
        case .conflict, .unavailable: return .primary
        case .starting, .stopping: return .secondary
        case .disconnected: return .secondary
        }
    }

    private var probeColor: Color {
        switch model.probe.state {
        case ProbeState.reachable.rawValue: return Color.primary
        case ProbeState.checking.rawValue: return Color.secondary
        case ProbeState.unverified.rawValue: return Color.secondary
        default: return Color.secondary
        }
    }

    private func noticeColor(_ kind: NoticeKind) -> Color {
        switch kind {
        case .error: return .primary
        case .warning: return .secondary
        case .success: return .primary
        case .progress, .info: return .secondary
        }
    }
}
