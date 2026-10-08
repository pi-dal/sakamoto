import SwiftUI
import UniformTypeIdentifiers
import Libbox
import SakamotoKit

// Config tab, mirroring the macOS TUI sections (docs/tui.md): Import config,
// Policy, Nodes & sources, and Regenerate → Reconnect — plus the raw
// generated-config editor as an advanced section.
//
// What is REAL on device:
//   * Import config: a Shadowrocket `.conf` URL is fetched by the app and the
//     content is parsed through the Go bridge (mobilecore.ParseConfContent —
//     the same parser the host importer runs). Files App documents are read
//     locally and parsed the same way. A failed fetch or parse keeps the
//     last-known-good import untouched.
//   * Policy rules are validated with the host's exact semantics
//     (mobilecore.NormalizePolicyRule) before staging.
//   * Manual share links are validated with mobilecore.ParseShareLink (same
//     errors as the host's nodes.txt line parser) and staged raw-link-first;
//     rows render masked until revealed.
//   * The saved generated config is structurally checked in-process
//     (mobilecore.ValidateConfigJSON) before a provider reload.
//
// What stays HOST-owned, and the UI says so: .srs rule-set compilation,
// `sing-box check` semantic validation, include/RULE-SET fetching, and
// subscription body decoding. The generator (internal/gen) runs on the
// sakamoto host; the iOS Regenerate + Reconnect action collapses into one
// provider reload with the Keychain auth key injected at start time.

@MainActor
final class ConfigModel: ObservableObject {
    @Published var draft: String

    // Import config
    @Published var importURLText: String = ""
    @Published var importing = false
    @Published var importError: String?
    @Published var importReport: ImportSummary?
    @Published var showFileImporter = false
    @Published var importNodesFile = false
    @Published var nodeImportNotice: String?
    /// Structural-check failure from the advanced editor (never contains
    /// config content — bridge messages are generic).
    @Published var editorError: String?

    // Policy add-form
    @Published var showAddPolicy = false
    @Published var newPolicyMatch = ""
    @Published var newPolicyAction = "proxy"
    @Published var policyFormError: String?

    // Nodes & sources forms + reveal state
    @Published var showAddNode = false
    @Published var newNodeLink = ""
    @Published var nodeFormError: String?
    @Published var showAddSubscription = false
    @Published var newSubscriptionName = ""
    @Published var newSubscriptionURL = ""
    @Published var newSubscriptionFormat = "auto"
    @Published var subscriptionFormError: String?
    @Published var revealedNodeIDs: Set<String> = []
    @Published var revealedSubscriptionIDs: Set<String> = []

    // Pending deletion (second-click confirmation, like the TUI).
    @Published var pendingPolicyDeletion: StagedPolicyRule?
    @Published var pendingNodeDeletion: StagedNode?
    @Published var pendingSubscriptionDeletion: StagedSubscription?

    /// What the import report shows, decoded from the Go bridge's JSON
    /// (mobilecore.ParseConfContentJSON — the shape is asserted Go-side).
    /// Pending entries are masked references, never treated as generated
    /// rules or nodes.
    struct ImportSummary: Equatable, Codable {
        var totalRules: Int32
        var proxyRules: Int32
        var directRules: Int32
        var rejectRules: Int32
        var finalTarget: String
        var hostCount: Int32
        var dnsResolvers: [String]
        var includesPending: [String]
        var ruleSetsPending: [String]
        var unsupported: [String]
    }

    let store: ConfigStore
    private let tunnel: TunnelControlling

    init(tunnel: TunnelControlling, store: ConfigStore) {
        self.tunnel = tunnel
        self.store = store
        self.draft = store.content
    }

    /// Any saved change marks the config `modified` (NeedsRegenerate).
    func saveDraft() {
        // Structural check first: a truncated paste must fail loudly here,
        // not at provider reload. The semantic `sing-box check` stays on the
        // host and the section footer says so.
        var bridgeError: NSError?
        _ = MobilecoreValidateConfigJSON(draft, &bridgeError)
        if let bridgeError {
            editorError = "not saved: \(bridgeError.localizedDescription)"
            return
        }
        editorError = nil
        store.save(draft)
        // Keep the editor text and the saved config identical after save.
        draft = store.content
    }

    /// Fold the saved config into what the tunnel uses.
    func regenerateAndApply() async {
        await store.regenerateAndApply(tunnel: tunnel)
    }

    // MARK: Import config

