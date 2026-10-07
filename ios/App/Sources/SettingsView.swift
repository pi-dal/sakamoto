import SwiftUI
import Libbox
import SakamotoKit

// Settings tab: the TUI's Settings semantics, adapted to the iOS trust
// boundary (the config GENERATOR runs on the sakamoto host — the app only
// owns the generated config JSON and the runtime command channel):
//
//   - Runtime (command channel): routing mode (Rule/Global/Direct cycle,
//     the `m` key). Live state; failures are notices, never no-ops.
//   - Editable here (validated config-JSON merges, applied by
//     Regenerate+Reconnect through the ConfigStore): log level,
//     Block QUIC (UDP 443), Block STUN — the same knobs the TUI edits,
//     with the same value vocabularies.
//   - Host-owned (read-only here, values read from the config): route
//     final (unmatched policy), TUN stack, strict route; plus the
//     sidecar-owned fallback chain and chain SOCKS exit, which live in the
//     host's sakamoto.yaml and cannot be edited on device at all.
//
// Every edit lands in the shared ConfigStore → NeedsRegenerate → the user
// runs Regenerate+Reconnect (or the button below). Nothing applies silently.

@MainActor
final class SettingsModel: ObservableObject {
    let store: ConfigStore
    let tunnel: TunnelControlling
    let commanding: CoreCommanding?

    @Published private(set) var semantics: ConfigSemantics?
    @Published private(set) var parseError: String?
    @Published private(set) var notice: Notice?
    @Published private(set) var routingMode: RoutingMode?
    @Published private(set) var channelActive = false

    private var tasks: [Task<Void, Never>] = []

    init(store: ConfigStore, tunnel: TunnelControlling, commanding: CoreCommanding?) {
        self.store = store
        self.tunnel = tunnel
        self.commanding = commanding
        refreshSemantics()
    }

    func activate() {
        guard tasks.isEmpty else { return }
        guard let commanding else { return }
        tasks.append(Task { [weak self] in
            let availability = commanding.availability()
            for await active in availability {
                guard let self else { return }
                self.channelActive = active
                self.routingMode = active ? commanding.currentClashMode() : nil
            }
        })
    }

    // MARK: Read-only view of the saved config

    func refreshSemantics() {
        if store.content.isEmpty {
            semantics = nil
            parseError = "no saved config yet — import or generate one on the host"
            return
        }
        guard let parsed = ConfigSemanticsReader.read(store.content) else {
            semantics = nil
            parseError = "saved config is not valid JSON — fix it in Config"
            return
        }
        semantics = parsed
        parseError = nil
    }

    var isTailscaleEnabled: Bool {
        TailscaleEndpointProvisioning.isEnabled(in: store.content)
    }

    // MARK: Editable semantics (validated merges into the saved config)

    func setLogLevel(_ level: String) {
        apply("log level \(level)") { try SettingsOverrides.setLogLevel(level, in: $0) }
    }

    func setBlockQUIC(_ enabled: Bool) {
        apply("Block QUIC \(enabled ? "on" : "off")") {
            try SettingsOverrides.setBlockQUIC(enabled, in: $0)
        }
    }

    func setBlockSTUN(_ enabled: Bool) {
        apply("Block STUN \(enabled ? "on" : "off")") {
            try SettingsOverrides.setBlockSTUN(enabled, in: $0)
        }
    }

    private func apply(_ label: String, _ transform: (String) throws -> String) {
        do {
            let next = try transform(store.content)
            store.save(next)
            notice = Notice(kind: .info, text: "\(label) saved — regenerate + reconnect to apply")
            refreshSemantics()
        } catch {
            notice = Notice(kind: .error, text: "\(label): \(error.localizedDescription)")
        }
    }

    // MARK: Runtime mode (same action as the Home [ Mode ] button)

    func setRoutingMode(_ mode: RoutingMode) async {
        guard let commanding, channelActive else {
            notice = Notice(kind: .warning, text: "Connect the tunnel before changing mode")
            return
        }
        do {
            try await commanding.setClashMode(mode)
            routingMode = mode
            notice = Notice(kind: .info, text: "Mode: \(mode.rawValue)")
        } catch {
            notice = Notice(kind: .error, text: "Mode change failed — reconnect and retry")
        }
    }

    func cycleRoutingMode() async {
        guard let commanding else {
            notice = Notice(kind: .warning, text: "mode: command channel unavailable")
            return
        }
        guard channelActive else {
            notice = Notice(kind: .warning, text: "mode: command channel unavailable — connect the tunnel first")
            return
        }
        let current = routingMode?.rawValue ?? commanding.currentClashMode()?.rawValue ?? ""
        let next = MobilecoreNextRoutingMode(current)
        guard let nextMode = RoutingMode(rawValue: next) else { return }
        do {
            try await commanding.setClashMode(nextMode)
            routingMode = nextMode
            notice = Notice(kind: .info, text: "Mode: \(nextMode.rawValue)")
        } catch {
            notice = Notice(
                kind: .error,
                text: "mode \(nextMode.rawValue) unavailable — regenerate the config and reconnect"
            )
        }
    }

