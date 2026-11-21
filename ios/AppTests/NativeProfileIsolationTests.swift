import XCTest
import SakamotoKit
import SakamotoNE
import Libbox
import NetworkExtension
import os
import Security
@testable import Sakamoto

// Lock the baseline immediately after a real replacement to exercise the second write window.
private struct UploadFaultFilesystem: ICloudFilesystem {
    let base = LocalFilesystem()
    let afterReplace: @Sendable (String, Data) throws -> Void
    func contents(atPath path: String) throws -> Data? { try base.contents(atPath: path) }
    func fileExists(atPath path: String) -> Bool { base.fileExists(atPath: path) }
    func isSymbolicLink(atPath path: String) throws -> Bool { try base.isSymbolicLink(atPath: path) }
    func ensureDirectory(atPath path: String) throws { try base.ensureDirectory(atPath: path) }
    func writeAtomic(_ data: Data, toPath path: String) throws { try base.writeAtomic(data, toPath: path) }
    func sourceNames(under root: String) throws -> [String] { try base.sourceNames(under: root) }
    func replace(_ data: Data, atPath path: String, matching expected: Data?) throws {
        try base.replace(data, atPath: path, matching: expected)
        try afterReplace(path, data)
    }
}

private final class IndexFailureDefaults: UserDefaults, @unchecked Sendable {
    var failNextFlush = false
    var beforeFlush: (() -> Void)?
    override func synchronize() -> Bool {
        beforeFlush?()
        if failNextFlush {
            failNextFlush = false
            return false
        }
        return super.synchronize()
    }
}

