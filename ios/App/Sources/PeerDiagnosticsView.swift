import SwiftUI
import SakamotoKit

struct PeerDiagnosticsView: View {
    @ObservedObject var controller: TailscaleController
    let endpoint: TailscaleEndpointSummary
    let peer: TailscalePeerSummary
    @Environment(\.dismiss) private var dismiss
    @State private var address: String
    @State private var portDraft = "22"
    @State private var pinging = false
    @State private var verifySSH = true

    init(controller: TailscaleController, endpoint: TailscaleEndpointSummary, peer: TailscalePeerSummary) {
        self.controller = controller
        self.endpoint = endpoint
        self.peer = peer
        _address = State(initialValue: peer.tailscaleIPs.first ?? "")
    }

    private var key: String { endpoint.endpointTag + "/" + peer.stableID }
    private var checking: Bool { controller.tcpChecking.contains(key) }
    private var port: UInt16? { UInt16(portDraft).flatMap { $0 > 0 ? $0 : nil } }
    private var peerAvailable: Bool {
        controller.channelActive && controller.endpoints.contains {
            $0.endpointTag == endpoint.endpointTag && $0.peers.contains { $0.stableID == peer.stableID && $0.tailscaleIPs.contains(address) }
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Target") {
                    Text(peer.displayName)
                    Picker("Tailscale IP", selection: $address) {
                        ForEach(peer.tailscaleIPs, id: \.self) { Text($0).tag($0) }
                    }
                    LabeledContent("TCP port") {
                        TextField("22", text: $portDraft)
                            .keyboardType(.numberPad)
                            .multilineTextAlignment(.trailing)
                            .accessibilityLabel("TCP port")
                    }
                    Toggle("Check SSH greeting", isOn: $verifySSH)
                    if port == nil { Text("Use a port from 1 to 65535.").font(.footnote) }
                    if !peerAvailable { Text("Connect the VPN and wait for this peer to appear.").font(.footnote) }
                }
                Section {
                    Button(pinging ? "Pinging…" : "Ping peer") {
                        pinging = true
                        Task {
                            await controller.ping(endpointTag: endpoint.endpointTag, peer: peer, address: address)
                            pinging = false
                        }
                    }.disabled(!peerAvailable || pinging)
                    if let ping = controller.pingResult(endpointTag: endpoint.endpointTag, peerID: peer.stableID) {
                        Text(ping.error ?? "\(ping.latencyMS ?? 0) ms · \(ping.pathDescription)")
                        Text("\(ping.targetIP ?? "unknown target") · \(ping.measuredAt.formatted(date: .omitted, time: .standard))").font(.footnote).foregroundStyle(.secondary)
                    }
                } header: { Text("Tailnet path") } footer: {
                    Text("Measures the embedded Tailscale endpoint's peer path. Ping success does not confirm that the TCP service is reachable.")
                }
                Section {
                    Button(checking ? "Checking TCP…" : "Check TCP port") {
                        if let port { controller.checkTCP(endpointTag: endpoint.endpointTag, peer: peer, address: address, port: port, verifySSH: verifySSH) }
                    }.disabled(!peerAvailable || port == nil || checking)
                    if checking {
                        Button("Cancel check") { cancel() }
                    }
                    if let result = controller.tcpResults[key], peerAvailable {
                        Text(result.detail)
                        Text("\(result.elapsedMS) ms · \(result.measuredAt.formatted())")
                            .font(.footnote).foregroundStyle(.secondary)
                        if let error = result.nativeError { Text(error).font(.footnote).textSelection(.enabled) }
                        ShareLink("Share diagnostic report", item: report(result))
                        Text("The report includes the target IP and peer name. Review it before sharing.")
                            .font(.footnote).foregroundStyle(.secondary)
                    }
                } header: { Text("App TCP path") } footer: {
                    Text("Opens one TCP connection through the app's system route, with an 8-second total timeout, then closes it. SSH greeting verifies a service response without signing in. Turn it off for other protocols. Results are measurements from the displayed time.")
                }
            }
            .navigationTitle("Peer diagnostics")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        }
        .onChange(of: address) { _ in cancel() }
        .onChange(of: portDraft) { _ in cancel() }
        .onChange(of: verifySSH) { _ in cancel() }
        .onChange(of: peerAvailable) { available in if !available { cancel() } }
        .onDisappear { cancel() }
    }

    private func cancel() { controller.cancelTCP(endpointTag: endpoint.endpointTag, peerID: peer.stableID) }

    private func report(_ result: TCPConnectivityProbe.Outcome) -> String {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "unknown"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "unknown"
        var text = "sakamoto iOS \(version) (\(build))\nOS: \(ProcessInfo.processInfo.operatingSystemVersionString)\nEndpoint: \(endpoint.endpointTag)\nPeer: \(peer.displayName)\n" + result.report
        if let ping = controller.pingResult(endpointTag: endpoint.endpointTag, peerID: peer.stableID) {
            text += "\nSeparate endpoint ping to \(ping.targetIP ?? "unknown target") at \(ISO8601DateFormatter().string(from: ping.measuredAt)): " + (ping.error ?? "\(ping.latencyMS ?? 0) ms · \(ping.pathDescription)")
        }
        return text
    }
}