    // MARK: Apply pipeline

    func regenerateAndApply() async {
        await store.regenerateAndApply(tunnel: tunnel)
        notice = Notice(
            kind: store.configState == .clean ? .success : .error,
            text: store.lastAction ?? ""
        )
        refreshSemantics()
    }
}

struct SettingsView: View {
    @ObservedObject var model: SettingsModel
    @ObservedObject var sync: ICloudSyncModel
    @ObservedObject var s3: S3SyncModel
    let commanding: LibboxCoreCommanding?

    @State private var confirmSyncEnable = false
    @State private var confirmApply = false
    @State private var showModes = false

    var body: some View {
        List {
            Section("Connections") {
                NavigationLink("Tunnel settings") {
                    List { runtimeSection; editableSection; applySection }
                        .listStyle(.insetGrouped)
                        .navigationTitle("Tunnel settings")
                        .navigationBarTitleDisplayMode(.inline)
                        .confirmationDialog("Routing mode", isPresented: $showModes, titleVisibility: .visible) {
                            ForEach(RoutingMode.allCases, id: \.rawValue) { mode in
                                Button(mode.rawValue) { Task { await model.setRoutingMode(mode) } }
                            }
                            Button("Cancel", role: .cancel) {}
                        }
                        .confirmationDialog("Apply saved configuration?", isPresented: $confirmApply, titleVisibility: .visible) {
                            Button("Apply & Reconnect") { Task { await model.regenerateAndApply() } }
                            Button("Cancel", role: .cancel) {}
                        } message: {
                            Text("Reloading the tunnel can interrupt active connections. Host-side generation is not performed on this device.")
                        }
                }
                NavigationLink("Tailscale") {
                    TailscaleView(store: model.store, tunnel: model.tunnel, commanding: commanding)
                }
            }
            Section("Source sync") {
                NavigationLink("iCloud Sync") {
                    List { iCloudSyncSection }
                        .listStyle(.insetGrouped).navigationTitle("iCloud Sync")
                        .navigationBarTitleDisplayMode(.inline)
                        .confirmationDialog("Sync sources to iCloud?", isPresented: $confirmSyncEnable, titleVisibility: .visible) {
                            Button("Enable sync") { sync.confirmEnable() }
                            Button("Cancel", role: .cancel) {}
                        } message: {
                            Text("Node links, subscription URLs and imported conf may contain credentials. They will be uploaded to iCloud Drive. Generated configs, keys and logs remain local.")
                        }
                }
                NavigationLink("S3 Sync") { S3SettingsView(model: s3) }
            }
            Section("Advanced") {
                NavigationLink("Host-owned configuration") {
                    List { hostOwnedSection }.listStyle(.insetGrouped).navigationTitle("Host configuration")
                        .navigationBarTitleDisplayMode(.inline)
                }
            }
            aboutSection
        }
        .navigationTitle("Settings")
        .sakamotoRootPage()
        .task {
            model.activate()
            sync.activate()
        }
    }

    private var runtimeSection: some View {
        Section {
            Button {
                showModes = true
            } label: {
                HStack {
                    Text("Routing mode")
                    Spacer()
                    Text(model.routingMode?.rawValue ?? "—")
                        .foregroundStyle(.secondary)
                }
            }
            .sakamotoGlassButton()
            .disabled(!model.channelActive)
            HStack {
                Text("Command channel")
                Spacer()
                Text(model.channelActive ? "Connected" : "Unavailable")
                    .foregroundStyle(model.channelActive ? Color.green : Color.secondary)
            }
            if let notice = model.notice {
                Text(notice.text)
                    .font(.footnote)
                    .foregroundStyle(notice.kind == .error ? Color.red : Color.secondary)
            }
        } header: {
            Text("Runtime")
        } footer: {
            Text("Mode is runtime state (Rule → Global → Direct), applied immediately through the command channel.")
        }
    }

    @ViewBuilder
    private var editableSection: some View {
        Section {
            if let error = model.parseError {
                Text(error)
                    .font(.footnote)
                    .foregroundStyle(.red)
            }
            if let semantics = model.semantics {
                Picker("Log level", selection: Binding(
                    get: { semantics.logLevel ?? "info" },
                    set: { model.setLogLevel($0) }
                )) {
                    ForEach(SettingsOverrides.allowedLogLevels, id: \.self) { level in
                        Text(level).tag(level)
                    }
                }
                Toggle("Block QUIC (UDP 443)", isOn: Binding(
                    get: { semantics.blockQUIC },
                    set: { model.setBlockQUIC($0) }
                ))
                Toggle("Block STUN / WebRTC", isOn: Binding(
                    get: { semantics.blockSTUN },
                    set: { model.setBlockSTUN($0) }
                ))
            }
        } header: {
            Text("Config (applied by Regenerate + Reconnect)")
        } footer: {
            Text("Edits are validated merges into the saved config. They take effect after Regenerate + Reconnect; the tunnel keeps the previous config until then.")
        }
    }

