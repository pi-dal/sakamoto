import SwiftUI
import SakamotoKit

struct NodesSourcesView: View {
    @ObservedObject var model: ConfigModel
    @State private var removal: SourceRemoval?
    @State private var error: String?
    private struct SourceRemoval {
        let node: StagedNode?
        let source: StagedSubscription?
        let profileID: String?
        var name: String { node?.tag ?? source?.name ?? "" }
    }
    private var savedNodes: [[String: Any]] {
        ProxyGroupEditing.outbounds(model.store.content).filter {
            !["selector", "urltest", "direct", "block", "dns"].contains($0["type"] as? String ?? "")
        }
    }
    var body: some View {
        List {
            Section {
                ForEach(model.store.nodesSources.nodes) { node in
                    HStack {
                        NavigationLink { SourceNodeForm(model: model, original: node) } label: {
                            VStack(alignment: .leading, spacing: 4) {
                                Text(node.tag)
                                Text("\(node.type) · \(node.server)").font(.caption).foregroundStyle(.secondary)
                            }
                        }.accessibilityIdentifier("SourceNode-\(node.tag)")
                        Button(role: .destructive) { removal = SourceRemoval(node: node, source: nil, profileID: model.store.selectedProfileID) } label: { Image(systemName: "trash").frame(minWidth: 44, minHeight: 44) }
                            .buttonStyle(.borderless).accessibilityLabel("Delete \(node.tag)").accessibilityIdentifier("DeleteSourceNode-\(node.tag)")
                    }
                    .swipeActions(allowsFullSwipe: false) {
                        Button("Delete", role: .destructive) { removal = SourceRemoval(node: node, source: nil, profileID: model.store.selectedProfileID) }
                    }
                }
                NavigationLink { SourceNodeForm(model: model, original: nil) } label: { Label("Add node", systemImage: "plus") }
                Button("Import nodes.txt from Files…") { model.importNodesFile = true; model.showFileImporter = true }
            } header: { Text("Manual nodes") } footer: { Text("Tap a node to edit its share link or saved settings. Changes stay local until Apply changes.") }
            if !savedNodes.isEmpty {
                Section {
                    NavigationLink { SavedConfigNodesView(store: model.store) } label: { LabeledContent("Saved config nodes", value: "\(savedNodes.count)") }
                } footer: { Text("Edit nodes from the saved configuration, including subscription and imported nodes. Source changes appear here after Apply changes.") }
            }
            Section {
                ForEach(model.store.nodesSources.subscriptions) { source in
                    HStack {
                        NavigationLink { SubscriptionSourceForm(model: model, original: source) } label: {
                            VStack(alignment: .leading, spacing: 4) { Text(source.name); Text(source.format).font(.caption).foregroundStyle(.secondary) }
                        }
                        Button(role: .destructive) { removal = SourceRemoval(node: nil, source: source, profileID: model.store.selectedProfileID) } label: { Image(systemName: "trash").frame(minWidth: 44, minHeight: 44) }
                            .buttonStyle(.borderless).accessibilityLabel("Delete \(source.name)")
                    }
                    .swipeActions(allowsFullSwipe: false) {
                        Button("Delete", role: .destructive) { removal = SourceRemoval(node: nil, source: source, profileID: model.store.selectedProfileID) }
                    }
                }
                NavigationLink { SubscriptionSourceForm(model: model, original: nil) } label: { Label("Add subscription", systemImage: "plus") }
            } header: { Text("Subscription sources") }
            if let notice = model.nodeImportNotice { Text(notice).font(.callout).foregroundStyle(.secondary) }
            if let error { Text(error).font(.callout) }
        }
        .navigationTitle("Nodes & sources").navigationBarTitleDisplayMode(.inline)
        .alert("Delete source?", isPresented: Binding(get: { removal != nil }, set: { if !$0 { removal = nil } }), presenting: removal) { selection in
            Button("Delete", role: .destructive) { delete(selection) }.disabled(blockedDeletion(selection) != nil)
            Button("Cancel", role: .cancel) { removal = nil }
        } message: { selection in Text(blockedDeletion(selection) ?? "Remove \(selection.name) from this configuration? The running VPN keeps its current configuration until you apply changes.") }
    }
    private func blockedDeletion(_ selection: SourceRemoval) -> String? {
        guard let node = selection.node, let group = model.store.selectedProfile?.proxyGroups?.first(where: { $0.members == [node.tag] }) else { return nil }
        return "\(node.tag) is the last member of \(group.tag). Choose another member in Proxy groups before deleting it."
    }
    private func delete(_ selection: SourceRemoval) {
        defer { removal = nil }
        do {
            guard model.store.selectedProfileID == selection.profileID, !model.store.applying, !model.store.generating else { throw TunnelProfile.InvalidProfile("Configuration changed. Reopen this page before deleting.") }
            if let node = selection.node { try model.removeNode(node) }
            if let source = selection.source {
                guard model.store.nodesSources.subscriptions.contains(source) else { throw TunnelProfile.InvalidProfile("This source changed or was removed. Reopen the list.") }
                model.removeSubscription(source)
            }
        } catch { self.error = TunnelDiagnostics.sanitized(error.localizedDescription) }
    }
}

