import SwiftUI
import Mobilecore
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
    let commanding: LibboxCoreCommanding?

    var body: some View {
        List {
            runtimeSection
            editableSection
            hostOwnedSection
            applySection
            tailscaleSection
        }
        .navigationTitle("Settings")
        .task { model.activate() }
    }

    private var runtimeSection: some View {
        Section {
            Button {
                Task { await model.cycleRoutingMode() }
            } label: {
                HStack {
                    Text("Routing mode")
                    Spacer()
                    Text(model.routingMode?.rawValue ?? "—")
                        .foregroundStyle(.secondary)
                }
            }
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
                Task { await model.regenerateAndApply() }
            }
            .disabled(model.store.configState == .clean)
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
