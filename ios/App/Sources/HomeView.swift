import SwiftUI
import SakamotoKit

// Home tab: tunnel switch, routing mode, and node groups. The phase is
// reported verbatim from the Go bridge in Connection details.

struct HomeView: View {
    @ObservedObject var model: HomeModel
    @State private var showRoutingModes = false
    @State private var showDisconnectConfirm = false

    var body: some View {
        List {
            Section {
                ConfigurationMenu(store: model.store)
                if let error = model.store.profileError { Text(error).font(.footnote).foregroundStyle(.primary) }
                if model.store.configState != .clean && model.store.canConnect && model.serviceState.running {
                    Button("Apply selected configuration") { Task { await model.store.regenerateAndApply(tunnel: model.tunnel) } }
                }
                if model.store.selectedProfile?.sourcesChanged == true {
                    Label("Sources changed — generate in Config",  systemImage: "exclamationmark.circle").font(.footnote).foregroundStyle(.secondary)
                }
                modeButton.sakamotoInspectTag("RoutingMode")
                if let notice = model.notice {
                    Text(notice.text)
                        .font(.footnote)
                        .foregroundStyle(noticeColor(notice.kind))
                }
            } header: {
                HStack {
                    Text("Tunnel")
                    Spacer()
                    tunnelSwitch.sakamotoInspectTag("TunnelConnect")
                }
                .textCase(nil)
            }

            groupsSection
            Section {
                DisclosureGroup("Connection details") {
                    LabeledRow("VPN status", model.phase.rawValue)
                    LabeledRow("Command channel", model.commandChannelActive ? "Connected" : "Unavailable")
                    LabeledRow("Service", model.serviceState.rawValue)
                    Button {
                        Task { await model.runProbe() }
                    } label: {
                        HStack {
                            Text(model.probe.state == ProbeState.checking.rawValue ? "Checking connection…" : "Check connection")
                            Spacer()
                            if model.probe.state == ProbeState.checking.rawValue {
                                ProgressView()
                            } else if model.probe.state != ProbeState.idle.rawValue {
                                Text(model.probe.state).foregroundStyle(.secondary)
                            }
                        }
                    }
                    .buttonStyle(.borderless)
                    .disabled(!model.serviceState.running || model.probe.state == ProbeState.checking.rawValue)
                    if !model.probe.path.isEmpty {
                        Text("Network reachable: \(model.probe.path)").font(.footnote)
                    }
                    if !model.probe.error.isEmpty {
                        Text(model.probe.error).font(.footnote).foregroundStyle(.secondary)
                    }
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

    private var tunnelIsOn: Bool {
        model.serviceState.running || model.serviceState == .starting || model.serviceState == .stopping
    }

    private var tunnelSwitch: some View {
        Toggle("Tunnel", isOn: Binding(
            get: { tunnelIsOn },
            set: { enabled in
                if enabled {
                    Task { await model.connect() }
                } else {
                    showDisconnectConfirm = true
                }
            }
        ))
        .labelsHidden()
        .toggleStyle(SakamotoSwitchStyle())
        .frame(minHeight: 44)
        .accessibilityValue("\(tunnelIsOn ? "On" : "Off"), \(model.phase.rawValue)")
        .accessibilityHint(tunnelIsOn ? "Disconnect tunnel" : "Connect tunnel")
        .disabled(model.busy || model.serviceState == .starting || model.serviceState == .stopping || (!tunnelIsOn && !model.store.canConnect))
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

    @ViewBuilder
    private var groupsSection: some View {
        Section {
            Button(model.testingAll ? "Testing nodes…" : "Test all nodes") {
                Task { await model.testAllNodes() }
            }.disabled(!model.commandChannelActive || model.testingAll || model.groups.isEmpty)
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
        VStack(alignment: .leading, spacing: 8) {
            Text(node.tag).lineLimit(2)
            HStack(spacing: 12) {
                VStack(alignment: .leading) {
                    Text(node.status.rawValue).font(.footnote).foregroundStyle(.secondary)
                    if node.status == .reachable { Text("\(node.latencyMS) ms").font(.footnote.monospacedDigit()) }
                }
                Spacer(minLength: 8)
                if group.selectable {
                    Button(node.selected ? "Picked" : "Pick") {
                        Task { await model.selectNode(groupTag: group.tag, node: node) }
                    }.font(.footnote).lineLimit(1).fixedSize(horizontal: true, vertical: false)
                        .sakamotoGlassButton().disabled(!model.commandChannelActive)
                }
                Button("Test") { Task { await model.testNode(node) } }
                    .font(.footnote).lineLimit(1).fixedSize(horizontal: true, vertical: false)
                    .sakamotoGlassButton().disabled(!model.commandChannelActive || model.testingAll || node.status == .testing)
            }
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
