import SwiftUI
import Combine
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
// Generation reuses the host pipeline on device through Mobilegen, including
// subscription decoding, remote rule fetching and binary rule compilation.
// Libbox performs semantic validation before the store commits a candidate.
// Apply starts or reloads the VPN with Keychain authentication.

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
    private var storeUpdates: AnyCancellable?

    init(tunnel: TunnelControlling, store: ConfigStore) {
        self.tunnel = tunnel
        self.store = store
        self.draft = store.content
        storeUpdates = store.objectWillChange.sink { [weak self] _ in self?.objectWillChange.send() }
    }

    /// Any saved change marks the config `modified` (NeedsRegenerate).
    func saveDraft() {
        // Structural check before saving an advanced edit. Apply additionally
        // performs Libbox semantic validation before reloading the provider.
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
            if url.pathExtension.lowercased() == "sakamoto" {
                try store.installProfile(TunnelProfile.decodePackage(data))
                draft = store.content
                importError = nil
                return
            }
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
                try store.installProfile(TunnelProfile(name: url.deletingPathExtension().lastPathComponent, config: content))
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
        if let profileError = error as? TunnelProfile.InvalidProfile { return profileError.localizedDescription }
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
            subscriptionFormError = "subscriptions need an HTTP(S) URL"
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
    @State private var showImportOptions = false
    @State private var showURLImport = false
    @State private var showEditor = false
    @State private var showScanner = false
    @State private var showURLKind = false
    @State private var pendingImport: ImportPayload?
    @State private var confirmTextImport = false
    @State private var payloadError: String?
    @State private var showClipboardImport = false
    @State private var clipboardDraft = ""

    var body: some View {
        List {
            Section {
                if model.store.profiles.count > 1 {
                    Picker("Configuration", selection: Binding(
                        get: { model.store.selectedProfileID ?? "" },
                        set: { id in
                            do { try model.store.selectProfile(id); model.draft = model.store.content; model.importError = nil }
                            catch { model.importError = error.localizedDescription }
                        }
                    )) {
                        ForEach(model.store.profiles) { profile in Text(profile.name).tag(profile.id) }
                    }
                    .pickerStyle(.menu)
                    .sakamotoInspectTag("ConfigurationPicker")
                } else {
                    LabeledRow("Configuration", model.store.selectedProfile?.name ?? "No sources yet")
                }
                if model.store.selectedProfile?.sourcesChanged == true {
                    Text("Changes are ready to apply.").font(.footnote).foregroundStyle(.secondary)
                }
            }
            Section {
                NavigationLink {
                    ConfSourceView(store: model.store)
                } label: {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("Rule source (.conf)")
                        Text(model.store.sourceBundle.mainConf.isEmpty ? "Choose .conf" : URL(fileURLWithPath: model.store.sourceBundle.mainConf).lastPathComponent)
                            .font(.subheadline).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                }
            }
            Section {
                Button { showImportOptions = true } label: { Label("Add sources", systemImage: "plus") }
                    .sakamotoInspectTag("ConfigurationImport")
                    .disabled(model.importing)
                if let payloadError { Text(payloadError).font(.footnote).foregroundStyle(.primary) }
                if let error = model.importError {
                    Text(error).font(.footnote).foregroundStyle(.primary)
                }
            }
            Section("Manage") {
                NavigationLink {
                    RoutingRulesView(store: model.store) {
                        List { policySection }
                            .listStyle(.insetGrouped)
                            .navigationTitle("Custom overrides")
                    }
                } label: { Label("Routing rules", systemImage: "arrow.triangle.branch") }
                NavigationLink { ProxyChainView(store: model.store) } label: { Label("Proxy chain", systemImage: "point.3.connected.trianglepath.dotted") }
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
            }
            Section {
                NavigationLink {
                    SettingsView(model: settings, sync: sync, s3: s3, commanding: nil, sourcesOnly: true)
                } label: {
                    Label("Sync sources", systemImage: "arrow.triangle.2.circlepath")
                }
            } footer: { Text("Keep sources aligned with your Mac or cloud storage.") }
            Section {
                Button {
                    Task { await model.regenerateAndApply() }
                } label: {
                    HStack {
                        Text(model.store.applying ? "Applying…" : "Apply changes")
                        if model.store.applying { Spacer(); ProgressView() }
                    }
                }
                .sakamotoInspectTag("ConfigurationApply")
                .disabled(model.store.applying || model.store.generating || model.store.configState == .clean || (!model.store.canConnect && !model.store.canGenerate))
                if let lastAction = model.store.lastAction {
                    Text(lastAction).font(.footnote).foregroundStyle(.secondary).textSelection(.enabled)
                }
            } footer: { Text("Builds the configuration and starts or reloads the VPN. Active connections may be interrupted.") }
            Section {
                NavigationLink("Advanced") { advancedConfiguration }
            }
        }
        .navigationTitle("Config")
        .sakamotoRootPage()
        .sheet(isPresented: $showClipboardImport, onDismiss: {
            clipboardDraft = ""
            routePendingImport()
        }) {
            NavigationStack {
                Form {
                    PasteButton(payloadType: String.self) { strings in
                        clipboardDraft = strings.first ?? ""
                        payloadError = nil
                    }
                    .accessibilityLabel("Paste source text")
                    TextEditor(text: $clipboardDraft)
                        .frame(minHeight: 160)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityLabel("Source text")
                    if let payloadError { Text(payloadError).font(.footnote) }
                }
                .navigationTitle("Import text")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { pendingImport = nil; showClipboardImport = false }
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button("Import") {
                            do {
                                pendingImport = try ImportPayload.detect(clipboardDraft)
                                payloadError = nil
                                showClipboardImport = false
                            } catch {
                                pendingImport = nil
                                payloadError = "No supported configuration or link found"
                            }
                        }.disabled(clipboardDraft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                }
            }
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
            allowedContentTypes: [.data, .plainText, .json],
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
                    if let error = model.importError { Text(error).font(.footnote).foregroundStyle(.primary) }
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
                    if let error = model.editorError { Text(error).foregroundStyle(.primary) }
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
        .confirmationDialog("Add sources", isPresented: $showImportOptions, titleVisibility: .visible) {
            Button("From Files…") { model.importNodesFile = false; model.showFileImporter = true }
            Button("From URL…") { showURLImport = true }
            Button("From clipboard…") {
                pendingImport = nil; payloadError = nil; clipboardDraft = ""; showClipboardImport = true
            }
            Button("Scan QR code") { pendingImport = nil; showScanner = true }
            Button("Cancel", role: .cancel) {}
        }
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
                try model.store.installProfile(TunnelProfile(name: "Clipboard configuration", config: candidate.text))
                model.draft = model.store.content
            } else {
                model.importReport = try model.importContent(candidate.text, displaySource: "Clipboard or QR code")
                model.importError = nil
            }
            payloadError = nil
        } catch { payloadError = TunnelDiagnostics.sanitized(error.localizedDescription) + " The saved configuration was kept." }
    }

    // MARK: State

    private var stateSection: some View {
        Section {
            HStack {
                Text("Config state")
                Spacer()
                Text(model.store.configState.rawValue)
                    .foregroundStyle(model.store.configState == .clean ? Color.primary : Color.secondary)
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
                    .foregroundStyle(.primary)
            }
            if let report = model.importReport {
                importReportRows(report)
            }
            Button("Import configuration…") { showImportOptions = true }.disabled(model.importing)
        } header: {
            Text("Import config")
        } footer: {
            Text("Rules use the shared parser. Generate combines local includes, fetches remote rule lists and compiles the runtime on this device. Missing local includes keep the previous configuration.")
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
            ForEach(report.unsupported, id: \.self) { Text($0).font(.footnote).foregroundStyle(.secondary) }
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
            Text("Hostname (exact), *.suffix, keyword:, cidr: and http(s) URLs (host only) with proxy / direct / reject — validated with the host's rules. Generate includes these rules; Apply activates them.")
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
                    Text(error).font(.footnote).foregroundStyle(.primary)
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
            if let error = model.importError { Text(error).font(.footnote).foregroundStyle(.primary) }
            Button("Add node (share link)…") { model.showAddNode = true }
            Button("Add subscription source…") { model.showAddSubscription = true }
        } header: {
            Text("Nodes & sources")
        } footer: {
            Text("Links and subscription URLs are masked until revealed and never appear in logs. Generate fetches and decodes subscriptions on this device; Apply activates the generated nodes.")
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
                Text("Fetched when generating")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
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
                    Text(error).font(.footnote).foregroundStyle(.primary)
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
                    Text(error).font(.footnote).foregroundStyle(.primary)
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

    private var advancedConfiguration: some View {
        List {
            if !model.store.confSources.isEmpty {
                Section("Rules") {
                    Picker("Rule source", selection: Binding(get: { model.store.sourceBundle.mainConf }, set: { name in
                        do { try model.store.selectConfSource(name); model.importError = nil }
                        catch { model.importError = TunnelDiagnostics.sanitized(error.localizedDescription) }
                    })) {
                        Text("Choose rule source").tag("")
                        ForEach(model.store.confSources, id: \.self) { Text($0).tag($0) }
                    }.pickerStyle(.menu)
                }
            }
            Section {
                Button("Generate without connecting") {
                    Task {
                        do { try await model.store.generateFromSources(); model.draft = model.store.content; model.importError = nil }
                        catch { model.importError = TunnelDiagnostics.sanitized(error.localizedDescription) }
                    }
                }.disabled(!model.store.canGenerate || model.store.applying)
                Button("Edit configuration JSON…") { model.draft = model.store.content; showEditor = true }
                NavigationLink("Import details") { List { importSection }.navigationTitle("Import details") }
            }
            Section("Diagnostics") {
                LabeledRow("Config state", model.store.configState.rawValue)
                if let failure = TunnelDiagnostics.latest() { Text("\(failure.stage): \(failure.message)").font(.footnote) }
                if let error = model.importError { Text(error).font(.footnote) }
            }
        }
        .navigationTitle("Advanced")
        .navigationBarTitleDisplayMode(.inline)
    }

    // MARK: Generate / Apply

    // MARK: Advanced (raw generated config)

    private var advancedSection: some View {
        Section {
            if let error = model.editorError {
                Text(error).font(.footnote).foregroundStyle(.primary)
            }
            Button("Edit generated configuration…") { model.draft = model.store.content; showEditor = true }
        } header: {
            Text("sing-box config (advanced)")
        } footer: {
            Text("Endpoints/outbounds JSON produced by the host generator. The Tailscale auth key is injected from the Keychain at start time and is never stored in this text. Saving runs a structural check; Apply also validates with Libbox.")
        }
    }

    private func actionColor(_ action: String) -> Color {
        switch action {
        case "proxy": return .blue
        case "direct": return .primary
        case "reject": return .primary
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