    @ViewBuilder
    private var hostOwnedSection: some View {
        Section {
            if let semantics = model.semantics {
                row("Unmatched policy (route final)", semantics.routeFinal ?? "—")
                row("TUN stack", semantics.tunStack ?? "—")
                row("Strict routing", semantics.strictRoute.map { $0 ? "On" : "Off" } ?? "—")
            }
            row("Automatic fallback", "host sidecar (sakamoto.yaml)")
            row("Chain SOCKS exit", "host daemon")
            row("Subscriptions / nodes", "host sidecar")
        } header: {
            Text("Host-owned semantics (read-only here)")
        } footer: {
            Text("These live in the host generator and its sakamoto.yaml sidecar; edit them on the sakamoto host, then re-import the regenerated config.")
        }
    }

    private var applySection: some View {
        Section {
            HStack {
                Text("Config state")
                Spacer()
                Text(model.store.configState.rawValue)
                    .foregroundStyle(model.store.configState == .clean ? Color.green : Color.orange)
            }
            Button("Regenerate + Reconnect") {
                confirmApply = true
            }
            .sakamotoGlassButton(prominent: true)
            .disabled(model.store.configState == .clean)
        }
    }

    private var iCloudSyncSection: some View {
        Section {
            Toggle("Sync sources to iCloud", isOn: Binding(
                get: { sync.settings.enabled },
                set: { enabled in
                    if enabled {
                        // Second-step confirmation with the credential-risk
                        // copy (docs/icloud.md: enabling needs confirmation).
                        confirmSyncEnable = true
                    } else {
                        sync.disable()
                    }
                }
            ))
            if sync.settings.enabled {
                Toggle("Include conf & rule includes", isOn: Binding(
                    get: { sync.settings.includesConf },
                    set: { sync.setIncludeConf($0) }
                ))
                Button {
                    sync.syncNow()
                } label: {
                    HStack {
                        Text("Sync now")
                        if sync.syncing { Spacer(); ProgressView() }
                    }
                }
                .sakamotoGlassButton()
                .disabled(sync.syncing)
            }
            HStack {
                Text("Last sync")
                Spacer()
                Text(sync.status.timestamp.map { ICloudSyncFormatting.timestamp($0) } ?? "never")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            if sync.settings.enabled {
                if case .conflict(_, let names) = sync.status {
                    Text("Conflict — not overwritten: " + names.joined(separator: ", "))
                        .font(.footnote)
                        .foregroundStyle(.red)
                    Text("Both sides changed without a shared baseline. Keep the copy you want staged here, then sync again.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                VStack(alignment: .leading, spacing: 4) {
                    Text("Additional source paths")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                    TextField("nodes.txt\npolicy.json", text: $sync.additionalPathsDraft, axis: .vertical)
                        .textFieldStyle(.roundedBorder)
                        .font(.footnote)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                    Button("Save paths") { sync.commitAdditionalPathsDraft() }
                        .font(.footnote)
                }
            }
            if let notice = sync.notice {
                Text(notice)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Text(sync.status.summary)
                .font(.footnote)
                .foregroundStyle(sync.status.isConflict ? Color.red : Color.secondary)
        } header: {
            Text("iCloud Sync")
        } footer: {
            Text("Off by default and never required: generated config.json, .srs, keys, Keychain content and logs stay local. The container id (iCloud.com.pidal.sakamoto) is a placeholder until a real Team signs the app — sync reports unavailable until then.")
        }
    }

    private var aboutSection: some View {
        Section {
            NavigationLink("Widgets & Shortcuts") { SystemSurfacesView() }
            NavigationLink("About sakamoto") { AboutView() }
        } header: {
            Text("App")
        }
    }

    private var tailscaleSection: some View {
        Section {
            NavigationLink {
                TailscaleView(store: model.store, tunnel: model.tunnel, commanding: commanding)
            } label: {
                HStack {
                    Text("Built-in Tailscale")
                    Spacer()
                    Text(model.isTailscaleEnabled ? "Endpoint configured" : "Not configured")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        } footer: {
            Text("The Tailscale integration is a sing-box endpoint inside the tunnel process — enable and configure it from this entry.")
        }
    }

    private func row(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label)
            Spacer()
            Text(value)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.trailing)
        }
    }
}
