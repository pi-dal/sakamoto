import Foundation
import SwiftUI
import Mobilecore
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
    private let baselineKey = "sakamoto.s3.baseline"
    private let bundleKey = "sakamoto.s3.bundle"

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
        var bundle = defaults.data(forKey: bundleKey).flatMap { try? JSONDecoder().decode(SourceBundle.self, from: $0) } ?? SourceBundle()
        let nodes = store.nodesSources.nodesText
        if !nodes.isEmpty || bundle.files["nodes.txt"] != nil { bundle.files["nodes.txt"] = nodes }
        if !store.policy.rules.isEmpty || bundle.files["policy.json"] != nil {
            bundle.files["policy.json"] = try json(store.policy.rules.map { SyncPolicy(match: $0.match, action: $0.action) })
        }
        if !store.nodesSources.subscriptions.isEmpty || bundle.files["subscriptions.json"] != nil {
            bundle.files["subscriptions.json"] = try json(store.nodesSources.subscriptions.map { SyncSubscription(name: $0.name, url: $0.url, format: $0.format) })
        }
        if let source = store.importedSource, !ICloudSyncModel.isRemoteURL(source.displaySource) {
            let name = "conf/" + (source.displaySource as NSString).lastPathComponent
            bundle.mainConf = name; bundle.files[name] = source.content
        }
        return bundle
    }
    func syncNow() {
        guard !syncing else { return }
        guard settings.enabled else { notice = "Enable S3 sync first"; return }
        do {
            let settingsJSON = try json(settings)
            let credentialsJSON = try json(S3SyncCredentials(accessKey: accessKey, secretKey: secretKey, sessionToken: sessionToken))
            let localJSON = try json(export())
            let baseline = defaults.string(forKey: baselineKey) ?? ""
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
                    guard try json(export()) == localJSON, try json(settings) == settingsJSON else {
                        throw AppSetupError("Local sources changed during sync; retry without overwriting")
                    }
                    try adopt(response)
                    defaults.set(try JSONEncoder().encode(response.bundle), forKey: bundleKey)
                    defaults.set(try json(response.baseline), forKey: baselineKey)
                    notice = "Synced \((response.downloads ?? []).count) source files" + (response.uploaded ? "; uploaded changes" : "")
                } catch { notice = "S3 sync: \(error.localizedDescription)" }
            }
        } catch { notice = "S3 sync: \(error.localizedDescription)" }
    }
    private func adopt(_ response: S3SyncResponse) throws {
        let files = response.bundle.files
        let downloads = Set(response.downloads ?? [])
        var nextNodes = store.nodesSources.nodes
        var nextSubs = store.nodesSources.subscriptions
        if downloads.contains("nodes.txt"), let content = files["nodes.txt"] {
            nextNodes = []
            for line in content.split(whereSeparator: { $0.isNewline }) {
                let raw = line.trimmingCharacters(in: .whitespaces)
                if raw.isEmpty || raw.hasPrefix("#") { continue }
                var error: NSError?
                guard let info = MobilecoreParseShareLink(raw, &error), error == nil else { throw AppSetupError("Downloaded node failed validation") }
                nextNodes.append(StagedNode(rawLink: raw, tag: info.tag, type: info.type, server: info.server))
            }
        }
        if downloads.contains("subscriptions.json"), let content = files["subscriptions.json"] {
            nextSubs = try JSONDecoder().decode([SyncSubscription].self, from: Data(content.utf8)).map { StagedSubscription(name: $0.name, url: $0.url, format: $0.format) }
        }
        let policy: PolicyBook?
        if downloads.contains("policy.json"), let content = files["policy.json"] {
            policy = PolicyBook(rules: try JSONDecoder().decode([SyncPolicy].self, from: Data(content.utf8)).map { StagedPolicyRule(match: $0.match, action: $0.action) })
        } else { policy = nil }
        if downloads.contains("nodes.txt") || downloads.contains("subscriptions.json") { store.commitNodesSources(NodesSourcesBook(nodes: nextNodes, subscriptions: nextSubs)) }
        if let policy { store.commitPolicy(policy) }
        if !response.bundle.mainConf.isEmpty, let content = files[response.bundle.mainConf],
           store.importedSource?.content != content || store.importedSource?.displaySource != response.bundle.mainConf {
            store.commitImport(ImportedSource(displaySource: response.bundle.mainConf, content: content))
        }
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
