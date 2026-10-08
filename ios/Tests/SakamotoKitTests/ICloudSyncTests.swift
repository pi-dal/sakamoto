import XCTest
@testable import SakamotoKit

// ICloudSyncStore unit tests — entirely in memory: no iCloud account, no
// FileManager touches, injected clock. The scenarios mirror the semantics of
// internal/icloud (docs/icloud.md): one-sided first use copies, identical
// sides establish a hash baseline, both-sides-changed without a shared
// baseline stops the pass before overwriting anything, deletions are not
// propagated or restored, and the forbidden-name denylist holds at any depth.

final class ICloudSyncTests: XCTestCase {

    // MARK: Fakes

    private struct FixedContainer: ICloudContainer {
        let url: URL?
        func containerURL() -> URL? { url }
    }

    private final class FakeClock: ICloudSyncClock, @unchecked Sendable {
        private let lock = NSLock()
        private var current: Date
        init(_ start: Date = Date(timeIntervalSince1970: 1_000_000)) { current = start }
        func now() -> Date {
            lock.lock(); defer { lock.unlock() }
            current = current.addingTimeInterval(60)
            return current
        }
    }

    /// Wraps an in-memory fs and fails writes below a given path prefix —
    /// for the mid-pass failure semantics. Flip `shouldFail` off to simulate
    /// the transient I/O problem clearing.
    final class FailingWriteFilesystem: ICloudFilesystem, @unchecked Sendable {
        let inner = InMemoryFilesystem()
        let failBelow: String
        var shouldFail = true
        init(failBelow: String) { self.failBelow = failBelow }

        func contents(atPath path: String) throws -> Data? { try inner.contents(atPath: path) }
        func fileExists(atPath path: String) -> Bool { inner.fileExists(atPath: path) }
        func isSymbolicLink(atPath path: String) throws -> Bool { try inner.isSymbolicLink(atPath: path) }
        func ensureDirectory(atPath path: String) throws { try inner.ensureDirectory(atPath: path) }
        func writeAtomic(_ data: Data, toPath path: String) throws {
            if shouldFail && path.hasPrefix(failBelow) {
                throw ICloudSyncError.filesystem("injected I/O failure for \(path)")
            }
            try inner.writeAtomic(data, toPath: path)
        }
    }

    private let cloudRoot = URL(fileURLWithPath: "/cloud", isDirectory: true)

    private func makeStore(
        settings: ICloudSyncSettings = ICloudSyncSettings(),
        local: InMemoryFilesystem = InMemoryFilesystem(),
        cloud: ICloudFilesystem = InMemoryFilesystem(),
        containerURL: URL? = URL(fileURLWithPath: "/cloud", isDirectory: true),
        clock: ICloudSyncClock = FakeClock(),
        localDirectory: String = "/local",
    ) -> ICloudSyncStore {
        var settings = settings
        settings.directoryName = "sakamoto"
        return ICloudSyncStore(
            settings: settings,
            localFilesystem: local,
            cloudFilesystem: cloud,
            container: FixedContainer(url: containerURL),
            clock: clock,
            localDirectory: localDirectory,
        )
    }

    private func enabled(includeConf: Bool? = nil, additional: [String] = []) -> ICloudSyncSettings {
        var s = ICloudSyncSettings()
        s.enabled = true
        s.includeConf = includeConf
        s.additionalSourcePaths = additional
        return s
    }

    private func entry(_ name: String, _ content: String) -> ICloudSyncPayload.Entry {
        .init(name: name, data: Data(content.utf8))
    }

    private func cloudPath(_ name: String) -> String {
        cloudRoot.appendingPathComponent("Documents/sakamoto", isDirectory: true).path + "/" + name
    }

    func testFirstSyncDownloadsNodesWithoutLocalPayloadFromSelectedTUIDirectory() async throws {
        let cloud = InMemoryFilesystem()
        try cloud.writeAtomic(Data("node-a".utf8), toPath: "/tui/nodes.txt")
        let store = makeStore(settings: enabled(), cloud: cloud)
        let outcome = await store.syncNow(staging: [], directoryURL: URL(fileURLWithPath: "/tui"), downloadNames: ["nodes.txt"])
        XCTAssertEqual(outcome.downloads.map(\.name), ["nodes.txt"])
        XCTAssertEqual(outcome.downloads.first?.data, Data("node-a".utf8))
        XCTAssertFalse(cloud.fileExists(atPath: "/tui/sakamoto/nodes.txt"))
    }

