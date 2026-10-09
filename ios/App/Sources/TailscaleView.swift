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
    @ObservedObject var store: ConfigStore
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
    @State private var pendingLogout: TailscaleEndpointSummary?
    @State private var pendingClearExit: TailscaleEndpointSummary?
    @State private var confirmDisable = false
    @State private var showAuthKey = false
    @State private var confirmDeleteKey = false

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
        .onChange(of: store.selectedProfileID) { _ in draftsLoaded = false; provisionNotice = nil; loadDrafts() }
        .confirmationDialog("Log out of tailnet?", isPresented: Binding(
            get: { pendingLogout != nil }, set: { if !$0 { pendingLogout = nil } }
        ), titleVisibility: .visible) {
            Button("Log out", role: .destructive) {
                if let endpoint = pendingLogout { Task { await controller.logout(endpointTag: endpoint.endpointTag) } }
                pendingLogout = nil
            }
            Button("Cancel", role: .cancel) { pendingLogout = nil }
        } message: { Text("This device will need to authenticate again to access the tailnet.") }
        .confirmationDialog("Clear exit node?", isPresented: Binding(
            get: { pendingClearExit != nil }, set: { if !$0 { pendingClearExit = nil } }
        ), titleVisibility: .visible) {
            Button("Clear exit node") {
                if let endpoint = pendingClearExit { Task { await controller.setExitNode(endpointTag: endpoint.endpointTag, peer: nil) } }
                pendingClearExit = nil
            }
            Button("Cancel", role: .cancel) { pendingClearExit = nil }
        } message: { Text("Traffic will stop using this exit node.") }
        .confirmationDialog("Remove Tailscale endpoint?", isPresented: $confirmDisable, titleVisibility: .visible) {
            Button("Remove endpoint", role: .destructive) { setEnabled(false) }
            Button("Cancel", role: .cancel) {}
        } message: { Text("The saved config will no longer include Tailscale. Apply & Reconnect is required; the stored key is not deleted.") }
        .confirmationDialog("Delete stored auth key?", isPresented: $confirmDeleteKey, titleVisibility: .visible) {
            Button("Delete key", role: .destructive) {
                do { try TailscaleKeychainStore().deleteAuthKey(); keyStoredTick = 0 }
                catch { provisionNotice = Notice(kind: .error, text: "Keychain deletion failed") }
            }
            Button("Cancel", role: .cancel) {}
        }
        .sheet(isPresented: $showAuthKey, onDismiss: { authKeyDraft = "" }) {
            NavigationStack {
                Form {
                    SecureField("tskey-auth-…", text: $authKeyDraft)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    if let notice = provisionNotice, notice.kind == .error { Text(notice.text).foregroundStyle(.primary) }
                }
                .navigationTitle("Auth key")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { showAuthKey = false } }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Store") {
                            do {
                                try TailscaleKeychainStore().storeAuthKey(authKeyDraft)
                                keyStoredTick += 1; showAuthKey = false
                            } catch { provisionNotice = Notice(kind: .error, text: "Keychain save failed") }
                        }.disabled(authKeyDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                }
            }.presentationDetents([.medium, .large]).presentationDragIndicator(.visible)
        }
    }

    // MARK: Endpoint provisioning (safe, validated, saved-config merge)

    @ViewBuilder
    private var endpointConfigSection: some View {
        Section {
            let enabled = TailscaleEndpointProvisioning.isEnabled(in: store.content)
            Toggle("Built-in Tailscale endpoint", isOn: Binding(
                get: { enabled },
                set: { value in if value { setEnabled(true) } else { confirmDisable = true } }
            ))
            if enabled {
                Toggle("Force DERP relay", isOn: Binding(
                    get: { store.selectedProfile?.forceTailscaleDERP == true },
                    set: { enabled in
                        do { try store.setForceTailscaleDERP(enabled) }
                        catch { provisionNotice = Notice(kind: .error, text: error.localizedDescription) }
                    }
                ))
                .disabled(store.applying || store.generating)
                Text("Use relays instead of direct peer connections. Apply & connect to switch; relay paths may be slower.")
                    .font(.footnote).foregroundStyle(.secondary)
                DisclosureGroup("Endpoint options") {
                    TextField("Hostname (optional)", text: $hostnameDraft)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                    Toggle("Accept tailnet subnet routes", isOn: $acceptRoutesDraft)
                    TextField("Exit node (name or IP)", text: $exitNodeDraft)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                    Toggle("Route LAN access via exit node", isOn: $allowLANDraft)
                    Button("Save endpoint settings") { applyOptions() }
                    Text("Saved settings apply on reconnect. Exit-node changes below apply immediately.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            if store.configState != .clean && (store.canConnect || store.canGenerate) {
                Button(store.applying ? "Applying…" : "Apply & connect") {
                    Task { await store.regenerateAndApply(tunnel: tunnel) }
                }.disabled(store.applying || store.generating)
            }
            if let action = store.lastAction { Text(action).font(.footnote).foregroundStyle(.secondary) }
            if let notice = provisionNotice {
                Text(notice.text)
                    .font(.footnote)
                    .foregroundStyle(notice.kind == .error ? Color.primary : Color.secondary)
            }
        } header: {
            Text("Endpoint configuration")
        } footer: {
            Text("Enable, then Apply & connect. Once the VPN is running, sign in using the login link below or a stored auth key.")
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
            try store.setTailscaleEnabled(enabled, options: currentOptions())
            provisionNotice = Notice(
                kind: .info,
                text: enabled
                    ? "Endpoint added — Apply & connect to sign in"
                    : "Endpoint removed — regenerate + reconnect to apply"
            )
        } catch {
            provisionNotice = Notice(kind: .error, text: error.localizedDescription)
        }
    }

    private func applyOptions() {
        do {
            try store.setTailscaleEnabled(true, options: currentOptions())
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
                    .foregroundStyle(.primary)
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
                    pendingClearExit = endpoint
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
            pendingLogout = endpoint
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
                    .foregroundStyle(.primary)
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
            DisclosureGroup("Available features") {
                Text("View your devices, select an exit node, test device latency and sign out.")
                Text("Sign in using the login link, or configure an optional auth key.")
                Text("File transfer, Tailscale SSH and service sharing are unavailable in this app.")
                    .foregroundStyle(.secondary)
            }
            .sakamotoInspectTag("TailscaleFeatures")
        }
    }

    private var authKeySection: some View {
        Section {
            Button("Edit auth key…") { showAuthKey = true }
            Button("Delete stored key…", role: .destructive) { confirmDeleteKey = true }
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