struct SavedConfigNodesView: View {
    @ObservedObject var store: ConfigStore
    var body: some View {
        List {
            ForEach(ProxyGroupEditing.outbounds(store.content).filter { !["selector", "urltest", "direct", "block", "dns"].contains($0["type"] as? String ?? "") }.compactMap { $0["tag"] as? String }, id: \.self) { tag in
                NavigationLink { SavedNodeEditor(store: store, tag: tag) } label: { Text(tag) }
            }
        }.navigationTitle("Saved config nodes").navigationBarTitleDisplayMode(.inline)
    }
}

struct SourceNodeForm: View {
    @ObservedObject var model: ConfigModel
    let original: StagedNode?
    private let profileID: String?
    @State private var link: String
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss
    init(model: ConfigModel, original: StagedNode?) {
        self.model = model; self.original = original; profileID = model.store.selectedProfileID
        _link = State(initialValue: original?.rawLink ?? "")
    }
    var body: some View {
        Form {
            Section("Share link") {
                TextEditor(text: $link).frame(minHeight: 150).textInputAutocapitalization(.never).autocorrectionDisabled().privacySensitive().accessibilityLabel("Node share link").accessibilityIdentifier("NodeShareLink")
            }
            if let original, model.store.nodesSources.nodes.filter({ $0.tag == original.tag }).count == 1, ProxyGroupEditing.outbounds(model.store.content).contains(where: { $0["tag"] as? String == original.tag }) {
                Section { NavigationLink("Edit server, credentials & TLS") { SavedNodeEditor(store: model.store, tag: original.tag) } }
            }
            if let error { Text(error).font(.callout) }
            Section { Text("Use the share link from your provider. Saving validates it and keeps the node’s place in the list. Apply changes to generate and use the edited source.").font(.callout).foregroundStyle(.secondary) }
        }
        .navigationTitle(original == nil ? "Add node" : "Edit node").navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Save") {
            do { try model.saveSourceNode(link, original: original, profileID: profileID); dismiss() }
            catch { self.error = TunnelDiagnostics.sanitized(error.localizedDescription) }
        }.accessibilityIdentifier("SourceNodeSave").disabled(link.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.store.applying || model.store.generating) } }
    }
}

struct SubscriptionSourceForm: View {
    @ObservedObject var model: ConfigModel
    let original: StagedSubscription?
    private let profileID: String?
    @State private var name: String
    @State private var url: String
    @State private var format: String
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss
    init(model: ConfigModel, original: StagedSubscription?) {
        self.model = model; self.original = original; profileID = model.store.selectedProfileID
        _name = State(initialValue: original?.name ?? ""); _url = State(initialValue: original?.url ?? ""); _format = State(initialValue: original?.format ?? "auto")
    }
    var body: some View {
        Form {
            Section("Subscription") {
                TextField("Name", text: $name)
                TextField("HTTP(S) URL", text: $url, axis: .vertical).keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled().privacySensitive()
                Picker("Format", selection: $format) { ForEach(StagedSubscription.formats, id: \.self) { Text($0).tag($0) } }
            }
            if let error { Text(error).font(.callout) }
            Text("Save stages this source. Apply changes fetches it and updates the configuration.").font(.callout).foregroundStyle(.secondary)
        }.navigationTitle(original == nil ? "Add subscription" : "Edit subscription").navigationBarTitleDisplayMode(.inline)
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Save") {
            do { try model.saveSourceSubscription(name: name, url: url, format: format, original: original, profileID: profileID); dismiss() }
            catch { self.error = TunnelDiagnostics.sanitized(error.localizedDescription) }
        }.disabled(name.isEmpty || url.isEmpty || model.store.applying || model.store.generating) } }
    }
}

