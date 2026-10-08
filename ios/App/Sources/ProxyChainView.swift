import SwiftUI
import SakamotoKit

struct ProxyChainView: View {
    @ObservedObject var store: ConfigStore
    @State private var settings = ProxyChainSettings()
    @State private var exitLink = ""
    @State private var notice: String?
    private var outbounds: [[String: Any]] {
        ((try? JSONSerialization.jsonObject(with: Data(store.content.utf8))) as? [String: Any])?["outbounds"] as? [[String: Any]] ?? []
    }
    private var upstreamTags: [String] {
        outbounds.compactMap { row in
            guard !["direct", "block", "dns"].contains(row["type"] as? String ?? ""), let tag = row["tag"] as? String, tag != settings.exit else { return nil }
            return tag
        }
    }
    var body: some View {
        Form {
            if upstreamTags.isEmpty {
                Section { Text("Add an upstream node or subscription in Config → Nodes & sources, then generate the configuration.").font(.footnote) }
            }
            Section {
                Toggle("Use proxy chain", isOn: $settings.enabled)
                Picker("Upstream", selection: $settings.upstream) {
                    Text("Choose upstream").tag("")
                    ForEach(upstreamTags, id: \.self) { Text($0).tag($0) }
                }
                Picker("Exit server", selection: $settings.exit) {
                    Text("Choose exit").tag("")
                    ForEach(outbounds.filter { ["socks", "http"].contains($0["type"] as? String ?? "") }.compactMap { $0["tag"] as? String }, id: \.self) { Text($0).tag($0) }
                }
                Text("Device → \(settings.upstream.isEmpty ? "upstream" : settings.upstream) → \(settings.exit.isEmpty ? "exit server" : settings.exit) → website")
                    .font(.footnote).foregroundStyle(.secondary)
                Button("Save chain") {
                    do { try store.saveProxyChain(settings); notice = "Saved. Apply changes in Config." }
                    catch { notice = error.localizedDescription }
                }.disabled(store.generating || store.applying || store.content.isEmpty || (settings.enabled && settings.exit.isEmpty))
            } footer: { Text("PROXY rules and Global mode use this chain. DIRECT and REJECT rules keep their behavior. Saving does not interrupt the running VPN.") }
            Section("Add a SOCKS exit") {
                TextField("SOCKS share link", text: $exitLink)
                    .textInputAutocapitalization(.never).autocorrectionDisabled().privacySensitive()
                Button(store.generating ? "Generating…" : "Add exit & generate") {
                    Task {
                        do {
                            guard var components = URLComponents(string: exitLink), ["socks", "socks5"].contains(components.scheme?.lowercased() ?? "") else { throw TunnelProfile.InvalidProfile("Use a SOCKS share link, or choose an HTTP exit from an imported configuration.") }
                            // The shared Shadowrocket parser expects userinfo;
                            // empty userinfo represents an unauthenticated exit.
                            if components.user == nil { components.user = "" }
                            let node = try ConfigModel.validateNode(components.string ?? exitLink)
                            store.commitNodesSources(store.nodesSources.adding(node: node))
                            try await store.generateFromSources()
                            exitLink = ""; notice = "Exit added. Choose it above and save the chain."
                        } catch { notice = TunnelDiagnostics.sanitized(error.localizedDescription) }
                    }
                }.disabled(!store.canGenerate || store.generating || store.applying || exitLink.isEmpty)
                if !store.canGenerate { Text("Add upstream source nodes before adding another exit. Existing imported exits can be selected above.").font(.footnote).foregroundStyle(.secondary) }
            }
            if let notice { Section { Text(notice).font(.footnote) } }
        }
        .navigationTitle("Proxy chain")
        .onAppear {
            settings = store.selectedProfile?.proxyChain ?? ProxyChainSettings.importedChain(in: store.content) ?? ProxyChainSettings()
            if !upstreamTags.contains(settings.upstream) { settings.upstream = upstreamTags.first ?? "" }
        }
    }
}