    func testInvalidDownloadedNodesDoNotAdvanceBaselineOrWriteOtherSources() async throws {
        let cloud = InMemoryFilesystem()
        let local = InMemoryFilesystem()
        try cloud.writeAtomic(Data("invalid".utf8), toPath: "/tui/nodes.txt")
        let store = makeStore(settings: enabled(), local: local, cloud: cloud)
        let result = await store.syncNow(staging: [entry("policy.json", "local")], directoryURL: URL(fileURLWithPath: "/tui"), downloadNames: ["nodes.txt"], validateDownload: { _, _ in throw NodeFileImport.Failure("invalid") })
        XCTAssertTrue(result.downloads.isEmpty)
        let scopedState = "/local/directories/" + ICloudSyncStore.digest(Data("/tui".utf8)) + "/icloud-state.json"
        XCTAssertFalse(local.fileExists(atPath: scopedState))
        XCTAssertFalse(cloud.fileExists(atPath: "/tui/policy.json"))
    }

    func testSelectedFolderConflictDoesNotOverwriteNodes() async throws {
        let cloud = InMemoryFilesystem()
        try cloud.writeAtomic(Data("remote".utf8), toPath: "/tui/nodes.txt")
        let store = makeStore(settings: enabled(), cloud: cloud)
        let result = await store.syncNow(staging: [entry("nodes.txt", "local")], directoryURL: URL(fileURLWithPath: "/tui"))
        XCTAssertTrue(result.status.isConflict)
        XCTAssertEqual(try cloud.contents(atPath: "/tui/nodes.txt"), Data("remote".utf8))
    }