    func importFromURL() async {
        let source = importURLText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !importing else { return }
        var bridgeError: NSError?
        _ = MobilecoreValidateSourceURL(source, &bridgeError)
        if let bridgeError {
            importError = bridgeError.localizedDescription
            return
        }
        importing = true
        defer { importing = false }
        do {
            guard let url = URL(string: source) else {
                importError = "the source is not a valid URL"
                return
            }
            var request = URLRequest(url: url)
            request.timeoutInterval = 30
            let (data, response) = try await URLSession.shared.data(for: request)
            if let http = response as? HTTPURLResponse, http.statusCode != 200 {
                importError = Self.failureMessage("HTTP \(http.statusCode)", source: source)
                return
            }
            guard data.count <= Self.maxImportBytes else {
                importError = Self.failureMessage("content exceeds the 16 MiB limit", source: source)
                return
            }
            let summary = try importContent(
                String(decoding: data, as: UTF8.self),
                displaySource: source
            )
            importError = nil
            importReport = summary
            importURLText = ""
        } catch {
            importError = Self.failureMessage(Self.reason(of: error), source: source)
        }
    }

    func importFromFile(at url: URL) {
        do {
            let scoped = url.startAccessingSecurityScopedResource()
            defer { if scoped { url.stopAccessingSecurityScopedResource() } }
            var coordinationError: NSError?
            var readResult: Result<Data, Error>?
            NSFileCoordinator().coordinate(readingItemAt: url, options: .withoutChanges, error: &coordinationError) { target in
                readResult = Result { try Data(contentsOf: target) }
            }
            if let coordinationError { throw coordinationError }
            guard let readResult else { throw ImportError(reason: "the content could not be read") }
            let data = try readResult.get()
            if importNodesFile || url.lastPathComponent.lowercased() == "nodes.txt" {
                let nodes = try NodeFileImport.parse(data, validate: Self.validateNode)
                store.commitNodesSources(NodeFileImport.merging(nodes, into: store.nodesSources))
                nodeImportNotice = "Imported \(nodes.count) node links. Existing nodes and sources were kept."
                importError = nil
                return
            }
            guard data.count <= Self.maxImportBytes else {
                importError = Self.failureMessage("content exceeds the 16 MiB limit", source: url.lastPathComponent)
                return
            }
            let content = String(decoding: data, as: UTF8.self)
            if content.trimmingCharacters(in: .whitespacesAndNewlines).hasPrefix("{") {
                var error: NSError?
                _ = MobilecoreValidateConfigJSON(content, &error)
                if let error { throw error }
                _ = LibboxCheckConfig(content, &error)
                if let error { throw error }
                store.save(content)
                draft = store.content
                importError = nil
                return
            }
            let summary = try importContent(content, displaySource: url.lastPathComponent)
            importError = nil
            importReport = summary
        } catch {
            importError = Self.failureMessage(Self.reason(of: error), source: url.lastPathComponent)
        }
    }

    nonisolated static func validateNode(_ link: String) throws -> StagedNode {
        var error: NSError?
        guard let info = MobilecoreParseShareLink(link, &error), error == nil else {
            throw ImportError(reason: "invalid node link")
        }
        return StagedNode(rawLink: link, tag: info.tag, type: info.type, server: info.server)
    }

    /// Parse → report → commit. Throwing keeps the last-known-good import
    /// untouched on any parse failure (docs/import.md: invalid imports do
    /// not replace the last working config).
    func importContent(_ content: String, displaySource: String) throws -> ImportSummary {
        var bridgeError: NSError?
        let reportJSON = MobilecoreParseConfContentJSON(content, &bridgeError)
        if let bridgeError {
            throw ImportError(reason: bridgeError.localizedDescription)
        }
        let summary: ImportSummary
        do {
            summary = try JSONDecoder().decode(ImportSummary.self, from: Data(reportJSON.utf8))
        } catch {
            throw ImportError(reason: "the bridge report could not be decoded")
        }
        store.commitImport(ImportedSource(
            displaySource: SecretMasking.maskSource(displaySource),
            content: content
        ))
        return summary
    }

    /// Error text that can never leak the full source URL (query strings may
    /// carry tokens): network errors are mapped to stable phrasing first.
    static func failureMessage(_ reason: String, source: String) -> String {
        "import failed, last working import kept (\(SecretMasking.maskSource(source))): \(reason)"
    }