@MainActor
final class NativeProfileIsolationTests: XCTestCase {
    override class func tearDown() {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        if FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-repeat").path) {
            FileManager.default.createFile(atPath: docs.appendingPathComponent("profile-restart").path, contents: nil)
        }
        super.tearDown()
    }

    func testHistoricalLearnedPackageImport() throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let old = docs.appendingPathComponent("historical-learned.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: old.path))
        let suite = "fixture.legacy-learned-import"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let receipt = docs.appendingPathComponent("legacy-safe-manifest")
        let freshLaunch = FileManager.default.fileExists(atPath: receipt.path)
        if !freshLaunch { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
        if !freshLaunch {
            model.importFromFile(at: docs.appendingPathComponent("legacy-control.sakamoto"))
            XCTAssertNil(model.importError)
        }
        let selected = try XCTUnwrap(store.selectedProfile)
        let manifest = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(selected.id)/profile.json")
        if !freshLaunch {
            try Data(contentsOf: manifest).write(to: receipt)
            model.importFromFile(at: old)
            XCTAssertTrue(model.importError?.lowercased().contains("re-export") == true, model.importError ?? "Old package accepted")
        }
        XCTAssertEqual(store.selectedProfile, selected)
        XCTAssertEqual(try Data(contentsOf: manifest), try Data(contentsOf: receipt))
        let runtime = try store.connectionContent()
        XCTAssertFalse(runtime.contains("host-learned.fixture.example"))
        let stage = freshLaunch ? "restart" : "first"
        try Data(runtime.utf8).write(to: docs.appendingPathComponent("legacy-runtime-\(stage).json"))
        try verifyInstalledLegacyProfile(old: old, docs: docs, restarting: freshLaunch)
    }

    private func verifyInstalledLegacyProfile(old: URL, docs: URL, restarting: Bool) throws {
        let suite = "fixture.upgrade.selected-historical-profile"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let package = try JSONDecoder().decode(TunnelProfile.Package.self, from: Data(contentsOf: old))
        let evidence = docs.appendingPathComponent("legacy-upgrade-manifest")
        if !restarting {
            defaults.removePersistentDomain(forName: suite)
            var saved = TunnelProfile(name: package.name, config: package.config, sourceDigest: package.sourceDigest, sourceBundleJSON: package.sourceBundleJSON)
            saved.pendingApply = false
            let directory = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(saved.id)")
            for (name, bytes) in package.files {
                let file = directory.appendingPathComponent(name)
                try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
                try bytes.write(to: file)
            }
            let manifest = try JSONEncoder().encode(saved)
            try manifest.write(to: directory.appendingPathComponent("profile.json"))
            try manifest.write(to: evidence)
            saved.config = ""; saved.sourceBundleJSON = nil
            defaults.set(try JSONEncoder().encode([saved]), forKey: ConfigStore.profilesKey)
            defaults.set(saved.id, forKey: ConfigStore.selectedProfileKey)
            let oldOptions = TunnelStartOptions(configContent: package.config, profileID: saved.id)
            let ref = try oldOptions.storedReference()
            try oldOptions.confirmStoredReference(ref)
        }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let profile = try XCTUnwrap(store.selectedProfile)
        let directory = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(profile.id)")
        let before = try Data(contentsOf: evidence)
        XCTAssertEqual(profile.config, package.config)
        XCTAssertEqual(profile.sourceBundleJSON, package.sourceBundleJSON)
        XCTAssertTrue(store.profileError?.lowercased().contains("re-export") == true)
        func rejected<T>(_ attempt: () throws -> T) {
            XCTAssertThrowsError(try attempt()) { XCTAssertTrue($0.localizedDescription.lowercased().contains("re-export")) }
        }
        rejected { try store.connectionContent() }
        rejected { try store.systemConnectionOptions() }
        let confirmed = try XCTUnwrap(TunnelStartOptions.confirmed(for: profile.id))
        let ref = try confirmed.storedReference()
        rejected { try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false) }
        XCTAssertEqual(try Data(contentsOf: directory.appendingPathComponent("profile.json")), before)
        for (name, bytes) in package.files { XCTAssertEqual(try Data(contentsOf: directory.appendingPathComponent(name)), bytes) }
        try Data("verified".utf8).write(to: docs.appendingPathComponent("legacy-upgrade-\(restarting ? "restart" : "first").ok"))
    }

    func testICloudMixedPassKeepsProfileOnWriteFailure() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let marker = docs.appendingPathComponent("mixed-pass-relaunch")
        let restarting = FileManager.default.fileExists(atPath: marker.path)
        try await verifyMixedPass(restarting: restarting)
        if restarting { try FileManager.default.removeItem(at: marker) }
        else { try Data().write(to: marker) }
    }

    private func verifyMixedPass(restarting: Bool) async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        for fault in ["baseline", "upload"] {
            let suite = "fixture.icloud-rollback." + fault
            let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
            let root = docs.appendingPathComponent(suite)
            let remote = root.appendingPathComponent("remote", isDirectory: true)
            let local = root.appendingPathComponent("local", isDirectory: true)
            let receipt = root.appendingPathComponent("before.json")
            if restarting {
                XCTAssertTrue(fm.fileExists(atPath: receipt.path))
            } else {
                defaults.removePersistentDomain(forName: suite)
                if fm.fileExists(atPath: root.path) { try fm.removeItem(at: root) }
                try fm.createDirectory(at: remote, withIntermediateDirectories: true)
                defaults.set(local.path, forKey: "sakamoto.icloud.sync.localdir")
            }
            let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
            let page = ICloudSyncModel(store: store, defaults: defaults, cloudFilesystem: LocalFilesystem(), accessDirectory: { _ in true }, releaseDirectory: { _ in })
            func sync() async throws {
                page.syncNow()
                for _ in 0..<400 where page.syncing { try await Task.sleep(nanoseconds: 25_000_000) }
                XCTAssertFalse(page.syncing)
            }
            let incoming = "trojan://fixture@remote.invalid:443#RemoteOnly\n"
            if !restarting {
                let profile = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto")))
                try store.installProfile(profile)
                page.selectDirectory(.success(remote))
                page.confirmEnable()
                page.setIncludeConf(false)
                try await sync()
                guard case .synced = page.status else { return XCTFail(page.status.summary) }
                try Data(incoming.utf8).write(to: remote.appendingPathComponent("nodes.txt"))
                // The conf files become first-time uploads alongside the node download.
                page.setIncludeConf(true)
            }
            let selected = try XCTUnwrap(store.selectedProfile)
            let manifest = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(selected.id)/profile.json")
            if !restarting {
                try Data(contentsOf: manifest).write(to: receipt)
                let directory: URL
                if fault == "baseline" {
                    let scopes = try fm.contentsOfDirectory(at: local.appendingPathComponent("directories"), includingPropertiesForKeys: nil)
                    directory = try XCTUnwrap(scopes.first)
                } else { directory = remote }
                try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: directory.path)
                defer { try? fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path) }
                try await sync()
                guard case .failed = page.status else { return XCTFail("Expected \(fault) failure") }
                XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), selected.id)
                XCTAssertTrue(defaults.synchronize())
            }
            let expected = try Data(contentsOf: receipt)
            let before = try JSONDecoder().decode(TunnelProfile.self, from: expected)
            XCTAssertFalse(before.sourcesChanged)
            XCTAssertTrue(try Data(contentsOf: manifest) == expected, fault)
            XCTAssertTrue(store.selectedProfile?.sourceBundleJSON == before.sourceBundleJSON, fault)
            XCTAssertEqual(store.selectedProfile?.proxyGroups, before.proxyGroups, fault)
            XCTAssertEqual(store.selectedProfile?.sourcesChanged, before.sourcesChanged, fault)
            if restarting {
                try await sync()
                guard case .synced = page.status else { return XCTFail(page.status.summary) }
                XCTAssertEqual(store.sourceBundle.files["nodes.txt"], incoming)
                XCTAssertEqual(store.selectedProfile?.sourcesChanged, true)
                XCTAssertTrue(fm.fileExists(atPath: remote.appendingPathComponent(store.sourceBundle.mainConf).path))
                try fm.removeItem(at: root)
                defaults.removePersistentDomain(forName: suite)
            }
        }
    }

    func testICloudUploadFailureAndNodeEditAcrossRelaunch() async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let marker = docs.appendingPathComponent("upload-edit-relaunch")
        let restart = fm.fileExists(atPath: marker.path)
        for afterReplace in [false, true] {
            let suite = "fixture.upload-edit." + (afterReplace ? "after" : "before")
            let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
            let root = docs.appendingPathComponent(suite)
            let local = root.appendingPathComponent("local")
            let remote = root.appendingPathComponent("remote", isDirectory: true)
            let cloudFile = remote.appendingPathComponent("nodes.txt")
            let a = "trojan://fixture@a.invalid:443#A\n"
            let b = "trojan://fixture@b.invalid:443#B\n"
            if !restart {
                defaults.removePersistentDomain(forName: suite)
                if fm.fileExists(atPath: root.path) { try fm.removeItem(at: root) }
                try fm.createDirectory(at: remote, withIntermediateDirectories: true)
                defaults.set(local.path, forKey: "sakamoto.icloud.sync.localdir")
            }
            let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
            let cloud = UploadFaultFilesystem { path, data in
                guard afterReplace, !restart, path == cloudFile.path, data == Data(b.utf8) else { return }
                let directories = try fm.contentsOfDirectory(at: local.appendingPathComponent("directories"), includingPropertiesForKeys: nil)
                try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: try XCTUnwrap(directories.first).path)
            }
            let page = ICloudSyncModel(store: store, defaults: defaults, cloudFilesystem: cloud, accessDirectory: { _ in true }, releaseDirectory: { _ in })
            func sync(_ choice: ICloudConflictChoice? = nil) async throws {
                page.syncNow(conflictChoice: choice)
                for _ in 0..<400 where page.syncing { try await Task.sleep(nanoseconds: 25_000_000) }
                XCTAssertFalse(page.syncing)
            }
            func save(_ body: String) throws {
                let node = try ConfigModel.validateNode(body.trimmingCharacters(in: .newlines))
                try store.commitNodesSources(NodesSourcesBook(nodes: [node]))
            }
            if !restart {
                try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
                try save(a)
                page.selectDirectory(.success(remote)); page.confirmEnable()
                try await sync()
                XCTAssertTrue(try Data(contentsOf: cloudFile) == Data(a.utf8))
                try save(b)
                let dirs = try fm.contentsOfDirectory(at: local.appendingPathComponent("directories"), includingPropertiesForKeys: nil)
                let scope = try XCTUnwrap(dirs.first)
                if !afterReplace { try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: scope.path) }
                defer { try? fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: scope.path) }
                try await sync()
                guard case .failed = page.status else { return XCTFail(page.status.summary) }
                XCTAssertTrue(try Data(contentsOf: cloudFile) == Data((afterReplace ? b : a).utf8))
                XCTAssertEqual(store.sourceBundle.files["nodes.txt"], b)
                try save(a)
                XCTAssertTrue(defaults.synchronize())
            } else {
                XCTAssertEqual(store.sourceBundle.files["nodes.txt"], a)
                try await sync()
                if afterReplace {
                    guard case .conflict(_, ["nodes.txt"]) = page.status else { return XCTFail(page.status.summary) }
                    XCTAssertEqual(store.sourceBundle.files["nodes.txt"], a)
                    XCTAssertTrue(try Data(contentsOf: cloudFile) == Data(b.utf8))
                    try await sync(.useDevice)
                    guard case .synced = page.status else { return XCTFail(page.status.summary) }
                } else {
                    guard case .upToDate = page.status else { return XCTFail(page.status.summary) }
                }
                XCTAssertEqual(store.sourceBundle.files["nodes.txt"], a)
                XCTAssertTrue(try Data(contentsOf: cloudFile) == Data(a.utf8))
                try fm.removeItem(at: root)
                defaults.removePersistentDomain(forName: suite)
            }
        }
        if restart { try fm.removeItem(at: marker) } else { try Data().write(to: marker) }
    }

    func testICloudFirstUploadConflictUsesCloudAcrossRelaunch() async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.icloud-first-upload"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let root = docs.appendingPathComponent(suite)
        let local = root.appendingPathComponent("local")
        let remote = root.appendingPathComponent("remote", isDirectory: true)
        let cloudFile = remote.appendingPathComponent("nodes.txt")
        let legacy = root.appendingPathComponent("legacy.json")
        let restart = fm.fileExists(atPath: legacy.path)
        let a = "trojan://fixture@a.invalid:443#A\n"
        let b = "trojan://fixture@b.invalid:443#B\n"
        if !restart {
            defaults.removePersistentDomain(forName: suite)
            if fm.fileExists(atPath: root.path) { try fm.removeItem(at: root) }
            try fm.createDirectory(at: remote, withIntermediateDirectories: true)
            defaults.set(local.path, forKey: "sakamoto.icloud.sync.localdir")
        }
        let dirs = local.appendingPathComponent("directories")
        let cloud = UploadFaultFilesystem { path, _ in
            guard !restart, path == cloudFile.path else { return }
            let scope = try XCTUnwrap(fm.contentsOfDirectory(at: dirs, includingPropertiesForKeys: nil).first)
            try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: scope.path)
        }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let page = ICloudSyncModel(store: store, defaults: defaults, cloudFilesystem: cloud, accessDirectory: { _ in true }, releaseDirectory: { _ in })
        func sync(_ choice: ICloudConflictChoice? = nil) async throws {
            page.syncNow(conflictChoice: choice)
            for _ in 0..<400 where page.syncing { try await Task.sleep(nanoseconds: 25_000_000) }
            XCTAssertFalse(page.syncing)
        }
        func save(_ body: String) throws {
            let node = try ConfigModel.validateNode(body.trimmingCharacters(in: .newlines))
            try store.commitNodesSources(NodesSourcesBook(nodes: [node]))
        }
        if !restart {
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
            try save(a)
            page.selectDirectory(.success(remote)); page.confirmEnable()
            try await sync()
            guard case .failed = page.status else { return XCTFail(page.status.summary) }
        }
        let scope = try XCTUnwrap(fm.contentsOfDirectory(at: dirs, includingPropertiesForKeys: nil).first)
        let baseline = scope.appendingPathComponent(ICloudSyncStore.stateFileName)
        XCTAssertTrue(try Data(contentsOf: cloudFile) == Data(a.utf8))
        if !restart {
            try fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: scope.path)
            let interrupted = try JSONDecoder().decode([String: String].self, from: Data(contentsOf: baseline))
            let creating = try XCTUnwrap(interrupted["nodes.txt"])
            XCTAssertTrue(creating.hasPrefix("creating:"))
            try save(b)
            try await sync()
            guard case .conflict(_, ["nodes.txt"]) = page.status else { return XCTFail(page.status.summary) }
            XCTAssertEqual(store.sourceBundle.files["nodes.txt"], b)
            try await sync(.useCloud)
            guard case .synced = page.status else { return XCTFail(page.status.summary) }
            XCTAssertEqual(store.sourceBundle.files["nodes.txt"], a)
            let batch = try XCTUnwrap(store.selectedProfile?.iCloudAdoptedBatches?[scope.lastPathComponent])
            var old = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: baseline)) as? [String: Any])
            old["nodes.txt"] = "pending:\(creating):\(creating.dropFirst("creating:".count)):\(batch.uuidString)"
            try JSONSerialization.data(withJSONObject: old).write(to: legacy)
            XCTAssertTrue(defaults.synchronize())
        } else {
            XCTAssertEqual(store.sourceBundle.files["nodes.txt"], a)
            try await sync()
            guard case .upToDate = page.status else { return XCTFail(page.status.summary) }
            try Data(contentsOf: legacy).write(to: baseline, options: .atomic)
            try save(b)
            try await sync()
            guard case .synced = page.status else { return XCTFail(page.status.summary) }
            XCTAssertEqual(store.sourceBundle.files["nodes.txt"], b)
            XCTAssertTrue(try Data(contentsOf: cloudFile) == Data(b.utf8))
            try fm.removeItem(at: root)
            defaults.removePersistentDomain(forName: suite)
        }
    }

    func testApplyManifestFailureAfterProviderReceiptAcrossRelaunch() async throws {
        try await verifyFinalSave(interrupt: false)
    }

    func testHomeAfterInterruptedApply() async throws {
        try await verifyFinalSave(interrupt: true)
    }

    private func verifyFinalSave(interrupt: Bool) async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let marker = docs.appendingPathComponent(interrupt ? "apply-interrupt-resume" : "apply-final-save-rollback")
        if interrupt { try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("arm-apply-interrupt").path)) }
        let restart = FileManager.default.fileExists(atPath: marker.path)
        let mode = (try? String(contentsOf: docs.appendingPathComponent("arm-apply-interrupt"), encoding: .utf8)) ?? ""
        let crossProfile = interrupt && mode.hasPrefix("cross-")
        let suite = interrupt ? "fixture.apply-interrupt" : "fixture.apply-final-save-rollback"
        let seeded = docs.appendingPathComponent("apply-interrupt-seeded")
        let interruptedReference = docs.appendingPathComponent("apply-interrupt-vpn-reference.json")
        let seed = interrupt && !FileManager.default.fileExists(atPath: seeded.path)
        let defaults = try XCTUnwrap(IndexFailureDefaults(suiteName: suite))
        if seed || (!interrupt && !restart) { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let manager = FixtureVPNPreferences()
        if interrupt {
            let exchange = try AppPaths.sharedDirectory().appendingPathComponent("InterruptedApplyFixture")
            if FileManager.default.fileExists(atPath: exchange.appendingPathComponent("armed").path) {
                manager.session.forwardReload = { previous, next, transaction in
                    struct Reload: Encodable { let previous, next: TunnelStartOptions; let transaction: UUID?; let requestID: UUID }
                    let request = Reload(previous: previous, next: next, transaction: transaction, requestID: UUID())
                    try JSONEncoder().encode(request).write(to: exchange.appendingPathComponent("reload.json"), options: .atomic)
                    let ready = exchange.appendingPathComponent(request.requestID.uuidString)
                    let deadline = Date().addingTimeInterval(12)
                    while !FileManager.default.fileExists(atPath: ready.path) {
                        guard Date() < deadline else { throw CocoaError(.fileReadUnknown) }
                        Thread.sleep(forTimeInterval: 0.02)
                    }
                }
            }
        }
        let controller = NETunnelController(providerBundleIdentifier: "fixture", manager: manager, isCurrent: { store.matchesConnectionOptions($0) })
        if restart {
            let previous = try JSONDecoder().decode(TunnelStartOptions.self, from: Data(contentsOf: marker))
            if interrupt {
                let transport = FinalRouteFixture()
                await HomeModel(tunnel: transport, commanding: nil, store: store).connect()
                if !crossProfile { XCTAssertTrue(transport.content == previous.configContent, "Home submitted the interrupted candidate") }
                let candidate = try store.connectionOptions()
                let ref = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: interruptedReference)) as? [String: Any])
                let persisted = NETunnelProviderProtocol()
                persisted.providerBundleIdentifier = "fixture"
                persisted.providerConfiguration = ref
                manager.saved = persisted
                XCTAssertTrue(try TunnelConfigurationReceipt(options: candidate).matches(XCTUnwrap(manager.readSaved())))
                let cold = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false)
                XCTAssertTrue(cold == previous)
                if mode == "cross-double" {
                    let second = docs.appendingPathComponent("apply-interrupt-second")
                    if FileManager.default.fileExists(atPath: second.path) {
                        let b = try String(contentsOf: second, encoding: .utf8)
                        XCTAssertNotEqual(candidate.profileID, b)
                        XCTAssertNotEqual(candidate.profileID, cold.profileID)
                        XCTAssertEqual(store.selectedProfile?.pendingApply, true)
                        try store.selectProfile(b)
                        XCTAssertEqual(store.selectedProfile?.pendingApply, true)
                        for file in [marker, seeded, interruptedReference, second] { try FileManager.default.removeItem(at: file) }
                        defaults.removePersistentDomain(forName: suite)
                        return
                    }
                    let model = ConfigModel(tunnel: controller, store: store)
                    model.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
                    XCTAssertNil(model.importError)
                    XCTAssertNotEqual(store.selectedProfileID, candidate.profileID)
                    manager.session.started = true
                    manager.session.liveOptions = candidate
                    manager.session.onFirstReload = {
                        defaults.beforeFlush = {
                            do {
                                let ref = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
                                try JSONSerialization.data(withJSONObject: ref).write(to: interruptedReference, options: .atomic)
                                try Data(XCTUnwrap(candidate.profileID).utf8).write(to: second, options: .atomic)
                            } catch { XCTFail(error.localizedDescription); return }
                            FileHandle.standardError.write(Data("intentional Apply interruption 85\n".utf8))
                            _exit(85)
                        }
                    }
                    await model.regenerateAndApply()
                    return XCTFail("Expected C to exit before commit")
                }
                store.reconcileAppliedConnection(service: .running, receipt: try TunnelConfigurationReceipt(options: candidate))
            }
            if !crossProfile { XCTAssertTrue(try store.systemConnectionOptions() == previous) }
            XCTAssertEqual(store.selectedProfile?.pendingApply, true)
            if interrupt {
                let candidate = try store.connectionOptions()
                manager.session.started = true
                manager.session.liveOptions = previous
                manager.session.onFirstReload = { defaults.failNextFlush = true }
                await store.regenerateAndApply(tunnel: controller)
                XCTAssertEqual(store.selectedProfile?.pendingApply, true)
                XCTAssertTrue(manager.session.liveOptions == previous)
                XCTAssertTrue(manager.readSaved() == previous)
                if !crossProfile { XCTAssertTrue(try store.systemConnectionOptions() == previous) }
                XCTAssertEqual(store.selectedProfileID, candidate.profileID)
                manager.session.onFirstReload = {}
                await store.regenerateAndApply(tunnel: controller)
                XCTAssertEqual(store.selectedProfile?.pendingApply, false)
                try FileManager.default.removeItem(at: interruptedReference)
                XCTAssertTrue(try TunnelConfigurationReceipt(options: candidate).matches(store.systemConnectionOptions()))
            }
            try FileManager.default.removeItem(at: marker)
            if interrupt { try FileManager.default.removeItem(at: seeded) }
            defaults.removePersistentDomain(forName: suite)
            return
        }
        if !interrupt || seed {
            let model = ConfigModel(tunnel: controller, store: store)
            model.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
            XCTAssertNil(model.importError)
            if seed {
                var config = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(store.content.utf8)) as? [String: Any])
                var route = try XCTUnwrap(config["route"] as? [String: Any])
                route["final"] = "MainProxy"
                config["route"] = route
                try store.save(String(decoding: JSONSerialization.data(withJSONObject: config), as: UTF8.self))
                XCTAssertTrue(defaults.synchronize())
                try Data().write(to: seeded)
                return
            }
        }
        let previous = try store.connectionOptions()
        manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: previous)
        try previous.confirmStoredReference(try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration))
        if crossProfile {
            let model = ConfigModel(tunnel: controller, store: store)
            model.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
            XCTAssertNil(model.importError)
            XCTAssertNotEqual(previous.profileID, store.selectedProfileID)
            if mode == "cross-confirmed" {
                let oldB = try store.connectionOptions()
                try oldB.confirmStoredReference(oldB.storedReference())
            }
        }
        var root = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(store.content.utf8)) as? [String: Any])
        var route = try XCTUnwrap(root["route"] as? [String: Any])
        route["final"] = crossProfile ? "direct" : ((route["final"] as? String == "direct") ? "MainProxy" : "direct")
        root["route"] = route
        try store.save(String(decoding: JSONSerialization.data(withJSONObject: root), as: UTF8.self))
        let candidate = try store.connectionOptions()
        XCTAssertFalse(candidate == previous)
        manager.session.started = true
        manager.session.liveOptions = previous
        if interrupt {
            let baseline = try JSONEncoder().encode(previous)
            manager.session.onFirstReload = {
                defaults.beforeFlush = {
                    do {
                        let reference = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
                        try JSONSerialization.data(withJSONObject: reference).write(to: interruptedReference, options: .atomic)
                        try baseline.write(to: marker, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
                    }
                    catch { XCTFail("Could not record interruption"); return }
                    FileHandle.standardError.write(Data("intentional Apply interruption 84\n".utf8))
                    _exit(84)
                }
            }
        } else { manager.session.onFirstReload = { defaults.failNextFlush = true } }
        await store.regenerateAndApply(tunnel: controller)
        XCTAssertTrue(try TunnelConfigurationReceipt(options: candidate).matches(XCTUnwrap(manager.session.reloaded.first)))
        XCTAssertTrue(manager.session.liveOptions == previous)
        XCTAssertEqual(manager.session.status, .connected)
        XCTAssertTrue(manager.readSaved() == previous)
        XCTAssertTrue(try store.systemConnectionOptions() == previous)
        XCTAssertEqual(store.selectedProfile?.pendingApply, true)
        XCTAssertNotEqual(store.lastAction, "applied to the running provider")
        try JSONEncoder().encode(previous).write(to: marker, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    func testReimportOnlyDamagedProfileAcrossRelaunch() throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.reimport-only-profile"
        let marker = docs.appendingPathComponent(suite)
        let phase = (try? String(contentsOf: marker, encoding: .utf8)) ?? "seed"
        let defaults = try XCTUnwrap(IndexFailureDefaults(suiteName: suite))
        if phase == "seed" { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
        let package = docs.appendingPathComponent("mixed.sakamoto")
        let root = try AppPaths.sharedDirectory().appendingPathComponent("Profiles")
        let key = ConfigStore.profilesKey
        if phase == "seed" {
            model.importFromFile(at: package)
            XCTAssertNil(model.importError)
            let id = try XCTUnwrap(store.selectedProfileID)
            let manifest = root.appendingPathComponent(id + "/profile.json")
            var profile = try JSONDecoder().decode(TunnelProfile.self, from: Data(contentsOf: manifest))
            profile.sourceBundleJSON = "{"
            try JSONEncoder().encode(profile).write(to: manifest, options: .atomic)
            try "recover".write(to: marker, atomically: true, encoding: .utf8)
            return
        }
        let ids = try XCTUnwrap(defaults.stringArray(forKey: key + ".ids"))
        let originalID = try XCTUnwrap(ids.first)
        let manifest = root.appendingPathComponent(originalID + "/profile.json")
        if phase == "recover" {
            XCTAssertEqual(ids.count, 1)
            let original = try Data(contentsOf: manifest)
            let references = defaults.data(forKey: key)
            let fm = FileManager.default
            let directories = try Set(fm.contentsOfDirectory(atPath: root.path))
            let exportedFiles = try TunnelProfile.decodePackage(Data(contentsOf: package)).files
            XCTAssertFalse(exportedFiles.isEmpty)
            XCTAssertThrowsError(try store.connectionContent())
            try store.recoverProfileIndex()
            XCTAssertTrue(store.recoveryNotice?.contains(".sakamoto") == true)
            defaults.failNextFlush = true
            model.importFromFile(at: package)
            XCTAssertNotNil(model.importError)
            XCTAssertNil(store.selectedProfileID)
            XCTAssertEqual(defaults.stringArray(forKey: key + ".ids"), ids)
            XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), originalID)
            XCTAssertTrue(defaults.data(forKey: key) == references)
            XCTAssertTrue(try Data(contentsOf: manifest) == original)
            XCTAssertTrue(try Set(fm.contentsOfDirectory(atPath: root.path)) == directories, "Failed import left a profile directory")
            for (name, bytes) in exportedFiles {
                XCTAssertTrue(try Data(contentsOf: manifest.deletingLastPathComponent().appendingPathComponent(name)) == bytes)
            }
            model.importFromFile(at: package)
            XCTAssertNil(model.importError)
            XCTAssertNotEqual(try XCTUnwrap(store.selectedProfileID), originalID)
            XCTAssertTrue(try Data(contentsOf: manifest) == original)
            try "verify".write(to: marker, atomically: true, encoding: .utf8)
            return
        }
        XCTAssertEqual(ids.count, 2)
        XCTAssertEqual(store.selectedProfileID, ids.last)
        let exported = try TunnelProfile.decodePackage(Data(contentsOf: package))
        XCTAssertTrue(store.content == exported.config)
        XCTAssertTrue(store.selectedProfile?.sourceBundleJSON == exported.sourceBundleJSON)
        XCTAssertFalse(try store.connectionContent().isEmpty)
        try FileManager.default.removeItem(at: marker)
        defaults.removePersistentDomain(forName: suite)
    }

    func testMissingBackupManifestAcrossRelaunch() throws {
        try verifyBackupManifest(damaged: false)
    }

    func testCorruptSelectedSourceBundleAcrossRelaunch() throws {
        try verifySelectedBundleDamage(intactIndex: false)
    }

    func testPolicySaveWithCorruptSelectedBundleAcrossRelaunch() throws {
        try verifySelectedBundleDamage(intactIndex: true)
    }

    private func verifySelectedBundleDamage(intactIndex: Bool) throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = intactIndex ? "fixture.intact-index-corrupt-bundle" : "fixture.corrupt-selected-bundle"
        let marker = docs.appendingPathComponent(suite)
        let receipt = docs.appendingPathComponent(suite + "-damaged")
        let phase = (try? String(contentsOf: marker, encoding: .utf8)) ?? "seed"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        if phase == "seed" { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let key = ConfigStore.profilesKey
        let dir = try AppPaths.sharedDirectory().appendingPathComponent("Profiles")
        if phase == "seed" {
            let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
            for name in ["groups.sakamoto", "mixed.sakamoto"] {
                model.importFromFile(at: docs.appendingPathComponent(name))
                XCTAssertNil(model.importError)
            }
            let refs = try JSONDecoder().decode([TunnelProfile].self, from: XCTUnwrap(defaults.data(forKey: key)))
            XCTAssertEqual(refs.count, 2)
            let b = try XCTUnwrap(refs.last)
            if !intactIndex { defaults.set(try JSONEncoder().encode([b]), forKey: key) }
            let file = dir.appendingPathComponent(b.id + "/profile.json")
            var manifest = try JSONDecoder().decode(TunnelProfile.self, from: Data(contentsOf: file))
            manifest.sourceBundleJSON = "{"
            let bytes = try JSONEncoder().encode(manifest)
            try bytes.write(to: file, options: .atomic)
            try bytes.write(to: receipt)
            XCTAssertTrue(defaults.synchronize())
            try "choose".write(to: marker, atomically: true, encoding: .utf8)
            return
        }
        let ids = try XCTUnwrap(defaults.stringArray(forKey: key + ".ids"))
        XCTAssertEqual(ids.count, 2)
        let a = try XCTUnwrap(ids.first)
        let b = try XCTUnwrap(ids.last)
        let file = dir.appendingPathComponent(b + "/profile.json")
        if phase == "choose" {
            let index = try XCTUnwrap(defaults.data(forKey: key))
            if intactIndex {
                let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
                model.beginPolicyRule()
                model.newPolicyType = .domain
                model.newPolicyMatch = "explicit.example"
                model.addPolicyRule()
                XCTAssertTrue(model.policyFormError?.contains("Recover configuration list") == true)
                XCTAssertTrue(try Data(contentsOf: receipt) == Data(contentsOf: file))
            }
            XCTAssertThrowsError(try store.systemConnectionOptions())
            try store.recoverProfileIndex()
            XCTAssertTrue(store.needsRecoveryChoice)
            XCTAssertEqual(store.profiles.map(\.id), [a])
            XCTAssertNil(store.selectedProfileID)
            XCTAssertEqual(defaults.data(forKey: key), index)
            XCTAssertEqual(defaults.stringArray(forKey: key + ".ids"), ids)
            XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), b)
            XCTAssertTrue(try Data(contentsOf: receipt) == Data(contentsOf: file))
            try store.selectProfile(a)
            try "verify".write(to: marker, atomically: true, encoding: .utf8)
            return
        }
        XCTAssertEqual(store.selectedProfileID, a)
        let expected = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto")))
        XCTAssertTrue(store.content == expected.config)
        XCTAssertFalse(try store.connectionContent().isEmpty)
        XCTAssertThrowsError(try store.selectProfile(b))
        XCTAssertEqual(store.selectedProfileID, a)
        XCTAssertTrue(try Data(contentsOf: receipt) == Data(contentsOf: file))
        try fm.removeItem(at: marker)
        try fm.removeItem(at: receipt)
        defaults.removePersistentDomain(forName: suite)
    }

    func testCorruptBackupManifestAcrossRelaunch() throws {
        try verifyBackupManifest(damaged: true)
    }

    private func verifyBackupManifest(damaged: Bool) throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = damaged ? "fixture.corrupt-backup-manifest" : "fixture.missing-backup-manifest"
        let originalA = docs.appendingPathComponent(suite + "-original")
        let truncated = Data(#"{"config":"#.utf8)
        let phaseFile = docs.appendingPathComponent(suite)
        let phase = (try? String(contentsOf: phaseFile, encoding: .utf8)) ?? "seed"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let key = ConfigStore.profilesKey
        if phase == "seed" { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        if phase == "seed" {
            let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
            for filename in ["groups.sakamoto", "mixed.sakamoto"] {
                model.importFromFile(at: docs.appendingPathComponent(filename))
                XCTAssertNil(model.importError)
            }
            let refs = try JSONDecoder().decode([TunnelProfile].self, from: XCTUnwrap(defaults.data(forKey: key)))
            XCTAssertEqual(refs.count, 2)
            let a = try XCTUnwrap(refs.first)
            let b = try XCTUnwrap(refs.last)
            defaults.set(try JSONEncoder().encode([b]), forKey: key)
            let manifest = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(a.id)/profile.json")
            if damaged {
                try Data(contentsOf: manifest).write(to: originalA)
                try truncated.write(to: manifest, options: .atomic)
            } else { try fm.removeItem(at: manifest) }
            XCTAssertTrue(defaults.synchronize())
            try "choose".write(to: phaseFile, atomically: true, encoding: .utf8)
            return
        }
        let b = try XCTUnwrap(defaults.string(forKey: ConfigStore.selectedProfileKey))
        if phase == "choose" {
            let original = defaults.data(forKey: key)
            let backup = try XCTUnwrap(defaults.stringArray(forKey: key + ".ids"))
            XCTAssertEqual(backup.count, 2)
            let missingID = try XCTUnwrap(backup.first)
            XCTAssertThrowsError(try store.systemConnectionOptions())
            try store.recoverProfileIndex()
            XCTAssertTrue(store.needsRecoveryChoice)
            XCTAssertNil(store.selectedProfileID)
            XCTAssertThrowsError(try store.connectionContent())
            XCTAssertEqual(store.profiles.map(\.id), [b])
            let notice = try XCTUnwrap(store.recoveryNotice)
            XCTAssertTrue(notice.contains(missingID))
            if !damaged { XCTAssertTrue(notice.contains("import its .sakamoto package")) }
            XCTAssertEqual(defaults.data(forKey: key), original)
            XCTAssertEqual(defaults.stringArray(forKey: key + ".ids"), backup)
            XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), b)
            try store.selectProfile(b)
            try "verify".write(to: phaseFile, atomically: true, encoding: .utf8)
            return
        }
        XCTAssertEqual(store.selectedProfileID, b)
        let savedIDs = try XCTUnwrap(defaults.stringArray(forKey: key + ".ids"))
        XCTAssertEqual(store.profiles.map(\.id), savedIDs)
        XCTAssertEqual(savedIDs.count, damaged ? 2 : 1)
        let expected = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("mixed.sakamoto")))
        XCTAssertTrue(store.content == expected.config)
        XCTAssertFalse(try store.connectionContent().isEmpty)
        if damaged {
            let a = try XCTUnwrap(savedIDs.first(where: { $0 != b }))
            let manifest = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(a)/profile.json")
            XCTAssertThrowsError(try store.selectProfile(a))
            XCTAssertEqual(store.selectedProfileID, b)
            XCTAssertEqual(try Data(contentsOf: manifest), truncated)
            try Data(contentsOf: originalA).write(to: manifest, options: .atomic)
            try store.selectProfile(a)
            XCTAssertEqual(store.selectedProfileID, a)
            let exportedA = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto")))
            XCTAssertTrue(store.content == exportedA.config)
            XCTAssertFalse(try store.connectionContent().isEmpty)
            try fm.removeItem(at: originalA)
        }
        try fm.removeItem(at: phaseFile)
        defaults.removePersistentDomain(forName: suite)
    }

    func testMissingSelectionRequiresChoiceAcrossRelaunch() throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let phaseFile = docs.appendingPathComponent("missing-selection-phase")
        let phase = (try? String(contentsOf: phaseFile, encoding: .utf8)) ?? "seed"
        for fault in ["missing", "partial", "dangling", "selection-only", "selection-outside-index"] {
            let suite = "fixture.missing-selection." + fault
            let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
            if phase == "seed" { defaults.removePersistentDomain(forName: suite) }
            let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
            let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
            let key = ConfigStore.profilesKey
            if phase == "seed" {
                for filename in ["groups.sakamoto", "mixed.sakamoto"] {
                    model.importFromFile(at: docs.appendingPathComponent(filename))
                    XCTAssertNil(model.importError)
                }
                XCTAssertEqual(store.profiles.count, 2)
                XCTAssertEqual(store.selectedProfileID, store.profiles.last?.id)
                if fault == "partial" || fault == "selection-outside-index" {
                    let data = try XCTUnwrap(defaults.data(forKey: key))
                    let refs = try JSONDecoder().decode([TunnelProfile].self, from: data)
                    defaults.set(try JSONEncoder().encode(Array(refs.prefix(1))), forKey: key)
                }
                if fault == "selection-outside-index" {
                    defaults.set(Array(store.profiles.map(\.id).prefix(1)), forKey: key + ".ids")
                    try JSONEncoder().encode(store.profiles.map(\.id)).write(to: docs.appendingPathComponent(suite))
                } else if fault == "selection-only" {
                    defaults.removeObject(forKey: key)
                    defaults.removeObject(forKey: key + ".ids")
                } else if fault == "dangling" {
                    defaults.set(UUID().uuidString, forKey: ConfigStore.selectedProfileKey)
                } else {
                    defaults.removeObject(forKey: ConfigStore.selectedProfileKey)
                }
                XCTAssertTrue(defaults.synchronize())
                continue
            }
            let backup = defaults.stringArray(forKey: key + ".ids")
            let ids = fault == "selection-outside-index"
                ? try JSONDecoder().decode([String].self, from: Data(contentsOf: docs.appendingPathComponent(suite)))
                : try backup ?? [XCTUnwrap(defaults.string(forKey: ConfigStore.selectedProfileKey))]
            let b = try XCTUnwrap(ids.last)
            if phase == "choose" {
                XCTAssertNil(store.selectedProfileID)
                XCTAssertThrowsError(try store.systemConnectionOptions())
                let index = defaults.data(forKey: key)
                let previousSelection = defaults.string(forKey: ConfigStore.selectedProfileKey)
                try store.recoverProfileIndex()
                XCTAssertTrue(store.needsRecoveryChoice)
                XCTAssertNil(store.selectedProfileID)
                XCTAssertThrowsError(try store.connectionContent())
                XCTAssertEqual(store.profiles.map(\.id), ids)
                XCTAssertTrue(defaults.data(forKey: key) == index)
                XCTAssertEqual(defaults.stringArray(forKey: key + ".ids"), backup)
                XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), previousSelection)
                try store.selectProfile(b)
            }
            XCTAssertEqual(store.profiles.map(\.id), ids)
            XCTAssertEqual(store.selectedProfileID, b)
            XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), b)
            let exported = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("mixed.sakamoto")))
            XCTAssertTrue(store.content == exported.config)
            XCTAssertFalse(try store.connectionContent().isEmpty)
            if phase == "verify" {
                if fault == "selection-outside-index" {
                    try store.selectProfile(try XCTUnwrap(ids.first))
                    let a = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto")))
                    XCTAssertTrue(store.content == a.config)
                    XCTAssertFalse(try store.connectionContent().isEmpty)
                    try fm.removeItem(at: docs.appendingPathComponent(suite))
                }
                defaults.removePersistentDomain(forName: suite)
            }
        }
        if phase == "verify" { try fm.removeItem(at: phaseFile) }
        else { try (phase == "seed" ? "choose" : "verify").write(to: phaseFile, atomically: true, encoding: .utf8) }
    }

    func testImportWithIndexFailureAcrossRelaunch() throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let marker = docs.appendingPathComponent("index-failure-relaunch")
        let restart = fm.fileExists(atPath: marker.path)
        for fault in ["missing", "empty", "partial", "flush"] {
            let suite = "fixture.index-failure." + fault
            let defaults = try XCTUnwrap(IndexFailureDefaults(suiteName: suite))
            if !restart { defaults.removePersistentDomain(forName: suite) }
            func load() -> ConfigStore { ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite)) }
            let index = ConfigStore.profilesKey
            let backup = index + ".ids"
            let receipt = docs.appendingPathComponent(suite)
            if !restart {
                let store = load()
                let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
                for name in ["groups.sakamoto", "mixed.sakamoto"] {
                    model.importFromFile(at: docs.appendingPathComponent(name))
                    XCTAssertNil(model.importError)
                }
                try XCTUnwrap(defaults.data(forKey: index)).write(to: receipt)
                if fault == "missing" { defaults.removeObject(forKey: index) }
                if fault == "empty" { defaults.set(Data("[]".utf8), forKey: index) }
                if fault == "partial" {
                    let references = try JSONDecoder().decode([TunnelProfile].self, from: Data(contentsOf: receipt))
                    defaults.set(try JSONEncoder().encode(Array(references.prefix(1))), forKey: index)
                }
                let before = defaults.data(forKey: index)
                let original = store.profiles.map(\.id)
                let active = try XCTUnwrap(store.selectedProfileID)
                let attempt = load()
                if fault == "partial" {
                    XCTAssertTrue(attempt.indexNeedsRecovery)
                    XCTAssertNil(attempt.selectedProfileID)
                    XCTAssertThrowsError(try attempt.connectionContent())
                }
                let importer = ConfigModel(tunnel: DelayedApplyTunnel(), store: attempt)
                defaults.failNextFlush = fault == "flush"
                importer.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
                XCTAssertNotNil(importer.importError, fault)
                XCTAssertTrue(defaults.data(forKey: index) == before, fault)
                XCTAssertEqual(defaults.stringArray(forKey: backup), original, fault)
                XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), active, fault)
                if fault == "flush" {
                    XCTAssertEqual(attempt.selectedProfileID, active)
                    XCTAssertTrue(try attempt.connectionContent() == store.connectionContent())
                }
                XCTAssertTrue(defaults.synchronize())
            } else {
                let expected = try JSONDecoder().decode([TunnelProfile].self, from: Data(contentsOf: receipt)).map(\.id)
                let store = load()
                if fault != "flush" {
                    XCTAssertTrue(store.indexNeedsRecovery)
                    XCTAssertNil(store.selectedProfileID)
                    try store.recoverProfileIndex()
                }
                XCTAssertEqual(store.profiles.map(\.id), expected, fault)
                XCTAssertEqual(store.selectedProfileID, expected.last, fault)
                for id in expected { try store.selectProfile(id); XCTAssertFalse(try store.connectionContent().isEmpty) }
                let importer = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
                importer.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
                XCTAssertNil(importer.importError)
                XCTAssertEqual(load().profiles.map(\.id), store.profiles.map(\.id))
                XCTAssertEqual(store.profiles.count, 3)
                try fm.removeItem(at: receipt)
                defaults.removePersistentDomain(forName: suite)
            }
        }
        if restart { try fm.removeItem(at: marker) } else { try Data().write(to: marker) }
    }

    func testImportAfterFailedFirstMigrationAcrossRelaunch() throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.import-after-first-migration"
        let marker = docs.appendingPathComponent(suite)
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let runtime = #"{"log":{},"dns":{"servers":[{"type":"local","tag":"local"}]},"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"]}],"outbounds":[{"type":"direct","tag":"legacy-direct"}],"route":{"final":"legacy-direct"}}"#
        if !fm.fileExists(atPath: marker.path) {
            defaults.removePersistentDomain(forName: suite)
            defaults.set(runtime, forKey: ConfigStore.storageKey)
            let dir = try AppPaths.sharedDirectory().appendingPathComponent("Profiles")
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
            let mode = try XCTUnwrap(fm.attributesOfItem(atPath: dir.path)[.posixPermissions] as? NSNumber)
            try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: dir.path)
            defer { try? fm.setAttributes([.posixPermissions: mode], ofItemAtPath: dir.path) }
            let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
            XCTAssertNil(store.selectedProfileID)
            XCTAssertTrue(defaults.string(forKey: ConfigStore.storageKey) == runtime)
            try fm.setAttributes([.posixPermissions: mode], ofItemAtPath: dir.path)
            let model = ConfigModel(tunnel: DelayedApplyTunnel(), store: store)
            model.importFromFile(at: docs.appendingPathComponent("mixed.sakamoto"))
            XCTAssertNil(model.importError)
            XCTAssertEqual(store.profiles.count, 2)
            try XCTUnwrap(store.selectedProfileID).write(to: marker, atomically: true, encoding: .utf8)
            return
        }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let imported = try String(contentsOf: marker, encoding: .utf8)
        XCTAssertEqual(store.selectedProfileID, imported)
        XCTAssertFalse(try store.connectionContent().isEmpty)
        let original = try XCTUnwrap(store.profiles.first(where: { $0.id != imported }))
        try store.selectProfile(original.id)
        XCTAssertTrue(Data(store.content.utf8) == Data(runtime.utf8))
        XCTAssertTrue(try store.connectionContent().contains("legacy-direct"))
        XCTAssertNil(defaults.object(forKey: ConfigStore.storageKey))
        try fm.removeItem(at: marker)
        defaults.removePersistentDomain(forName: suite)
    }

    func testFirstRuntimeMigrationRetriesAfterWriteFailure() throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.first-runtime-migration"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let marker = docs.appendingPathComponent(suite)
        // Standalone runtime from before packages carried separate rule files.
        let runtime = #"{"log":{},"dns":{"servers":[{"type":"local","tag":"local"}]},"inbounds":[{"type":"tun","tag":"tun-in","address":["172.19.0.1/30"]}],"outbounds":[{"type":"direct","tag":"fixture-direct"}],"route":{"final":"fixture-direct"}}"#
        func openStore() -> ConfigStore {
            ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        }
        if !fm.fileExists(atPath: marker.path) {
            defaults.removePersistentDomain(forName: suite)
            defaults.set(runtime, forKey: ConfigStore.storageKey)
            let profiles = try AppPaths.sharedDirectory().appendingPathComponent("Profiles", isDirectory: true)
            try fm.createDirectory(at: profiles, withIntermediateDirectories: true)
            let mode = try XCTUnwrap(fm.attributesOfItem(atPath: profiles.path)[.posixPermissions] as? NSNumber)
            try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: profiles.path)
            defer { try? fm.setAttributes([.posixPermissions: mode], ofItemAtPath: profiles.path) }
            let failed = openStore()
            XCTAssertNotNil(failed.profileError)
            XCTAssertNil(failed.selectedProfileID)
            XCTAssertNil(defaults.object(forKey: ConfigStore.profilesKey))
            XCTAssertNil(defaults.object(forKey: ConfigStore.selectedProfileKey))
            XCTAssertTrue(defaults.string(forKey: ConfigStore.storageKey).map { Data($0.utf8) } == Data(runtime.utf8))
            XCTAssertTrue(defaults.synchronize())
            try Data().write(to: marker)
            return
        }
        let migrated = openStore()
        let id = try XCTUnwrap(migrated.selectedProfileID)
        XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), id)
        XCTAssertEqual(try JSONDecoder().decode([TunnelProfile].self, from: XCTUnwrap(defaults.data(forKey: ConfigStore.profilesKey))).map(\.id), [id])
        let manifest = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(id)/profile.json")
        let saved = try JSONDecoder().decode(TunnelProfile.self, from: Data(contentsOf: manifest))
        XCTAssertTrue(Data(saved.config.utf8) == Data(runtime.utf8))
        XCTAssertNil(defaults.object(forKey: ConfigStore.storageKey))
        let reopened = openStore()
        XCTAssertEqual(reopened.selectedProfileID, id)
        XCTAssertTrue(try reopened.connectionContent().contains("fixture-direct"))
        try fm.removeItem(at: marker)
        defaults.removePersistentDomain(forName: suite)
    }

    func testUpgradeRuntimeAndIndexRecoveryAcrossRelaunch() throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.upgrade-index-recovery"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let receipt = docs.appendingPathComponent(suite)
        let runtime = #"{"outbounds":[{"type":"trojan","password":"retired-fixture"}]}"#
        func openStore() -> ConfigStore { ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite)) }
        if !fm.fileExists(atPath: receipt.path) {
            defaults.removePersistentDomain(forName: suite)
            let store = openStore()
            for filename in ["mixed.sakamoto", "groups.sakamoto"] {
                try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent(filename))))
            }
            try XCTUnwrap(defaults.data(forKey: ConfigStore.profilesKey)).write(to: receipt)
            defaults.set(runtime, forKey: ConfigStore.storageKey)
            XCTAssertTrue(defaults.synchronize())
            return
        }
        let index = try Data(contentsOf: receipt)
        var records = try XCTUnwrap(JSONSerialization.jsonObject(with: index) as? [[String: Any]])
        let ids = try records.map { try XCTUnwrap($0["id"] as? String) }
        let active = try XCTUnwrap(defaults.string(forKey: ConfigStore.selectedProfileKey))
        let root = try AppPaths.sharedDirectory().appendingPathComponent("Profiles")
        let manifest = root.appendingPathComponent(active + "/profile.json")
        try fm.setAttributes([.posixPermissions: 0o000], ofItemAtPath: manifest.path)
        defer { try? fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: manifest.path) }
        XCTAssertNil(openStore().selectedProfile)
        XCTAssertTrue(defaults.string(forKey: ConfigStore.storageKey) == runtime)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: manifest.path)
        let restored = openStore()
        XCTAssertEqual(restored.selectedProfileID, active)
        XCTAssertFalse(try restored.connectionContent().isEmpty)
        XCTAssertNil(defaults.object(forKey: ConfigStore.storageKey))
        records[0]["name"] = 42
        let damaged = try JSONSerialization.data(withJSONObject: records)
        defaults.set(damaged, forKey: ConfigStore.profilesKey)
        let locked = openStore()
        let c = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("mixed.sakamoto")))
        XCTAssertThrowsError(try locked.installProfile(c))
        XCTAssertTrue(defaults.data(forKey: ConfigStore.profilesKey) == damaged)
        XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), active)
        try locked.recoverProfileIndex()
        XCTAssertEqual(locked.profiles.map(\.id), ids)
        XCTAssertEqual(locked.selectedProfileID, active)
        try locked.installProfile(c)
        let reopened = openStore()
        XCTAssertEqual(reopened.profiles.map(\.id), ids + [c.id])
        for id in ids { try reopened.selectProfile(id); XCTAssertFalse(try reopened.connectionContent().isEmpty) }
        try fm.removeItem(at: receipt)
        defaults.removePersistentDomain(forName: suite)
    }

    func testRetryUnreadableManifestAcrossRelaunch() throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.unreadable-manifest"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let receipt = docs.appendingPathComponent(suite)
        let restart = fm.fileExists(atPath: receipt.path)
        if !restart { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let root = try AppPaths.sharedDirectory().appendingPathComponent("Profiles")
        if !restart {
            let b = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("mixed.sakamoto")))
            let a = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto")))
            try store.installProfile(b)
            try store.installProfile(a)
            let manifest = root.appendingPathComponent(b.id + "/profile.json")
            try Data(contentsOf: manifest).write(to: receipt)
            try fm.setAttributes([.posixPermissions: 0o000], ofItemAtPath: manifest.path)
            return
        }
        let before = try Data(contentsOf: receipt)
        let b = try JSONDecoder().decode(TunnelProfile.self, from: before)
        let manifest = root.appendingPathComponent(b.id + "/profile.json")
        defer { try? fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: manifest.path) }
        let a = try XCTUnwrap(store.selectedProfileID)
        let runtimeA = try store.connectionContent()
        XCTAssertNotEqual(a, b.id)
        XCTAssertThrowsError(try store.selectProfile(b.id))
        XCTAssertEqual(store.selectedProfileID, a)
        XCTAssertEqual(defaults.string(forKey: ConfigStore.selectedProfileKey), a)
        XCTAssertTrue(try store.connectionContent() == runtimeA)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: manifest.path)
        XCTAssertTrue(try Data(contentsOf: manifest) == before)
        try store.selectProfile(b.id)
        XCTAssertEqual(store.selectedProfileID, b.id)
        XCTAssertTrue(store.selectedProfile?.sourceBundleJSON == b.sourceBundleJSON)
        XCTAssertTrue(store.content == b.config)
        XCTAssertFalse(try store.connectionContent().isEmpty)
        try fm.removeItem(at: receipt)
        defaults.removePersistentDomain(forName: suite)
    }

    func testInstalledProfileMigratesLegacyPreferences() async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let endpoint = docs.appendingPathComponent("draft-fixture-endpoint")
        try XCTSkipUnless(fm.fileExists(atPath: endpoint.path))
        let base = try String(contentsOf: endpoint, encoding: .utf8)
        let marker = docs.appendingPathComponent("legacy-drafts-relaunch")
        let restarting = fm.fileExists(atPath: marker.path)
        for deny in [false, true] {
            let suite = "fixture.legacy-drafts." + String(deny)
            let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
            let vault = TailscaleKeychainStore(account: suite)
            let feed = base + "/adopted.json"
            let keys = [ConfigStore.nodesSourcesKey, ConfigStore.importKey]
            if !restarting {
                defaults.removePersistentDomain(forName: suite)
                let store = ConfigStore(persistence: defaults, keyStore: vault)
                try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
                try store.commitNodesSources(store.nodesSources.adding(subscription: StagedSubscription(name: "Current", url: feed, format: "singbox")))
                let old = try ConfigModel.validateNode("trojan://retired-fixture@old.invalid:443#Old")
                let oldBook = NodesSourcesBook(nodes: [old]).adding(subscription: StagedSubscription(name: "Old", url: base + "/retired-fixture"))
                defaults.set(try oldBook.encoded(), forKey: keys[0])
                defaults.set(try ImportedSource(displaySource: "Old.conf", content: "# retired-fixture").encoded(), forKey: keys[1])
                XCTAssertTrue(defaults.synchronize())
                continue
            }
            let id = try XCTUnwrap(defaults.string(forKey: "sakamoto.privateDraftDirectory"))
            let parent = try AppPaths.sharedDirectory().appendingPathComponent("PrivateDrafts")
            let directory = parent.appendingPathComponent(id)
            let bytes = try keys.map { try XCTUnwrap(defaults.data(forKey: $0)) }
            if deny {
                try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: directory.path)
                defer { try? fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path) }
                let store = ConfigStore(persistence: defaults, keyStore: vault)
                XCTAssertEqual(store.nodesSources.subscriptions.map(\.url), [feed])
                XCTAssertFalse(try store.connectionContent().isEmpty)
                XCTAssertTrue(keys.map { defaults.data(forKey: $0) } == bytes)
            }
            let store = ConfigStore(persistence: defaults, keyStore: vault)
            XCTAssertTrue(keys.allSatisfy { defaults.object(forKey: $0) == nil })
            XCTAssertEqual(try parent.resourceValues(forKeys: [.isExcludedFromBackupKey]).isExcludedFromBackup, true)
            for (key, expected) in zip(keys, bytes) {
                XCTAssertTrue(try Data(contentsOf: directory.appendingPathComponent("legacy-" + key)) == expected)
            }
            XCTAssertEqual(store.nodesSources.subscriptions.map(\.url), [feed])
            try await store.generateFromSources()
            XCTAssertTrue(try store.connectionContent().contains("FromAdoptedFeed"))
            XCTAssertFalse(store.sourceBundle.files.values.contains(where: { $0.contains("retired-fixture") }))
            defaults.removePersistentDomain(forName: suite)
        }
        if restarting { try fm.removeItem(at: marker) } else { try Data().write(to: marker) }
    }

    func testICloudAdoptedSourcesSurviveStaleDraft() async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let endpoint = docs.appendingPathComponent("draft-fixture-endpoint")
        try XCTSkipUnless(fm.fileExists(atPath: endpoint.path))
        let base = try String(contentsOf: endpoint, encoding: .utf8)
        let suite = "fixture.icloud-stale-draft"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let root = docs.appendingPathComponent(suite)
        let remote = root.appendingPathComponent("remote", isDirectory: true)
        let marker = root.appendingPathComponent("restart")
        let restarting = fm.fileExists(atPath: marker.path)
        if !restarting {
            defaults.removePersistentDomain(forName: suite)
            if fm.fileExists(atPath: root.path) { try fm.removeItem(at: root) }
            try fm.createDirectory(at: remote, withIntermediateDirectories: true)
            defaults.set(root.appendingPathComponent("local").path, forKey: "sakamoto.icloud.sync.localdir")
        }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let adopted = "trojan://new-fixture@new.invalid:443#Adopted"
        let feed = base + "/adopted.json"
        if !restarting {
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
            let old = try ConfigModel.validateNode("trojan://retired-fixture@old.invalid:443#Retired")
            try store.commitNodesSources(store.nodesSources.adding(node: old).adding(subscription: StagedSubscription(name: "Old", url: base + "/retired-fixture")))
            let page = ICloudSyncModel(store: store, defaults: defaults, cloudFilesystem: LocalFilesystem(), accessDirectory: { _ in true }, releaseDirectory: { _ in })
            page.selectDirectory(.success(remote)); page.confirmEnable()
            func sync() async throws {
                page.syncNow()
                for _ in 0..<400 where page.syncing { try await Task.sleep(nanoseconds: 25_000_000) }
                guard case .synced = page.status else { return XCTFail(page.status.summary) }
            }
            try await sync()
            try Data((adopted + "\n").utf8).write(to: remote.appendingPathComponent("nodes.txt"))
            try JSONSerialization.data(withJSONObject: [["name": "New", "url": feed, "format": "singbox"]]).write(to: remote.appendingPathComponent("subscriptions.json"))
            let draftID = try XCTUnwrap(defaults.string(forKey: "sakamoto.privateDraftDirectory"))
            let dir = try AppPaths.sharedDirectory().appendingPathComponent("PrivateDrafts/\(draftID)")
            let file = dir.appendingPathComponent(ConfigStore.nodesSourcesKey)
            let stale = try Data(contentsOf: file)
            try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: dir.path)
            defer { try? fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: dir.path) }
            try await sync()
            XCTAssertTrue(try Data(contentsOf: file) == stale)
            XCTAssertEqual(store.nodesSources.subscriptions.map(\.url), [feed])
            XCTAssertTrue(defaults.synchronize())
            try Data().write(to: marker)
        } else {
            XCTAssertEqual(store.nodesSources.nodes.map(\.rawLink), [adopted])
            XCTAssertEqual(store.nodesSources.subscriptions.map(\.url), [feed])
            let added = try ConfigModel.validateNode("trojan://added-fixture@added.invalid:443#Added")
            try store.commitNodesSources(store.nodesSources.adding(node: added))
            try await store.generateFromSources()
            XCTAssertEqual(store.nodesSources.subscriptions.map(\.url), [feed])
            let runtime = try store.connectionContent()
            for tag in ["Adopted", "Added", "FromAdoptedFeed"] { XCTAssertTrue(runtime.contains(tag), tag) }
            XCTAssertFalse(runtime.contains("retired-fixture"))
            XCTAssertFalse(store.sourceBundle.files.values.contains(where: { $0.contains("retired-fixture") }))
            try fm.removeItem(at: root)
            defaults.removePersistentDomain(forName: suite)
        }
    }

    func testICloudCredentialDownloadsAcrossRelaunch() async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let marker = docs.appendingPathComponent("credential-download-relaunch")
        let restarting = fm.fileExists(atPath: marker.path)
        for reject in [true, false] {
            let suite = "fixture.icloud-credential-download." + (reject ? "failed" : "saved")
            let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
            let root = docs.appendingPathComponent(suite)
            let local = root.appendingPathComponent("local", isDirectory: true)
            let sources = local.appendingPathComponent("sources", isDirectory: true)
            let remote = root.appendingPathComponent("remote", isDirectory: true)
            let originalManifest = root.appendingPathComponent("before.json")
            let nodes = Data("trojan://fixture-secret@remote.invalid:443#Remote\n".utf8)
            let subs = Data(#"[{"name":"fixture","url":"https://remote.invalid/feed?token=fixture-token","format":"auto"}]"#.utf8)
            if !restarting {
                defaults.removePersistentDomain(forName: suite)
                if fm.fileExists(atPath: root.path) { try fm.removeItem(at: root) }
                try fm.createDirectory(at: sources, withIntermediateDirectories: true)
                try fm.createDirectory(at: remote, withIntermediateDirectories: true)
                try nodes.write(to: sources.appendingPathComponent("nodes.txt"))
                try subs.write(to: sources.appendingPathComponent("subscriptions.json"))
                defaults.set(local.path, forKey: "sakamoto.icloud.sync.localdir")
            }
            let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
            if !restarting {
                try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
            }
            let page = ICloudSyncModel(store: store, defaults: defaults, cloudFilesystem: LocalFilesystem(), accessDirectory: { _ in true }, releaseDirectory: { _ in })
            func checkPrivacy() throws {
                XCTAssertEqual(try local.resourceValues(forKeys: [.isExcludedFromBackupKey]).isExcludedFromBackup, true)
                XCTAssertFalse(fm.fileExists(atPath: sources.path))
            }
            try checkPrivacy()
            let id = try XCTUnwrap(store.selectedProfileID)
            let profileDir = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(id)")
            let manifest = profileDir.appendingPathComponent("profile.json")
            func sync() async throws {
                page.syncNow()
                for _ in 0..<400 where page.syncing { try await Task.sleep(nanoseconds: 25_000_000) }
                XCTAssertFalse(page.syncing)
            }
            if !restarting {
                try Data(contentsOf: manifest).write(to: originalManifest)
                page.selectDirectory(.success(remote)); page.confirmEnable()
                try await sync()
                guard case .synced = page.status else { return XCTFail(page.status.summary) }
                try nodes.write(to: remote.appendingPathComponent("nodes.txt"))
                try subs.write(to: remote.appendingPathComponent("subscriptions.json"))
                if reject { try fm.setAttributes([.posixPermissions: 0o500], ofItemAtPath: profileDir.path) }
                defer { try? fm.setAttributes([.posixPermissions: 0o700], ofItemAtPath: profileDir.path) }
                try await sync()
                if reject {
                    guard case .failed = page.status else { return XCTFail("Expected manifest write failure") }
                } else {
                    guard case .synced = page.status else { return XCTFail(page.status.summary) }
                }
                try checkPrivacy()
                XCTAssertTrue(defaults.synchronize())
            }
            if reject {
                XCTAssertTrue(try Data(contentsOf: manifest) == Data(contentsOf: originalManifest))
                XCTAssertFalse(try store.connectionContent().isEmpty)
            } else {
                XCTAssertTrue(store.sourceBundle.files["nodes.txt"] == String(decoding: nodes, as: UTF8.self))
                XCTAssertTrue(store.sourceBundle.files["subscriptions.json"] == String(decoding: subs, as: UTF8.self))
            }
            if restarting { try fm.removeItem(at: root); defaults.removePersistentDomain(forName: suite) }
        }
        if restarting { try fm.removeItem(at: marker) } else { try Data().write(to: marker) }
    }

    func testICloudUploadsSavedReversionAfterDownload() async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.icloud-edit." + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let root = fm.temporaryDirectory.appendingPathComponent(suite)
        let remote = root.appendingPathComponent("remote", isDirectory: true)
        try fm.createDirectory(at: remote, withIntermediateDirectories: true)
        defer { try? fm.removeItem(at: root); defaults.removePersistentDomain(forName: suite) }
        defaults.set(root.appendingPathComponent("local").path, forKey: "sakamoto.icloud.sync.localdir")
        let vault = TailscaleKeychainStore(account: suite)
        let store = ConfigStore(persistence: defaults, keyStore: vault)
        try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
        let original = NodesSourcesBook(nodes: [try ConfigModel.validateNode("trojan://fixture@a.invalid:443#A")])
        try store.commitNodesSources(original)
        let a = Data(try XCTUnwrap(store.sourceBundle.files["nodes.txt"]).utf8)
        let b = Data("trojan://fixture@b.invalid:443#B\n".utf8)
        let cloudFile = remote.appendingPathComponent("nodes.txt")
        func sync(_ active: ConfigStore) async throws {
            let page = ICloudSyncModel(store: active, defaults: defaults, cloudFilesystem: LocalFilesystem(), accessDirectory: { _ in true }, releaseDirectory: { _ in })
            page.selectDirectory(.success(remote)); page.confirmEnable(); page.syncNow()
            for _ in 0..<400 where page.syncing { try await Task.sleep(nanoseconds: 25_000_000) }
            guard case .synced = page.status else { return XCTFail(page.status.summary) }
        }
        try await sync(store)
        XCTAssertTrue(try Data(contentsOf: cloudFile) == a)
        try b.write(to: cloudFile)
        try await sync(store)
        XCTAssertEqual(store.sourceBundle.files["nodes.txt"], String(decoding: b, as: UTF8.self))
        try store.commitNodesSources(original)
        let reopened = ConfigStore(persistence: defaults, keyStore: vault)
        XCTAssertEqual(reopened.sourceBundle.files["nodes.txt"], String(decoding: a, as: UTF8.self))
        try await sync(reopened)
        XCTAssertEqual(reopened.sourceBundle.files["nodes.txt"], String(decoding: a, as: UTF8.self))
        XCTAssertTrue(try Data(contentsOf: cloudFile) == a)
    }

    func testICloudSyncUsesProfileSelectedAtTap() async throws {
        let fm = FileManager.default
        let docs = fm.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let suite = "fixture.icloud-click." + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let root = fm.temporaryDirectory.appendingPathComponent(suite)
        let remote = root.appendingPathComponent("remote", isDirectory: true)
        try fm.createDirectory(at: remote, withIntermediateDirectories: true)
        defer { try? fm.removeItem(at: root); defaults.removePersistentDomain(forName: suite) }
        defaults.set(root.appendingPathComponent("local").path, forKey: "sakamoto.icloud.sync.localdir")
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        func install(_ filename: String, marker: String) throws -> String {
            let profile = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent(filename)))
            try store.installProfile(profile)
            let node = try ConfigModel.validateNode("trojan://fixture@\(marker).invalid:443#\(marker)")
            let subscription = StagedSubscription(name: marker, url: "https://\(marker).invalid/feed", format: "auto")
            try store.commitNodesSources(store.nodesSources.adding(node: node).adding(subscription: subscription))
            return profile.id
        }
        let b = try install("mixed.sakamoto", marker: "fixture-b")
        let bSources = try XCTUnwrap(store.selectedProfile?.sourceBundleJSON)
        let a = try install("groups.sakamoto", marker: "fixture-a")
        let aFiles = store.sourceBundle.files
        let page = ICloudSyncModel(store: store, defaults: defaults, cloudFilesystem: LocalFilesystem(), accessDirectory: { _ in true }, releaseDirectory: { _ in })
        page.selectDirectory(.success(remote))
        XCTAssertTrue(page.hasSyncDirectory)
        page.confirmEnable()
        func finishPass() async throws {
            for _ in 0..<400 where page.syncing { try await Task.sleep(nanoseconds: 25_000_000) }
            XCTAssertFalse(page.syncing)
        }
        page.syncNow()
        try store.selectProfile(b)
        try await finishPass()
        guard case .failed = page.status else { return XCTFail("Expected stale sync to fail") }
        XCTAssertEqual(try fm.contentsOfDirectory(atPath: remote.path), [])
        XCTAssertEqual(store.selectedProfile?.sourceBundleJSON, bSources)
        try store.selectProfile(a)
        page.syncNow()
        try await finishPass()
        guard case .synced = page.status else { return XCTFail(page.status.summary) }
        for (name, content) in aFiles {
            let uploaded = try Data(contentsOf: remote.appendingPathComponent(name))
            XCTAssertTrue(uploaded == Data(content.utf8), "Unexpected contents for \(name)")
        }
    }

    func testS3SettingsLifecycle() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let endpointFile = docs.appendingPathComponent("s3-endpoint")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: endpointFile.path))
        let suite = "fixture.s3-settings"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("s3-restart").path)
        let vault = TailscaleKeychainStore(account: suite)
        if !restart { defaults.removePersistentDomain(forName: suite); try vault.deleteAuthKey() }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite + ".tunnel"))
        let page = S3SyncModel(store: store, defaults: defaults, vault: vault)
        func waitUntil(_ condition: () -> Bool) async throws {
            for _ in 0..<400 {
                if condition() { return }
                try await Task.sleep(nanoseconds: 50_000_000)
            }
            throw AppSetupError("S3 fixture timed out: \(page.notice)")
        }
        func sync() async throws {
            page.syncNow()
            try await waitUntil { !page.syncing }
            XCTAssertTrue(page.notice.hasPrefix("Synced "), page.notice)
        }
        if restart {
            XCTAssertEqual(page.settings.prefix, "control")
            try await sync()
            try vault.deleteAuthKey()
            return
        }
        try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
        let firstID = try XCTUnwrap(store.selectedProfileID)
        try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("mixed.sakamoto"))))
        let otherID = try XCTUnwrap(store.selectedProfileID)
        let otherSources = store.sourceBundle.files
        try store.selectProfile(firstID)
        page.settings.enabled = true
        page.settings.endpoint = try String(contentsOf: endpointFile, encoding: .utf8)
        page.settings.bucket = "fixture"
        let initial = S3SyncCredentials(accessKey: "fixture", secretKey: "fixture")
        for prefix in ["cancel", "switch", "edit", "control"] {
            page.settings.prefix = prefix
            XCTAssertNotNil(page.beginCredentialEdit())
            XCTAssertTrue(page.saveSettings(credentials: initial))
            try await sync()
            var local = store.sourceBundle
            let main = try XCTUnwrap(local.files[local.mainConf])
            local.files[local.mainConf] = main + "\n# pending-\(prefix)-fixture\n"
            try store.adoptSourceBundle(local)
            page.syncNow()
            try await waitUntil { FileManager.default.fileExists(atPath: docs.appendingPathComponent("s3-waiting-" + prefix).path) }
            if prefix == "cancel" { page.settings.enabled = false }
            if prefix == "switch" { try store.selectProfile(otherID) }
            if prefix == "edit" {
                local.files[local.mainConf, default: ""] += "\n# newer edit\n"
                try store.adoptSourceBundle(local)
            }
            try Data().write(to: docs.appendingPathComponent("s3-release-" + prefix))
            try await waitUntil { !page.syncing }
            XCTAssertEqual(page.notice.hasPrefix("Synced "), prefix == "control", page.notice)
            if prefix == "switch" {
                XCTAssertEqual(store.sourceBundle.files, otherSources)
                try store.selectProfile(firstID)
            }
            if prefix == "edit" { XCTAssertEqual(store.sourceBundle.files, local.files) }
            page.settings.enabled = true
        }
        let colliding = TailscaleKeychainStore(account: suite + ".occupied")
        try colliding.storeAuthKey("fixture-occupied")
        defer { try? colliding.deleteAuthKey() }
        let failingVault = TailscaleKeychainStore(account: suite, update: { query, attributes in
            var values = attributes as! [String: Any]
            values[kSecAttrAccount as String] = suite + ".occupied"
            return SecItemUpdate(query, values as CFDictionary)
        })
        let failing = S3SyncModel(store: store, defaults: defaults, vault: failingVault)
        var draft = try XCTUnwrap(failing.beginCredentialEdit())
        draft.secretKey = "invalid-replacement"
        failing.settings.prefix = "failed-save"
        XCTAssertFalse(failing.saveSettings(credentials: draft))
        XCTAssertTrue(failing.notice.contains(String(errSecDuplicateItem)), failing.notice)
        failing.endCredentialEdit()
        let readFault = UnavailableVault(vault)
        let reopened = S3SyncModel(store: store, defaults: defaults, vault: readFault)
        XCTAssertNil(reopened.beginCredentialEdit())
        readFault.unavailable = false
        XCTAssertFalse(reopened.saveSettings(credentials: S3SyncCredentials()))
        draft = try XCTUnwrap(reopened.beginCredentialEdit())
        XCTAssertTrue(draft == initial)
        draft.secretKey = "discarded-draft"
        reopened.endCredentialEdit()
        XCTAssertFalse(reopened.saveSettings(credentials: draft))
        try Data().write(to: docs.appendingPathComponent("s3-after-rejection"))
        reopened.syncNow()
        try await waitUntil { !reopened.syncing }
        XCTAssertTrue(reopened.notice.hasPrefix("Synced "), reopened.notice)
        try Data().write(to: docs.appendingPathComponent("s3-restart"))
    }

    func testSOCKSGroupLifecycle() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let file = docs.appendingPathComponent("socks-groups.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: file.path))
        let suite = "fixture.socks-group"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let restarting = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        if !restarting { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        var report = [String: [String]]()
        var tolerances = [String: Int]()
        func verify(_ stage: String) throws {
            let content = try store.connectionContent()
            let nodes = ProxyGroupEditing.outbounds(content)
            XCTAssertEqual(nodes.first { $0["tag"] as? String == "Allowed" }?["type"] as? String, "socks")
            XCTAssertEqual(nodes.first { $0["tag"] as? String == "Excluded" }?["type"] as? String, "vless")
            let group = try XCTUnwrap(ProxyGroupEditing.groups(content).first { $0.tag == "OthersAuto" })
            XCTAssertEqual(group.members, ["Allowed"])
            let tolerance = try XCTUnwrap(nodes.first { $0["tag"] as? String == "OthersAuto" }?["tolerance"] as? Int)
            XCTAssertEqual(tolerance, 250, stage)
            tolerances[stage] = tolerance
            report[stage] = group.members
        }
        if restarting { try verify("restart") } else {
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: file)))
            try verify("import")
            for pass in 1...2 { try await store.generateFromSources(); try verify("generate\(pass)") }
        }
        let output = docs.appendingPathComponent(restarting ? "socks-restart.json" : "socks-first.json")
        try JSONEncoder().encode(report).write(to: output)
        try JSONEncoder().encode(tolerances).write(to: docs.appendingPathComponent(restarting ? "tolerance-restart.json" : "tolerance-first.json"))
    }

    func testImplicitChainAndLegacyGeneration() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let package = docs.appendingPathComponent("implicit.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: package.path))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let suiteFile = docs.appendingPathComponent("implicit-test-suite")
        if !restart { try UUID().uuidString.write(to: suiteFile, atomically: true, encoding: .utf8) }
        let suite = "fixture.chain." + (try String(contentsOf: suiteFile, encoding: .utf8))
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        var report = [String: [[String]]]()
        func check(_ stage: String, _ runtime: String) throws {
            let rows = ProxyGroupEditing.outbounds(runtime)
            func row(_ tag: String) throws -> [String: Any] { try XCTUnwrap(rows.first { $0["tag"] as? String == tag }) }
            let observed = [try XCTUnwrap(row("OthersAuto")["outbounds"] as? [String]), try XCTUnwrap(row("ManualPick")["outbounds"] as? [String]), [try XCTUnwrap(row("ExitOne")["detour"] as? String)]]
            XCTAssertEqual(observed, [["Allowed"], ["Allowed", "Excluded"], ["MainProxy"]], stage)
            report[stage] = observed
        }
        if restart { try check("restart", store.connectionContent()) } else {
            let legacy = try TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("legacy-sr-only.sakamoto")))
            try store.installProfile(legacy)
            let before = store.selectedProfile
            XCTAssertFalse(store.canGenerate)
            let hint = try XCTUnwrap(store.generationUpgradeMessage)
            XCTAssertTrue(hint.hasPrefix("Reimport"))
            do { try await store.generateFromSources(); XCTFail("Generated an incomplete legacy profile") }
            catch { XCTAssertTrue(error.localizedDescription.hasPrefix("Reimport")); report["legacy"] = [[error.localizedDescription]] }
            XCTAssertEqual(before, store.selectedProfile)
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: package)))
            try check("import", store.connectionContent())
            for pass in 1...2 { try await store.generateFromSources(); try check("generate\(pass)", store.connectionContent()) }
            let tunnel = FinalRouteFixture()
            await store.regenerateAndApply(tunnel: tunnel)
            try check("apply", tunnel.content)
        }
        try JSONEncoder().encode(report).write(to: docs.appendingPathComponent(restart ? "implicit-restart.json" : "implicit-first.json"))
    }

    func testMixedGenerationLifecycle() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let package = docs.appendingPathComponent("mixed.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: package.path))
        let restarting = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let suiteFile = docs.appendingPathComponent("mixed-test-suite")
        if !restarting { try ("fixture.mixed." + UUID().uuidString).write(to: suiteFile, atomically: true, encoding: .utf8) }
        let suite = try String(contentsOf: suiteFile, encoding: .utf8)
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        if !restarting { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        var report = [String: [String]]()
        func verify(_ stage: String, _ content: String) throws {
            let rows = ProxyGroupEditing.outbounds(content)
            let members = try XCTUnwrap(rows.first { $0["tag"] as? String == "OthersAuto" }?["outbounds"] as? [String])
            let tls = try XCTUnwrap(rows.first { $0["tag"] as? String == "RealityLink" }?["tls"] as? [String: Any])
            let utls = try XCTUnwrap(tls["utls"] as? [String: Any])
            report[stage] = members + [try XCTUnwrap(utls["fingerprint"] as? String)]
            XCTAssertEqual(report[stage], ["Imported", "Allowed", "firefox"], stage)
        }
        let identity = docs.appendingPathComponent("mixed-applied-identity.json")
        func selectedIdentity() throws -> [String] {
            [try XCTUnwrap(store.selectedProfileID), try XCTUnwrap(store.selectedProfile?.ruleRevisionID)]
        }
        if restarting {
            XCTAssertEqual(try selectedIdentity(), try JSONDecoder().decode([String].self, from: Data(contentsOf: identity)))
            try verify("restart", store.connectionContent())
        } else {
            let bytes = try Data(contentsOf: package)
            try store.installProfile(TunnelProfile.decodePackage(bytes))
            try verify("import", store.connectionContent())
            for pass in 1...2 { try await store.generateFromSources(); try verify("generate\(pass)", store.connectionContent()) }
            let applied = try XCTUnwrap(store.selectedProfileID)
            let tunnel = FinalRouteFixture()
            await store.regenerateAndApply(tunnel: tunnel)
            try verify("apply", tunnel.content)
            try store.installProfile(TunnelProfile.decodePackage(bytes))
            let url = try String(contentsOf: docs.appendingPathComponent("subscription-url"), encoding: .utf8)
            try store.commitNodesSources(store.nodesSources.adding(subscription: StagedSubscription(name: "Delayed", url: url)))
            let snapshot = try XCTUnwrap(store.selectedProfile)
            let manifest = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/" + snapshot.id + "/profile.json")
            let before = try Data(contentsOf: manifest)
            let generating = Task { try await store.generateFromSources() }
            let started = docs.appendingPathComponent("subscription-started").path
            for _ in 0..<500 where !FileManager.default.fileExists(atPath: started) { try await Task.sleep(nanoseconds: 10_000_000) }
            XCTAssertTrue(FileManager.default.fileExists(atPath: started))
            generating.cancel()
            try Data().write(to: docs.appendingPathComponent("subscription-release"))
            do { try await generating.value; XCTFail("Cancelled generation published") } catch is CancellationError { }
            XCTAssertEqual(store.selectedProfile, snapshot)
            XCTAssertEqual(try Data(contentsOf: manifest), before)
            let reopened = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
            XCTAssertEqual(reopened.selectedProfile?.ruleRevisionID, snapshot.ruleRevisionID)
            XCTAssertEqual(reopened.content, snapshot.config)
            report["cancel"] = ["revision and manifest preserved"]
            try store.selectProfile(applied)
            try verify("selected", store.connectionContent())
            try JSONEncoder().encode(selectedIdentity()).write(to: identity)
        }
        try JSONEncoder().encode(report).write(to: docs.appendingPathComponent(restarting ? "mixed-restart.json" : "mixed-first.json"))
    }

    func testOnDemandPreferenceRollback() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let file = docs.appendingPathComponent("on-demand-system.archive")
        let manager = FixtureVPNPreferences()
        manager.onDemandFile = file
        let expected = AutomaticConnectionSettings(mode: .domains, domains: ["trigger.fixture.example"], probeURL: "http://127.0.0.1/check")
        try await SystemTunnelControl.$testManager.withValue(manager) {
            if !restart {
                if FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
                let suite = "fixture.ondemand." + UUID().uuidString
                let store = ConfigStore(persistence: try XCTUnwrap(UserDefaults(suiteName: suite)), keyStore: TailscaleKeychainStore(account: suite))
                try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
                await store.regenerateAndApply(tunnel: FinalRouteFixture())
                manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: SystemTunnelControl.providerIdentifier, options: store.connectionOptions())
                try await SystemTunnelControl.setAutomaticConnection(expected)
                let page = AutomaticConnectionForm()
                manager.failNextRead = true
                await page.load()
                XCTAssertFalse(page.canSave)
                await page.save()
                XCTAssertEqual(manager.demandWrites, [true])
                await page.load()
                XCTAssertTrue(page.canSave)
                XCTAssertEqual(page.mode, .domains)
                XCTAssertEqual(page.domains, expected.domains.joined(separator: "\n"))
                XCTAssertEqual(page.probeURL, expected.probeURL)
                manager.failReadAfterSave = true
                do { try await SystemTunnelControl.setAutomaticConnection(.init()); XCTFail("Accepted failed preference reload") }
                catch { XCTAssertEqual((error as NSError).domain, "FixturePreferences") }
                XCTAssertEqual(manager.demandWrites, [true, false, true])
                let options = try store.connectionOptions()
                let controller = NETunnelController(providerBundleIdentifier: SystemTunnelControl.providerIdentifier, manager: manager)
                manager.session.liveOptions = options
                manager.session.started = true
                manager.holdNextSave = true
                let stopping = Task { try await controller.disconnect() }
                for _ in 0..<500 where manager.finishSave == nil { try await Task.sleep(nanoseconds: 10_000_000) }
                let release = try XCTUnwrap(manager.finishSave)
                let reconnect = Task { @MainActor in try await controller.connect(options: options) }
                await Task.yield()
                try await Task.sleep(nanoseconds: 30_000_000)
                release()
                do { try await stopping.value; XCTFail("Superseded disconnect succeeded") } catch is CancellationError {}
                try await reconnect.value
                XCTAssertEqual(manager.session.status, .connected)
                XCTAssertEqual(manager.session.stopCount, 0)
                XCTAssertEqual(Array(manager.demandWrites.suffix(3)), [false, true, true])
                try await controller.disconnect()
                let paused = AutomaticConnectionForm()
                await paused.load()
                XCTAssertEqual(paused.mode, .off)
                XCTAssertEqual(paused.domains, expected.domains.joined(separator: "\n"))
                XCTAssertEqual(paused.probeURL, expected.probeURL)
                try await controller.connect(options: options)
                let stopsBefore = manager.session.stopCount
                manager.holdNextRead = true
                let cancelled = Task { try await controller.disconnect() }
                for _ in 0..<500 where manager.finishRead == nil { try await Task.sleep(nanoseconds: 10_000_000) }
                let resumeRead = try XCTUnwrap(manager.finishRead)
                cancelled.cancel()
                resumeRead()
                do { try await cancelled.value; XCTFail("Cancelled disconnect succeeded") } catch is CancellationError {}
                XCTAssertEqual(manager.session.stopCount, stopsBefore)
                XCTAssertEqual(manager.session.status, .connected)
                let probe = FinalRouteFixture()
                try await probe.connect(options: XCTUnwrap(manager.session.liveOptions))
                XCTAssertEqual(try probe.request("explicit-proxy.fixture.example"), 204)
                paused.mode = .domains
                await paused.save()
            }
            let restored = try await SystemTunnelControl.automaticConnectionSettings()
            XCTAssertEqual(restored, expected)
            XCTAssertTrue(manager.isEnabled && manager.isOnDemandEnabled)
            XCTAssertEqual(manager.onDemandRules?.count, 2)
            XCTAssertTrue(manager.onDemandRules?.last is NEOnDemandRuleIgnore)
            try JSONEncoder().encode(restored).write(to: docs.appendingPathComponent(restart ? "on-demand-restart.json" : "on-demand-first.json"))
        }
    }

    func testDisableOnDemandWithInvalidDraft() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        var flags = [String: Bool]()
        for field in ["url", "domain"] {
            let manager = FixtureVPNPreferences()
            let file = docs.appendingPathComponent("off-" + field + ".archive")
            manager.onDemandFile = file
            try await SystemTunnelControl.$testManager.withValue(manager) {
                if !restart {
                    if FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
                    let suite = "fixture.off." + UUID().uuidString
                    let store = ConfigStore(persistence: try XCTUnwrap(UserDefaults(suiteName: suite)), keyStore: TailscaleKeychainStore(account: suite))
                    try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
                    await store.regenerateAndApply(tunnel: FinalRouteFixture())
                    manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: SystemTunnelControl.providerIdentifier, options: store.connectionOptions())
                    let active = AutomaticConnectionSettings(mode: .domains, domains: ["trigger.fixture.example"], probeURL: "http://127.0.0.1/check")
                    try await SystemTunnelControl.setAutomaticConnection(active)
                    let page = AutomaticConnectionForm()
                    await page.load()
                    XCTAssertEqual(page.mode, .domains)
                    if field == "url" { page.probeURL = "bad-url" } else { page.domains = "bad_domain" }
                    await page.save()
                    let retained = try await SystemTunnelControl.automaticConnectionSettings()
                    XCTAssertEqual(retained, active)
                    page.mode = .off
                    await page.save()
                    XCTAssertEqual(manager.demandWrites, [true, false])
                }
                try await manager.loadFromPreferences()
                flags[field] = manager.isOnDemandEnabled
                XCTAssertFalse(manager.isOnDemandEnabled)
                XCTAssertTrue(manager.onDemandRules?.isEmpty == true)
            }
        }
        try JSONEncoder().encode(flags).write(to: docs.appendingPathComponent(restart ? "hidden-draft-restart.json" : "hidden-draft-first.json"))
    }

    func testShortcutPreferenceRollback() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let suite = "fixture.shortcut." + UUID().uuidString
        let store = ConfigStore(persistence: try XCTUnwrap(UserDefaults(suiteName: suite)), keyStore: TailscaleKeychainStore(account: suite))
        let previousStore = Sakamoto.SystemSurfaceConfiguration.store
        Sakamoto.SystemSurfaceConfiguration.store = store
        defer { Sakamoto.SystemSurfaceConfiguration.store = previousStore }
        try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
        let a = try store.connectionOptions()
        let original = try NETunnelController.makeProviderProtocol(bundleIdentifier: SystemTunnelControl.providerIdentifier, options: a)
        let reference = try XCTUnwrap(original.providerConfiguration)
        try a.confirmStoredReference(reference)
        try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("socks-groups.sakamoto"))))
        let b = try XCTUnwrap(store.selectedProfileID)
        var report = [String: Int32]()
        for fault in ["read", "selection", "start"] {
            try store.selectProfile(b)
            let manager = FixtureVPNPreferences()
            manager.saved = original
            manager.session.stopped = true
            manager.failReadAfterSave = fault == "read"
            manager.session.rejectStart = fault == "start"
            if fault == "selection" { manager.afterSave = { try store.selectProfile(try XCTUnwrap(a.profileID)) } }
            try await SystemTunnelControl.$testManager.withValue(manager) {
                do { _ = try await Sakamoto.ConnectTunnelIntent().perform(); XCTFail("Shortcut succeeded during " + fault) } catch { }
                try await manager.loadFromPreferences()
                let saved = try XCTUnwrap((manager.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration)
                XCTAssertEqual(saved["profileID"] as? String, a.profileID)
                XCTAssertEqual(saved["payloadDigest"] as? String, reference["payloadDigest"] as? String)
                XCTAssertEqual(manager.savedIDs, [b, try XCTUnwrap(a.profileID)])
                let cold = try XCTUnwrap(TunnelStartOptions(providerConfiguration: saved))
                let tunnel = FinalRouteFixture()
                try await tunnel.connect(options: cold)
                report[fault] = try tunnel.request("explicit-proxy.fixture.example")
                XCTAssertEqual(report[fault], 204)
            }
        }
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        try JSONEncoder().encode(report).write(to: docs.appendingPathComponent(restart ? "shortcut-restart.json" : "shortcut-first.json"))
    }

    func testApplyMigratesKeyOnlyForTailscaleEndpoint() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let package = docs.appendingPathComponent("groups.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: package.path))
        try XCTSkipUnless(try SharedTailscaleKey.load() == nil, "Use an isolated simulator keychain")
        let suite = "fixture.tskey." + UUID().uuidString
        let key = UUID().uuidString
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                    kSecAttrService as String: suite, kSecAttrAccount as String: "tailscale-auth-key"]
        let item = query.merging([kSecValueData as String: Data(key.utf8)]) { _, new in new }
        XCTAssertEqual(SecItemAdd(item as CFDictionary, nil), errSecSuccess)
        defer {
            SecItemDelete(query as CFDictionary)
            if (try? SharedTailscaleKey.load()) == key { try? SharedTailscaleKey.delete() }
        }
        let fault = FileManager.default.temporaryDirectory.appendingPathComponent(suite)
        try Data().write(to: fault)
        defer { try? FileManager.default.removeItem(at: fault) }
        let cleanupFault = fault.appendingPathExtension("delete")
        defer { try? FileManager.default.removeItem(at: cleanupFault) }
        let vault = TailscaleKeychainStore(service: suite, saveShared: { key in
            if FileManager.default.fileExists(atPath: fault.path) { throw AppSetupError("Shared key write unavailable") }
            try SharedTailscaleKey.save(key)
        }, removeLegacy: { query in
            if FileManager.default.fileExists(atPath: cleanupFault.path) { return errSecInteractionNotAllowed }
            return SecItemDelete(query)
        })
        let store = ConfigStore(persistence: try XCTUnwrap(UserDefaults(suiteName: suite)), keyStore: vault)
        try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: package)))
        let tunnel = FinalRouteFixture()
        await store.regenerateAndApply(tunnel: tunnel)
        XCTAssertFalse(tunnel.content.isEmpty)
        XCTAssertFalse(TailscaleEndpointProvisioning.isEnabled(in: tunnel.content))
        XCTAssertEqual(SecItemCopyMatching(query as CFDictionary, nil), errSecSuccess)
        XCTAssertTrue(try SharedTailscaleKey.load() == nil)
        let previous = tunnel.content
        try store.setTailscaleEnabled(true, options: TailscaleEndpointOptions())
        await store.regenerateAndApply(tunnel: tunnel)
        XCTAssertTrue(store.lastAction?.contains("Shared key write unavailable") == true)
        XCTAssertEqual(tunnel.content, previous)
        XCTAssertEqual(SecItemCopyMatching(query as CFDictionary, nil), errSecSuccess)
        XCTAssertTrue(try SharedTailscaleKey.load() == nil)
        try FileManager.default.removeItem(at: fault)
        try Data().write(to: cleanupFault)
        await store.regenerateAndApply(tunnel: tunnel)
        XCTAssertTrue(store.lastAction?.contains("keychain delete failed") == true)
        XCTAssertEqual(tunnel.content, previous)
        XCTAssertEqual(SecItemCopyMatching(query as CFDictionary, nil), errSecSuccess)
        XCTAssertTrue(try SharedTailscaleKey.load() == key)
        let replacement = UUID().uuidString
        XCTAssertThrowsError(try vault.storeAuthKey(replacement))
        XCTAssertTrue(try SharedTailscaleKey.load() == key)
        try FileManager.default.removeItem(at: cleanupFault)
        await store.regenerateAndApply(tunnel: tunnel)
        XCTAssertTrue(TailscaleEndpointProvisioning.isEnabled(in: tunnel.content))
        XCTAssertEqual(SecItemCopyMatching(query as CFDictionary, nil), errSecItemNotFound)
        XCTAssertTrue(try SharedTailscaleKey.load() == key)
        let runtime = try TailscaleConfigInjection.forTunnel(tunnel.content) { try SharedTailscaleKey.load() }
        let document = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(runtime.utf8)) as? [String: Any])
        let endpoints = try XCTUnwrap(document["endpoints"] as? [[String: Any]])
        XCTAssertTrue(endpoints.first(where: { $0["type"] as? String == "tailscale" })?["auth_key"] as? String == key)
    }

    func testHomePreferenceBeforeStartup() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("arm-home-write").path))
        let archive = docs.appendingPathComponent("home-write-reference.json")
        let suite = "fixture.home-write"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        if FileManager.default.fileExists(atPath: archive.path) {
            let record = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: archive)) as? [String: Any])
            let ref = try XCTUnwrap(record["reference"] as? [String: Any])
            let cold = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false)
            XCTAssertEqual(cold.profileID, try XCTUnwrap(record["expectedA"] as? String))
            let probe = FinalRouteFixture()
            try await probe.connect(options: cold)
            let status = try probe.request()
            XCTAssertEqual(status, 204)
            let savedB = try XCTUnwrap(record["successfulReference"] as? [String: Any])
            let started = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: savedB, requiresApply: false)
            XCTAssertEqual(started.profileID, try XCTUnwrap(record["expectedB"] as? String))
            let expectedLearning = try XCTUnwrap(record["learning"] as? String)
            let id = try XCTUnwrap(started.profileID)
            let index = try AppPaths.sharedDirectory().appendingPathComponent("AppliedVPN/\(id).revisions")
            let before = try Data(contentsOf: index)
            var directStatus: Int32 = 0
            try await started.startResolvedProfile(startOptions: nil, saved: savedB) { transaction in
                XCTAssertNil(transaction)
                XCTAssertEqual(started.experimentJSON, expectedLearning)
                let (prepared, settings) = try ProviderExperimentRunner.prepared(started, root: AppPaths.sharedDirectory())
                XCTAssertEqual(settings?.learningEnabled, true)
                try await probe.reload(configContent: prepared)
                directStatus = try probe.request("home-route.fixture.example")
            }
            XCTAssertEqual(try Data(contentsOf: index), before)
            XCTAssertEqual(directStatus, 202)
            var report: [String: Int32] = ["proxy_status": status, "confirmed_direct_status": directStatus]
            for (saved, expected, key, expectedStatus) in [(savedB, started, "widget_confirmed", Int32(202)), (ref, cold, "widget_interrupted", Int32(204))] {
                let manager = FixtureVPNPreferences()
                let proto = NETunnelProviderProtocol()
                proto.providerBundleIdentifier = SystemTunnelControl.providerIdentifier
                proto.providerConfiguration = saved
                manager.saved = proto
                manager.isEnabled = true
                manager.session.stopped = true
                manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: expected)))
                let reads = OSAllocatedUnfairLock(initialState: Int32(0))
                let countRead: @Sendable () -> Void = { reads.withLock { $0 += 1 } }
                try await TunnelStartOptions.$observePayloadRead.withValue(countRead) {
                    try await SystemTunnelControl.$testManager.withValue(manager) {
                        _ = try await ConnectTunnelIntent().perform()
                    }
                }
                report[key + "_host_reads"] = reads.withLock { $0 }
                XCTAssertEqual(report[key + "_host_reads"], 0)
                XCTAssertNil(manager.session.startOptions)
                XCTAssertTrue(manager.session.started)
                XCTAssertEqual(manager.saves, 0)
                let persisted = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
                reads.withLock { $0 = 0 }
                let options = try TunnelStartOptions.$observePayloadRead.withValue(countRead) {
                    try TunnelStartOptions.resolveForProvider(startOptions: manager.session.startOptions, providerConfiguration: persisted, requiresApply: false)
                }
                report[key + "_provider_reads"] = reads.withLock { $0 }
                XCTAssertGreaterThan(reads.withLock { $0 }, 0)
                XCTAssertEqual(options, expected)
                try await options.startResolvedProfile(startOptions: manager.session.startOptions, saved: persisted) { _ in
                    try await probe.connect(options: options)
                    report[key] = try probe.request("home-route.fixture.example")
                }
                XCTAssertEqual(report[key], expectedStatus)
                try await SystemTunnelControl.$testManager.withValue(manager) {
                    _ = try await DisconnectTunnelIntent().perform()
                }
                XCTAssertTrue(manager.session.stopped)
            }
            try JSONEncoder().encode(report).write(to: docs.appendingPathComponent("home-write-result.json"))
            return
        }
        defaults.removePersistentDomain(forName: suite)
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let manager = FixtureVPNPreferences()
        let controller = NETunnelController(providerBundleIdentifier: "fixture", manager: manager)
        let config = ConfigModel(tunnel: controller, store: store)
        func prepare(_ final: String) async throws -> String {
            config.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
            XCTAssertNil(config.importError)
            var json = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(store.content.utf8)) as? [String: Any])
            var route = try XCTUnwrap(json["route"] as? [String: Any])
            route["final"] = final
            let port = try XCTUnwrap(Int(String(contentsOf: docs.appendingPathComponent("home-direct-port"), encoding: .utf8)))
            let localRule: [String: Any] = ["domain": "home-route.fixture.example", "action": "route", "outbound": final, "override_address": "127.0.0.1", "override_port": port]
            route["rules"] = [localRule] + (route["rules"] as? [[String: Any]] ?? [])
            json["route"] = route
            try store.save(String(decoding: JSONSerialization.data(withJSONObject: json), as: UTF8.self))
            let options = try store.connectionOptions()
            manager.session.messages = 0
            manager.session.started = false
            manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: options)))
            await config.regenerateAndApply()
            XCTAssertEqual(store.selectedProfile?.pendingApply, false)
            return try XCTUnwrap(options.profileID)
        }
        let a = try await prepare("MainProxy")
        let installedA = try XCTUnwrap(manager.saved?.copy() as? NEVPNProtocol)
        let b = try await prepare("direct")
        XCTAssertNotEqual(a, b)
        // The selected profile is clean and independently confirmed. Simulate the
        // system having the previously installed A preference when Home opens.
        manager.saved = installedA
        XCTAssertEqual(store.selectedProfile?.pendingApply, false)
        XCTAssertEqual(try store.systemConnectionOptions().profileID, b)
        let home = HomeModel(tunnel: controller, commanding: nil, store: store)
        func resolveSaved() throws -> TunnelStartOptions {
            let ref = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
            return try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false)
        }
        manager.session.started = false
        manager.saves = 0
        manager.afterSave = {
            manager.failNextRead = true
            throw NSError(domain: "FixturePreferences", code: 2)
        }
        await home.connect()
        XCTAssertFalse(manager.session.started)
        XCTAssertEqual(try resolveSaved().profileID, a)
        manager.session.messages = 0
        let options = try store.systemConnectionOptions()
        manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: options)))
        await home.connect()
        XCTAssertTrue(manager.session.started)
        XCTAssertEqual(try resolveSaved(), options)
        let successfulReference = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
        let learning = try XCTUnwrap(options.experimentJSON)
        manager.saved = installedA
        manager.session.started = false
        manager.saves = 0
        manager.afterSave = {
            XCTAssertFalse(manager.session.started)
            let ref = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
            XCTAssertEqual(ref["profileID"] as? String, b)
            try JSONSerialization.data(withJSONObject: ["expectedA": a, "reference": ref, "expectedB": b, "successfulReference": successfulReference, "learning": learning]).write(to: archive, options: .atomic)
            FileHandle.standardError.write(Data("intentional Home interruption 86\n".utf8))
            _exit(86)
        }
        await HomeModel(tunnel: controller, commanding: nil, store: store).connect()
        XCTFail("Expected interruption before VPN startup")
    }

    func testRejectedApplyReconnect() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let suiteFile = docs.appendingPathComponent("rejected-apply-suite")
        if !restart { try UUID().uuidString.write(to: suiteFile, atomically: true, encoding: .utf8) }
        let suite = "fixture.rejected." + (try String(contentsOf: suiteFile, encoding: .utf8))
        let store = ConfigStore(persistence: try XCTUnwrap(UserDefaults(suiteName: suite)), keyStore: TailscaleKeychainStore(account: suite))
        let manager = FixtureVPNPreferences()
        let controller = NETunnelController(providerBundleIdentifier: "fixture", manager: manager, isCurrent: { store.matchesConnectionOptions($0) })
        if !restart {
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
            try await store.generateFromSources()
            let first = try store.connectionOptions()
            manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: first)))
            try await controller.connect(options: first)
            manager.session.liveOptions = first
            let bundle = store.sourceBundle
            let rules = try XCTUnwrap(bundle.files[bundle.mainConf])
            let candidate = rules.replacingOccurrences(of: "explicit-proxy.fixture.example,PROXY", with: "explicit-proxy.fixture.example,REJECT")
            XCTAssertNotEqual(candidate, rules)
            try store.commitImport(ImportedSource(displaySource: bundle.mainConf, content: candidate))
            await store.regenerateAndApply(tunnel: controller)
            XCTAssertEqual(manager.session.reloaded.count, 2)
            XCTAssertEqual(manager.session.liveOptions, first)
            XCTAssertEqual(manager.readSaved(), first)
            await HomeModel(tunnel: controller, commanding: nil, store: store).disconnect()
        }
        let tunnel = FinalRouteFixture()
        await HomeModel(tunnel: tunnel, commanding: nil, store: store).connect()
        XCTAssertTrue(store.matchesConnectionOptions(try store.systemConnectionOptions()))
        let status = try tunnel.request("explicit-proxy.fixture.example")
        XCTAssertEqual(status, 204)
        let candidate = try store.connectionOptions()
        XCTAssertNotEqual(candidate.configContent, tunnel.content)
        XCTAssertEqual(store.selectedProfile?.pendingApply, true)
        if restart {
            manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: candidate)))
            await store.regenerateAndApply(tunnel: controller)
            XCTAssertEqual(store.selectedProfile?.pendingApply, false)
            XCTAssertTrue(try TunnelConfigurationReceipt(options: candidate).matches(store.systemConnectionOptions()))
        }
        let report: [String: Any] = ["home_proxy_status": status, "explicit_retry_confirmed": restart]
        try JSONSerialization.data(withJSONObject: report).write(to: docs.appendingPathComponent(restart ? "apply-rejected-restart.json" : "apply-rejected-first.json"))
    }

    func testSavedSubscriptionFailureReconnect() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let suiteFile = docs.appendingPathComponent("subscription-test-suite")
        if !restart { try UUID().uuidString.write(to: suiteFile, atomically: true, encoding: .utf8) }
        let suite = "fixture.subscription." + (try String(contentsOf: suiteFile, encoding: .utf8))
        let store = ConfigStore(persistence: try XCTUnwrap(UserDefaults(suiteName: suite)), keyStore: TailscaleKeychainStore(account: suite))
        let identity = docs.appendingPathComponent("subscription-baseline.json")
        let url = try String(contentsOf: docs.appendingPathComponent("subscription-url"), encoding: .utf8).replacingOccurrences(of: "slow-subscription", with: "missing-subscription")
        if !restart {
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
            try await store.generateFromSources()
            let active = FinalRouteFixture()
            let home = HomeModel(tunnel: active, commanding: nil, store: store)
            await home.connect()
            XCTAssertEqual(try active.request("explicit-proxy.fixture.example"), 204)
            let profile = try XCTUnwrap(store.selectedProfile)
            try JSONEncoder().encode([profile.id, try XCTUnwrap(profile.ruleRevisionID)]).write(to: identity)
            let form = ConfigModel(tunnel: active, store: store)
            try form.saveSourceSubscription(name: "Needs repair", url: url, format: "auto", original: nil, profileID: profile.id)
            await form.regenerateAndApply()
            XCTAssertTrue(store.lastAction?.contains("404") == true)
            await home.disconnect()
        }
        let tunnel = FinalRouteFixture()
        await HomeModel(tunnel: tunnel, commanding: nil, store: store).connect()
        XCTAssertFalse(tunnel.content.isEmpty)
        let status = try tunnel.request("explicit-proxy.fixture.example")
        XCTAssertEqual(status, 204)
        store.connectionConfirmed(content: tunnel.content)
        XCTAssertNotEqual(store.configState, .clean)
        let profile = try XCTUnwrap(store.selectedProfile)
        let previous = try JSONDecoder().decode([String].self, from: Data(contentsOf: identity))
        let matches = previous == [profile.id, profile.ruleRevisionID ?? ""]
        let pending = profile.sourcesChanged && store.nodesSources.subscriptions.map(\.url) == [url]
        XCTAssertTrue(matches && pending)
        let result: [String: Any] = ["home_proxy_status": status, "pending_subscription": pending, "same_profile_and_revision": matches]
        try JSONSerialization.data(withJSONObject: result).write(to: docs.appendingPathComponent(restart ? "subscription-restart.json" : "subscription-first.json"))
    }

    func testGeneratedEditorRejectsMissingTUN() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let suiteFile = docs.appendingPathComponent("editor-test-suite")
        if !restart { try UUID().uuidString.write(to: suiteFile, atomically: true, encoding: .utf8) }
        let suite = "fixture.editor." + (try String(contentsOf: suiteFile, encoding: .utf8))
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let identity = docs.appendingPathComponent("editor-baseline.json")
        func homeRequest() async throws -> Int32 {
            let tunnel = FinalRouteFixture()
            await HomeModel(tunnel: tunnel, commanding: nil, store: store).connect()
            _ = try XCTUnwrap(tunnel.content.isEmpty ? nil : tunnel.content, "Home must submit the selected configuration")
            return try tunnel.request("explicit-proxy.fixture.example")
        }
        if !restart {
            let package = try Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))
            try store.installProfile(TunnelProfile.decodePackage(package))
            try await store.generateFromSources()
            let status = try await homeRequest()
            XCTAssertEqual(status, 204)
            let before = try XCTUnwrap(store.selectedProfile)
            let manifest = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/" + before.id + "/profile.json")
            let bytes = try Data(contentsOf: manifest)
            try JSONEncoder().encode([before.id, try XCTUnwrap(before.ruleRevisionID)]).write(to: identity)
            let editor = ConfigModel(tunnel: FinalRouteFixture(), store: store)
            var root = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(editor.draft.utf8)) as? [String: Any])
            root["inbounds"] = []
            let invalid = String(decoding: try JSONSerialization.data(withJSONObject: root), as: UTF8.self)
            editor.draft = invalid
            editor.saveDraft()
            XCTAssertNotNil(editor.editorError)
            XCTAssertEqual(editor.draft, invalid)
            XCTAssertEqual(store.selectedProfile, before)
            XCTAssertEqual(try Data(contentsOf: manifest), bytes)
        }
        let selected = try XCTUnwrap(store.selectedProfile)
        let previous = try JSONDecoder().decode([String].self, from: Data(contentsOf: identity))
        let matches = [selected.id, selected.ruleRevisionID ?? ""] == previous
        XCTAssertTrue(matches)
        let status = try await homeRequest()
        XCTAssertEqual(status, 204)
        let report: [String: Any] = ["same_profile_and_revision": matches, "home_proxy_status": status]
        try JSONSerialization.data(withJSONObject: report).write(to: docs.appendingPathComponent(restart ? "editor-restart.json" : "editor-first.json"))
    }

    func testAddExitGenerationFailure() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let package = docs.appendingPathComponent("groups.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let restart = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let suiteFile = docs.appendingPathComponent("exit-test-suite")
        if !restart { try UUID().uuidString.write(to: suiteFile, atomically: true, encoding: .utf8) }
        let suite = "fixture.exit." + (try String(contentsOf: suiteFile, encoding: .utf8))
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let tunnel = FinalRouteFixture()
        let identity = docs.appendingPathComponent("exit-baseline.json")
        func baseline() throws -> [String] {
            [try XCTUnwrap(store.selectedProfileID), try XCTUnwrap(store.selectedProfile?.ruleRevisionID), store.nodesSources.nodesText]
        }
        if !restart {
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: package)))
            let url = try String(contentsOf: docs.appendingPathComponent("subscription-url"), encoding: .utf8).replacingOccurrences(of: "slow-subscription", with: "exit-subscription")
            try store.commitNodesSources(store.nodesSources.adding(subscription: StagedSubscription(name: "Seed", url: url)))
            try await store.generateFromSources()
            await store.regenerateAndApply(tunnel: tunnel)
            try await tunnel.connect(options: store.connectionOptions())
            XCTAssertEqual(try tunnel.request("explicit-proxy.fixture.example"), 204)
            let before = try XCTUnwrap(store.selectedProfile)
            try JSONEncoder().encode(baseline()).write(to: identity)
            try Data().write(to: docs.appendingPathComponent("fail-exit-download"))
            let added = try ConfigModel.validateNode("socks://@127.0.0.1:18587#NewExit")
            do { try await store.generateFromSources(adding: added); XCTFail("Accepted a failed subscription") }
            catch { XCTAssertTrue(error.localizedDescription.contains("404")) }
            XCTAssertEqual(store.selectedProfile, before)
        }
        let matched = try baseline() == JSONDecoder().decode([String].self, from: Data(contentsOf: identity))
        XCTAssertTrue(matched)
        try await tunnel.connect(options: store.connectionOptions())
        let status = try tunnel.request("explicit-proxy.fixture.example")
        XCTAssertEqual(status, 204)
        let report: [String: Any] = ["proxy_status": status, "source_and_revision_match": matched]
        try JSONSerialization.data(withJSONObject: report).write(to: docs.appendingPathComponent(restart ? "exit-failure-restart.json" : "exit-failure-first.json"))
    }

    func testRegionalDNSGeneratedProfile() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let file = docs.appendingPathComponent("regional-ios.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: file.path))
        let suite = "fixture.regional-generated"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let referenceFile = docs.appendingPathComponent("regional-reference.json")
        let restart = FileManager.default.fileExists(atPath: referenceFile.path)
        if !restart { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let tunnel = FinalRouteFixture()
        if restart {
            let reference = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: referenceFile)) as? [String: Any])
            let options = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: reference, requiresApply: false)
            XCTAssertEqual(options, try store.systemConnectionOptions())
            try await tunnel.connect(options: options)
            try Data(tunnel.content.utf8).write(to: docs.appendingPathComponent("regional-restart.json"))
        } else {
            let model = ConfigModel(tunnel: tunnel, store: store)
            model.importFromFile(at: file)
            XCTAssertNil(model.importError)
            let local = store.selectedProfile?.regionalDNS == nil
            if local {
                XCTAssertEqual(store.selectedProfile?.routeSourceVersion, 1)
                try store.saveRegionalDNS(["domains": "regional.fixture.example", "resolver_ip": "192.0.2.53", "tls_name": "allowed.fixture.example", "relay": "Excluded"], expected: store.selectedProfile)
            }
            for stage in local ? ["local"] : ["generate1", "generate2"] {
                if !local {
                    try await store.generateFromSources()
                    XCTAssertEqual(store.selectedProfile?.routeSourceVersion, 1)
                }
                await model.regenerateAndApply()
                XCTAssertEqual(store.selectedProfile?.pendingApply, false, store.lastAction ?? "Apply did not finish")
                let applied = try store.connectionOptions()
                XCTAssertEqual(tunnel.content, applied.configContent)
                let reference = try applied.storedReference()
                try applied.confirmStoredReference(reference)
                try JSONSerialization.data(withJSONObject: reference).write(to: referenceFile)
                try Data(tunnel.content.utf8).write(to: docs.appendingPathComponent("regional-\(stage).json"))
            }
        }
    }

    func testCrossProfileNativeStartFailureRestoresRunningProfile() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("arm-native-replace").path))
        for fault in ReplacementFault.allCases {
            try await verifyNativeReplacement(docs: docs, fault: fault)
        }
    }

    private enum ReplacementFault: String, CaseIterable {
        case nativeFailure = "native-replace"
        case droppedFailure = "native-replace-dropped"
        case droppedRestore = "native-replace-restore"
        case sendRejected = "native-replace-unsent"
        case restoreRejected = "native-replace-retry"
        case providerBusy = "native-replace-busy"
        case overlappingCompletion = "native-replace-overlap"

        var rejectsWhileBusy: Bool { self == .providerBusy || self == .overlappingCompletion }

        var exercisesConditionalRestore: Bool {
            self == .droppedRestore || self == .restoreRejected || rejectsWhileBusy
        }
    }

    private func verifyNativeReplacement(docs: URL, fault: ReplacementFault) async throws {
        let dropped = fault == .droppedFailure
        let prefix = fault.rawValue
        let archive = docs.appendingPathComponent(prefix + "-reference.json")
        let suite = "fixture." + prefix
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        if FileManager.default.fileExists(atPath: archive.path) {
            let ref = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: archive)) as? [String: Any])
            let cold = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false)
            XCTAssertEqual(cold.profileID, defaults.string(forKey: "expectedA"))
            let probe = FinalRouteFixture()
            try await probe.connect(options: cold)
            let status = try probe.request("explicit-proxy.fixture.example")
            XCTAssertEqual(status, 204)
            try JSONEncoder().encode(["explicit_proxy_status": status]).write(to: docs.appendingPathComponent(prefix + "-restart.json"))
            return
        }
        defaults.removePersistentDomain(forName: suite)
        let manager = FixtureVPNPreferences()
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let config = ConfigModel(tunnel: NETunnelController(providerBundleIdentifier: "fixture", manager: manager), store: store)
        config.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
        XCTAssertNil(config.importError)
        let a = try store.connectionOptions()
        let original = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: a)
        manager.saved = original
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root); try? AppServiceSetup.apply() }
        let setup = LibboxSetupOptions()
        setup.basePath = root.path; setup.workingPath = root.path; setup.tempPath = root.path
        setup.commandServerListenPort = 19583; setup.commandServerSecret = UUID().uuidString
        var error: NSError?
        LibboxSetup(setup, &error)
        if let error { throw error }
        let platform = ObservationTestPlatform()
        platform.testInterface = "lo0"
        let server = try XCTUnwrap(LibboxNewCommandServer(platform, platform, &error))
        try server.start()
        defer { try? server.closeService(); server.close() }
        // The simulator uses loopback ingress. Port 18581 is held by the fixture proxy.
        func payload(_ input: TunnelStartOptions, occupied: Bool) throws -> TunnelStartOptions {
            var options = input
            var content = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(options.configContent.utf8)) as? [String: Any])
            content["inbounds"] = occupied ? [["type": "socks", "listen": "127.0.0.1", "listen_port": 18581]] : []
            content["services"] = (content["services"] as? [[String: Any]])?.filter { $0["type"] as? String != "api" }
            options.configContent = String(decoding: try JSONSerialization.data(withJSONObject: content), as: UTF8.self)
            return options
        }
        let runningA = try payload(a, occupied: false)
        try server.startOrReloadService(runningA.configContent, options: LibboxOverrideOptions())
        var receipt = try TunnelConfigurationReceipt(options: a) as TunnelConfigurationReceipt?
        let initialInstance = server.runningInstanceID()
        let earlyReceipt = receipt
        var observedEarlySnapshot = false
        var observedCompletedSnapshot = false
        var completions = TunnelReloadCompletions()
        var completedRequest: UUID? { didSet { completions.record(completedRequest) } }
        var bRejected = false
        var pendingRestore: UUID?
        var restoreCommands = 0
        var startingReads = 0
        var finishedRestore = false
        manager.session.rejectFirstRestore = fault == .restoreRejected
        var recheckedCandidate = false
        var busyRequest: UUID?
        var competingRequest: UUID?
        var sawCompetingCompletion = false
        manager.session.rejectReloadSend = fault == .sendRejected
        manager.session.started = true
        manager.session.providerHandler = { message in
            do {
                if case .reloadProfile(let options, let transaction, let replacing, let requestID) = try TunnelRequest(data: message) {
                    if fault.exercisesConditionalRestore {
                        if let replacing {
                            if fault == .restoreRejected { XCTAssertTrue(recheckedCandidate) }
                            XCTAssertEqual(replacing, receipt)
                            XCTAssertEqual(options, a)
                            restoreCommands += 1
                            let id = try XCTUnwrap(requestID)
                            if fault.rejectsWhileBusy {
                                if busyRequest == nil {
                                    busyRequest = id
                                    completedRequest = id
                                    if fault == .overlappingCompletion { return nil }
                                    return try TunnelResponse.failure("Configuration application is already running").encode()
                                }
                                XCTAssertTrue(recheckedCandidate)
                                XCTAssertNotEqual(busyRequest, id)
                            }
                            pendingRestore = id
                        } else {
                            XCTAssertNotEqual(options.profileID, a.profileID)
                            let candidate = try payload(options, occupied: false)
                            try server.startOrReloadService(candidate.configContent, options: LibboxOverrideOptions())
                            receipt = try TunnelConfigurationReceipt(options: options)
                            completedRequest = try XCTUnwrap(requestID)
                        }
                        return nil
                    }
                    let candidate = try payload(options, occupied: true)
                    var preflightError: NSError?
                    _ = LibboxCheckConfig(candidate.configContent, &preflightError)
                    XCTAssertNil(preflightError)
                    let replacement = TunnelLiveReplacement(previous: a, receipt: receipt)
                    receipt = nil
                    do {
                        try await replacement.perform(isCurrent: { true }) {
                            try server.startOrReloadService(candidate.configContent, options: LibboxOverrideOptions())
                        } restore: { previous in
                            XCTAssertEqual(previous, a)
                            XCTAssertFalse(server.isServiceRunning())
                            let restored = try payload(previous, occupied: false)
                            try server.startOrReloadService(restored.configContent, options: LibboxOverrideOptions())
                            receipt = try TunnelConfigurationReceipt(options: previous)
                        }
                    } catch {
                        bRejected = true
                        completedRequest = try XCTUnwrap(requestID ?? transaction)
                        if dropped { return nil }
                        return try TunnelResponse.failure("Native service replacement failed").encode()
                    }
                }
                if fault == .overlappingCompletion, let busyRequest, restoreCommands == 1 {
                    if competingRequest == nil {
                        competingRequest = UUID()
                        completedRequest = competingRequest
                        var state = TunnelStateSnapshot(serviceState: .starting)
                        state.completedReloadID = busyRequest
                        return try TunnelResponse(ok: true, state: state).encode()
                    }
                    XCTAssertEqual(completedRequest, competingRequest)
                    sawCompetingCompletion = true
                    recheckedCandidate = true
                }
                if let busyRequest, completedRequest == busyRequest, restoreCommands == 1 {
                    XCTAssertNotEqual(receipt?.profileID, a.profileID)
                    recheckedCandidate = true
                }
                if fault == .restoreRejected, manager.session.rejectedSends == 1, restoreCommands == 0, receipt?.profileID != a.profileID {
                    recheckedCandidate = true
                }
                if let restoreID = pendingRestore {
                    if startingReads == 0 {
                        startingReads += 1
                        return try TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .starting)).encode()
                    }
                    try server.startOrReloadService(runningA.configContent, options: LibboxOverrideOptions())
                    receipt = try TunnelConfigurationReceipt(options: a)
                    completedRequest = restoreID
                    pendingRestore = nil
                    finishedRestore = true
                }
                if dropped, bRejected, !observedEarlySnapshot {
                    // Deliver a queued snapshot of A from before this request finished.
                    observedEarlySnapshot = true
                    return try TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: earlyReceipt)).encode()
                }
                var state = TunnelStateSnapshot(serviceState: server.isServiceRunning() ? .running : .unavailable, appliedReceipt: receipt)
                state.completedReloadID = completedRequest
                if fault == .overlappingCompletion { state.recentCompletedReloadIDs = completions.recentIDs }
                if completedRequest != nil { observedCompletedSnapshot = true }
                return try TunnelResponse(ok: true, state: state).encode()
            } catch { XCTFail(error.localizedDescription); return nil }
        }
        config.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
        XCTAssertNil(config.importError)
        XCTAssertNotEqual(try store.connectionOptions().profileID, a.profileID)
        await config.regenerateAndApply()
        XCTAssertEqual(bRejected, fault == .nativeFailure || fault == .droppedFailure)
        XCTAssertEqual(manager.session.rejectedSends, fault == .sendRejected || fault == .restoreRejected ? 1 : 0)
        if fault.exercisesConditionalRestore {
            XCTAssertEqual(restoreCommands, fault.rejectsWhileBusy ? 2 : 1)
            XCTAssertEqual(sawCompetingCompletion, fault == .overlappingCompletion)
            XCTAssertEqual(startingReads, 1)
            XCTAssertTrue(finishedRestore)
            XCTAssertEqual(receipt?.matches(a), true)
        }
        XCTAssertEqual(observedEarlySnapshot, dropped)
        XCTAssertEqual(observedCompletedSnapshot, fault != .sendRejected)
        XCTAssertEqual(store.selectedProfile?.pendingApply, true)
        XCTAssertEqual(manager.session.stopCount, 0)
        let saved = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
        XCTAssertTrue(NSDictionary(dictionary: saved).isEqual(to: original.providerConfiguration ?? [:]))
        XCTAssertTrue(server.isServiceRunning())
        XCTAssertEqual(server.runningInstanceID() == initialInstance, fault == .sendRejected)
        var status: Int32 = 0
        try server.probeAutomaticOrigin("explicit-proxy.fixture.example", port: 80, outboundTag: "", expectedInstanceID: server.runningInstanceID(), timeoutMillis: 3000, ret0_: &status)
        XCTAssertEqual(status, 204)
        defaults.set(a.profileID, forKey: "expectedA")
        XCTAssertTrue(defaults.synchronize())
        try JSONSerialization.data(withJSONObject: saved).write(to: archive)
        var report = ["explicit_proxy_status": Int(status), "stop_requests": manager.session.stopCount]
        if fault == .sendRejected {
            report["rejected_sends"] = manager.session.rejectedSends
            report["same_instance"] = server.runningInstanceID() == initialInstance ? 1 : 0
        }
        if fault == .restoreRejected {
            report["rejected_sends"] = manager.session.rejectedSends
            report["candidate_rechecked"] = recheckedCandidate ? 1 : 0
        }
        if fault == .overlappingCompletion { report["competing_completion"] = sawCompetingCompletion ? 1 : 0 }
        if fault.rejectsWhileBusy {
            report["provider_rejections"] = busyRequest == nil ? 0 : 1
            report["candidate_rechecked"] = recheckedCandidate ? 1 : 0
        }
        if fault.exercisesConditionalRestore {
            report["restore_commands"] = restoreCommands
            report["starting_snapshots"] = startingReads
            report["completed_restore_snapshots"] = finishedRestore ? 1 : 0
        }
        if dropped { report["snapshots_checked"] = [observedEarlySnapshot, observedCompletedSnapshot].filter { $0 }.count }
        try JSONEncoder().encode(report).write(to: docs.appendingPathComponent(prefix + "-live.json"))
    }

    func testCancelledApplyKeepsNewerVPNReference() async throws {
        try await verifyNewerVPNReference(afterStart: false)
    }

    func testApplyKeepsVPNTakingOverDuringStart() async throws {
        try await verifyNewerVPNReference(afterStart: true)
    }

    func testApplyKeepsVPNTakingOverDuringRollback() async throws {
        try await verifyNewerVPNReference(afterStart: true, duringRollback: true)
    }

    private func verifyNewerVPNReference(afterStart: Bool, duringRollback: Bool = false) async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("arm-newer-vpn").path))
        let prefix = duringRollback ? "reload-takeover-vpn" : afterStart ? "takeover-vpn" : "newer-vpn"
        let archive = docs.appendingPathComponent(prefix + "-reference.json")
        let restart = FileManager.default.fileExists(atPath: archive.path)
        let suite = "fixture." + prefix + "-reference"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        if !restart { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        if restart {
            let ref = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: archive)) as? [String: Any])
            let cold = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false)
            XCTAssertEqual(cold.profileID, defaults.string(forKey: "expectedC"))
            let probe = FinalRouteFixture()
            try await probe.connect(options: cold)
            let status = try probe.request("explicit-proxy.fixture.example")
            XCTAssertEqual(status, 204)
            try JSONEncoder().encode(["explicit_proxy_status": status]).write(to: docs.appendingPathComponent(prefix + "-restart.json"))
            return
        }
        let manager = FixtureVPNPreferences()
        let controller = NETunnelController(providerBundleIdentifier: "fixture", manager: manager)
        let config = ConfigModel(tunnel: controller, store: store)
        func imported() throws -> TunnelStartOptions {
            config.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
            XCTAssertNil(config.importError)
            return try store.connectionOptions()
        }
        let a = try imported()
        manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: a)
        let c = try imported()
        let replacement = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: c)
        let b = try imported()
        XCTAssertEqual(Set([a.profileID, b.profileID, c.profileID]).count, 3)
        var apply: Task<Void, Never>?
        if duringRollback {
            manager.session.started = true
            manager.session.liveOptions = a
            manager.session.beforeRestore = {
                XCTAssertEqual(manager.session.liveOptions?.profileID, b.profileID)
                manager.saved = replacement
                manager.session.liveOptions = c
            }
        } else if afterStart {
            manager.session.afterStart = {
                XCTAssertEqual(manager.readSaved()?.profileID, b.profileID)
                manager.saved = replacement
                manager.session.liveOptions = c
            }
        } else {
            manager.afterSave = {
                XCTAssertEqual(manager.readSaved()?.profileID, b.profileID)
                manager.saved = replacement
                apply?.cancel()
            }
        }
        apply = Task { await config.regenerateAndApply() }
        await apply?.value
        XCTAssertEqual(manager.savedIDs, [b.profileID].compactMap { $0 })
        XCTAssertEqual(manager.session.started, afterStart)
        if afterStart {
            XCTAssertEqual(manager.session.stopCount, 0)
            XCTAssertEqual(manager.session.status, .connected)
            let live = try XCTUnwrap(manager.session.liveOptions)
            XCTAssertTrue(live == c)
            let probe = FinalRouteFixture()
            try await probe.connect(options: live)
            let status = try probe.request("explicit-proxy.fixture.example")
            XCTAssertEqual(status, 204)
            try JSONEncoder().encode(["explicit_proxy_status": Int(status), "stop_requests": manager.session.stopCount]).write(to: docs.appendingPathComponent(prefix + "-live.json"))
        }
        XCTAssertEqual(store.selectedProfile?.pendingApply, true)
        let saved = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
        XCTAssertTrue(NSDictionary(dictionary: saved).isEqual(to: replacement.providerConfiguration ?? [:]))
        defaults.set(c.profileID, forKey: "expectedC")
        XCTAssertTrue(defaults.synchronize())
        try JSONSerialization.data(withJSONObject: saved).write(to: archive)
        try JSONEncoder().encode(["extra_writes": manager.saves - 1]).write(to: docs.appendingPathComponent(prefix + "-cancel.json"))
    }

    func testApplyAfterLostManifest() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("arm-lost-vpn").path))
        for fault in ["legacy-prior-id-only", "legacy-prior-missing", "legacy-prior-corrupt", "legacy-missing", "legacy-corrupt", "missing", "corrupt"] {
            let hasPrior = fault.hasPrefix("legacy-prior-")
            let legacy = fault.hasPrefix("legacy-")
            let suite = "fixture.applied-manifest-" + fault
            let archive = docs.appendingPathComponent(suite + ".json")
            let defaults = try XCTUnwrap(IndexFailureDefaults(suiteName: suite))
            let restarting = FileManager.default.fileExists(atPath: archive.path)
            if !restarting { defaults.removePersistentDomain(forName: suite) }
            func openStore() -> ConfigStore { ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite)) }
            let manager = FixtureVPNPreferences()
            let controller = NETunnelController(providerBundleIdentifier: "fixture", manager: manager)
            let probe = FinalRouteFixture()
            if restarting {
                let reference = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: archive)) as? [String: Any])
                let options = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: reference, requiresApply: false)
                XCTAssertEqual(options.profileID, openStore().selectedProfileID)
                try await probe.connect(options: options)
            } else {
                let package = docs.appendingPathComponent("groups.sakamoto")
                let initial = openStore()
                let editor = ConfigModel(tunnel: controller, store: initial)
                var priorID: String?
                if hasPrior {
                    editor.importFromFile(at: package)
                    XCTAssertNil(editor.importError)
                    let prior = try initial.connectionOptions()
                    manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: prior)))
                    await editor.regenerateAndApply()
                    XCTAssertEqual(initial.selectedProfile?.pendingApply, false)
                    manager.session.liveOptions = try XCTUnwrap(manager.readSaved())
                    manager.session.onFirstReload = {}
                    priorID = try XCTUnwrap(prior.profileID)
                }
                editor.importFromFile(at: package)
                XCTAssertNil(editor.importError)
                let a = try initial.connectionOptions()
                manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: a)))
                await editor.regenerateAndApply()
                XCTAssertEqual(initial.selectedProfile?.pendingApply, false)
                let appliedA = try XCTUnwrap(manager.readSaved())
                let receiptA = try TunnelConfigurationReceipt(options: a)
                XCTAssertTrue(receiptA.matches(appliedA))
                manager.session.liveOptions = appliedA
                let original = try AppPaths.sharedDirectory().appendingPathComponent("Profiles/\(try XCTUnwrap(a.profileID))/profile.json")
                let savedRef = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
                if legacy {
                    let index = try AppPaths.sharedDirectory().appendingPathComponent("AppliedVPN/\(try XCTUnwrap(a.profileID)).revisions")
                    var revisions = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: index)) as? [String: Any])
                    XCTAssertNotNil(revisions.removeValue(forKey: "finalizedTransaction"))
                    XCTAssertEqual((savedRef["previousProfile"] as? [String: String])?["profileID"], priorID)
                    try JSONSerialization.data(withJSONObject: revisions).write(to: index, options: .atomic)
                }
                if fault.hasSuffix("id-only") {
                    try JSONEncoder().encode(["id": try XCTUnwrap(a.profileID)]).write(to: original, options: .atomic)
                } else if fault.hasSuffix("missing") { try FileManager.default.removeItem(at: original) }
                else { try Data("{".utf8).write(to: original, options: .atomic) }
                let recovered = openStore()
                try recovered.recoverProfileIndex()
                if let priorID { try recovered.selectProfile(priorID) }
                let importer = ConfigModel(tunnel: controller, store: recovered)
                importer.importFromFile(at: package)
                XCTAssertNil(importer.importError)
                let b = try recovered.connectionOptions()
                XCTAssertNotEqual(b.profileID, a.profileID)
                let savesBefore = manager.saves
                manager.session.reloaded.removeAll()
                manager.session.onFirstReload = { defaults.failNextFlush = true }
                await importer.regenerateAndApply()
                if legacy {
                    XCTAssertEqual(manager.saves, savesBefore)
                    XCTAssertTrue(manager.session.reloaded.isEmpty)
                    XCTAssertEqual(manager.session.status, .connected)
                    XCTAssertTrue(manager.session.liveOptions == appliedA)
                    let retained = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
                    XCTAssertTrue(NSDictionary(dictionary: savedRef).isEqual(to: retained))
                    XCTAssertEqual(recovered.selectedProfile?.pendingApply, true)
                    XCTAssertTrue(recovered.lastAction?.contains("Settings") == true)
                    try await probe.connect(options: XCTUnwrap(manager.session.liveOptions))
                    let status = try probe.request("explicit-proxy.fixture.example")
                    XCTAssertEqual(status, 204)
                    try JSONEncoder().encode(["explicit_proxy_status": Int(status), "preference_writes": manager.saves - savesBefore]).write(to: docs.appendingPathComponent("lost-vpn-\(fault).json"))
                    continue
                }
                XCTAssertTrue(try TunnelConfigurationReceipt(options: b).matches(XCTUnwrap(manager.session.reloaded.first)))
                XCTAssertTrue(manager.session.liveOptions == appliedA)
                XCTAssertTrue(manager.readSaved() == appliedA)
                XCTAssertEqual(recovered.selectedProfile?.pendingApply, true)
                try await probe.connect(options: XCTUnwrap(manager.session.liveOptions))
                XCTAssertEqual(try probe.request("explicit-proxy.fixture.example"), 204)
                await importer.regenerateAndApply()
                XCTAssertEqual(recovered.selectedProfile?.pendingApply, false)
                let saved = try XCTUnwrap(manager.readSaved())
                XCTAssertEqual(saved.profileID, b.profileID)
                try await probe.connect(options: saved)
                let reference = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
                try JSONSerialization.data(withJSONObject: reference).write(to: archive)
            }
            let status = try probe.request("explicit-proxy.fixture.example")
            XCTAssertEqual(status, 204)
            let phase = restarting ? "restart" : "apply"
            try JSONEncoder().encode(["explicit_proxy_status": status]).write(to: docs.appendingPathComponent("lost-vpn-\(fault)-\(phase).json"))
        }
    }

    func testApplyAfterUnidentifiedLegacyVPN() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("arm-no-id-vpn").path))
        let receipt = docs.appendingPathComponent("no-id-reference.json")
        let restart = FileManager.default.fileExists(atPath: receipt.path)
        let suite = "fixture.no-id-legacy-vpn"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        if !restart { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let probe = FinalRouteFixture()
        var report = [String: Int32]()
        if restart {
            let ref = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: receipt)) as? [String: Any])
            let launch = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false)
            XCTAssertTrue(try launch == store.systemConnectionOptions())
            try await launch.startResolvedProfile(startOptions: nil, saved: ref) { _ in
                try await probe.connect(options: launch)
            }
        } else {
            let manager = FixtureVPNPreferences()
            let legacy = NETunnelProviderProtocol()
            legacy.providerBundleIdentifier = SystemTunnelControl.providerIdentifier
            legacy.providerConfiguration = ["configContent": #"{"route":{"final":"direct"}}"#]
            manager.protocolConfiguration = legacy
            manager.saved = legacy.copy() as? NEVPNProtocol
            let previous = AutomaticConnectionSettings(mode: .wifi)
            try AutomaticConnectionPolicy.apply(previous, to: manager)
            let page = AutomaticConnectionForm()
            let shared = try XCTUnwrap(UserDefaults(suiteName: SystemSurfaceStore.groupIdentifier))
            let flag = "sakamoto.profile.requiresApply"
            let originalFlag = shared.object(forKey: flag)
            defer { shared.set(originalFlag, forKey: flag) }
            shared.removeObject(forKey: flag)
            await SystemTunnelControl.$testManager.withValue(manager) {
                await page.load()
                XCTAssertTrue(page.canSave)
                page.mode = .anyNetwork
                await page.save()
            }
            XCTAssertEqual(page.notice, SystemTunnelControl.ControlError.needsApply.localizedDescription)
            XCTAssertEqual(AutomaticConnectionPolicy.settings(from: manager), previous)
            XCTAssertEqual(manager.saves, 0)
            report["legacy_preference_writes"] = Int32(manager.saves)
            let controller = NETunnelController(providerBundleIdentifier: SystemTunnelControl.providerIdentifier, manager: manager)
            let config = ConfigModel(tunnel: controller, store: store)
            _ = try await controller.status()
            XCTAssertFalse(manager.isOnDemandEnabled)
            XCTAssertTrue((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration?.isEmpty == true)
            config.importFromFile(at: docs.appendingPathComponent("groups.sakamoto"))
            XCTAssertNil(config.importError)
            manager.session.rejectStart = true
            await config.regenerateAndApply()
            XCTAssertFalse(manager.session.started)
            XCTAssertEqual(store.selectedProfile?.pendingApply, true)
            XCTAssertFalse(manager.isOnDemandEnabled)
            XCTAssertTrue((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration?.isEmpty == true)
            manager.session.rejectStart = false
            let options = try store.connectionOptions()
            manager.session.result = TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, appliedReceipt: try TunnelConfigurationReceipt(options: options)))
            await config.regenerateAndApply()
            XCTAssertEqual(store.selectedProfile?.pendingApply, false)
            let saved = try XCTUnwrap(manager.readSaved())
            XCTAssertTrue(try TunnelConfigurationReceipt(options: options).matches(saved))
            try await probe.connect(options: saved)
            await SystemTunnelControl.$testManager.withValue(manager) {
                await page.load()
                page.mode = .anyNetwork
                await page.save()
            }
            XCTAssertEqual(page.notice, "Automatic connection saved in iOS VPN settings.")
            XCTAssertEqual(AutomaticConnectionPolicy.settings(from: manager).mode, .anyNetwork)
            report["enabled_after_apply"] = manager.isOnDemandEnabled ? 1 : 0
            let ref = try XCTUnwrap((manager.saved as? NETunnelProviderProtocol)?.providerConfiguration)
            try JSONSerialization.data(withJSONObject: ref).write(to: receipt)
        }
        let status = try probe.request("explicit-proxy.fixture.example")
        XCTAssertEqual(status, 204)
        report["explicit_proxy_status"] = status
        try JSONEncoder().encode(report).write(to: docs.appendingPathComponent(restart ? "no-id-restart.json" : "no-id-apply.json"))
    }

    func testLegacyManualDomainEdit() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let file = docs.appendingPathComponent("manual-old.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: file.path))
        let suite = "fixture.legacy-manual"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        let refFile = docs.appendingPathComponent("manual-reference.json")
        let restart = FileManager.default.fileExists(atPath: refFile.path)
        if !restart { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let tunnel = FinalRouteFixture()
        let host = "manual.fixture.example"
        if restart {
            let ref = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(contentsOf: refFile)) as? [String: Any])
            let cold = try TunnelStartOptions.resolveForProvider(startOptions: nil, providerConfiguration: ref, requiresApply: false)
            XCTAssertEqual(cold, try store.systemConnectionOptions())
            try await tunnel.connect(options: cold)
        } else {
            let editor = ConfigModel(tunnel: tunnel, store: store)
            editor.importFromFile(at: file)
            XCTAssertNil(editor.importError)
            var root = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(store.content.utf8)) as? [String: Any])
            var route = try XCTUnwrap(root["route"] as? [String: Any])
            var rules = try XCTUnwrap(route["rules"] as? [[String: Any]])
            rules.insert(["domain": [host], "action": "route", "outbound": "MainProxy"], at: 0)
            route["rules"] = rules; root["route"] = route
            editor.draft = String(decoding: try JSONSerialization.data(withJSONObject: root), as: UTF8.self)
            editor.saveDraft()
            XCTAssertNil(editor.editorError)
            await editor.regenerateAndApply()
            XCTAssertEqual(store.selectedProfile?.pendingApply, false)
            let options = try store.connectionOptions()
            XCTAssertEqual(options.configContent, tunnel.content)
            let ref = try options.storedReference()
            try options.confirmStoredReference(ref)
            try JSONSerialization.data(withJSONObject: ref).write(to: refFile)
        }
        let status = try tunnel.request(host)
        XCTAssertEqual(status, 204)
        try JSONEncoder().encode(["explicit_proxy_status": status]).write(to: docs.appendingPathComponent(restart ? "manual-restart.json" : "manual-apply.json"))
    }

    func testEditedFinalRoundTrip() async throws {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        try XCTSkipUnless(FileManager.default.fileExists(atPath: docs.appendingPathComponent("final-route-fixture").path))
        let restarting = FileManager.default.fileExists(atPath: docs.appendingPathComponent("profile-restart").path)
        let suite = "fixture.final-route"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        if !restarting { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: suite))
        let tunnel = FinalRouteFixture()
        if !restarting {
            try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: docs.appendingPathComponent("groups.sakamoto"))))
            XCTAssertEqual(store.selectedProfile?.sourceFinal, "DIRECT")
            let editor = ConfigModel(tunnel: tunnel, store: store)
            editor.draft = try SettingsOverrides.setExperiment(true, in: editor.draft)
            editor.saveDraft()
            XCTAssertNil(editor.editorError)
            await editor.regenerateAndApply()
            XCTAssertEqual(try tunnel.request(), 204)
            var settings = store.experimentSettings
            XCTAssertEqual(settings.mode, .auto)
            settings.mode = .on
            try store.saveExperimentSettings(settings)
            settings.mode = .auto
            try store.saveExperimentSettings(settings)
            await editor.regenerateAndApply()
        } else {
            try await tunnel.connect(options: store.connectionOptions())
        }
        XCTAssertEqual(store.selectedProfile?.pendingApply, false)
        let status = try tunnel.request()
        XCTAssertEqual(status, 204)
        let report: [String: Any] = ["proxy_status": status, "mode": store.experimentSettings.mode.rawValue,
                                    "route_final": ConfigSemanticsReader.read(store.content)?.routeFinal ?? "", "fresh_process": restarting]
        try JSONSerialization.data(withJSONObject: report, options: [.sortedKeys]).write(to: docs.appendingPathComponent(restarting ? "final-restart.json" : "final-apply.json"))
    }

    /// Stage the generated package in the isolated simulator's Documents directory.
    /// A second invocation loads the saved profile in a fresh app process.
    func testExportedGroupPolicyLifecycle() async throws {
        let documents = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let package = documents.appendingPathComponent("groups.sakamoto")
        try XCTSkipUnless(FileManager.default.fileExists(atPath: package.path), "Stage the profile lifecycle fixture first")
        let defaults = try XCTUnwrap(UserDefaults(suiteName: "fixture.mobile-group-lifecycle"))
        let restarting = FileManager.default.fileExists(atPath: documents.appendingPathComponent("profile-restart").path)
        if !restarting { defaults.removePersistentDomain(forName: "fixture.mobile-group-lifecycle") }
        let store = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: "fixture-groups"))
        if !restarting { try store.installProfile(TunnelProfile.decodePackage(Data(contentsOf: package))) }
        func verify() throws {
            let runtime = try store.connectionContent()
            let group = try XCTUnwrap(ProxyGroupEditing.groups(runtime).first { $0.tag == "OthersAuto" })
            XCTAssertEqual(group.members, ["Allowed"])
            let raw = try XCTUnwrap(store.sourceBundle.files[AutomaticGroupSources.fileName])
            XCTAssertEqual(try AutomaticGroupSources.decode(Data(raw.utf8))["OthersAuto"], ["Excluded"])
        }
        try verify()
        if !restarting {
            for _ in 0..<2 { try await store.generateFromSources(); try verify() }
            let previous = try XCTUnwrap(store.selectedProfile)
            let root = try AppPaths.sharedDirectory().appendingPathComponent("Profiles")
            let manifest = root.appendingPathComponent(previous.id + "/profile.json")
            let backup = manifest.deletingLastPathComponent().appendingPathComponent("fixture-previous.json")
            let fm = FileManager.default
            try fm.moveItem(at: manifest, to: backup)
            try fm.createDirectory(at: manifest, withIntermediateDirectories: false)
            let previousAction = store.lastAction
            let originalPolicy = store.policy
            let model = ConfigModel(tunnel: NETunnelController(providerBundleIdentifier: "com.pidal.sakamoto.PacketTunnel"), store: store)
            model.beginPolicyRule()
            model.newPolicyMatch = "policy-fault.fixture.example"
            model.addPolicyRule()
            XCTAssertTrue(model.showAddPolicy)
            XCTAssertNotNil(model.policyFormError)
            XCTAssertEqual(model.newPolicyMatch, "policy-fault.fixture.example")
            XCTAssertThrowsError(try store.addProxyPolicyRules(["policy-fault.fixture.example"]))
            XCTAssertEqual(store.policy, originalPolicy)
            var expanded = try XCTUnwrap(ProxyGroupEditing.groups(store.content).first { $0.tag == "OthersAuto" })
            expanded.members.append("Excluded")
            XCTAssertThrowsError(try store.saveProxyGroup(expanded, expectedProfile: previous.id, expectedContent: previous.config))
            XCTAssertThrowsError(try store.saveRegionalDNS([:], expected: previous))
            XCTAssertEqual(store.selectedProfile, previous)
            XCTAssertEqual(store.content, previous.config)
            XCTAssertEqual(store.lastAction, previousAction)
            try verify()
            do { try await store.generateFromSources(); XCTFail("Generation reported success after a failed atomic replace") }
            catch { XCTAssertEqual(store.selectedProfile, previous) }
            try fm.removeItem(at: manifest)
            try fm.moveItem(at: backup, to: manifest)
            let candidate = try TunnelProfile.decodePackage(Data(contentsOf: package))
            let obstruction = root.appendingPathComponent(candidate.id + "/profile.json")
            try fm.createDirectory(at: obstruction, withIntermediateDirectories: true)
            XCTAssertThrowsError(try store.installProfile(candidate))
            XCTAssertEqual(store.selectedProfile, previous)
            let reopened = ConfigStore(persistence: defaults, keyStore: TailscaleKeychainStore(account: "fixture-groups"))
            XCTAssertEqual(reopened.selectedProfile?.ruleRevisionID, previous.ruleRevisionID)
            XCTAssertEqual(reopened.content, previous.config)
            try fm.removeItem(at: obstruction.deletingLastPathComponent())
            let configuration = URLSessionConfiguration.ephemeral
            configuration.protocolClasses = [DelayedConfResponse.self]
            let delayed = DelayedApplyTunnel()
            let importer = ConfigModel(tunnel: delayed, store: store, importConfiguration: configuration)
            let other = try TunnelProfile.decodePackage(Data(contentsOf: package))
            try store.installProfile(other)
            for action in ["cancel", "switch", "success"] {
                try store.selectProfile(action == "success" ? other.id : previous.id)
                let snapshot = store.selectedProfile
                importer.beginURLImport("https://fixture.example/main.conf")
                let download = Task { await importer.importFromURL() }
                while !importer.importing { await Task.yield() }
                if action == "cancel" { importer.cancelURLImport() }
                if action == "switch" { try store.selectProfile(other.id) }
                let accepted = await download.value
                XCTAssertEqual(accepted, action == "success")
                if action == "switch" { XCTAssertFalse(try XCTUnwrap(store.selectedProfile).sourcesChanged) }
                if action != "success" { try store.selectProfile(previous.id); XCTAssertEqual(store.selectedProfile, snapshot) }
            }
            try store.selectProfile(previous.id)
            let applying = Task { await store.regenerateAndApply(tunnel: delayed) }
            while !delayed.checking { await Task.yield() }
            try store.selectProfile(other.id)
            delayed.resume()
            await applying.value
            XCTAssertTrue(delayed.submissions.isEmpty)
            try store.selectProfile(previous.id)
            let oldOptions = try store.connectionOptions()
            let manager = FixtureVPNPreferences()
            manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: oldOptions)
            let controller = NETunnelController(providerBundleIdentifier: "fixture", manager: manager, isCurrent: { store.matchesConnectionOptions($0) })
            manager.afterSave = { try store.selectProfile(other.id) }
            do { try await controller.connect(options: oldOptions); XCTFail("Started superseded profile") }
            catch { XCTAssertTrue(error is CancellationError) }
            XCTAssertEqual(manager.saves, 2)
            XCTAssertEqual(manager.readSaved(), oldOptions)
            try store.selectProfile(previous.id)
            var prior = oldOptions
            prior.forceTailscaleDERP = !(oldOptions.forceTailscaleDERP ?? false)
            manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: prior)
            manager.failReadAfterSave = true
            do { try await controller.reload(options: oldOptions); XCTFail("Accepted failed preference read") }
            catch { XCTAssertEqual((error as NSError).domain, "FixturePreferences") }
            XCTAssertEqual(manager.saves, 4)
            XCTAssertEqual(manager.readSaved(), prior)
            var oldRules = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(oldOptions.configContent.utf8)) as? [String: Any])
            var route = try XCTUnwrap(oldRules["route"] as? [String: Any])
            route["final"] = "MainProxy"
            oldRules["route"] = route
            prior.configContent = String(decoding: try JSONSerialization.data(withJSONObject: oldRules), as: UTF8.self)
            prior.forceTailscaleDERP = oldOptions.forceTailscaleDERP
            manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: prior)
            manager.session.started = false
            let cancelledStart = Task { await store.regenerateAndApply(tunnel: controller) }
            for _ in 0..<1000 where !manager.session.started { try await Task.sleep(nanoseconds: 1_000_000) }
            XCTAssertTrue(manager.session.started)
            try store.selectProfile(other.id)
            await cancelledStart.value
            XCTAssertEqual(manager.session.status, .disconnected)
            XCTAssertEqual(manager.readSaved(), prior)
            XCTAssertEqual(store.selectedProfile?.pendingApply, true)
            try store.selectProfile(previous.id)
            manager.session.started = true
            manager.session.stopped = false
            manager.session.liveOptions = prior
            manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: prior)
            await store.regenerateAndApply(tunnel: controller)
            XCTAssertEqual(manager.session.reloaded.count, 2)
            XCTAssertTrue(try TunnelConfigurationReceipt(options: oldOptions).matches(XCTUnwrap(manager.session.reloaded.first)))
            XCTAssertEqual(manager.session.reloaded.last, prior)
            XCTAssertEqual(manager.session.liveOptions, prior)
            XCTAssertEqual(manager.session.status, .connected)
            XCTAssertEqual(manager.readSaved(), prior)
            XCTAssertEqual(store.selectedProfile?.pendingApply, true)
            XCTAssertNotEqual(store.lastAction, "applied to the running provider")
            manager.session.reloaded.removeAll()
            manager.session.rejectRollback = true
            await store.regenerateAndApply(tunnel: controller)
            XCTAssertEqual(manager.session.reloaded.count, 2)
            XCTAssertEqual(manager.session.status, .disconnected)
            XCTAssertEqual(manager.readSaved(), prior)
            XCTAssertEqual(store.selectedProfile?.pendingApply, true)
            manager.session.liveOptions = nil
            manager.session.stopped = true
            for result in ["failed", "recovered", "applied"] {
                manager.saved = try NETunnelController.makeProviderProtocol(bundleIdentifier: "fixture", options: prior)
                let running = result == "applied" ? oldOptions : prior
                manager.session.result = result == "failed" ? .failure("Fixture extension startup failed") : TunnelResponse(ok: true, state: TunnelStateSnapshot(serviceState: .running, nativeInstanceID: 2, appliedReceipt: try TunnelConfigurationReceipt(options: running)))
                manager.session.messages = 0
                manager.session.started = false
                await store.regenerateAndApply(tunnel: controller)
                XCTAssertTrue(manager.session.started)
                XCTAssertEqual(manager.session.messages, 1)
                XCTAssertEqual(store.selectedProfile?.pendingApply, result != "applied")
                XCTAssertEqual(store.lastAction == "applied to the running provider", result == "applied")
                let saved = try XCTUnwrap(manager.readSaved())
                XCTAssertTrue(try TunnelConfigurationReceipt(options: running).matches(saved))
            }
            let stopping = DelayedStopTunnel()
            let home = HomeModel(tunnel: stopping, commanding: nil, store: store)
            let reconnect = Task { await home.repair(restart: true) }
            for _ in 0..<1000 where !stopping.waiting { try await Task.sleep(nanoseconds: 1_000_000) }
            XCTAssertTrue(stopping.waiting)
            try store.selectProfile(other.id)
            stopping.finishStop()
            await reconnect.value
            XCTAssertEqual(stopping.starts, 0)
            XCTAssertEqual(home.repairResult?.state, "failed")
            let legacy = try TunnelProfile.decodePackage(Data(contentsOf: documents.appendingPathComponent("legacy-groups.sakamoto")))
            try store.installProfile(legacy)
            do { try await store.generateFromSources(); XCTFail("Legacy host export lacked exclusion intent") }
            catch { XCTAssertTrue(error.localizedDescription.contains("Reimport")) }
            XCTAssertEqual(ProxyGroupEditing.groups(try store.connectionContent()).first { $0.tag == "OthersAuto" }?.members, ["Allowed"])
            try store.selectProfile(previous.id)
        }
        let receipt = XCTAttachment(string: restarting ? "fresh process: Allowed" : "import, two generations, legacy rejection: Allowed")
        receipt.name = "profile-group-lifecycle"; receipt.lifetime = .keepAlways; add(receipt)
    }
}

