import SwiftUI
import SakamotoKit

// Tailscale tool page: live status of the BUILT-IN Tailscale endpoint
// (inside the tunnel process) plus the endpoint provisioning flow.
//
// Placement: reached from Settings → Built-in Tailscale; the five main
// tabs (Home/Config/Data/Settings/About) stay untouched.
//
// Semantics verified against sing-box v1.14.2 (docs/configuration/endpoint/
// tailscale.md): the integration is an ENDPOINT — inbound+outbound behavior
// inside the tunnel process — never presented here as a proxy outbound or
// a routing source for the generator's groups. Supported actions are
// status/auth-URL/exit-node/ping/logout; everything else is listed as
// unsupported with its reason. Login with an auth key is configuration
// (Keychain → start-time injection), not a UI login call.

struct TailscaleView: View {
    let store: ConfigStore
    let tunnel: TunnelControlling
    let commanding: LibboxCoreCommanding?

    @StateObject private var controller: TailscaleController
    @State private var authKeyDraft = ""
    @State private var keyStoredTick = 0
    // Provisioning drafts (applied to the saved config on commit).
    @State private var hostnameDraft = ""
    @State private var exitNodeDraft = ""
    @State private var acceptRoutesDraft = false
    @State private var allowLANDraft = false
    @State private var provisionNotice: Notice?
    @State private var draftsLoaded = false

    init(store: ConfigStore, tunnel: TunnelControlling, commanding: LibboxCoreCommanding?) {
        self.store = store
        self.tunnel = tunnel
        self.commanding = commanding
        _controller = StateObject(wrappedValue: TailscaleController(commanding: commanding))
    }

    var body: some View {
        List {
            endpointConfigSection
            loginSection
            endpointSections
            unsupportedSection
            authKeySection
        }
        .navigationTitle("Tailscale")
        .listStyle(.insetGrouped)
        .onAppear {
            controller.subscribe()
            loadDrafts()
        }
        .onDisappear { controller.cancel() }
    }

    // MARK: Endpoint provisioning (safe, validated, saved-config merge)

