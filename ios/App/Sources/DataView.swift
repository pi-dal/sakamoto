import SwiftUI
import SakamotoKit

// Data tab: live traffic, connections and core logs — the iOS counterpart
// of the macOS TUI's Data page (internal/tui renderData). Every number on
// this page comes from a real Libbox command stream (CommandStatus /
// CommandConnections / CommandLog) through the shared command channel:
//
//   - channel down  → an explicit "unavailable" row (the reason included),
//     never zeros, never mock rows;
//   - trafficAvailable=false (core cannot measure yet) → same rule;
//   - connection rows fold from the stream exactly like the TUI's m.conns
//     map: NEW inserts, UPDATE adds deltas, CLOSED marks without deleting;
//   - Close connection is a real RPC; failure lands in the notice line.

@MainActor
final class DataModel: ObservableObject {
    @Published private(set) var traffic: TrafficSnapshot?
    @Published private(set) var connections: [ConnectionRecord] = []
    @Published private(set) var logs: [String] = []
    @Published private(set) var channelActive = false
    @Published private(set) var channelError: String?
    @Published var selectedConnID: String?
    @Published private(set) var notice: Notice?
    @Published private(set) var closeInFlight = false

    private let commanding: CoreCommanding?
    private var tasks: [Task<Void, Never>] = []
    private var activated = false

    /// Number of rows shown before "… N more" (TUI caps the same way).
    static let visibleConnectionRows = 8
    static let visibleLogLines = 6

    init(commanding: CoreCommanding?) {
        self.commanding = commanding
    }

    var selectedConnection: ConnectionRecord? {
        guard let selectedConnID else { return nil }
        return connections.first { $0.id == selectedConnID }
    }

    var unavailableReason: String? {
        if commanding == nil {
            return "command channel unavailable — this build has no command bridge"
        }
        if !channelActive {
            return channelError ?? "command channel unavailable — connect the tunnel first"
        }
        if let traffic, !traffic.trafficAvailable {
            return "traffic measurement unavailable (the core has not reported traffic yet)"
        }
        return nil
    }

    /// One-time activation from the view. Idempotent; resubscribes the
    /// availability consumer so tunnel restarts re-arm the page.
    func activate() {
        guard !activated else { return }
        activated = true
        guard let commanding else { return }
        tasks.append(Task { [weak self] in
            let availability = commanding.availability()
            for await active in availability {
                guard let self else { return }
                self.channelActive = active
                self.channelError = active ? nil : commanding.lastChannelError
            }
        })
        tasks.append(Task { [weak self] in
            let stream = commanding.traffic()
            for await snapshot in stream {
                self?.traffic = snapshot
            }
        })
        tasks.append(Task { [weak self] in
            let stream = commanding.connections()
            for await records in stream {
                self?.connections = records
            }
        })
        tasks.append(Task { [weak self] in
            let stream = commanding.logs()
            for await lines in stream {
                self?.logs = lines
            }
        })
    }

    func deactivate() {
        // Keep consuming while the tab exists; streams are cheap and the
        // TUI keeps its counters across page switches too. Nothing to do.
    }

    func closeSelectedConnection() async {
        guard let commanding, let id = selectedConnID else { return }
        closeInFlight = true
        defer { closeInFlight = false }
        do {
            try await commanding.closeConnection(id: id)
            notice = Notice(kind: .success, text: "connection closed")
            selectedConnID = nil
        } catch {
            notice = Notice(kind: .error, text: "close: \(error.localizedDescription)")
        }
    }
}

struct DataView: View {
    @StateObject private var model: DataModel
    @State private var showConnectionDetails = false
    @State private var confirmClose = false
    @State private var connectionQuery = ""
    @State private var showAllConnections = false
    @State private var showAvailability = false

    init(commanding: CoreCommanding?) {
        _model = StateObject(wrappedValue: DataModel(commanding: commanding))
    }