    /// Stable phrasing for fetch/read errors — never the raw localized
    /// description, which may embed the full URL with query tokens.
    static func reason(of error: any Error) -> String {
        if let importError = error as? ImportError {
            return importError.reason // bridge text already masks URLs
        }
        if let urlError = error as? URLError {
            switch urlError.code {
            case .timedOut: return "the request timed out"
            case .cannotFindHost, .dnsLookupFailed: return "the host could not be resolved"
            case .notConnectedToInternet: return "the network is unreachable"
            case .secureConnectionFailed, .serverCertificateUntrusted: return "the TLS connection failed"
            default: return "the request failed"
            }
        }
        return "the content could not be read"
    }

    static let maxImportBytes = 16 << 20

    struct ImportError: LocalizedError {
        let reason: String
        var errorDescription: String? { reason }
    }

    // MARK: Policy

    func addPolicyRule() {
        policyFormError = nil
        let match = newPolicyMatch.trimmingCharacters(in: .whitespacesAndNewlines)
        var bridgeError: NSError?
        guard let info = MobilecoreNormalizePolicyRule(match, newPolicyAction, &bridgeError) else {
            policyFormError = bridgeError?.localizedDescription ?? "invalid rule"
            return
        }
        store.commitPolicy(store.policy.adding(StagedPolicyRule(match: info.match, action: info.action)))
        newPolicyMatch = ""
        newPolicyAction = "proxy"
        showAddPolicy = false
    }

    func removePolicyRule(_ rule: StagedPolicyRule) {
        store.commitPolicy(store.policy.removing(id: rule.id))
    }

    // MARK: Nodes & sources

    func addNode() {
        nodeFormError = nil
        let raw = newNodeLink.trimmingCharacters(in: .whitespacesAndNewlines)
        if MobilecoreIsSubLink(raw) {
            nodeFormError = "sub:// links are subscription sources — add the subscription URL under Sources instead"
            return
        }
        var bridgeError: NSError?
        guard let info = MobilecoreParseShareLink(raw, &bridgeError) else {
            nodeFormError = bridgeError?.localizedDescription ?? "unsupported or invalid node share link"
            return
        }
        if store.nodesSources.nodes.contains(where: { $0.rawLink == raw }) {
            nodeFormError = "this share link is already staged"
            return
        }
        store.commitNodesSources(store.nodesSources.adding(node: StagedNode(
            rawLink: raw, tag: info.tag, type: info.type, server: info.server
        )))
        newNodeLink = ""
        showAddNode = false
    }

    func removeNode(_ node: StagedNode) {
        store.commitNodesSources(store.nodesSources.removingNode(id: node.id))
        revealedNodeIDs.remove(node.id)
    }

    func addSubscription() {
        subscriptionFormError = nil
        let name = newSubscriptionName.trimmingCharacters(in: .whitespacesAndNewlines)
        let url = newSubscriptionURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            subscriptionFormError = "give the source a name"
            return
        }
        var bridgeError: NSError?
        _ = MobilecoreValidateSourceURL(url, &bridgeError)
        if let bridgeError {
            subscriptionFormError = bridgeError.localizedDescription
            return
        }
        guard let parsed = URL(string: url), let scheme = parsed.scheme?.lowercased(),
              scheme == "https" || scheme == "http" else {
            subscriptionFormError = "subscriptions need an HTTP(S) URL; fetching runs on the host"
            return
        }
        guard StagedSubscription.formats.contains(newSubscriptionFormat) else {
            subscriptionFormError = "unknown format"
            return
        }
        store.commitNodesSources(store.nodesSources.adding(
            subscription: StagedSubscription(name: name, url: url, format: newSubscriptionFormat)
        ))
        newSubscriptionName = ""
        newSubscriptionURL = ""
        newSubscriptionFormat = "auto"
        showAddSubscription = false
    }

    func removeSubscription(_ subscription: StagedSubscription) {
        store.commitNodesSources(store.nodesSources.removingSubscription(id: subscription.id))
        revealedSubscriptionIDs.remove(subscription.id)
    }
}

struct ConfigView: View {
    @ObservedObject var model: ConfigModel
    @ObservedObject var sync: ICloudSyncModel
    @ObservedObject var s3: S3SyncModel
    @ObservedObject var settings: SettingsModel
    @Environment(\.scenePhase) private var scenePhase
    @State private var showImportOptions = false
    @State private var showURLImport = false
    @State private var showEditor = false
    @State private var confirmApply = false
    @State private var showScanner = false
    @State private var showURLKind = false
    @State private var pendingImport: ImportPayload?
    @State private var confirmTextImport = false
    @State private var payloadError: String?
    /// Foreground clipboard offer: presence-only detection (hasStrings /
    /// hasURLs), deduped by UIPasteboard.changeCount. Payload content is
    /// never read silently — the row embeds a user-directed PasteButton.
    @State private var clipboardOffer: ClipboardOffer?
    @State private var clipboardOfferContent: ClipboardOffer.Content?

