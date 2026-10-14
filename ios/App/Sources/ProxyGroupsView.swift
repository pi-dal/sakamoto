import SwiftUI
import SakamotoKit

struct ProxyGroupsView: View {
    @ObservedObject var store: ConfigStore
    var body: some View {
        List {
            Section {
                ForEach(ProxyGroupEditing.groups(store.content)) { group in
                    NavigationLink { ProxyGroupEditor(store: store, group: group) } label: {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(group.tag)
                            Text("\(group.type == "selector" ? "Manual selection" : "Automatic by latency") · \(group.members.count) members").font(.caption).foregroundStyle(.secondary)
                        }
                    }.accessibilityIdentifier("Group-\(group.tag)")
                }
                if store.content.isEmpty { Text("Generate or import a configuration first.").foregroundStyle(.secondary) }
                else { NavigationLink { ProxyGroupEditor(store: store, group: ProxyGroupEdit(tag: "")) } label: { Label("Add group", systemImage: "plus") } }
            } footer: { Text("Automatic groups, including RealityAuto, test only their selected members. Remove a member to exclude it from that group; the node stays available for manual selection. Save, then Apply changes in Config.") }
        }.navigationTitle("Proxy groups").navigationBarTitleDisplayMode(.inline)
    }
}

struct ProxyGroupEditor: View {
    @ObservedObject var store: ConfigStore
    private let originalTag: String
    private let baseline: String
    private let profileID: String?
    private let candidates: [(String, String)]
    @State private var edit: ProxyGroupEdit
    @State private var query = ""
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss
    init(store: ConfigStore, group: ProxyGroupEdit) {
        self.store = store; originalTag = group.tag; baseline = store.content; profileID = store.selectedProfileID
        let root = (try? JSONSerialization.jsonObject(with: Data(store.content.utf8))) as? [String: Any] ?? [:]
        let rows = ProxyGroupEditing.outbounds(store.content) + (root["endpoints"] as? [[String: Any]] ?? [])
        candidates = rows.compactMap { row in
            guard let tag = row["tag"] as? String, tag != group.tag else { return nil }
            return (tag, row["type"] as? String ?? "")
        }
        _edit = State(initialValue: group)
    }
    var body: some View {
        List {
            Section("Group") {
                if originalTag.isEmpty { TextField("Group name", text: $edit.tag).autocorrectionDisabled() }
                else { LabeledContent("Name", value: edit.tag) }
                Picker("Selection", selection: $edit.type) {
                    Text("Manual").tag("selector")
                    Text("Automatic by latency").tag("urltest")
                }
                if edit.type == "selector" {
                    Picker("Default node", selection: $edit.defaultMember) {
                        Text("First selected member").tag("")
                        ForEach(edit.members, id: \.self) { Text($0).tag($0) }
                    }
                } else {
                    TextField("Latency test URL", text: $edit.url, axis: .vertical).keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    Picker("Test interval", selection: $edit.interval) {
                        ForEach(Array(Set(["30s", "1m", "3m", "5m", "10m", edit.interval])).sorted(), id: \.self) { Text($0).tag($0) }
                    }
                }
            }
            Section {
                if edit.members.isEmpty { Text("Select members below.").foregroundStyle(.secondary) }
                ForEach(edit.members, id: \.self) { member in
                    HStack { Text(member); Spacer(); Button { toggle(member) } label: { Image(systemName: "minus.circle").frame(minWidth: 44, minHeight: 44) }.buttonStyle(.borderless).accessibilityLabel("Remove \(member) from group") }
                }
                .onMove { offsets, destination in edit.members.move(fromOffsets: offsets, toOffset: destination) }
            } header: { Text("Selected members (\(edit.members.count))") } footer: {
                if ProxyGroupEditing.followsGeneratedNodes(edit) {
                    Text("Excluded nodes stay out when sources update. New matching nodes join automatically. Removing a node here keeps it available in other groups; keep at least one member.")
                } else if edit.type == "urltest" {
                    Text("Only these members take part in automatic latency testing. Remove a node here to exclude it from this group without deleting it or changing other groups. Keep at least one member.")
                } else {
                    Text("Use Edit to reorder members. The first member is the fallback default for manual selection.")
                }
            }
            Section("Choose members") {
                TextField("Search nodes & groups", text: $query).autocorrectionDisabled()
                let visible = candidates.filter { query.isEmpty || $0.0.localizedCaseInsensitiveContains(query) }
                if visible.isEmpty { Text("No matching members").foregroundStyle(.secondary) }
                ForEach(visible, id: \.0) { tag, type in
                    Button { toggle(tag) } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: 4) { Text(tag).foregroundStyle(.primary); Text(type).font(.caption).foregroundStyle(.secondary) }
                            Spacer()
                            Image(systemName: edit.members.contains(tag) ? "checkmark.circle.fill" : "circle")
                        }
                    }.accessibilityLabel("\(tag), \(edit.members.contains(tag) ? "selected" : "not selected")").accessibilityIdentifier("GroupMember-\(tag)")
                }
            }
            if let error { Section { Text(error).font(.callout) } }
        }
        .navigationTitle(originalTag.isEmpty ? "Add group" : "Edit group").navigationBarTitleDisplayMode(.inline)
        .alert("Couldn’t save group", isPresented: Binding(get: { error != nil }, set: { if !$0 { error = nil } })) {
            Button("OK", role: .cancel) { error = nil }
        } message: { Text(error ?? "") }
        .toolbar {
            ToolbarItem(placement: .primaryAction) { EditButton() }
            ToolbarItem(placement: .confirmationAction) { Button("Save") {
                do {
                    edit.tag = edit.tag.trimmingCharacters(in: .whitespacesAndNewlines)
                    if originalTag.isEmpty && candidates.contains(where: { $0.0 == edit.tag }) { throw TunnelProfile.InvalidProfile("This name already exists. Choose another group name.") }
                    try store.saveProxyGroup(edit, expectedProfile: profileID, expectedContent: baseline)
                    dismiss()
                } catch { self.error = TunnelDiagnostics.sanitized(error.localizedDescription) }
            }.accessibilityIdentifier("GroupSave").disabled(edit.tag.isEmpty || edit.members.isEmpty || store.generating || store.applying) }
        }
    }
    private func toggle(_ tag: String) {
        if edit.members.contains(tag) { edit.members.removeAll { $0 == tag }; if edit.defaultMember == tag { edit.defaultMember = "" } }
        else { edit.members.append(tag) }
    }
}
