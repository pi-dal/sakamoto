import Foundation
import SwiftUI
import Libbox
import SakamotoKit

@MainActor
final class S3SyncModel: ObservableObject {
    @Published var settings: S3SyncSettings
    @Published var accessKey = ""
    @Published var secretKey = ""
    @Published var sessionToken = ""
    @Published private(set) var syncing = false
    @Published private(set) var notice = "Not synced yet"
    private let store: ConfigStore
    private let defaults: UserDefaults
    private let vault = TailscaleKeychainStore(account: "s3-sync-credentials")
    private let settingsKey = "sakamoto.s3.settings"
    private var baselineKey: String { "sakamoto.s3.baseline." + (store.selectedProfileID ?? "unassigned") }

    init(store: ConfigStore, defaults: UserDefaults = .standard) {
        self.store = store; self.defaults = defaults
        settings = defaults.data(forKey: settingsKey).flatMap { try? JSONDecoder().decode(S3SyncSettings.self, from: $0) } ?? S3SyncSettings()
        if let raw = vault.readAuthKey(), let data = raw.data(using: .utf8),
           let c = try? JSONDecoder().decode(S3SyncCredentials.self, from: data) {
            accessKey = c.accessKey; secretKey = c.secretKey; sessionToken = c.sessionToken
        }
    }
    private func json<T: Encodable>(_ value: T) throws -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        return String(decoding: try encoder.encode(value), as: UTF8.self)
    }
    @discardableResult func saveSettings() -> Bool {
        do {
            let raw = try json(settings)
            var error: NSError?
            MobilecoreValidateS3SettingsJSON(raw, &error)
            if let error { throw error }
            let c = S3SyncCredentials(accessKey: accessKey, secretKey: secretKey, sessionToken: sessionToken)
            try vault.storeAuthKey(try json(c))
            defaults.set(Data(raw.utf8), forKey: settingsKey)
            notice = "S3 settings saved"
            return true
        } catch {
            notice = "Could not save S3 settings: \(error.localizedDescription)"
            return false
        }
    }
    private func export() throws -> SourceBundle {
        var bundle = store.sourceBundle
        // S3's contract is conf/<relative path>, whereas iCloud preserves
        // the TUI's runtime-relative names. Convert this snapshot explicitly.
        if bundle.files.keys.contains(where: { $0.hasSuffix(".conf") && !$0.hasPrefix("conf/") }) {
            let confs = bundle.files.filter { $0.key.lowercased().hasSuffix(".conf") }
            for name in confs.keys { bundle.files.removeValue(forKey: name) }
            for (name, content) in confs { bundle.files["conf/" + name] = content }
            if !bundle.mainConf.isEmpty { bundle.mainConf = "conf/" + bundle.mainConf }
        }
        return bundle
    }
    func syncNow() {
        guard !syncing else { return }
        guard settings.enabled else { notice = "Enable S3 sync first"; return }
        store.ensureSourceProfile()
        do {
            let settingsJSON = try json(settings)
            let credentialsJSON = try json(S3SyncCredentials(accessKey: accessKey, secretKey: secretKey, sessionToken: sessionToken))
            let localJSON = try json(export())
            let baseline = defaults.string(forKey: baselineKey) ?? ""
            let profileID = store.selectedProfileID
            let sourcesBefore = store.sourceBundle
            let capturedBaselineKey = baselineKey
            syncing = true
            Task {
                defer { syncing = false }
                do {
                    let raw = try await Task.detached {
                        var error: NSError?
                        let raw = MobilecoreSyncSourcesS3JSON(settingsJSON, credentialsJSON, localJSON, baseline, &error)
                        if let error { throw error }
                        return raw
                    }.value
                    let response = try JSONDecoder().decode(S3SyncResponse.self, from: Data(raw.utf8))
                    guard store.selectedProfileID == profileID, try json(export()) == localJSON, try json(settings) == settingsJSON else {
                        throw AppSetupError("Local sources changed during sync; retry without overwriting")
                    }
                    var bundle = response.bundle
                    if store.confSources.isEmpty || store.confSources.contains(where: { !$0.hasPrefix("conf/") }) {
                        bundle.files = Dictionary(uniqueKeysWithValues: bundle.files.map { name, body in
                            (name.hasPrefix("conf/") ? String(name.dropFirst(5)) : name, body)
                        })
                        if bundle.mainConf.hasPrefix("conf/") { bundle.mainConf = String(bundle.mainConf.dropFirst(5)) }
                    }
                    for (name, body) in sourcesBefore.files where !(response.downloads ?? []).contains(name) && (name == "policy.json" || name == "subscriptions.json") {
                        bundle.files[name] = body
                    }
                    try store.adoptSourceBundle(bundle)
                    defaults.set(try json(response.baseline), forKey: capturedBaselineKey)
                    notice = "Synced \((response.downloads ?? []).count) source files" + (response.uploaded ? "; uploaded changes" : "")
                } catch { notice = "S3 sync: \(error.localizedDescription)" }
            }
        } catch { notice = "S3 sync: \(error.localizedDescription)" }
    }
}

struct S3SyncSection: View {
    @ObservedObject var model: S3SyncModel
    var body: some View {
        Section("S3 Sync") {
            Toggle("Sync sources to S3", isOn: $model.settings.enabled)
            TextField("HTTPS endpoint", text: $model.settings.endpoint)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
            TextField("Region (auto for R2)", text: $model.settings.region)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
            TextField("Bucket", text: $model.settings.bucket)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
            TextField("Prefix", text: $model.settings.prefix)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
            SecureField("Access key", text: $model.accessKey)
            SecureField("Secret key", text: $model.secretKey)
            SecureField("Session token (optional)", text: $model.sessionToken)
            Button("Save S3 settings") { model.saveSettings() }.disabled(model.syncing)
            Button("Sync now") { model.syncNow() }.disabled(model.syncing || !model.settings.enabled)
            if model.syncing { ProgressView() }
            Text(model.notice).font(.footnote).foregroundStyle(.secondary)
            Text("Syncs nodes, subscriptions, policy and conf sources. Credentials stay in Keychain. Conflicts keep both copies; downloaded edits need Regenerate + Reconnect.")
                .font(.footnote).foregroundStyle(.secondary)
        }
    }
}
