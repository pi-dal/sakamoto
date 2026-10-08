import SwiftUI
import SakamotoKit

/// Settings share the selected profile. Runtime data belongs to the running
/// extension profile and remains local; no UI-process monitoring is required.
struct ExperimentView: View {
    @ObservedObject var model: SettingsModel
    @State private var settings = ExperimentSettings()
    @State private var runtime = ExperimentRuntimeState()
    @State private var error: String?
    @State private var busy = false
    @State private var confirmAuto = false
    @State private var selectedID: String?
    @State private var priorityGroup = ""
    @State private var priorityMembers = ""
    @State private var confirmApply = false

    var body: some View {
        Form {
            Section("Traffic policy") {
                Picker("Unmatched policy", selection: Binding(get: { settings.mode }, set: { mode in
                    if mode == .auto { confirmAuto = true }
                    else { update { $0.mode = mode } }
                })) {
                    ForEach(ExperimentMode.allCases, id: \.self) { Text($0.rawValue.capitalized).tag($0) }
                }
                .sakamotoInspectTag("ExperimentMode")
                Text("Off: unmatched traffic connects directly. On: uses the configured proxy. Auto: starts direct and learns only repeated, correlated failures.")
                    .font(.footnote).foregroundStyle(.secondary)
                Stepper("Direct failure threshold: \(settings.threshold)", value: Binding(get: { settings.threshold }, set: { value in update { $0.threshold = value } }), in: 1...20)
                Toggle("CF region auto-proxy", isOn: Binding(get: { settings.cfRegionBlock }, set: { value in update { $0.cfRegionBlock = value } }))
                Text("Requires explicit Cloudflare region-block evidence and a recent healthy proxy. Generic HTTP 403s, challenge pages and zero traffic never count. HTTPS response bodies are not intercepted.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            .disabled(SettingsOverrides.experimentProxy(in: model.store.content) == nil || busy)

            Section("Proxy fallback") {
                Toggle("Automatic fallback", isOn: Binding(get: { settings.fallbackEnabled }, set: { value in update { $0.fallbackEnabled = value } }))
                Stepper("Recovery rounds: \(settings.recoverAfter)", value: Binding(get: { settings.recoverAfter }, set: { value in update { $0.recoverAfter = value } }), in: 1...20)
                ForEach(settings.fallbacks.keys.sorted(), id: \.self) { group in
                    VStack(alignment: .leading) {
                        Text(group)
                        Text(settings.fallbacks[group, default: []].joined(separator: " → ")).font(.footnote).foregroundStyle(.secondary)
                    }
                    .swipeActions { Button("Remove", role: .destructive) { update { $0.fallbacks.removeValue(forKey: group) } } }
                }
                DisclosureGroup("Edit priority chain") {
                    TextField("Selector group tag", text: $priorityGroup).textInputAutocapitalization(.never).autocorrectionDisabled()
                    TextField("Priority tags, comma-separated", text: $priorityMembers).textInputAutocapitalization(.never).autocorrectionDisabled()
                    Button("Save priority chain") {
                        let group = priorityGroup.trimmingCharacters(in: .whitespacesAndNewlines)
                        let members = priorityMembers.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                        update { $0.fallbacks[group] = members }
                    }
                }
                Button("Run recovery tests") { Task { await perform { try await model.tunnel.recoverExperiment() } } }
                    .disabled(busy || !model.channelActive || !appliedProfile)
                Text("Fresh URL tests only. A failing priority falls back immediately; returning to a higher priority needs consecutive healthy rounds. Manual selections are preserved. Automatic rounds run in the VPN extension, including while the app is backgrounded.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
            .disabled(model.store.selectedProfile == nil || busy)

            Section("Learned routes") {
                if runtime.learned.isEmpty { Text("No learned routes").foregroundStyle(.secondary) }
                ForEach(runtime.learned, id: \.self) { domain in
                    Text(domain).swipeActions {
                        Button("Remove", role: .destructive) {
                            Task { await perform { try await model.tunnel.removeLearnedDomain(domain) } }
                        }.disabled(!model.channelActive || !appliedProfile || busy)
                    }
                }
                Text(runtime.status).font(.footnote).foregroundStyle(.secondary)
                Text("Learned domains stay local to this profile. Source DIRECT and REJECT rules keep priority.").font(.footnote).foregroundStyle(.secondary)
            }
            Section {
                Button("Apply Experiment & configuration") { confirmApply = true }
                    .disabled(model.store.generating || (!model.store.canConnect && !model.store.canGenerate) || busy)
                if SettingsOverrides.experimentProxy(in: model.store.content) == nil {
                    Text("Add nodes or subscriptions and Generate in Config to enable Experiment.").font(.footnote).foregroundStyle(.secondary)
                }
                if let error { Text(error).font(.footnote).foregroundStyle(.primary) }
                if let action = model.store.lastAction { Text(action).font(.footnote).foregroundStyle(.secondary) }
                if busy { ProgressView() }
            }
        }
        .navigationTitle("Experiment")
        .navigationBarTitleDisplayMode(.inline)
        .sakamotoInspectTag("ExperimentView")
        .onAppear { loadSettings() }
        .onChange(of: model.store.selectedProfileID) { _ in loadSettings() }
        .task {
            model.activate()
            while !Task.isCancelled {
                readRuntime()
                try? await Task.sleep(nanoseconds: 1_000_000_000)
            }
        }
        .confirmationDialog("Enable automatic learning?", isPresented: $confirmAuto, titleVisibility: .visible) {
            Button("Enable Auto") { update { $0.mode = .auto } }
            Button("Cancel", role: .cancel) {}
        } message: { Text("Correlated direct failures can add local proxy routes while this VPN profile runs. No HTTPS content interception. You can review and remove learned routes here.") }
        .confirmationDialog("Apply selected configuration?", isPresented: $confirmApply, titleVisibility: .visible) {
            Button("Apply") { Task { await perform { await model.store.regenerateAndApply(tunnel: model.tunnel) } } }
            Button("Cancel", role: .cancel) {}
        } message: { Text("Reloading may interrupt active connections. It also activates these Experiment settings in the VPN extension.") }
    }
    private var appliedProfile: Bool { model.store.selectedProfile?.pendingApply == false && model.store.selectedProfile?.sourcesChanged == false }
    private func loadSettings() { settings = model.store.experimentSettings; selectedID = model.store.selectedProfileID; readRuntime() }
    private func update(_ edit: (inout ExperimentSettings) -> Void) {
        guard selectedID == model.store.selectedProfileID else { loadSettings(); return }
        var next = settings; edit(&next)
        do { try model.store.saveExperimentSettings(next); settings = next; error = nil }
        catch { self.error = TunnelDiagnostics.sanitized(error.localizedDescription) }
    }
    private func readRuntime() {
        guard let id = model.store.selectedProfileID, let root = try? AppPaths.sharedDirectory() else { runtime = ExperimentRuntimeState(); return }
        do { runtime = try ExperimentFiles.read(profileID: id, root: root) }
        catch { self.error = "Experiment status could not be read; previous data kept." }
    }
    @MainActor private func perform(_ action: () async throws -> Void) async {
        guard !busy else { return }; busy = true; defer { busy = false; readRuntime() }
        do { try await action(); error = nil } catch { self.error = TunnelDiagnostics.sanitized(error.localizedDescription) }
    }
}