struct SavedNodeEditor: View {
    @ObservedObject var store: ConfigStore
    let tag: String
    private let baseline: String
    private let profileID: String?
    private let original: [String: Any]
    private let specs: [Field]
    private struct Field: Identifiable {
        let path: String; let label: String; let section: String; let kind: String
        var id: String { path }
    }
    @State private var values: [String: String]
    @State private var reveal = false
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss
    init(store: ConfigStore, tag: String) {
        self.store = store; self.tag = tag; baseline = store.content; profileID = store.selectedProfileID
        let row = ProxyGroupEditing.outbounds(store.content).first { $0["tag"] as? String == tag } ?? [:]; original = row
        var plan = [Field(path: "server", label: "Server", section: "Server", kind: "text"), Field(path: "server_port", label: "Port", section: "Server", kind: "number")]
        for key in ["username", "password", "uuid", "method", "auth_str", "token", "private_key", "peer_public_key"] where row[key] is String {
            plan.append(Field(path: key, label: key.replacingOccurrences(of: "_", with: " ").capitalized, section: "Authentication", kind: ["method", "username"].contains(key) ? "text" : "secret"))
        }
        if row["tls"] != nil {
            plan += [Field(path: "tls.server_name", label: "Server name (SNI)", section: "TLS", kind: "text"), Field(path: "tls.insecure", label: "Skip certificate verification", section: "TLS", kind: "bool")]
            for (path, label) in [("tls.utls.fingerprint", "TLS fingerprint"), ("tls.reality.public_key", "Reality public key"), ("tls.reality.short_id", "Reality short ID")] where Self.read(row, path) != nil {
                plan.append(Field(path: path, label: label, section: "TLS", kind: "text"))
            }
        }
        for (path, label) in [("transport.path", "Path"), ("transport.headers.Host", "Host header"), ("transport.service_name", "Service name")] where Self.read(row, path) is String {
            plan.append(Field(path: path, label: label, section: "Transport", kind: "text"))
        }
        specs = plan
        _values = State(initialValue: Dictionary(uniqueKeysWithValues: plan.map { field in
            let value = Self.read(row, field.path)
            return (field.path, field.kind == "bool" ? ((value as? Bool ?? false) ? "true" : "false") : value.map { String(describing: $0) } ?? "")
        }))
    }
    private static func read(_ row: [String: Any], _ path: String) -> Any? {
        var value: Any = row
        for component in path.split(separator: ".") { guard let next = (value as? [String: Any])?[String(component)] else { return nil }; value = next }
        return value
    }
    private static func set(_ value: Any?, path: ArraySlice<String>, row: inout [String: Any]) {
        guard let key = path.first else { return }
        if path.count == 1 { row[key] = value; return }
        var child = row[key] as? [String: Any] ?? [:]
        set(value, path: path.dropFirst(), row: &child); row[key] = child
    }
    var body: some View {
        Form {
            Section { LabeledContent("Node", value: tag); LabeledContent("Protocol", value: original["type"] as? String ?? "") }
            ForEach(["Server", "Authentication", "TLS", "Transport"], id: \.self) { section in
                let fields = specs.filter { $0.section == section }
                if !fields.isEmpty {
                    Section(section) {
                        ForEach(fields) { field in
                            let binding = Binding(get: { values[field.path] ?? "" }, set: { values[field.path] = $0 })
                            if field.kind == "bool" { Toggle(field.label, isOn: Binding(get: { values[field.path] == "true" }, set: { values[field.path] = $0 ? "true" : "false" })) }
                            else {
                                LabeledContent(field.label) {
                                    if field.kind == "secret" && !reveal { SecureField("", text: binding).accessibilityLabel(field.label).textInputAutocapitalization(.never).autocorrectionDisabled().multilineTextAlignment(.trailing) }
                                    else { TextField("", text: binding).accessibilityLabel(field.label).keyboardType(field.kind == "number" ? .numberPad : .default).textInputAutocapitalization(.never).autocorrectionDisabled().multilineTextAlignment(.trailing).accessibilityIdentifier("NodeField-\(field.path)") }
                                }
                            }
                        }
                        if section == "Authentication" { Toggle("Show credentials", isOn: $reveal) }
                    }.privacySensitive()
                }
            }
            if let error { Text(error).font(.callout) }
            Section { } footer: { Text("These settings stay on this device and are kept when sources refresh. Editing the original share link replaces them. Apply changes to activate.") }
        }.navigationTitle("Edit node settings").navigationBarTitleDisplayMode(.inline)
        .alert("Couldn’t save node", isPresented: Binding(get: { error != nil }, set: { if !$0 { error = nil } })) {
            Button("OK", role: .cancel) { error = nil }
        } message: { Text(error ?? "") }
        .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Save") { save() }.accessibilityIdentifier("NodeSettingsSave").disabled(store.generating || store.applying || original.isEmpty) } }
    }
    private func save() {
        do {
            var node = original
            for field in specs {
                let value = values[field.path] ?? ""
                let replacement: Any?
                if field.kind == "number" { guard let port = Int(value), (1...65535).contains(port) else { throw TunnelProfile.InvalidProfile("Port must be 1–65535.") }; replacement = port }
                else if field.kind == "bool" { replacement = value == "true" }
                else { replacement = value.isEmpty ? nil : value }
                Self.set(replacement, path: ArraySlice(field.path.components(separatedBy: ".")), row: &node)
            }
            let json = String(decoding: try JSONSerialization.data(withJSONObject: node, options: [.sortedKeys]), as: UTF8.self)
            try store.saveNodeSettings(tag: tag, nodeJSON: json, expectedProfile: profileID, expectedContent: baseline)
            dismiss()
        } catch { self.error = TunnelDiagnostics.sanitized(error.localizedDescription) }
    }
}
