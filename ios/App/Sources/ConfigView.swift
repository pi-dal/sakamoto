import SwiftUI
import Mobilecore
import SakamotoKit

// Config tab: the config-state machine runs through the Go bridge
// (mobilecore.ConfigStateTransition) inside the shared ConfigStore;
// regenerate on iOS means "hand the latest config to the running provider"
// (Regenerate->Reconnect collapse), because the config *generator*
// (internal/gen) runs on the sakamoto host. That boundary is stated here
// rather than faked.

@MainActor
final class ConfigModel: ObservableObject {
    @Published var draft: String
    let store: ConfigStore

    private let tunnel: TunnelControlling

    init(tunnel: TunnelControlling, store: ConfigStore) {
        self.tunnel = tunnel
        self.store = store
        self.draft = store.content
    }

    /// Any saved change marks the config `modified` (NeedsRegenerate).
    func saveDraft() {
        store.save(draft)
        // Keep the editor text and the saved config identical after save.
        draft = store.content
    }

    /// Fold the saved config into what the tunnel uses.
    func regenerateAndApply() async {
        await store.regenerateAndApply(tunnel: tunnel)
    }
}

struct ConfigView: View {
    @ObservedObject var model: ConfigModel

    var body: some View {
        List {
            Section {
                HStack {
                    Text("Config state")
                    Spacer()
                    Text(model.store.configState.rawValue)
                        .foregroundStyle(model.store.configState == .clean ? Color.green : Color.orange)
                }
                if let lastAction = model.store.lastAction {
                    Text(lastAction)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            } header: {
                Text("State")
            } footer: {
                Text("Modified configs need Regenerate, then a Reconnect to apply. The generator itself runs on the sakamoto host; this screen feeds its output to the tunnel provider.")
            }

            Section {
                Button("Regenerate + Reconnect") {
                    Task { await model.regenerateAndApply() }
                }
                .disabled(model.store.configState == .clean)
            }

            Section {
                TextEditor(text: $model.draft)
                    .font(.footnote.monospaced())
                    .frame(minHeight: 220)
                Button("Save (marks modified)") {
                    model.saveDraft()
                }
            } header: {
                Text("sing-box config (endpoints/outbounds JSON)")
            } footer: {
                Text("Secrets: the Tailscale auth key is injected from the Keychain at start time and is never stored in this text or the repo. Enable the built-in Tailscale endpoint from Settings.")
            }
        }
        .navigationTitle("Config")
    }
}