    func testChangingDirectoryDoesNotReusePreviousBaseline() async throws {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), cloud: cloud)
        _ = await store.syncNow(staging: [entry("nodes.txt", "baseline")], directoryURL: URL(fileURLWithPath: "/one"))
        try cloud.writeAtomic(Data("baseline".utf8), toPath: "/two/nodes.txt")
        let result = await store.syncNow(staging: [entry("nodes.txt", "local-change")], directoryURL: URL(fileURLWithPath: "/two"))
        XCTAssertTrue(result.status.isConflict)
        XCTAssertEqual(try cloud.contents(atPath: "/two/nodes.txt"), Data("baseline".utf8))
    }

    func testChangedLocalSourcesAbortBeforeCloudAndBaselineWrites() async {
        let cloud = InMemoryFilesystem()
        let local = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), local: local, cloud: cloud)
        let result = await store.syncNow(staging: [entry("nodes.txt", "local")], sourcesAreCurrent: { false })
        XCTAssertTrue(result.updates.isEmpty)
        XCTAssertTrue(result.downloads.isEmpty)
        XCTAssertTrue(cloud.allFiles().isEmpty)
        XCTAssertTrue(local.allFiles().isEmpty)
    }

    func testFirstUseDiscoversMainConfAndRelativeIncludes() async throws {
        let cloud = InMemoryFilesystem(), local = InMemoryFilesystem()
        try cloud.writeAtomic(Data("[General]\ninclude = child.conf\n[Rule]\nFINAL,DIRECT".utf8), toPath: "/tui/sources/current/main.conf")
        try cloud.writeAtomic(Data("[Rule]\nDOMAIN,example.com,DIRECT".utf8), toPath: "/tui/sources/current/child.conf")
        let store = makeStore(settings: enabled(includeConf: true), local: local, cloud: cloud)
        let outcome = await store.syncNow(staging: [], directoryURL: URL(fileURLWithPath: "/tui"), downloadNames: ["nodes.txt"], discoverConf: true)
        XCTAssertEqual(outcome.discoveredMainConf, "sources/current/main.conf")
        XCTAssertEqual(Set(outcome.downloads.map(\.name)), Set(["sources/current/main.conf", "sources/current/child.conf"]))
    }
    func testDiscoveryFailsClosedOnMissingInclude() async throws {
        let cloud = InMemoryFilesystem(), local = InMemoryFilesystem()
        try cloud.writeAtomic(Data("[General]\ninclude = missing.conf\n[Rule]\nFINAL,DIRECT".utf8), toPath: "/tui/main.conf")
        let store = makeStore(settings: enabled(includeConf: true), local: local, cloud: cloud)
        let outcome = await store.syncNow(staging: [], directoryURL: URL(fileURLWithPath: "/tui"), discoverConf: true)
        XCTAssertTrue(outcome.downloads.isEmpty)
        XCTAssertTrue(local.allFiles().isEmpty)
    }

    // MARK: Path policy (ValidSourceName port)

    func testValidSourceNameDenylist() {
        // Generated state / credentials / sockets / logs — rejected at any depth.
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("config.json"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("nested/config.json"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("rules.srs"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("deep/dir/cache.db"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("watch.sock"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("svc/run.lock"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("app.log"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("auth.json"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("server.key"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("cert.pem"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("secrets.zsh"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("icloud-state.json"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("sakamoto.yaml"))
        // Traversal / hidden / absolute / unclean.
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("../escape.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("a/../b.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName(".hidden/x.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("/absolute/path.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("a//b.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("./a.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("back\\slash.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName(""))
        // The logs TOP directory is reserved; nested same-named file is not.
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("logs/x.txt"))
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("LOGS/x.txt"))
        // Legitimate source names.
        XCTAssertTrue(ICloudSyncPaths.isValidSourceName("nodes.txt"))
        XCTAssertTrue(ICloudSyncPaths.isValidSourceName("policy.json"))
        XCTAssertTrue(ICloudSyncPaths.isValidSourceName("subscriptions.json"))
        XCTAssertTrue(ICloudSyncPaths.isValidSourceName("conf/macOS.conf"))
        XCTAssertTrue(ICloudSyncPaths.isValidSourceName("conf/includes/sr_top500.conf"))
        XCTAssertTrue(ICloudSyncPaths.isValidSourceName("sources/current/deep/rule.conf"))
        // iOS-local staging filenames stay local-only.
        XCTAssertFalse(ICloudSyncPaths.isValidSourceName("sakamoto-config.json"))
    }

    // MARK: Conf include discovery

    func testLocalIncludesParseAndValidate() throws {
        let conf = """
        \u{FEFF}[General]
        // comment line
        include = sr_top500_banlist_ad.conf, https://remote.example/list.conf
        include=deep/nested rule.conf // trailing comment
        [Rule]
        DOMAIN-SUFFIX,example.com,DIRECT
        """
        let includes = try ICloudSyncConf.localIncludes(ofConfContent: Data(conf.utf8))
        XCTAssertEqual(includes, ["sr_top500_banlist_ad.conf", "deep/nested rule.conf"])
    }

    func testLocalIncludesRejectUnsafeAndSectionless() {
        let unsafe = "[General]\ninclude = ../../etc/passwd\n"
        XCTAssertThrowsError(try ICloudSyncConf.localIncludes(ofConfContent: Data(unsafe.utf8))) { error in
            XCTAssertEqual(error as? ICloudSyncError, .unsafeInclude("../../etc/passwd"))
        }
        let sectionless = "DOMAIN-SUFFIX,example.com,DIRECT\n"
        XCTAssertThrowsError(try ICloudSyncConf.localIncludes(ofConfContent: Data(sectionless.utf8))) { error in
            XCTAssertEqual(error as? ICloudSyncError, .confLacksSections)
        }
    }

    // MARK: Digest

    func testDigestMatchesSHA256Vector() {
        // echo -n "abc" | shasum -a 256
        XCTAssertEqual(ICloudSyncStore.digest(Data("abc".utf8)),
                       "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
    }

    // MARK: Pass semantics

    func testDisabledSyncDoesNothing() async {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: ICloudSyncSettings(), cloud: cloud) // enabled=false
        let outcome = await store.syncNow(staging: [entry("nodes.txt", "vless://x\n")])
        XCTAssertEqual(outcome.status, .disabled)
        XCTAssertTrue(cloud.allFiles().isEmpty)
    }

    func testUnavailableWithoutContainer() async {
        let store = makeStore(settings: enabled(), containerURL: nil)
        let outcome = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        guard case .unavailable = outcome.status else {
            return XCTFail("expected unavailable, got \(outcome.status)")
        }
    }

    func testFirstSyncUploadsAndEstablishesBaseline() async throws {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), cloud: cloud)
        let outcome = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        XCTAssertEqual(outcome.updates, ["uploaded: nodes.txt"])
        XCTAssertEqual(try cloud.contents(atPath: cloudPath("nodes.txt")), Data("v1\n".utf8))

        // Identical second pass: up to date, baseline unchanged.
        let second = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        XCTAssertTrue(second.updates.isEmpty)
        guard case .upToDate = second.status else {
            return XCTFail("expected upToDate, got \(second.status)")
        }
        XCTAssertEqual(try cloud.contents(atPath: cloudPath("nodes.txt")), Data("v1\n".utf8))
    }

    func testBaselineMakesOneSidedChangeSyncable() async throws {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), cloud: cloud)
        _ = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])

        // Local edit → uploaded update.
        let uploaded = await store.syncNow(staging: [entry("nodes.txt", "v2\n")])
        XCTAssertEqual(uploaded.updates, ["uploaded update: nodes.txt"])
        XCTAssertEqual(try cloud.contents(atPath: cloudPath("nodes.txt")), Data("v2\n".utf8))

        // Cloud edit (another device) → download returned for adoption.
        try? cloud.writeAtomic(Data("v3\n".utf8), toPath: cloudPath("nodes.txt"))
        let downloaded = await store.syncNow(staging: [entry("nodes.txt", "v2\n")])
        XCTAssertEqual(downloaded.updates, ["updated from iCloud: nodes.txt"])
        XCTAssertEqual(downloaded.downloads.first?.name, "nodes.txt")
        XCTAssertEqual(downloaded.downloads.first?.data, Data("v3\n".utf8))
        // The local staging file holds the downloaded bytes too.
        let local = await store.stagedLocalContent(name: "nodes.txt")
        XCTAssertEqual(local, Data("v3\n".utf8))
    }

    func testBothSidesChangedWithoutBaselineStopsPass() async throws {
        let cloud = InMemoryFilesystem()
        // Simulate another device having uploaded v2 with a baseline this
        // store never shared: cloud has content, our baseline state is empty.
        try? cloud.writeAtomic(Data("v2-other-device\n".utf8), toPath: cloudPath("nodes.txt"))
        let store = makeStore(settings: enabled(), cloud: cloud)

        let outcome = await store.syncNow(staging: [
            entry("policy.json", "{}"),
            entry("nodes.txt", "v2-locally-changed\n"),
        ])
        guard case .conflict(_, let names) = outcome.status else {
            return XCTFail("expected conflict, got \(outcome.status)")
        }
        XCTAssertEqual(names, ["nodes.txt"])
        // Neither copy was overwritten — cloud AND local staging untouched.
        XCTAssertEqual(try cloud.contents(atPath: cloudPath("nodes.txt")), Data("v2-other-device\n".utf8))
        // The unrelated clean pair was ALSO not copied: the whole graph is
        // preflighted before anything is written (docs/icloud.md).
        XCTAssertNil(try cloud.contents(atPath: cloudPath("policy.json")))
        XCTAssertTrue(outcome.downloads.isEmpty)
    }

    func testDeletedInCloudStopsInsteadOfRestoring() async throws {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), cloud: cloud)
        _ = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        // Simulate an external deletion in iCloud Drive.
        cloud.removeFile(atPath: cloudPath("nodes.txt"))

        let outcome = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        guard case .failed(_, let message) = outcome.status else {
            return XCTFail("expected failed, got \(outcome.status)")
        }
        XCTAssertTrue(message.contains("deleted in iCloud"), "unexpected message: \(message)")
        XCTAssertNil(try cloud.contents(atPath: cloudPath("nodes.txt")))
    }

    func testMidPassFailureKeepsCompletedBaselines() async throws {
        let cloud = FailingWriteFilesystem(failBelow: cloudPath("policy.json"))
        let store = makeStore(settings: enabled(), cloud: cloud)
        let outcome = await store.syncNow(staging: [
            entry("nodes.txt", "v1\n"),          // succeeds
            entry("policy.json", "{}"),          // injected failure
        ])
        guard case .failed = outcome.status else {
            return XCTFail("expected failed, got \(outcome.status)")
        }
        // The completed copy keeps its file AND its baseline.
        XCTAssertEqual(try cloud.contents(atPath: cloudPath("nodes.txt")), Data("v1\n".utf8))
        // The transient failure clears: retry copies only the remaining file.
        cloud.shouldFail = false
        let second = await store.syncNow(staging: [entry("nodes.txt", "v1\n"), entry("policy.json", "{}")])
        XCTAssertTrue(second.updates.contains("uploaded: policy.json"))
        XCTAssertFalse(second.updates.contains { $0.contains("nodes.txt") },
                       "already-synced file must not copy again")
    }

    func testDeletionsAreNotPropagated() async throws {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), cloud: cloud)
        _ = await store.syncNow(staging: [entry("nodes.txt", "v1\n"), entry("policy.json", "{}")])
        // The app stops staging policy.json (e.g. list emptied) — the cloud
        // copy stays, no error, no deletion.
        let outcome = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        guard case .upToDate = outcome.status else {
            return XCTFail("expected upToDate, got \(outcome.status)")
        }
        XCTAssertEqual(try cloud.contents(atPath: cloudPath("policy.json")), Data("{}".utf8))
    }

    func testForbiddenNameFailsThePass() async {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), cloud: cloud)
        let outcome = await store.syncNow(staging: [
            entry("config.json", "{\"inbounds\":[]}"),
        ])
        guard case .failed(_, let message) = outcome.status else {
            return XCTFail("expected failed, got \(outcome.status)")
        }
        XCTAssertTrue(message.contains("forbidden"), "unexpected message: \(message)")
        XCTAssertTrue(cloud.allFiles().isEmpty)
    }

    func testOversizedEntryFailsThePass() async throws {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(), cloud: cloud)
        let oversized = Data(repeating: 0x61, count: ICloudSyncLimits.maxSourceBytes + 1)
        let storeOutcome = await store.syncNow(staging: [
            entry("nodes.txt", "small\n"),
            ICloudSyncPayload.Entry(name: "big.txt", data: oversized),
        ])
        guard case .failed(_, let message) = storeOutcome.status else {
            return XCTFail("expected failed, got \(storeOutcome.status)")
        }
        XCTAssertTrue(message.contains("32 MiB"), "unexpected message: \(message)")
    }

    func testAdditionalPathsWithoutStagedEntryAreSkipped() async {
        let cloud = InMemoryFilesystem()
        let store = makeStore(settings: enabled(additional: ["nodes.txt", "wishlist.txt"]), cloud: cloud)
        let outcome = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        XCTAssertEqual(outcome.skipped, ["wishlist.txt"])
        XCTAssertEqual(outcome.updates, ["uploaded: nodes.txt"])
    }

    func testSettingsRejectForbiddenAdditionalPaths() async {
        let store = makeStore(settings: enabled())
        var settings = ICloudSyncSettings()
        settings.enabled = true
        settings.additionalSourcePaths = ["config.json"]
        do {
            _ = try await store.updateSettings(settings)
            XCTFail("expected forbiddenSourceName")
        } catch let error as ICloudSyncError {
            XCTAssertEqual(error, .forbiddenSourceName("config.json"))
        } catch {
            XCTFail("unexpected error type: \(error)")
        }
    }

    func testCloudSymlinkIsRefused() async {
        let cloud = InMemoryFilesystem()
        cloud.createSymbolicLink(atPath: cloudPath("nodes.txt"), toDestination: "/etc/passwd")
        let store = makeStore(settings: enabled(), cloud: cloud)
        let outcome = await store.syncNow(staging: [entry("nodes.txt", "v1\n")])
        guard case .failed(_, let message) = outcome.status else {
            return XCTFail("expected failed, got \(outcome.status)")
        }
        XCTAssertTrue(message.contains("symlink"), "unexpected message: \(message)")
    }

    func testNoSourcesConfigured() async {
        let store = makeStore(settings: enabled())
        let outcome = await store.syncNow(staging: [])
        guard case .failed(_, let message) = outcome.status else {
            return XCTFail("expected failed, got \(outcome.status)")
        }
        XCTAssertTrue(message.contains("no local source"), "unexpected message: \(message)")
    }

    func testTwoDevicesConvergeThroughSharedBaseline() async {
        let cloud = InMemoryFilesystem()
        // Device A (uploads first) and device B (downloads the shared file),
        // each with its own local state but the same iCloud container.
        let deviceA = makeStore(settings: enabled(), cloud: cloud, localDirectory: "/local-a")
        let deviceB = makeStore(settings: enabled(), cloud: cloud, localDirectory: "/local-b")

        _ = await deviceA.syncNow(staging: [entry("nodes.txt", "shared-v1\n")])
        let bDownload = await deviceB.syncNow(staging: [entry("nodes.txt", "shared-v1\n")])
        XCTAssertTrue(bDownload.updates.isEmpty, "identical content must establish baseline, not copy")

        // B edits and uploads; A (unchanged since baseline) downloads.
        _ = await deviceB.syncNow(staging: [entry("nodes.txt", "shared-v2\n")])
        let aFollow = await deviceA.syncNow(staging: [entry("nodes.txt", "shared-v1\n")])
        XCTAssertEqual(aFollow.downloads.first?.data, Data("shared-v2\n".utf8))
    }
}