/// The simulator substitutes the command-server probe for TUN ingress. The exported
/// routing rules, outbounds and absolute rule file paths come from the Apply payload.
private final class UnavailableVault: TailscaleAuthKeyStoring, @unchecked Sendable {
    let vault: TailscaleKeychainStore
    var unavailable = true
    init(_ vault: TailscaleKeychainStore) { self.vault = vault }
    var hasAuthKey: Bool { vault.hasAuthKey }
    func readAuthKey() throws -> String? {
        if unavailable { throw AppSetupError("Fixture read unavailable") }
        return try vault.readAuthKey()
    }
    func storeAuthKey(_ key: String) throws { try vault.storeAuthKey(key) }
    func deleteAuthKey() throws { try vault.deleteAuthKey() }
}

@MainActor private final class FinalRouteFixture: TunnelControlling {
    private(set) var content = ""
    func ping() async throws -> TunnelStateSnapshot { TunnelStateSnapshot(serviceState: .running) }
    func reload(configContent: String) async throws { content = configContent }
    func connect(options: TunnelStartOptions) async throws { content = options.configContent }
    func disconnect() async throws {}
    nonisolated func observations() -> AsyncStream<TunnelObservation> { AsyncStream { $0.finish() } }

    func request(_ domain: String = "unmatched-final.fixture.invalid") throws -> Int32 {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let setup = LibboxSetupOptions()
        setup.basePath = dir.path
        setup.workingPath = dir.path
        setup.tempPath = dir.path
        setup.commandServerListenPort = 19583
        setup.commandServerSecret = UUID().uuidString
        var error: NSError?
        LibboxSetup(setup, &error)
        if let error { throw error }
        defer { try? AppServiceSetup.apply() }
        let platform = ObservationTestPlatform()
        platform.testInterface = "lo0"
        let server = try XCTUnwrap(LibboxNewCommandServer(platform, platform, &error))
        if let error { throw error }
        try server.start()
        defer { try? server.closeService(); server.close() }
        var config = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(content.utf8)) as? [String: Any])
        let logfile = dir.appendingPathComponent("core.log")
        config["log"] = ["level": "debug", "output": logfile.path]
        defer { if let log = try? String(contentsOf: logfile, encoding: .utf8) { print(log) } }
        config["inbounds"] = []
        config["services"] = (config["services"] as? [[String: Any]])?.filter { $0["type"] as? String != "api" }
        try server.startOrReloadService(String(decoding: try JSONSerialization.data(withJSONObject: config), as: UTF8.self), options: LibboxOverrideOptions())
        var status: Int32 = 0
        try server.probeAutomaticOrigin(domain, port: 80, outboundTag: "", expectedInstanceID: server.runningInstanceID(), timeoutMillis: 3000, ret0_: &status)
        return status
    }
}