    var body: some View {
        List {
            trafficSection
            connectionsSection
            logsSection
        }
        .navigationTitle("Data")
        .sakamotoRootPage()
        .searchable(text: $connectionQuery, prompt: "Website, rule or outbound")
        .alert("Data unavailable", isPresented: $showAvailability) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(model.unavailableReason ?? "Waiting for traffic measurements")
        }
        .task { model.activate() }
        .sheet(isPresented: $showConnectionDetails, onDismiss: { model.selectedConnID = nil }) {
            NavigationStack {
                List { detailSection }
                    .listStyle(.insetGrouped)
                    .navigationTitle("Connection details")
                    .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Done") { showConnectionDetails = false } } }
                    .confirmationDialog("Close this connection?", isPresented: $confirmClose, titleVisibility: .visible) {
                        Button("Close connection", role: .destructive) { Task { await model.closeSelectedConnection() } }
                        Button("Cancel", role: .cancel) {}
                    } message: { Text("The selected connection will be interrupted. Applications may reconnect automatically.") }
            }
            .presentationDetents([.medium, .large])
            .presentationDragIndicator(.visible)
        }
    }

    private var trafficSection: some View {
        Section {
            if model.unavailableReason != nil || model.traffic == nil {
                Button { showAvailability = true } label: {
                    HStack {
                        Text("Status").foregroundStyle(.primary)
                        Spacer()
                        Text("Unavailable").foregroundStyle(.secondary)
                    }
                }
            }
            trafficRow("Upload", model.unavailableReason == nil ? model.traffic.map { formatBytes($0.uplink) } : nil)
            trafficRow("Download", model.unavailableReason == nil ? model.traffic.map { formatBytes($0.downlink) } : nil)
            trafficRow("Uploaded", model.unavailableReason == nil ? model.traffic.map { formatBytes($0.uplinkTotal) } : nil)
            trafficRow("Downloaded", model.unavailableReason == nil ? model.traffic.map { formatBytes($0.downlinkTotal) } : nil)
            trafficRow("Connections", model.unavailableReason == nil ? model.traffic.map { "\($0.connectionsIn) in / \($0.connectionsOut) out" } : nil)
            if let notice = model.notice {
                Text(notice.text)
                    .font(.footnote)
                    .foregroundStyle(notice.kind == .error ? Color.primary : Color.secondary)
            }
        } header: {
            Text("Traffic")
        }
    }

    @ViewBuilder
    private var connectionsSection: some View {
        Section {
            if model.connections.isEmpty {
                Text("No recent connections")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            ForEach(visibleConnections) { record in
                Button {
                    model.selectedConnID = record.id
                    showConnectionDetails = true
                } label: {
                    HStack {
                        VStack(alignment: .leading) {
                            Text(record.displayName)
                                .lineLimit(1)
                            Text("Rule: \(record.rule.isEmpty ? "Not reported by core" : record.rule)")
                                .font(.caption).foregroundStyle(.secondary).lineLimit(2)
                            Text(record.closed
                                 ? "closed · \(record.outbound)"
                                 : "\(record.outbound) · ↑\(formatBytes(record.uplinkTotal)) ↓\(formatBytes(record.downlinkTotal))")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        if record.closed {
                            Text("closed")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                .buttonStyle(.plain)
            }
            if !showAllConnections && filteredConnections.count > DataModel.visibleConnectionRows {
                Button("Show all \(filteredConnections.count) connections") { showAllConnections = true }
            }
        } header: {
            Text("Recent connections")
        }
    }

    private func trafficRow(_ title: String, _ value: String?) -> some View {
        HStack {
            Text(title)
            Spacer()
            Text(value ?? "—")
                .monospacedDigit()
                .foregroundStyle(.secondary)
        }
    }

    private var filteredConnections: [ConnectionRecord] {
        model.connections.filter { connectionQuery.isEmpty || [$0.displayName, $0.destination, $0.rule, $0.outbound].contains { $0.localizedCaseInsensitiveContains(connectionQuery) } }
    }

    private var visibleConnections: [ConnectionRecord] {
        showAllConnections ? filteredConnections : Array(filteredConnections.prefix(DataModel.visibleConnectionRows))
    }

    @ViewBuilder
    private var detailSection: some View {
        Section {
            if let conn = model.selectedConnection {
                detailRow("Target", conn.destination)
                detailRow("Source", conn.source)
                detailRow("Outbound", conn.outbound)
                if !conn.chain.isEmpty {
                    detailRow("Chain", conn.chain.joined(separator: " → "))
                }
                detailRow("Rule", conn.rule.isEmpty ? "Not reported by core" : conn.rule)
                detailRow("Traffic", "↑\(formatBytes(conn.uplinkTotal)) ↓\(formatBytes(conn.downlinkTotal))")
                Button("Close connection", role: .destructive) { confirmClose = true }
                    .disabled(model.closeInFlight || conn.closed)
                if let notice = model.notice { Text(notice.text).font(.footnote).foregroundStyle(.secondary) }
            }
        } header: {
            Text("Connection details")
        }
    }

    private func detailRow(_ label: String, _ value: String) -> some View {
        HStack(alignment: .top) {
            Text(label)
            Spacer()
            Text(value)
                .font(.footnote.monospaced())
                .multilineTextAlignment(.trailing)
                .foregroundStyle(.secondary)
                .textSelection(.enabled)
        }
    }

    private var logsSection: some View {
        Section {
            if model.logs.isEmpty {
                Text("No core logs")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            ForEach(Array(visibleLogs.enumerated()), id: \.offset) { _, line in
                Text(line)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
        } header: {
            Text("Core logs")
        }
    }

    private var visibleLogs: [String] {
        Array(model.logs.suffix(DataModel.visibleLogLines))
    }
}
