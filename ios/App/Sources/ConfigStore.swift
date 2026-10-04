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

@MainActor
final class ConfigStore: ObservableObject {
    static let storageKey = "sakamoto.config.content"

    /// The last saved config — what Connect and Regenerate+Reconnect use.
    @Published private(set) var content: String
    @Published private(set) var configState: ConfigState = .clean
    @Published private(set) var lastAction: String?

    private let persistence: UserDefaults
    private let keyStore: TailscaleAuthKeyStoring

    init(persistence: UserDefaults = .standard, keyStore: TailscaleAuthKeyStoring) {
        self.persistence = persistence
        self.keyStore = keyStore
        self.content = persistence.string(forKey: Self.storageKey) ?? ""
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

    /// Regenerate + Reconnect on iOS: validate + merge the Keychain auth
    /// key into the saved config, then hand it to the running provider.
    /// The generator itself runs on the sakamoto host — that boundary is
    /// stated in the UI, not faked here.
    func regenerateAndApply(tunnel: TunnelControlling) async {
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
        // Top-level gomobile functions import the trailing NSError** as an
        // NSErrorPointer (they are C functions, not methods, so no `throws`).
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
