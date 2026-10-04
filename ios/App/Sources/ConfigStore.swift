import Foundation
import Mobilecore
import SakamotoKit

// Single owner of the SAVED sing-box config content and its ConfigState
// machine. Before this store existed the app draft lived in ConfigModel
// while HomeModel kept its own copy of the same string — two copies drift,
// and Connect would inject from the stale one. Now Home, Config and
// Settings all read store.content and every saved change funnels through
// save(_:), which runs the bridge transitions (mobilecore
// .ConfigStateTransition): modified → NeedsRegenerate, provider reload →
// Clean. The state machine itself still lives in Go.
//
// Secrets: content here is the host-generated config WITHOUT the Tailscale
// auth key; the key is injected from the Keychain only in
// regenerateAndApply/connect at start time (TailscaleConfigInjection).
//
// Staged Config-tab state (policy rules, nodes & sources metadata, the
// last-known-good import) is owned here too: the Config UI stages, this
// store persists and transitions. Nothing here generates — folding staged
// rules/nodes into sing-box output stays host-owned (internal/gen); the UI
// states that boundary instead of faking it. Raw share links and
// subscription URLs never reach lastAction strings; rows render them
// masked (SakamotoKit.SecretMasking) until revealed.
@MainActor
final class ConfigStore: ObservableObject {
    static let storageKey = "sakamoto.config.content"
    static let policyKey = "sakamoto.config.policy"
    static let nodesSourcesKey = "sakamoto.config.nodessources"
    static let importKey = "sakamoto.config.importsource"

    /// The last saved config — what Connect and Regenerate+Reconnect use.
    @Published private(set) var content: String
    @Published private(set) var configState: ConfigState = .clean
    @Published private(set) var lastAction: String?

    /// Staged policy rules (docs/tui.md Config → Policy semantics; validated
    /// through mobilecore before they reach this store).
    @Published private(set) var policy: PolicyBook
    /// Staged manual nodes and subscription metadata.
    @Published private(set) var nodesSources: NodesSourcesBook
    /// Last-known-good import. Only commitImport replaces it, and only after
    /// the Go bridge validated the content — a failed import never clobbers.
    @Published private(set) var importedSource: ImportedSource?

    private let persistence: UserDefaults
    private let keyStore: TailscaleAuthKeyStoring

    init(persistence: UserDefaults = .standard, keyStore: TailscaleAuthKeyStoring) {
        self.persistence = persistence
        self.keyStore = keyStore
        self.content = persistence.string(forKey: Self.storageKey) ?? ""
        if let data = persistence.data(forKey: Self.policyKey) {
            self.policy = PolicyBook.decode(data) ?? PolicyBook()
        } else {
            self.policy = PolicyBook()
        }
        if let data = persistence.data(forKey: Self.nodesSourcesKey) {
            self.nodesSources = NodesSourcesBook.decode(data) ?? NodesSourcesBook()
        } else {
            self.nodesSources = NodesSourcesBook()
        }
        if let data = persistence.data(forKey: Self.importKey) {
            self.importedSource = ImportedSource.decode(data)
        } else {
            self.importedSource = nil
        }
    }

    /// Save a new config (Config editor draft or a validated Settings/
    /// Tailscale edit). Any actual change marks it `modified` — the tunnel
    /// still runs the previous config until Regenerate+Reconnect.
    func save(_ newContent: String) {
        guard newContent != content else { return }
        content = newContent
        persistence.set(newContent, forKey: Self.storageKey)
        transition(.modified)
    }

    /// Commit a staged policy book. The caller has validated every rule
    /// through mobilecore; this only persists and marks the state.
    func commitPolicy(_ book: PolicyBook) {
        policy = book
        if let data = try? book.encoded() {
            persistence.set(data, forKey: Self.policyKey)
        }
        transition(.modified)
    }

    /// Commit staged nodes/sources (validated share links + subscription
    /// metadata). Subscription fetching/decoding stays host-owned.
    func commitNodesSources(_ book: NodesSourcesBook) {
        nodesSources = book
        if let data = try? book.encoded() {
            persistence.set(data, forKey: Self.nodesSourcesKey)
        }
        transition(.modified)
    }

    /// Replace the last-known-good import. Call ONLY after
    /// MobilecoreParseConfContent accepted the content.
    func commitImport(_ source: ImportedSource) {
        importedSource = source
        if let data = try? source.encoded() {
            persistence.set(data, forKey: Self.importKey)
        }
        transition(.modified)
    }

    /// Regenerate + Reconnect on iOS: structurally validate the saved
    /// config, merge the Keychain auth key, then hand it to the running
    /// provider (a reload applies immediately — the iOS Regenerate and
    /// Reconnect collapse into one provider reload).
    ///
    /// Boundaries stated, not faked: the .srs/`sing-box check` GENERATION
    /// runs on the sakamoto host. The Go bridge check here is structural
    /// (mobilecore.ValidateConfigJSON), and the auth key never appears in
    /// the stored config or any action string.
    func regenerateAndApply(tunnel: TunnelControlling) async {
        // In-process structural validation before the provider sees it.
        // Top-level gomobile functions import the trailing NSError** (they
        // are C functions, not methods, so no `throws`).
        var bridgeError: NSError?
        _ = MobilecoreValidateConfigJSON(content, &bridgeError)
        if let bridgeError {
            transition(.regenerateFailed)
            lastAction = "structural check failed: \(bridgeError.localizedDescription)"
            return
        }
        do {
            let content = try TailscaleConfigInjection.inject(
                authKey: keyStore.readAuthKey() ?? "",
                into: self.content
            )
            try await tunnel.reload(configContent: content)
            transition(.regenerateSucceeded)
            transition(.applied)
            lastAction = "applied to the running provider"
        } catch {
            transition(.regenerateFailed)
            lastAction = "regenerate failed: \(error.localizedDescription)"
        }
    }

    private func transition(_ event: ConfigEvent) {
        var bridgeError: NSError?
        let next = MobilecoreConfigStateTransition(configState.rawValue, event.rawValue, &bridgeError)
        if let bridgeError {
            lastAction = "state machine error: \(bridgeError.localizedDescription)"
            return
        }
        configState = ConfigState(rawValue: next) ?? configState
    }
}

enum ConfigEvent: String {
    case modified
    case regenerateSucceeded = "regenerate_succeeded"
    case regenerateFailed = "regenerate_failed"
    case applied
}