    @ViewBuilder
    private var endpointConfigSection: some View {
        Section {
            let enabled = TailscaleEndpointProvisioning.isEnabled(in: store.content)
            Toggle("Built-in Tailscale endpoint", isOn: Binding(
                get: { enabled },
                set: { setEnabled($0) }
            ))
            if enabled {
                TextField("Hostname (optional; device name by default)", text: $hostnameDraft)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                Toggle("Accept tailnet subnet routes", isOn: $acceptRoutesDraft)
                TextField("Exit node (optional; name or IP)", text: $exitNodeDraft)
                    .autocorrectionDisabled()
                    .textInputAutocapitalization(.never)
                Toggle("Route LAN access via exit node", isOn: $allowLANDraft)
                Button("Save endpoint settings") {
                    applyOptions()
                }
                Text("Runtime exit-node changes on this page override the saved exit node until the next reconnect.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            if let notice = provisionNotice {
                Text(notice.text)
                    .font(.footnote)
                    .foregroundStyle(notice.kind == .error ? Color.red : Color.secondary)
            }
        } header: {
            Text("Endpoint configuration")
        } footer: {
            Text("Writes a minimal, legal tailscale endpoint (endpoints[].type == \"tailscale\") into the saved config. It is an endpoint inside the tunnel process — not a proxy outbound; Home's groups never list tailnet peers. Saving marks the config modified: run Regenerate + Reconnect in Config/Settings to apply.")
        }
    }

    private func loadDrafts() {
        guard !draftsLoaded else { return }
        draftsLoaded = true
        if let options = TailscaleEndpointProvisioning.describe(in: store.content) {
            hostnameDraft = options.hostname ?? ""
            exitNodeDraft = options.exitNode ?? ""
            acceptRoutesDraft = options.acceptRoutes
            allowLANDraft = options.exitNodeAllowLANAccess
        }
    }

    private func setEnabled(_ enabled: Bool) {
        do {
            let next = enabled
                ? try TailscaleEndpointProvisioning.enable(options: currentOptions(), in: store.content)
                : try TailscaleEndpointProvisioning.disable(in: store.content)
            store.save(next)
            provisionNotice = Notice(
                kind: .info,
                text: enabled
                    ? "Endpoint added — regenerate + reconnect to apply"
                    : "Endpoint removed — regenerate + reconnect to apply"
            )
        } catch {
            provisionNotice = Notice(kind: .error, text: error.localizedDescription)
        }
    }

    private func applyOptions() {
        do {
            let next = try TailscaleEndpointProvisioning.enable(options: currentOptions(), in: store.content)
            store.save(next)
            provisionNotice = Notice(kind: .info, text: "Endpoint settings saved — regenerate + reconnect to apply")
        } catch {
            provisionNotice = Notice(kind: .error, text: error.localizedDescription)
        }
    }

    private func currentOptions() -> TailscaleEndpointOptions {
        var options = TailscaleEndpointProvisioning.describe(in: store.content) ?? TailscaleEndpointOptions()
        options.hostname = hostnameDraft.isEmpty ? nil : hostnameDraft
        options.acceptRoutes = acceptRoutesDraft
        options.exitNode = exitNodeDraft.isEmpty ? nil : exitNodeDraft
        options.exitNodeAllowLANAccess = allowLANDraft
        return options
    }

    // MARK: Live status (real stream; unavailable states are explicit)

    @ViewBuilder
    private var loginSection: some View {
        Section {
            if !controller.channelActive {
                Text(controller.lastError ?? "No tailnet status. Connect the tunnel; the built-in endpoint runs inside the tunnel process, and this page follows the command channel.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            } else if controller.endpoints.isEmpty {
                Text("Command channel connected; no tailscale endpoint is reporting. Enable the endpoint above, then regenerate + reconnect.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            if let error = controller.lastError, controller.channelActive {
                Text(error)
                    .font(.footnote)
                    .foregroundStyle(.red)
            }
        } header: {
            Text("Built-in Tailscale")
        }
    }

    @ViewBuilder
    private var endpointSections: some View {
        ForEach(controller.endpoints) { endpoint in
            Section {
                statusRow("State", value: stateText(endpoint))
                if !endpoint.networkName.isEmpty {
                    statusRow("Tailnet", value: endpoint.networkName)
                }
                // MagicDNS suffix is a tailnet fact from the status stream;
                // shown as information — it is not a DNS setting here.
                if !endpoint.magicDNSSuffix.isEmpty {
                    statusRow("MagicDNS suffix", value: endpoint.magicDNSSuffix)
                }
                if let selfPeer = endpoint.selfPeer {
                    statusRow("This device", value: selfPeer.displayName)
                }
                if endpoint.backendState.needsLoginFlow, !endpoint.authURL.isEmpty {
                    if let url = URL(string: endpoint.authURL) {
                        Link("Open login URL", destination: url)
                    }
                    Text(endpoint.authURL)
                        .font(.footnote.monospaced())
                        .textSelection(.enabled)
                }
                exitNodeRows(endpoint)
                logoutButton(endpoint)

                ForEach(endpoint.peers) { peer in
                    peerRow(endpoint, peer)
                }
            } header: {
                Text(endpoint.endpointTag)
            } footer: {
                Text("Peers, exit node and login state are live tailnet facts from the status stream. Exit-node selection is an intent, not a reachability claim — ping a peer to measure.")
            }
        }
    }

    private func stateText(_ endpoint: TailscaleEndpointSummary) -> String {
        switch endpoint.backendState {
        case .stopped: return "Stopped"
        case .starting: return "Starting"
        case .needsLogin: return "Needs login"
        case .needsMachineAuth: return "Needs machine auth"
        case .running: return "Running"
        case .unrecognized(let raw): return "Unrecognized (\(raw))"
        }
    }

    @ViewBuilder
    private func exitNodeRows(_ endpoint: TailscaleEndpointSummary) -> some View {
        if let exitNode = endpoint.exitNodePeer {
            HStack {
                Text("Exit node")
                Spacer()
                Text(exitNode.displayName)
                    .foregroundStyle(.secondary)
                Button("Clear") {
                    Task { await controller.setExitNode(endpointTag: endpoint.endpointTag, peer: nil) }
                }
                .font(.footnote)
                .sakamotoGlassButton()
            }
        }
        Menu("Set exit node") {
            if endpoint.exitNodeCandidates.isEmpty {
                Text("No candidates on this tailnet")
            }
            ForEach(endpoint.exitNodeCandidates) { peer in
                Button(peer.displayName) {
                    Task { await controller.setExitNode(endpointTag: endpoint.endpointTag, peer: peer) }
                }
            }
        }
    }

    private func logoutButton(_ endpoint: TailscaleEndpointSummary) -> some View {
        Button("Log out of tailnet", role: .destructive) {
            Task { await controller.logout(endpointTag: endpoint.endpointTag) }
        }
    }

    private func peerRow(_ endpoint: TailscaleEndpointSummary, _ peer: TailscalePeerSummary) -> some View {
        HStack {
            VStack(alignment: .leading) {
                Text(peer.displayName)
                Text(peer.online ? "online" : "last seen \(Date(timeIntervalSince1970: TimeInterval(peer.lastSeenUnixSeconds)), style: .relative) ago")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if peer.exitNode {
                Text("exit")
                    .font(.caption2)
                    .padding(.horizontal, 6)
                    .background(Color.accentColor.opacity(0.2), in: Capsule())
            }
            Button("Ping") {
                Task { await controller.ping(endpointTag: endpoint.endpointTag, peer: peer) }
            }
            .font(.footnote)
            .sakamotoGlassButton()
            .disabled(!controller.channelActive)
            if let result = controller.pingResults[peer.stableID] {
                Text(result.error ?? (result.isDirect == true ? "\(result.latencyMS ?? 0) ms direct" : "\(result.latencyMS ?? 0) ms"))
                    .font(.footnote.monospacedDigit())
                    .foregroundStyle(result.error == nil ? Color.green : Color.red)
            }
        }
    }

    private func statusRow(_ label: String, value: String) -> some View {
        HStack {
            Text(label)
            Spacer()
            Text(value)
                .foregroundStyle(.secondary)
        }
    }

    private var unsupportedSection: some View {
        Section {
            ForEach(TailscaleCapabilities.unsupported) { capability in
                VStack(alignment: .leading) {
                    Text("Unsupported: \(capability.capability.rawValue)")
                        .font(.footnote.weight(.semibold))
                    Text(capability.reason)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        } header: {
            Text("Capabilities")
        } footer: {
            Text("Supported actions: \(TailscaleCapabilities.supportedActions.map { $0.rawValue }.joined(separator: ", ")). iOS has no tailscale CLI, Taildrop, SSH, or serve surface in libbox — those entries are facts, not omissions.")
        }
    }

    private var authKeySection: some View {
        Section {
            SecureField("tsauth-… auth key", text: $authKeyDraft)
            Button("Store auth key in Keychain") {
                let keyStore = TailscaleKeychainStore()
                do {
                    try keyStore.storeAuthKey(authKeyDraft)
                    authKeyDraft = ""
                    keyStoredTick += 1
                } catch {
                    provisionNotice = Notice(kind: .error, text: "keychain: \(error.localizedDescription)")
                }
            }
            .disabled(authKeyDraft.isEmpty)
            if keyStoredTick > 0 {
                Text("Stored. It is injected into the endpoint config at start time; it never enters the repository, logs, or UserDefaults.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        } header: {
            Text("Auth key (optional)")
        } footer: {
            Text("Without a key the endpoint uses the auth-URL login flow above. A stored key without an enabled endpoint is a blocking error at connect — enable the endpoint first.")
        }
    }
}
