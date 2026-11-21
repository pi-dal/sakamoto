import Foundation
import SwiftUI
import Libbox
import SakamotoKit

@MainActor
final class S3SyncModel: ObservableObject {
    @Published var settings: S3SyncSettings {
        didSet { if settings != oldValue { activeRequest?.cancel() } }
    }
    private var activeRequest: MobilecoreS3SyncRequest?
    private var editingOriginal: S3SyncCredentials?
    private var savedSettings: S3SyncSettings
    @Published private(set) var syncing = false
    @Published private(set) var notice = "Not synced yet"
    private let store: ConfigStore
    private let defaults: UserDefaults
    private let vault: TailscaleAuthKeyStoring
    private let settingsKey = "sakamoto.s3.settings"
    private var baselineKey: String { "sakamoto.s3.baseline." + (store.selectedProfileID ?? "unassigned") }

    init(store: ConfigStore, defaults: UserDefaults = .standard,
         vault: TailscaleAuthKeyStoring = TailscaleKeychainStore(account: "s3-sync-credentials")) {
        self.vault = vault
        self.store = store; self.defaults = defaults
        let saved = defaults.data(forKey: settingsKey).flatMap { try? JSONDecoder().decode(S3SyncSettings.self, from: $0) } ?? S3SyncSettings()
        savedSettings = saved
        settings = saved
    }

    private func savedCredentials() throws -> S3SyncCredentials {
        guard let raw = try vault.readAuthKey() else { return S3SyncCredentials() }
        return try JSONDecoder().decode(S3SyncCredentials.self, from: Data(raw.utf8))
    }

    func beginCredentialEdit() -> S3SyncCredentials? {
        do {
            editingOriginal = try savedCredentials()
        } catch {
            editingOriginal = nil
            notice = "Could not read S3 credentials. Try again."
        }
        return editingOriginal
    }

    func endCredentialEdit() { editingOriginal = nil }
    private func json<T: Encodable>(_ value: T) throws -> String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        return String(decoding: try encoder.encode(value), as: UTF8.self)
    }
    @discardableResult func saveSettings(credentials candidate: S3SyncCredentials? = nil) -> Bool {
        do {
            let raw = try json(settings)
            var error: NSError?
            MobilecoreValidateS3SettingsJSON(raw, &error)
            if let error { throw error }
            if let candidate {
                guard let original = editingOriginal, try savedCredentials() == original else {
                    throw AppSetupError("Reopen the credential editor before saving.")
                }
                try vault.storeAuthKey(try json(candidate))
                endCredentialEdit()
            }
            defaults.set(Data(raw.utf8), forKey: settingsKey)
            savedSettings = settings
            notice = "S3 settings saved"
            return true
        } catch {
            settings = savedSettings
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
        do {
            try store.ensureSourceProfile()
            let settingsJSON = try json(settings)
            guard settings == savedSettings else { throw AppSetupError("Save your S3 settings before syncing.") }
            let credentialsJSON = try json(savedCredentials())
            let localJSON = try json(export())
            let baseline = defaults.string(forKey: baselineKey) ?? ""
            let profileID = store.selectedProfileID
            let sourcesBefore = store.sourceBundle
            let capturedBaselineKey = baselineKey
            guard let request = MobilecoreNewS3SyncRequest() else { throw AppSetupError("Could not start S3 sync") }
            // Selection and source edits both publish a new profile snapshot. Cancel
            // synchronously so a pending GET cannot turn into an obsolete PUT.
            let profileChanges = store.$profiles.dropFirst().sink { _ in request.cancel() }
            activeRequest = request
            syncing = true
            Task {
                defer { profileChanges.cancel(); activeRequest = nil; syncing = false }
                do {
                    let raw = try await Task.detached {
                        var error: NSError?
                        let result = request.run(settingsJSON, credentials: credentialsJSON, local: localJSON, baseline: baseline, error: &error)
                        if let error { throw error }
                        return result
                    }.value
                    profileChanges.cancel()
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
                    for (name, body) in sourcesBefore.files where !(response.downloads ?? []).contains(name) && (name == "policy.json" || name == "subscriptions.json" || name == AutomaticGroupSources.fileName) {
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
