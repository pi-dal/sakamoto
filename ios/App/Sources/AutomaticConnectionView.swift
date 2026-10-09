import SwiftUI
import SakamotoKit
import SakamotoNE
import Libbox

struct AutomaticConnectionView: View {
    @ObservedObject var store: ConfigStore
    @State private var mode: AutomaticConnectionSettings.Mode = .off
    @State private var domains = ""
    @State private var probeURL = ""
    @State private var saving = false
    @State private var importingDomains = false
    @State private var notice: String?

    var body: some View {
        List {
            Section {
                Picker("Connect on", selection: $mode) {
                    Text("Off").tag(AutomaticConnectionSettings.Mode.off)
                    Text("Any network").tag(AutomaticConnectionSettings.Mode.anyNetwork)
                    Text("Wi-Fi").tag(AutomaticConnectionSettings.Mode.wifi)
                    Text("Cellular").tag(AutomaticConnectionSettings.Mode.cellular)
                    Text("Domains").tag(AutomaticConnectionSettings.Mode.domains)
                }
            } header: { Text("On demand") } footer: {
                Text("iOS reconnects the saved VPN when the selected condition matches, including after a restart once the device can access its saved configuration. Connect once in Home before enabling. Manually disconnecting pauses automatic connection; enable it again here to resume.")
            }
            if mode == .domains {
                Section {
                    Button {
                        Task { await importProxyDomains() }
                    } label: {
                        HStack {
                            Text("Use PROXY domains from .conf")
                            if importingDomains { Spacer(); ProgressView() }
                        }
                    }
                    .disabled(importingDomains || saving || store.generating)
                    if !store.sourceBundle.mainConf.isEmpty {
                        Text(URL(fileURLWithPath: store.sourceBundle.mainConf).lastPathComponent)
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    TextField("example.com\nhttps://example.org", text: $domains, axis: .vertical)
                        .lineLimit(3...8)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityLabel("Domains or URLs")
                    TextField("Optional check URL", text: $probeURL)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                } header: { Text("Domains or URLs") } footer: {
                    Text("URLs match by host and include subdomains; paths are ignored. iOS connects when DNS resolution fails, or when the optional check URL does not return HTTP 200. A successful direct connection may not trigger the VPN. These conditions start the VPN; proxy routing still follows your configuration.")
                }
                Section {
                    Button("Add these domains to proxy rules") { addProxyRules() }
                        .disabled(saving || importingDomains || store.generating || domains.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                } footer: {
                    Text("Adds PROXY rules to the selected configuration. Apply changes in Config, then save automatic connection here.")
                }
            }
            Section {
                Button { Task { await save() } } label: {
                    HStack {
                        Text("Save automatic connection")
                        if saving { Spacer(); ProgressView() }
                    }
                }
                .disabled(saving || importingDomains)
                if let notice { Text(notice).font(.footnote).foregroundStyle(.secondary) }
            }
            Section {
                NavigationLink {
                    AppAutomationView()
                } label: {
                    Label("Connect when an app opens", systemImage: "app.badge")
                }
            } header: { Text("App automation") } footer: {
                Text("Choose apps in Shortcuts and use sakamoto’s Connect VPN action. Setup steps and an optional disconnect action are available here.")
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Automatic connection")
        .navigationBarTitleDisplayMode(.inline)
        .onChange(of: mode) { next in
            if next == .domains && domains.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                Task { await importProxyDomains() }
            }
        }
        .task {
            do {
                let settings = try await SystemTunnelControl.automaticConnectionSettings()
                mode = settings.mode; domains = settings.domains.joined(separator: "\n"); probeURL = settings.probeURL
            } catch { notice = error.localizedDescription }
        }
    }

    private func importProxyDomains() async {
        guard !importingDomains else { return }
        importingDomains = true
        defer { importingDomains = false }
        let bundle = store.sourceBundle
        let profile = store.selectedProfile
        let draftBefore = domains
        do {
            let imported = try await Task.detached(priority: .userInitiated) {
                try OnDemandDomainImport.read(bundle: bundle, profile: profile)
            }.value
            let encoder = JSONEncoder(); encoder.outputFormatting = .sortedKeys
            guard try encoder.encode(store.sourceBundle) == encoder.encode(bundle), store.selectedProfile == profile, domains == draftBefore else {
                notice = "Configuration or domains changed. Retry importing its proxy domains."
                return
            }
            guard !imported.domains.isEmpty else {
                notice = (["No host-based PROXY rules found in the selected conf. Generate its sources or add domains manually."] + imported.notes).joined(separator: " ")
                return
            }
            let current = try AutomaticConnectionSettings.normalizeDomains(domains)
            domains = Array(Set(current + imported.domains)).sorted().joined(separator: "\n")
            notice = (["Imported \(imported.domains.count) proxy domains. Review and save automatic connection."] + imported.notes).joined(separator: " ")
        } catch { notice = error.localizedDescription }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        do {
            let settings = try AutomaticConnectionSettings(mode: mode, domains: AutomaticConnectionSettings.normalizeDomains(domains), probeURL: probeURL).validated()
            try await SystemTunnelControl.setAutomaticConnection(settings)
            SystemSurfaceReload.reload()
            notice = mode == .off ? "Automatic connection is off." : "Automatic connection saved in iOS VPN settings."
        } catch { notice = error.localizedDescription }
    }

    private func addProxyRules() {
        do {
            let hosts = try AutomaticConnectionSettings.normalizeDomains(domains)
            guard !hosts.isEmpty else { throw AutomaticConnectionSettings.ValidationError.emptyDomains }
            var book = store.policy
            for host in hosts {
                let match = "*." + host
                var error: NSError?
                _ = MobilecoreNormalizePolicyRule(match, "proxy", &error)
                if let error { throw error }
                if !book.rules.contains(where: { $0.match == match && $0.action == "proxy" }) {
                    book.rules.append(StagedPolicyRule(match: match, action: "proxy"))
                }
            }
            store.commitPolicy(book)
            notice = "Proxy rules saved. Apply changes in Config before enabling on demand."
        } catch { notice = error.localizedDescription }
    }
}