private final class DelayedConfResponse: URLProtocol, @unchecked Sendable {
    private var responseWork: DispatchWorkItem?
    override class func canInit(with request: URLRequest) -> Bool { request.url?.host == "fixture.example" }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let work = DispatchWorkItem { [weak self] in
            guard let self, let url = request.url else { return }
            client?.urlProtocol(self, didReceive: HTTPURLResponse(url: url, statusCode: 200, httpVersion: "HTTP/1.1", headerFields: nil)!, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: Data("[Rule]\nFINAL,DIRECT\n".utf8))
            client?.urlProtocolDidFinishLoading(self)
        }
        responseWork = work
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1, execute: work)
    }
    override func stopLoading() { responseWork?.cancel() }
}

private final class FixtureSession: NETunnelProviderSession, @unchecked Sendable {
    var started = false
    var result = TunnelResponse.failure("Fixture startup")
    var messages = 0
    var forwardReload: ((TunnelStartOptions, TunnelStartOptions, UUID?) throws -> Void)?
    var beforeRestore: (() -> Void)?
    var onFirstReload: (() -> Void)?
    var rejectRollback = false
    private var liveReceipt: TunnelConfigurationReceipt?
    var liveOptions: TunnelStartOptions? {
        didSet { liveReceipt = liveOptions.flatMap { try? TunnelConfigurationReceipt(options: $0) } }
    }
    var reloaded: [TunnelStartOptions] = []
    var stopped = false
    override var status: NEVPNStatus { stopped ? .disconnected : (started ? .connected : .invalid) }
    var afterStart: (() -> Void)?
    var rejectStart = false
    private(set) var startOptions: [String: NSObject]?
    override func startVPNTunnel(options: [String: NSObject]? = nil) throws {
        startOptions = options
        if rejectStart { throw NSError(domain: "FixtureStart", code: 1) }
        stopped = false; started = true
        afterStart?()
    }
    var stopCount = 0
    override func stopVPNTunnel() { stopCount += 1; stopped = true }
    var providerHandler: ((Data) async -> Data?)?
    var rejectReloadSend = false
    var rejectFirstRestore = false
    var rejectedSends = 0
    override func sendProviderMessage(_ messageData: Data, responseHandler: ((Data?) -> Void)? = nil) throws {
        if case .reloadProfile(_, _, let expected, _) = try TunnelRequest(data: messageData),
           rejectReloadSend || (rejectFirstRestore && expected != nil && rejectedSends == 0) {
            rejectedSends += 1
            throw NSError(domain: "FixtureSend", code: 1)
        }
        if let providerHandler {
            Task { responseHandler?(await providerHandler(messageData)) }
            return
        }
        if var live = liveOptions {
            if case .reloadProfile(let candidate, let transaction, let replacing, _) = try TunnelRequest(data: messageData) {
                if reloaded.count == 1 {
                    beforeRestore?()
                    live = liveOptions ?? live
                }
                if let replacing, replacing != liveReceipt {
                    responseHandler?(try TunnelResponse.failure("VPN configuration changed before rollback").encode())
                    return
                }
                try forwardReload?(live, candidate, transaction)
                reloaded.append(candidate)
                if reloaded.count == 2 && rejectRollback { responseHandler?(nil); return }
                live = candidate
                liveOptions = candidate
                if reloaded.count == 1 {
                    if let onFirstReload { onFirstReload() }
                    else { responseHandler?(nil); return }
                }
            }
            let snapshot = TunnelStateSnapshot(serviceState: .running, appliedReceipt: liveReceipt)
            responseHandler?(try TunnelResponse(ok: true, state: snapshot).encode())
            return
        }
        messages += 1
        responseHandler?(try (messages == 1 ? result : .failure("Unexpected second confirmation")).encode())
    }
}