    var body: some View {
        List {
            Section("Current configuration") {
                LabeledRow("VPN configuration", model.store.content.isEmpty ? "Not imported" : "Saved on this device")
                if let imported = model.store.importedSource {
                    LabeledRow("Rule source", imported.displaySource)
                }
                LabeledRow("Pending changes", model.store.configState == .clean ? "None" : "Apply required")
            }
            Section {
                Button { showImportOptions = true } label: { Label("Import configuration", systemImage: "square.and.arrow.down") }
                    .disabled(model.importing)
                if let clipboardOfferContent {
                    clipboardOfferRow(content: clipboardOfferContent)
                }
                PasteButton(payloadType: String.self) { strings in
                    guard let text = strings.first else { return }
                    consumeClipboardOffer()
                    receiveImport(text)
                }
                .controlSize(.large)
                .frame(minHeight: 44)
                .accessibilityLabel("Import from clipboard")
                .disabled(model.importing)
                Button { pendingImport = nil; showScanner = true } label: { Label("Scan QR code", systemImage: "qrcode.viewfinder") }
                    .disabled(model.importing)
                if let payloadError { Text(payloadError).font(.footnote).foregroundStyle(.red) }
                if let error = model.importError {
                    Text(error).font(.footnote).foregroundStyle(.red)
                }
            }
            Section("Manage") {
                NavigationLink {
                    List { policySection }
                        .listStyle(.insetGrouped)
                        .navigationTitle("Policy")
                        .navigationBarTitleDisplayMode(.inline)
                } label: { Label("Routing rules", systemImage: "arrow.triangle.branch") }
                NavigationLink {
                    List { nodesSourcesSection }
                        .listStyle(.insetGrouped)
                        .navigationTitle("Nodes & sources")
                        .navigationBarTitleDisplayMode(.inline)
                } label: {
                    HStack {
                        Label("Nodes & sources", systemImage: "network")
                        Spacer()
                        Text("\(model.store.nodesSources.nodes.count) nodes").foregroundStyle(.secondary)
                    }
                }
                NavigationLink("Import details") {
                    List { importSection }
                        .listStyle(.insetGrouped)
                        .navigationTitle("Import details")
                        .navigationBarTitleDisplayMode(.inline)
                }
            }
            Section {
                NavigationLink {
                    SettingsView(model: settings, sync: sync, s3: s3, commanding: nil, sourcesOnly: true)
                } label: {
                    Label("Sync sources", systemImage: "arrow.triangle.2.circlepath")
                }
            } footer: { Text("Import adds files to this device. Sync keeps source files aligned with your Mac or cloud storage.") }
            Section("Apply changes") {
                LabeledRow("Config state", model.store.configState.rawValue)
                Button("Apply saved configuration") { confirmApply = true }
                    .disabled(model.store.configState == .clean)
                Button("Edit generated configuration…") { showEditor = true }
                if let lastAction = model.store.lastAction {
                    DisclosureGroup("Last action") {
                        Text(lastAction).font(.footnote).foregroundStyle(.secondary)
                    }
                }
            }
        }
        .navigationTitle("Config")
        .sakamotoRootPage()
        .onAppear(perform: probeClipboard)
        .onChange(of: scenePhase) { phase in
            if phase == .active { probeClipboard() }
        }
        .sheet(isPresented: $showScanner, onDismiss: processScannedImport) {
            QRScannerView { code in
                do { pendingImport = try ImportPayload.detect(code); payloadError = nil }
                catch { pendingImport = nil; payloadError = "No supported configuration or link found" }
                showScanner = false
            }
        }
        .confirmationDialog("Import link as", isPresented: $showURLKind, titleVisibility: .visible) {
            Button("Configuration") {
                model.importURLText = pendingImport?.text ?? ""
                pendingImport = nil
                showURLImport = true
            }
            Button("Subscription") {
                model.newSubscriptionURL = pendingImport?.text ?? ""
                model.newSubscriptionName = ""
                pendingImport = nil
                model.showAddSubscription = true
            }
            Button("Cancel", role: .cancel) { pendingImport = nil }
        }
        .confirmationDialog("Import configuration?", isPresented: $confirmTextImport, titleVisibility: .visible) {
            Button("Import") { commitTextImport() }
            Button("Cancel", role: .cancel) { pendingImport = nil }
        } message: {
            Text("Save locally. The running tunnel keeps its current configuration until you apply the change.")
        }
        .fileImporter(
            isPresented: $model.showFileImporter,
            allowedContentTypes: [UTType(filenameExtension: "conf") ?? .data, .plainText, .json],
            allowsMultipleSelection: false
        ) { result in
            if case .success(let urls) = result, let url = urls.first {
                model.importFromFile(at: url)
            }
        }
        .sheet(isPresented: $showURLImport) {
            NavigationStack {
                Form {
                    TextField("Shadowrocket .conf URL", text: $model.importURLText)
                        .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    if let error = model.importError { Text(error).font(.footnote).foregroundStyle(.red) }
                }
                .navigationTitle("Import from URL")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { showURLImport = false } }
                    ToolbarItem(placement: .confirmationAction) {
                        Button(model.importing ? "Importing…" : "Import") {
                            Task { await model.importFromURL(); if model.importError == nil { showURLImport = false } }
                        }.disabled(model.importing || model.importURLText.trimmingCharacters(in: .whitespaces).isEmpty)
                    }
                }
            }.presentationDetents([.medium, .large]).presentationDragIndicator(.visible)
        }
        .sheet(isPresented: $showEditor) {
            NavigationStack {
                Form {
                    TextEditor(text: $model.draft).font(.footnote.monospaced()).frame(minHeight: 300)
                        .textInputAutocapitalization(.never).autocorrectionDisabled()
                    if let error = model.editorError { Text(error).foregroundStyle(.red) }
                }
                .navigationTitle("Generated config")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) { Button("Cancel") { showEditor = false } }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Save") { model.saveDraft(); if model.editorError == nil { showEditor = false } }
                    }
                }
            }
        }
        .confirmationDialog("Import configuration", isPresented: $showImportOptions, titleVisibility: .visible) {
            Button("Rule configuration (.conf) from URL") { showURLImport = true }
            Button("Rule configuration (.conf) from Files") { model.importNodesFile = false; model.showFileImporter = true }
            Button("Node list (nodes.txt) from Files") { model.importNodesFile = true; model.showFileImporter = true }
            Button("VPN configuration (JSON) from Files") { model.importNodesFile = false; model.showFileImporter = true }
            Button("Cancel", role: .cancel) {}
        }
        .confirmationDialog("Apply saved configuration?", isPresented: $confirmApply, titleVisibility: .visible) {
            Button("Apply & Reconnect") { Task { await model.regenerateAndApply() } }
            Button("Cancel", role: .cancel) {}
        } message: { Text("The tunnel will reload and active connections may be interrupted. Source generation must be completed on the host first.") }
        .sheet(isPresented: $model.showAddPolicy) { addPolicySheet }
        .sheet(isPresented: $model.showAddNode) { addNodeSheet }
        .sheet(isPresented: $model.showAddSubscription) { addSubscriptionSheet }
        // Second-click confirmations (TUI: deletion requires confirmation).
        .confirmationDialog(
            "Remove policy rule?", isPresented: Binding(
                get: { model.pendingPolicyDeletion != nil },
                set: { if !$0 { model.pendingPolicyDeletion = nil } }
            ), titleVisibility: .visible
        ) {
            Button("Remove", role: .destructive) {
                if let rule = model.pendingPolicyDeletion { model.removePolicyRule(rule) }
                model.pendingPolicyDeletion = nil
            }
        } message: {
            Text(model.pendingPolicyDeletion.map { "\($0.match) → \($0.action)" } ?? "")
        }
        .confirmationDialog(
            "Remove staged node?", isPresented: Binding(
                get: { model.pendingNodeDeletion != nil },
                set: { if !$0 { model.pendingNodeDeletion = nil } }
            ), titleVisibility: .visible
        ) {
            Button("Remove", role: .destructive) {
                if let node = model.pendingNodeDeletion { model.removeNode(node) }
                model.pendingNodeDeletion = nil
            }
        } message: {
            Text(model.pendingNodeDeletion.map { "\($0.tag) (\($0.type))" } ?? "")
        }
        .confirmationDialog(
            "Remove subscription source?", isPresented: Binding(
                get: { model.pendingSubscriptionDeletion != nil },
                set: { if !$0 { model.pendingSubscriptionDeletion = nil } }
            ), titleVisibility: .visible
        ) {
            Button("Remove", role: .destructive) {
                if let sub = model.pendingSubscriptionDeletion { model.removeSubscription(sub) }
                model.pendingSubscriptionDeletion = nil
            }
        } message: {
            Text(model.pendingSubscriptionDeletion.map { SecretMasking.maskSecret($0.url) } ?? "")
        }
    }

    // MARK: Clipboard offer

    /// Presence-only probe: changeCount revision + hasStrings/hasURLs say
    /// whether something is waiting, never what it is. Dismissed revisions
    /// stay suppressed; a new copy (new changeCount) offers again.
    private func probeClipboard() {
        let pasteboard = UIPasteboard.general
        let revision = pasteboard.changeCount
        var offer = clipboardOffer ?? ClipboardOffer()
        guard offer.shouldOffer(revision: revision) else {
            clipboardOffer = offer
            clipboardOfferContent = nil
            return
        }
        if pasteboard.hasURLs { clipboardOfferContent = .link }
        else if pasteboard.hasStrings { clipboardOfferContent = .text }
        else { clipboardOfferContent = nil }
        clipboardOffer = offer
    }

    private func consumeClipboardOffer() {
        var offer = clipboardOffer ?? ClipboardOffer()
        offer.dismiss(revision: UIPasteboard.general.changeCount)
        clipboardOffer = offer
        clipboardOfferContent = nil
    }

    /// Compact offer row: honest wording ("text" / "link" — the payload is
    /// unknown until the user directs the paste), a real PasteButton that
    /// routes through the same import pipeline, and a dismiss affordance.
    private func clipboardOfferRow(content: ClipboardOffer.Content) -> some View {
        HStack(spacing: 12) {
            Text(content == .link
                 ? "A link is on your clipboard — paste to import it"
                 : "Text is on your clipboard — paste to import it")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .lineLimit(2)
            Spacer()
            PasteButton(payloadType: String.self) { strings in
                guard let text = strings.first else { return }
                consumeClipboardOffer()
                receiveImport(text)
            }
            .controlSize(.large)
            .frame(minWidth: 44, minHeight: 44)
            .accessibilityLabel("Paste clipboard import suggestion")
            .disabled(model.importing)
            Button {
                consumeClipboardOffer()
            } label: {
                Image(systemName: "xmark")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .frame(minWidth: 44, minHeight: 44)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.borderless)
            .accessibilityLabel("Dismiss clipboard import suggestion")
        }
    }

    private func receiveImport(_ raw: String) {
        do { pendingImport = try ImportPayload.detect(raw); payloadError = nil; routePendingImport() }
        catch { pendingImport = nil; payloadError = "No supported configuration or link found" }
    }

    private func processScannedImport() {
        guard pendingImport != nil else { return }
        routePendingImport()
    }

    private func routePendingImport() {
        guard let candidate = pendingImport else { return }
        switch candidate.kind {
        case .node:
            var error: NSError?
            _ = MobilecoreParseShareLink(candidate.text, &error)
            guard error == nil else { payloadError = "Invalid node link"; pendingImport = nil; return }
            model.newNodeLink = candidate.text
            model.nodeFormError = nil
            pendingImport = nil
            model.showAddNode = true
        case .url: showURLKind = true
        case .generatedConfig, .conf: confirmTextImport = true
        }
    }

    private func commitTextImport() {
        guard let candidate = pendingImport else { return }
        defer { pendingImport = nil }
        do {
            if candidate.kind == .generatedConfig {
                var error: NSError?
                _ = MobilecoreValidateConfigJSON(candidate.text, &error)
                if let error { throw error }
                _ = LibboxCheckConfig(candidate.text, &error)
                if let error { throw error }
                model.store.save(candidate.text)
                model.draft = model.store.content
            } else {
                model.importReport = try model.importContent(candidate.text, displaySource: "Clipboard or QR code")
                model.importError = nil
            }
            payloadError = nil
        } catch { payloadError = "Invalid configuration. The saved configuration was kept." }
    }

    // MARK: State

    private var stateSection: some View {
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
            Text("Modified configs need Regenerate, then the provider reload to apply. TUN running is not network reachable — Home shows the probed phase.")
        }
    }

    // MARK: Import config

    private var importSection: some View {
        Section {
            if let imported = model.store.importedSource {
                HStack {
                    Text("Last working import")
                    Spacer()
                    Text(imported.displaySource)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            if let error = model.importError {
                Text(error)
                    .font(.footnote)
                    .foregroundStyle(.red)
            }
            if let report = model.importReport {
                importReportRows(report)
            }
            Button("Import configuration…") { showImportOptions = true }.disabled(model.importing)
        } header: {
            Text("Import config")
        } footer: {
            Text("The .conf is parsed in-process with the host's parser. Relative includes and remote RULE-SETs are staged as pending references — the sakamoto host merges and fetches them during Regenerate. A failed import keeps the last working import.")
        }
    }

    private func importReportRows(_ report: ConfigModel.ImportSummary) -> some View {
        Group {
            LabeledRow("Rules", "\(report.totalRules) (proxy \(report.proxyRules) · direct \(report.directRules) · reject \(report.rejectRules))")
            if !report.finalTarget.isEmpty {
                LabeledRow("Final", report.finalTarget)
            }
            if report.hostCount > 0 {
                LabeledRow("[Host] overrides", "\(report.hostCount)")
            }
            if !report.dnsResolvers.isEmpty {
                LabeledRow("DNS resolvers", report.dnsResolvers.joined(separator: ", "))
            }
            ForEach(report.includesPending, id: \.self) { Text("Include pending: \(SecretMasking.maskSource($0))").font(.footnote) }
            ForEach(report.ruleSetsPending, id: \.self) { Text("RULE-SET pending: \(SecretMasking.maskSource($0))").font(.footnote) }
            ForEach(report.unsupported, id: \.self) { Text($0).font(.footnote).foregroundStyle(.orange) }
        }
    }

    // MARK: Policy

    private var policySection: some View {
        Section {
            ForEach(model.store.policy.rules) { rule in
                Button {
                    model.pendingPolicyDeletion = rule
                } label: {
                    HStack {
                        Text(rule.match)
                            .foregroundStyle(.primary)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Spacer()
                        Text(rule.action)
                            .font(.caption.weight(.semibold))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(actionColor(rule.action).opacity(0.15), in: Capsule())
                            .foregroundStyle(actionColor(rule.action))
                    }
                }
            }
            .onDelete { offsets in
                for index in offsets where index < model.store.policy.rules.count {
                    model.pendingPolicyDeletion = model.store.policy.rules[index]
                }
            }
            Button("Add rule…") { model.showAddPolicy = true }
        } header: {
            Text("Policy")
        } footer: {
            Text("Hostname (exact), *.suffix, keyword:, cidr: and http(s) URLs (host only) with proxy / direct / reject — validated with the host's rules. The host folds these in during Regenerate; reconnect applies them.")
        }
    }

    private var addPolicySheet: some View {
        NavigationStack {
            Form {
                TextField("Match (host, *.suffix, keyword:, cidr:, URL)", text: $model.newPolicyMatch, axis: .vertical)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                Picker("Action", selection: $model.newPolicyAction) {
                    ForEach(["proxy", "direct", "reject"], id: \.self) { Text($0).tag($0) }
                }
                .pickerStyle(.segmented)
                if let error = model.policyFormError {
                    Text(error).font(.footnote).foregroundStyle(.red)
                }
            }
            .navigationTitle("Add policy rule")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { model.showAddPolicy = false }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add") { model.addPolicyRule() }
                }
            }
        }
        .presentationDetents([.medium])
    }

    // MARK: Nodes & sources

    private var nodesSourcesSection: some View {
        Section {
            if model.store.nodesSources.nodes.isEmpty && model.store.nodesSources.subscriptions.isEmpty {
                Text("No manual nodes or sources staged.").font(.footnote).foregroundStyle(.secondary)
            }
            ForEach(model.store.nodesSources.nodes) { node in
                Button {
                    model.pendingNodeDeletion = node
                } label: {
                    nodeRow(node)
                }
            }
            .onDelete { offsets in
                for index in offsets where index < model.store.nodesSources.nodes.count {
                    model.pendingNodeDeletion = model.store.nodesSources.nodes[index]
                }
            }
            ForEach(model.store.nodesSources.subscriptions) { subscription in
                Button {
                    model.pendingSubscriptionDeletion = subscription
                } label: {
                    subscriptionRow(subscription)
                }
            }
            .onDelete { offsets in
                for index in offsets where index < model.store.nodesSources.subscriptions.count {
                    model.pendingSubscriptionDeletion = model.store.nodesSources.subscriptions[index]
                }
            }
            Button("Import nodes.txt from Files…") { model.importNodesFile = true; model.showFileImporter = true }
            if let notice = model.nodeImportNotice { Text(notice).font(.footnote).foregroundStyle(.secondary) }
            if let error = model.importError { Text(error).font(.footnote).foregroundStyle(.red) }
            Button("Add node (share link)…") { model.showAddNode = true }
            Button("Add subscription source…") { model.showAddSubscription = true }
        } header: {
            Text("Nodes & sources")
        } footer: {
            Text("Links and subscription URLs are masked until revealed and never appear in logs. Subscription bodies are fetched and decoded on the sakamoto host during Regenerate — staged sources stay pending here, never fake nodes.")
        }
    }

    private func nodeRow(_ node: StagedNode) -> some View {
        let revealed = model.revealedNodeIDs.contains(node.id)
        return HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(node.tag).foregroundStyle(.primary).lineLimit(1)
                Text(revealed ? node.rawLink : SecretMasking.maskSecret(node.rawLink))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            Spacer()
            Text(node.type).font(.caption).foregroundStyle(.secondary)
            Button {
                if revealed { model.revealedNodeIDs.remove(node.id) } else { model.revealedNodeIDs.insert(node.id) }
            } label: {
                Image(systemName: revealed ? "eye.slash" : "eye")
            }
            .buttonStyle(.borderless)
        }
    }

    private func subscriptionRow(_ subscription: StagedSubscription) -> some View {
        let revealed = model.revealedSubscriptionIDs.contains(subscription.id)
        return HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(subscription.name).foregroundStyle(.primary).lineLimit(1)
                Text(revealed ? subscription.url : SecretMasking.maskSecret(subscription.url))
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text("Pending — fetched on the host")
                    .font(.caption2)
                    .foregroundStyle(.orange)
            }
            Spacer()
            Text(subscription.format).font(.caption).foregroundStyle(.secondary)
            Button {
                if revealed { model.revealedSubscriptionIDs.remove(subscription.id) } else { model.revealedSubscriptionIDs.insert(subscription.id) }
            } label: {
                Image(systemName: revealed ? "eye.slash" : "eye")
            }
            .buttonStyle(.borderless)
        }
    }

    private var addNodeSheet: some View {
        NavigationStack {
            Form {
                TextField("vless://… / vmess://… / hy2://… / tuic://…", text: $model.newNodeLink, axis: .vertical)
                    .font(.footnote.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                if let error = model.nodeFormError {
                    Text(error).font(.footnote).foregroundStyle(.red)
                }
            }
            .navigationTitle("Add manual node")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { model.showAddNode = false }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add") { model.addNode() }
                }
            }
        }
        .presentationDetents([.medium])
    }

    private var addSubscriptionSheet: some View {
        NavigationStack {
            Form {
                TextField("Name", text: $model.newSubscriptionName)
                TextField("https://subscription URL", text: $model.newSubscriptionURL)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                Picker("Format", selection: $model.newSubscriptionFormat) {
                    ForEach(StagedSubscription.formats, id: \.self) { Text($0).tag($0) }
                }
                if let error = model.subscriptionFormError {
                    Text(error).font(.footnote).foregroundStyle(.red)
                }
            }
            .navigationTitle("Add subscription source")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { model.showAddSubscription = false }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Add") { model.addSubscription() }
                }
            }
        }
        .presentationDetents([.medium])
    }

    // MARK: Generate / Apply

    private var generateSection: some View {
        Section {
            Button("Regenerate + Reconnect") {
                confirmApply = true
            }
            .disabled(model.store.configState == .clean)
        } header: {
            Text("Generate / Apply")
        } footer: {
            Text("Generation (.srs rule sets, sing-box check) runs on the sakamoto host — this screen validates the saved config structurally, injects the Tailscale auth key from the Keychain at start, and reloads the provider. Regenerate on the host after staging imports, policy or nodes here.")
        }
    }

    // MARK: Advanced (raw generated config)

    private var advancedSection: some View {
        Section {
            if let error = model.editorError {
                Text(error).font(.footnote).foregroundStyle(.red)
            }
            Button("Edit generated configuration…") { showEditor = true }
        } header: {
            Text("sing-box config (advanced)")
        } footer: {
            Text("Endpoints/outbounds JSON produced by the host generator. The Tailscale auth key is injected from the Keychain at start time and is never stored in this text. Saving runs the structural check; semantic validation stays on the host.")
        }
    }

    private func actionColor(_ action: String) -> Color {
        switch action {
        case "proxy": return .blue
        case "direct": return .green
        case "reject": return .red
        default: return .secondary
        }
    }
}

struct LabeledRow: View {
    let label: String
    let value: String

    init(_ label: String, _ value: String) {
        self.label = label
        self.value = value
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(label)
            Spacer()
            Text(value)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.trailing)
        }
    }
}
