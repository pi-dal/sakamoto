import SwiftUI

struct S3SettingsView: View {
    @ObservedObject var model: S3SyncModel
    @State private var confirmEnable = false
    @State private var showCredentials = false
    @State private var accessDraft = ""
    @State private var secretDraft = ""
    @State private var tokenDraft = ""

    var body: some View {
        Form {
            Section {
                Toggle("Enable source sync", isOn: Binding(
                    get: { model.settings.enabled },
                    set: { enabled in
                        if enabled { confirmEnable = true } else { model.settings.enabled = false }
                    }
                ))
                TextField("HTTPS endpoint", text: $model.settings.endpoint)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                TextField("Region", text: $model.settings.region)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                TextField("Bucket", text: $model.settings.bucket)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                TextField("Path prefix", text: $model.settings.prefix)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                Button("Save settings") { model.saveSettings() }
                    .disabled(model.syncing)
            } header: { Text("Sync sources") } footer: {
                Text("Only configuration sources sync. Downloaded changes require Apply & Reconnect. Conflicting edits are kept for review.")
            }
            Section {
                Button("Edit credentials…") {
                    accessDraft = model.accessKey; secretDraft = model.secretKey; tokenDraft = model.sessionToken
                    showCredentials = true
                }
                .disabled(model.syncing)
            } header: { Text("Credentials") } footer: {
                Text("Credentials are encrypted in Keychain, never uploaded with sources and masked until you choose to edit them.")
            }
            Section("Actions") {
                Button("Sync now") { model.syncNow() }
                    .disabled(model.syncing || !model.settings.enabled)
                if model.syncing { ProgressView() }
                Text(model.notice).font(.footnote).foregroundStyle(.secondary)
            }
        }
        .navigationTitle("S3 Sync")
        .confirmationDialog("Enable cloud source sync?", isPresented: $confirmEnable, titleVisibility: .visible) {
            Button("Enable") { model.settings.enabled = true }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Node links, subscription URLs and imported configuration may contain access tokens or private mappings. They will be sent to the configured object storage. Generated configs, keys and logs remain local.")
        }
        .sheet(isPresented: $showCredentials, onDismiss: clearDrafts) {
            NavigationStack {
                Form {
                    SecureField("Access key", text: $accessDraft)
                    SecureField("Secret key", text: $secretDraft)
                    SecureField("Session token (optional)", text: $tokenDraft)
                }
                .textInputAutocapitalization(.never).autocorrectionDisabled()
                .navigationTitle("Credentials")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { showCredentials = false } }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Save") {
                            model.accessKey = accessDraft; model.secretKey = secretDraft; model.sessionToken = tokenDraft
                            if model.saveSettings() { showCredentials = false }
                        }
                    }
                }
            }
            .presentationDetents([.medium, .large])
            .presentationDragIndicator(.visible)
        }
    }

    private func clearDrafts() { accessDraft = ""; secretDraft = ""; tokenDraft = "" }
}
