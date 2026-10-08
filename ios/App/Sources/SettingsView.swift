import SwiftUI
import Combine
import UniformTypeIdentifiers
import Libbox
import SakamotoKit

// Settings edits share ConfigStore with Home and Config. Runtime mode uses
// the live command channel; saved changes wait for Apply. Experiment learning
// and fallback monitoring run in the VPN extension, including in background.
// Source generation preserves device endpoint identity and runtime overrides.

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
    private var storeUpdates: AnyCancellable?

    init(store: ConfigStore, tunnel: TunnelControlling, commanding: CoreCommanding?) {
        self.store = store
        self.tunnel = tunnel
        self.commanding = commanding
        refreshSemantics()
        storeUpdates = store.objectWillChange.sink { [weak self] _ in
            Task { @MainActor [weak self] in self?.refreshSemantics() }
        }
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
            parseError = "no saved configuration — add sources and Generate in Config"
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

    func setExperiment(_ enabled: Bool) {
        apply("Experiment \(enabled ? "on" : "off")") { try SettingsOverrides.setExperiment(enabled, in: $0) }
    }

    private func apply(_ label: String, _ transform: (String) throws -> String) {
        do {
            let next = try transform(store.content)
            store.save(next)
            notice = Notice(kind: .info, text: "\(label) saved — apply configuration to take effect")
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
    var sourcesOnly = false

    @State private var confirmSyncEnable = false
    @State private var showSyncDirectoryPicker = false
    @State private var confirmApply = false
    @State private var showModes = false

    var body: some View {
        List {
            if !sourcesOnly {
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
                            Text("Reloading the tunnel can interrupt active connections. Sources are generated and validated on this device before applying.")
                        }
                }
                NavigationLink("Tailscale") {
                    TailscaleView(store: model.store, tunnel: model.tunnel, commanding: commanding)
                }
            }
            Section {
                NavigationLink {
                    ExperimentView(model: model)
                } label: {
                    Label("Experiment", systemImage: "flask")
                }
                .sakamotoInspectTag("ExperimentEntry")
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
            if !sourcesOnly {
            Section("Advanced") {
                NavigationLink("Configuration details") {
                    List { hostOwnedSection }.listStyle(.insetGrouped).navigationTitle("Configuration details")
                        .navigationBarTitleDisplayMode(.inline)
                }
            }
            aboutSection
            }
        }
        .fileImporter(isPresented: $showSyncDirectoryPicker, allowedContentTypes: [.folder]) { result in
            if case .success(let url) = result { sync.selectDirectory(url) }
        }
        .navigationTitle(sourcesOnly ? "Sync sources" : "Settings")
        .sakamotoRootPage()
        .onChange(of: model.store.content) { _ in model.refreshSemantics() }
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
                    .foregroundStyle(model.channelActive ? Color.primary : Color.secondary)
            }
            if let notice = model.notice {
                Text(notice.text)
                    .font(.footnote)
                    .foregroundStyle(notice.kind == .error ? Color.primary : Color.secondary)
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
                    .foregroundStyle(.primary)
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
            Text("Saved configuration")
        } footer: {
            Text("Changes are saved on this device. Apply configuration & reconnect when you are ready.")
        }
    }

    @ViewBuilder
    private var hostOwnedSection: some View {
        Section {
            if let semantics = model.semantics {
                row("Saved unmatched route", semantics.routeFinal ?? "—")
                row("TUN stack", semantics.tunStack ?? "—")
                row("Strict routing", semantics.strictRoute.map { $0 ? "On" : "Off" } ?? "—")
            }
            row("Automatic fallback", "Settings → Experiment")
            row("Chain SOCKS exit", "Imported configuration")
            row("Subscriptions / nodes", "Config → Nodes & sources")
        } header: {
            Text("Host-owned semantics (read-only here)")
        } footer: {
            Text("Sources are generated on this device. Imported advanced routing and chain intent should be reviewed before regeneration.")
        }
    }

    private var applySection: some View {
        Section {
            HStack {
                Text("Config state")
                Spacer()
                Text(model.store.configState.rawValue)
                    .foregroundStyle(model.store.configState == .clean ? Color.primary : Color.secondary)
            }
            Button("Apply configuration & reconnect") {
                confirmApply = true
            }
            .disabled(model.store.generating || (!model.store.canConnect && !model.store.canGenerate))
            .sakamotoGlassButton(prominent: true)
            .disabled(model.store.configState == .clean)
        }
    }

    @ViewBuilder
    private var iCloudSyncSection: some View {
        Section {
            Button { showSyncDirectoryPicker = true } label: {
                HStack {
                    Label("Sync folder", systemImage: "folder")
                    Spacer()
                    Text(sync.directoryLabel).font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.trailing)
                    Image(systemName: "chevron.right").font(.caption).foregroundStyle(.tertiary)
                }
            }.disabled(sync.syncing)
        } header: { Text("Destination") } footer: {
            Text("For Mac sync, select iCloud Drive → sakamoto, or the custom folder selected in the TUI. The app's own iCloud folder is separate.")
        }
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
                LabeledRow("Nodes", "nodes.txt")
                Toggle("Include configuration source", isOn: Binding(
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
                        .foregroundStyle(.primary)
                    Text("Both sides changed without a shared baseline. Keep the copy you want staged here, then sync again.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                DisclosureGroup("Advanced source paths") {
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
                .foregroundStyle(sync.status.isConflict ? Color.primary : Color.secondary)
        } header: {
            Text("Sync sources")
        } footer: {
            Text("Sync transfers source files. It does not choose or apply the running VPN configuration. Generated configs, keys and logs stay local.")
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
