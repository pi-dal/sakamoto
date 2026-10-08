import SwiftUI
import Libbox
import SakamotoKit

struct ConfSourceView: View {
    @ObservedObject var store: ConfigStore
    @Environment(\.dismiss) private var dismiss
    @State private var error: String?
    var body: some View {
        List {
            if store.confSources.isEmpty { Text("Add a .conf file or sync your sources first.").foregroundStyle(.secondary) }
            ForEach(store.confSources, id: \.self) { name in
                Button {
                    do { try store.selectConfSource(name); dismiss() }
                    catch { self.error = error.localizedDescription }
                } label: {
                    HStack {
                        VStack(alignment: .leading) {
                            Text(URL(fileURLWithPath: name).lastPathComponent)
                            Text(name).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                        }
                        Spacer()
                        if name == store.sourceBundle.mainConf { Image(systemName: "checkmark") }
                    }
                }
            }
            if let error { Text(error).font(.footnote) }
        }.navigationTitle("Rule source (.conf)")
    }
}

struct RoutingRulesView<Overrides: View>: View {
    @ObservedObject var store: ConfigStore
    @ViewBuilder let overrides: () -> Overrides
    private var root: [String: Any] {
        let config = (try? store.selectedProfile?.proxyChain?.applying(to: store.content)) ?? store.content
        return ((try? JSONSerialization.jsonObject(with: Data(config.utf8))) as? [String: Any]) ?? [:]
    }
    var body: some View {
        List {
            Section {
                NavigationLink("Edit custom overrides", destination: overrides)
                NavigationLink("Choose rule source (.conf)") { ConfSourceView(store: store) }
            }
            Section("Source rules") {
                ForEach(store.confSources, id: \.self) { name in
                    NavigationLink(name) {
                        RuleTextView(title: URL(fileURLWithPath: name).lastPathComponent,
                                     rows: sourceRows(store.sourceBundle.files[name] ?? ""))
                    }
                }
            }
            Section {
                let route = root["route"] as? [String: Any] ?? [:]
                let rules = route["rules"] as? [[String: Any]] ?? []
                LabeledRow("Unmatched traffic", route["final"] as? String ?? "Core default")
                ForEach(Array(rules.enumerated()), id: \.offset) { index, rule in
                    VStack(alignment: .leading, spacing: 4) {
                        Text("\(index + 1). \(rule["outbound"] as? String ?? rule["action"] as? String ?? "Rule")").font(.headline)
                        Text(ruleDescription(rule)).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                    }
                }
                if rules.isEmpty { Text("Generate the configuration to inspect its rules.").foregroundStyle(.secondary) }
            } header: { Text("Generated routing rules") }
            footer: { Text("Saved configuration, in matching order. Apply changes to use it. Data shows the running VPN's reported matches.") }
            Section("Compiled rule sets") {
                let sets = (root["route"] as? [String: Any])?["rule_set"] as? [[String: Any]] ?? []
                ForEach(sets.compactMap { $0["tag"] as? String }, id: \.self) { tag in
                    let set = sets.first { $0["tag"] as? String == tag }
                    let key = "rules/" + URL(fileURLWithPath: set?["path"] as? String ?? "").lastPathComponent
                    NavigationLink(tag) { CompiledRuleSetView(title: tag, data: store.selectedProfile?.files[key]) }
                }
            }
        }
        .navigationTitle("Routing rules")
    }
    private func ruleDescription(_ rule: [String: Any]) -> String {
        let names = ["domain": "Domain", "domain_suffix": "Domain suffix", "domain_keyword": "Domain contains", "ip_cidr": "IP range", "ip_is_private": "Private IP", "rule_set": "Rule set", "clash_mode": "Mode", "protocol": "Protocol", "network": "Network", "port": "Port", "preferred_by": "Preferred route", "inbound": "Inbound"]
        let predicates = rule.keys.sorted().filter { !["outbound", "action"].contains($0) }.map { key in
            let value = rule[key]!
            let text = (value as? [Any]).map { $0.map { String(describing: $0) }.joined(separator: ", ") } ?? String(describing: value)
            return "\(names[key] ?? key): \(text)"
        }
        return predicates.isEmpty ? "All traffic reaching this rule" : predicates.joined(separator: " · ")
    }
    private func sourceRows(_ body: String) -> [String] {
        var section = ""
        return body.components(separatedBy: .newlines).compactMap { raw in
            let line = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            if line.hasPrefix("[") { section = line.lowercased(); return nil }
            guard section == "[rule]" || line.lowercased().hasPrefix("include") else { return nil }
            return line.isEmpty ? nil : SecretMasking.maskSecret(line)
        }
    }
}

struct RuleTextView: View {
    let title: String
    let rows: [String]
    @State private var query = ""
    @State private var limit = 100
    private var filtered: [String] { Array(rows.lazy.filter { query.isEmpty || $0.localizedCaseInsensitiveContains(query) }.prefix(limit)) }
    var body: some View {
        List {
            Text("\(rows.count) entries in this saved snapshot").font(.footnote).foregroundStyle(.secondary)
            ForEach(Array(filtered.enumerated()), id: \.offset) { _, row in Text(row).font(.caption.monospaced()).textSelection(.enabled) }
            if filtered.count == limit { Button("Show more") { limit += 100 } }
        }.navigationTitle(title).searchable(text: $query, prompt: "Domain, IP or rule")
    }
}

struct CompiledRuleSetView: View {
    let title: String
    let data: Data?
    @State private var rows: [String]?
    @State private var error: String?
    var body: some View {
        Group {
            if let rows { RuleTextView(title: title, rows: rows) }
            else if let error { Text(error).padding() }
            else { ProgressView("Reading rule set…") }
        }.task {
            guard rows == nil else { return }
            guard let data else { error = "No local rule snapshot. Generate or import a complete configuration."; return }
            do {
                rows = try await Task.detached(priority: .userInitiated) {
                    var error: NSError?
                    let raw = MobilegenInspectRuleSetJSON(data, &error)
                    if let error { throw error }
                    guard let object = try JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any] else { throw TunnelProfile.InvalidProfile("Cannot inspect this rule set.") }
                    var lines: [String] = []
                    func walk(_ value: Any, key: String) {
                        if let dict = value as? [String: Any] { for name in dict.keys.sorted() { walk(dict[name]!, key: name) } }
                        else if let array = value as? [Any] { for item in array { walk(item, key: key) } }
                        else { lines.append("\(key): \(value)") }
                    }
                    walk(object["rules"] ?? [], key: "rule")
                    return lines
                }.value
            } catch { self.error = error.localizedDescription }
        }
    }
}