private final class FixtureVPNPreferences: NETunnelProviderManager, @unchecked Sendable {
    let session = FixtureSession()
    override var connection: NEVPNConnection { session }
    var saved: NEVPNProtocol?
    var onDemandFile: URL?
    var demandWrites = [Bool]()
    var savedIDs = [String]()
    var saves = 0
    var failReadAfterSave = false
    var afterSave: (@MainActor () throws -> Void)?
    var failNextRead = false
    var holdNextRead = false
    var finishRead: (() -> Void)?
    var holdNextSave = false
    var finishSave: (() -> Void)?
    private var rejectRead = false
    func readSaved() -> TunnelStartOptions? {
        TunnelStartOptions(providerConfiguration: (saved as? NETunnelProviderProtocol)?.providerConfiguration ?? [:])
    }
    override func loadFromPreferences(completionHandler: @escaping (Error?) -> Void) {
        Task { @MainActor in
            if rejectRead || failNextRead { failNextRead = false; rejectRead = false; completionHandler(NSError(domain: "FixturePreferences", code: 1)); return }
            if let action = afterSave, saves == 1 {
                afterSave = nil
                do { try action() } catch { completionHandler(error); return }
            }
            if let file = onDemandFile, FileManager.default.fileExists(atPath: file.path) {
                do {
                    let data = try Data(contentsOf: file)
                    let classes: [AnyClass] = [NSDictionary.self, NSArray.self, NSNumber.self, NSString.self, NSURL.self, NEOnDemandRule.self, NEEvaluateConnectionRule.self]
                    guard let values = try NSKeyedUnarchiver.unarchivedObject(ofClasses: classes, from: data) as? [String: Any] else { throw CocoaError(.coderReadCorrupt) }
                    isEnabled = values["enabled"] as? Bool == true
                    isOnDemandEnabled = values["onDemand"] as? Bool == true
                    onDemandRules = values["rules"] as? [NEOnDemandRule]
                } catch { completionHandler(error); return }
            }
            protocolConfiguration = saved
            if holdNextRead {
                holdNextRead = false
                finishRead = { [weak self] in self?.finishRead = nil; completionHandler(nil) }
                return
            }
            completionHandler(nil)
        }
    }
    override func saveToPreferences(completionHandler: ((Error?) -> Void)? = nil) {
        if let id = (protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration?["profileID"] as? String { savedIDs.append(id) }
        if let file = onDemandFile {
            do {
                let values: [String: Any] = ["enabled": isEnabled, "onDemand": isOnDemandEnabled, "rules": onDemandRules ?? []]
                try NSKeyedArchiver.archivedData(withRootObject: values, requiringSecureCoding: true).write(to: file, options: .atomic)
                demandWrites.append(isOnDemandEnabled)
            } catch { completionHandler?(error); return }
        }
        saves += 1
        saved = protocolConfiguration?.copy() as? NEVPNProtocol
        rejectRead = failReadAfterSave
        failReadAfterSave = false
        if holdNextSave {
            holdNextSave = false
            finishSave = { [weak self] in self?.finishSave = nil; completionHandler?(nil) }
            return
        }
        completionHandler?(nil)
    }
}

@MainActor private final class DelayedStopTunnel: TunnelControlling {
    var starts = 0
    var waiting = false
    private var stopped = false
    private var pending: CheckedContinuation<Void, Never>?
    func finishStop() { stopped = true; pending?.resume(); pending = nil }
    func disconnect() async throws { waiting = true; await withCheckedContinuation { pending = $0 } }
    func status() async throws -> ServiceState { stopped ? .stopped : .running }
    func ping() async throws -> TunnelStateSnapshot { TunnelStateSnapshot(serviceState: .running, nativeInstanceID: 1) }
    func connect(options: TunnelStartOptions) async throws { starts += 1 }
    func reload(configContent: String) async throws { XCTFail("Unexpected reload") }
    nonisolated func observations() -> AsyncStream<TunnelObservation> { AsyncStream { $0.finish() } }
}

@MainActor private final class DelayedApplyTunnel: TunnelControlling {
    var checking = false
    var submissions: [String] = []
    private var pending: CheckedContinuation<Void, Never>?
    func resume() { pending?.resume(); pending = nil }
    func ping() async throws -> TunnelStateSnapshot {
        checking = true
        await withCheckedContinuation { pending = $0 }
        return TunnelStateSnapshot(serviceState: .running)
    }
    func reload(configContent: String) async throws { submissions.append(configContent) }
    func connect(options: TunnelStartOptions) async throws { submissions.append(options.configContent) }
    func disconnect() async throws {}
    nonisolated func observations() -> AsyncStream<TunnelObservation> { AsyncStream { $0.finish() } }
}
