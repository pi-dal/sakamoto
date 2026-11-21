import CryptoKit
import Foundation

// The iCloud sync pass for the iOS app — a faithful port of
// internal/icloud/sync.go's conflict-safe algorithm onto the app's staged
// state, with the filesystem and clock injected so tests run entirely in
// memory (no iCloud account is ever touched by the unit tests).
//
// Model (mirrors the macOS daemon):
//   local side  — source bytes supplied by the profile. Downloads stay in memory
//                 until the app adopts them into the protected profile manifest.
//   cloud side  — `<containerURL>/<directoryName>/<name>` inside the real
//                 ubiquity container (NSFileCoordinator-coordinated writes).
//   baseline    — `localDirectory/icloud-state.json`: name → sha256 hex of
//                 the last copy both sides agreed on. Local-only, and
//                 rejected as a source name (denylist).
//
// Pass rules (docs/icloud.md):
//   one-sided first use   → copy (upload or download)
//   identical             → establish/refresh the shared hash baseline
//   both changed, and no
//   common baseline       → STOP. Nothing is overwritten. The outcome
//                           carries the conflicting names for the UI.
//   deletions             → not propagated, not silently restored.
//   failure mid-pass      → completed copies keep their baselines; the
//                           error is reported (never a fake success).
// The whole source graph is preflighted before any byte is copied.

// MARK: - Outcome

public struct ICloudSyncOutcome: Equatable, Sendable {
    /// Terminal status of the pass (also kept inside the store).
    public var status: ICloudSyncStatus
    /// Human-readable copy messages ("uploaded: nodes.txt", ...).
    public var updates: [String]
    /// Files the cloud had a newer copy of; the app adopts them into its
    /// ConfigStore staging state (the store never edits app state itself).
    public var downloads: [Download]
    /// Names the pass skipped (e.g. additional paths with no staged data).
    public var skipped: [String]
    public var discoveredMainConf: String? = nil

    public struct Download: Equatable, Sendable {
        public var name: String
        public var data: Data

        public init(name: String, data: Data) {
            self.name = name
            self.data = data
        }
    }
}

// MARK: - Store

public struct ICloudAdoptionReceipt: Sendable {
    public let scope: String
    public let batch: UUID
}

public actor ICloudSyncStore {

    public static let stateFileName = "icloud-state.json"
    private let localFilesystem: ICloudFilesystem
    private let cloudFilesystem: ICloudFilesystem
    private let container: ICloudContainer
    private let clock: ICloudSyncClock
    private let localDirectory: String

    private var settings: ICloudSyncSettings
    private var lastStatus: ICloudSyncStatus?
    private struct Conflict: Equatable {
        let local: String
        let remote: String
    }
    private var reviewedScope: String?
    private var reviewedConflicts: [String: Conflict] = [:]

    public init(
        settings: ICloudSyncSettings = ICloudSyncSettings(),
        localFilesystem: ICloudFilesystem,
        cloudFilesystem: ICloudFilesystem,
        container: ICloudContainer,
        clock: ICloudSyncClock = ICloudSystemClock(),
        localDirectory: String,
    ) {
        self.settings = settings
        self.localFilesystem = localFilesystem
        self.cloudFilesystem = cloudFilesystem
        self.container = container
        self.clock = clock
        self.localDirectory = localDirectory
        self.lastStatus = nil
    }

    // MARK: Settings & status

    public func currentSettings() -> ICloudSyncSettings { settings }

    public func currentStatus() -> ICloudSyncStatus {
        lastStatus ?? (settings.enabled ? .waitingFirstRun : .disabled)
    }

    /// Replace settings. Source names are validated here so a bad additional
    /// path can never get as far as the pass. Persisting is the caller's job
    /// (the App layer keeps an encoded copy in UserDefaults).
    public func updateSettings(_ new: ICloudSyncSettings) throws {
        if !new.directoryName.isEmpty {
            guard ICloudSyncPaths.isValidSourceName(new.directoryName),
                  !new.directoryName.contains("/") else {
                throw ICloudSyncError.forbiddenSourceName(new.directoryName)
            }
        }
        for path in new.additionalSourcePaths {
            guard ICloudSyncPaths.isValidSourceName(path) else {
                throw ICloudSyncError.forbiddenSourceName(path)
            }
        }
        settings = new
        if !new.enabled {
            lastStatus = .disabled
        }
    }

    // MARK: The pass

    /// Run one sync pass over the staged entries. See the file header for
    /// the conflict rules; this is the ONLY method that touches iCloud.
    public func syncNow(
        staging: [ICloudSyncPayload.Entry],
        directoryURL: URL? = nil,
        downloadNames: [String] = [],
        sourceScope: String = "",
        mainConf: String = "",
        discoverConf: Bool = false,
        conflictChoice: ICloudConflictChoice? = nil,
        validateDownload: @Sendable (String, Data) throws -> Void = { _, _ in },
        sourcesAreCurrent: @Sendable () async -> Bool = { true },
        adoptedBatches: [String: UUID] = [:],
        adoptDownloads: @Sendable ([ICloudSyncOutcome.Download], String?, ICloudAdoptionReceipt) async throws -> Void = { _, _, _ in }
    ) async -> ICloudSyncOutcome {
        guard settings.enabled else {
            lastStatus = .disabled
            return ICloudSyncOutcome(status: .disabled, updates: [], downloads: [], skipped: [])
        }

        // --- resolve the container -----------------------------------------

        let containerURL = directoryURL ?? container.containerURL()
        guard let containerURL else {
            let status = ICloudSyncStatus.unavailable(
                "no iCloud account signed in or container not entitled",
            )
            lastStatus = status
            return ICloudSyncOutcome(status: status, updates: [], downloads: [], skipped: [])
        }
        if let directoryURL {
            do { try cloudFilesystem.validateDestination(directoryURL) }
            catch {
                let status = ICloudSyncStatus.unavailable("Choose an accessible shared folder in iCloud Drive.")
                lastStatus = status
                return ICloudSyncOutcome(status: status, updates: [], downloads: [], skipped: [])
            }
        }
        let directoryName = settings.directoryName.isEmpty ? "sakamoto" : settings.directoryName
        let cloudRoot = directoryURL?.path ?? containerURL
            .appendingPathComponent("Documents", isDirectory: true)
            .appendingPathComponent(directoryName, isDirectory: true).path

        // Discover only consented conf sources. If multiple roots exist,
        // preserve all and require the UI to select; never choose arbitrarily.
        var confNames: [String] = []
        var discoveredMain: String?
        var deferredConf = false
        var downloading = Set<String>()
        var skipped: [String] = []
        if discoverConf {
            do {
                var queue = mainConf.isEmpty ? try cloudFilesystem.sourceNames(under: cloudRoot).filter { $0.lowercased().hasSuffix(".conf") } : [mainConf]
                var visited = Set<String>()
                var children = Set<String>()
                while let name = queue.popLast() {
                    guard visited.insert(name).inserted else { continue }
                    guard visited.count <= ICloudSyncLimits.maxIncludeFiles, ICloudSyncPaths.isValidSourceName(name), name.lowercased().hasSuffix(".conf") else { throw ICloudSyncError.unsafeInclude(name) }
                    try Self.checkPath(root: cloudRoot, path: cloudRoot + "/" + name, filesystem: cloudFilesystem)
                    let data: Data?
                    do { data = try cloudFilesystem.contents(atPath: cloudRoot + "/" + name) }
                    catch {
                        if isICloudSourceDownloading(error) { downloading.insert(name); deferredConf = true; continue }
                        throw error
                    }
                    let parent = (name as NSString).deletingLastPathComponent
                    let staged = staging.first(where: { $0.name == name })?.data
                    var relatives = Set<String>()
                    do {
                        for body in [data, staged].compactMap({ $0 }) {
                            relatives.formUnion(try ICloudSyncConf.localIncludes(ofConfContent: body))
                        }
                    } catch let error as ICloudSyncError {
                        // A draft found by enumeration is not a rule source. Required
                        // includes are still checked against the assembled graph.
                        guard mainConf.isEmpty, staged == nil else { throw error }
                        skipped.append(name)
                        continue
                    }
                    confNames.append(name)
                    for relative in relatives {
                        let child = parent.isEmpty ? relative : parent + "/" + relative
                        children.insert(child)
                        if !mainConf.isEmpty { queue.append(child) }
                    }
                }
                let roots = confNames.filter { !children.contains($0) }
                if !deferredConf && mainConf.isEmpty && roots.count == 1 { discoveredMain = roots.first }
            } catch { return fail(error) }
        }
        let effectiveMain = mainConf.isEmpty ? discoveredMain ?? "" : mainConf
        let requestedDownloads = Array(Set(downloadNames + confNames)).sorted()

        // --- validate + resolve names ---------------------------------------

        var pairs: [Pair] = []
        for entry in staging {
            if discoverConf && !mainConf.isEmpty && entry.name.lowercased().hasSuffix(".conf") && !confNames.contains(entry.name) { continue }
            guard ICloudSyncPaths.isValidSourceName(entry.name) else {
                return fail(ICloudSyncError.forbiddenSourceName(entry.name))
            }
            guard entry.data.count <= ICloudSyncLimits.maxSourceBytes else {
                return fail(ICloudSyncError.sourceTooLarge(entry.name))
            }
            pairs.append(Pair(name: entry.name, data: entry.data, cloudPath: cloudRoot + "/" + entry.name))
        }
        for name in requestedDownloads where !staging.contains(where: { $0.name == name }) {
            guard ICloudSyncPaths.isValidSourceName(name) else { return fail(ICloudSyncError.forbiddenSourceName(name)) }
            pairs.append(Pair(name: name, data: nil, cloudPath: cloudRoot + "/" + name))
        }
        // Additional source paths must name staged entries; anything else is
        // reported as skipped — the pass never invents files.
        for additional in settings.additionalSourcePaths {
            if !staging.contains(where: { $0.name == additional }) && !requestedDownloads.contains(additional) {
                skipped.append(additional)
            }
        }
        guard !pairs.isEmpty else {
            return fail(ICloudSyncError.noSourcesConfigured)
        }

        // --- load baseline ---------------------------------------------------

        // A baseline belongs to one remote directory. Switching folders must
        // never reuse another directory's hash to choose an automatic upload.
        let scope = Self.digest(Data((sourceScope.isEmpty ? cloudRoot : cloudRoot + "\n" + sourceScope).utf8))
        let statePath = localDirectory + "/directories/" + scope + "/" + Self.stateFileName
        let state: [String: String]
        do {
            var saved = try Self.loadState(from: statePath, filesystem: localFilesystem)
            // Resolve the previous batch for every file, including sources currently
            // excluded by settings, before recording another batch for this folder.
            for (name, marker) in saved where marker.hasPrefix("pending:") {
                let parts = marker.split(separator: ":", omittingEmptySubsequences: false)
                if (parts.count == 4 || (parts.count == 5 && parts[1] == "creating")),
                   let batch = UUID(uuidString: String(parts.last!)) {
                    // Older writers could embed a creating:<digest> upload intent here.
                    // Only an adopted batch establishes the incoming common ancestor.
                    let previous = parts.count == 5 ? "unconfirmed" : String(parts[1])
                    let baseline = adoptedBatches[scope] == batch ? String(parts[parts.count - 2]) : previous
                    saved[name] = baseline.isEmpty ? nil : baseline
                } else if parts.count == 3 {
                    // Older manifests have no adoption receipt. Equal copies establish
                    // a baseline naturally. Divergent copies require explicit review.
                    saved[name] = "unconfirmed"
                } else {
                    throw ICloudSyncError.filesystem("invalid pending baseline")
                }
            }
            state = saved
        } catch {
            return fail(ICloudSyncError.filesystem("baseline state unreadable: \(error)"))
        }

        // --- preflight the whole graph before touching anything --------------

        struct Plan {
            var uploads: [Action] = []
            var downloads: [Action] = []
            var baselines: [String: String] = [:]
        }
        struct Action {
            var name: String
            var data: Data
            var message: String
        }

        var plan = Plan()
        var sourceFiles: [String: Data] = [:]
        var conflictNames: [String] = []

        var remoteCopies: [String: Data] = [:]
        for pair in pairs {
            do { try Self.checkPath(root: cloudRoot, path: pair.cloudPath, filesystem: cloudFilesystem) }
            catch { return fail(error) }
            do {
                if let remote = try cloudFilesystem.contents(atPath: pair.cloudPath) { remoteCopies[pair.name] = remote }
            } catch {
                if isICloudSourceDownloading(error) {
                    downloading.insert(pair.name)
                    if pair.name.lowercased().hasSuffix(".conf") { deferredConf = true }
                    continue
                }
                return fail(ICloudSyncError.filesystem("cannot read cloud copy of \(pair.name): \(error)"))
            }
        }
        for pair in pairs {
            if downloading.contains(pair.name) || (deferredConf && pair.name.lowercased().hasSuffix(".conf")) { continue }
            let remote = remoteCopies[pair.name]

            if let remote {
                guard remote.count <= ICloudSyncLimits.maxSourceBytes else { return fail(ICloudSyncError.sourceTooLarge(pair.name)) }
                do { try validateDownload(pair.name, remote) }
                catch { return fail(ICloudSyncError.filesystem("invalid cloud source \(pair.name); current data kept")) }
            }
            if let data = pair.data ?? remote { sourceFiles[pair.name] = data }
            guard let localData = pair.data else {
                if state[pair.name] != nil { return fail(ICloudSyncError.deletedLocally(pair.name)) }
                if let remote {
                    plan.downloads.append(Action(name: pair.name, data: remote, message: "downloaded from iCloud: \(pair.name)"))
                    plan.baselines[pair.name] = Self.digest(remote)
                }
                continue
            }
            let localDigest = Self.digest(localData)

            guard let remote else {
                // Retry an interrupted first upload only with its original payload.
                if let baseline = state[pair.name], baseline != "creating:\(localDigest)" {
                    // Known baseline + vanished cloud copy: a deletion we do
                    // NOT silently restore (docs/icloud.md) — stop and let
                    // the user resolve.
                    return fail(ICloudSyncError.deletedInCloud(pair.name))
                }
                // One-sided first use → upload.
                plan.uploads.append(Action(name: pair.name, data: localData, message: "uploaded: \(pair.name)"))
                plan.baselines[pair.name] = localDigest
                continue
            }

            let remoteDigest = Self.digest(remote)
            if localDigest == remoteDigest {
                // Identical → establish/refresh the shared baseline.
                plan.baselines[pair.name] = localDigest
                continue
            }
            if let previous = state[pair.name], previous == localDigest {
                // Cloud moved ahead of our baseline → download.
                plan.downloads.append(
                    Action(name: pair.name, data: remote, message: "updated from iCloud: \(pair.name)"),
                )
                plan.baselines[pair.name] = remoteDigest
                continue
            }
            if let previous = state[pair.name], previous == remoteDigest {
                // Local moved ahead of our baseline → upload.
                plan.uploads.append(Action(name: pair.name, data: localData, message: "uploaded update: \(pair.name)"))
                plan.baselines[pair.name] = localDigest
                continue
            }
            // No common baseline and both sides differ → STOP. Whole pass.
            conflictNames.append(pair.name)
        }

        if deferredConf {
            let isConf: (String) -> Bool = { $0.lowercased().hasSuffix(".conf") }
            plan.uploads.removeAll { isConf($0.name) }
            plan.downloads.removeAll { isConf($0.name) }
            plan.baselines = plan.baselines.filter { !isConf($0.key) }
            conflictNames.removeAll(where: isConf)
            skipped.append(contentsOf: pairs.map(\.name).filter(isConf))
            discoveredMain = nil
        }
        skipped = Array(Set(skipped).union(downloading)).sorted()
        let conflicts = Dictionary(uniqueKeysWithValues: pairs.compactMap { pair -> (String, Conflict)? in
            guard conflictNames.contains(pair.name), let local = pair.data, let remote = remoteCopies[pair.name] else { return nil }
            return (pair.name, Conflict(local: Self.digest(local), remote: Self.digest(remote)))
        })
        if let conflictChoice, !conflicts.isEmpty, reviewedScope == scope, reviewedConflicts == conflicts {
            for pair in pairs where conflicts[pair.name] != nil {
                guard let data = conflictChoice == .useCloud ? remoteCopies[pair.name] : pair.data else { continue }
                if conflictChoice == .useCloud {
                    plan.downloads.append(Action(name: pair.name, data: data, message: "used iCloud copy: " + pair.name))
                } else {
                    plan.uploads.append(Action(name: pair.name, data: data, message: "used device copy: " + pair.name))
                }
                plan.baselines[pair.name] = Self.digest(data)
            }
        } else if !conflictNames.isEmpty {
            reviewedScope = scope
            reviewedConflicts = conflicts
            let status = ICloudSyncStatus.conflict(clock.now(), conflictNames)
            lastStatus = status
            return ICloudSyncOutcome(
                status: status,
                updates: [],
                downloads: [],
                skipped: skipped,
            )
        } else if conflictChoice != nil {
            return fail(ICloudSyncError.filesystem("The files changed. Sync again to review their current versions."))
        }
        reviewedConflicts = [:]
        reviewedScope = nil

        for action in plan.downloads { sourceFiles[action.name] = action.data }
        if !deferredConf {
            do { try ICloudSyncPaths.validateConfGraph(main: effectiveMain, files: sourceFiles) }
            catch { return fail(error) }
        }

        guard await sourcesAreCurrent() else {
            return fail(ICloudSyncError.filesystem("local sources changed during sync; no copies or baselines were written"))
        }

        // --- execute: local staging, cloud copies, baseline per action -------

        var updates: [String] = []
        let incoming = plan.downloads.map { ICloudSyncOutcome.Download(name: $0.name, data: $0.data) }
        var currentState = state
        guard settings.enabled else { return fail(ICloudSyncError.filesystem("sync was disabled during preflight")) }

        for action in plan.uploads {
            do {
                try Self.checkPath(root: cloudRoot, path: cloudRoot + "/" + action.name, filesystem: cloudFilesystem)
                try cloudFilesystem.ensureDirectory(atPath: cloudRoot)
                // Invalidate the old common ancestor before changing the other filesystem.
                // A failed or interrupted replacement can leave either version in the cloud.
                let digest = Self.digest(action.data)
                currentState[action.name] = remoteCopies[action.name] == nil ? "creating:\(digest)" : "unconfirmed"
                try Self.saveState(currentState, to: statePath, filesystem: localFilesystem)
                try cloudFilesystem.replace(action.data, atPath: cloudRoot + "/" + action.name, matching: remoteCopies[action.name])
            } catch {
                return fail(error, completed: updates, downloads: [], skipped: skipped)
            }
            currentState[action.name] = Self.digest(action.data)
            do {
                try Self.saveState(currentState, to: statePath, filesystem: localFilesystem)
            } catch {
                return fail(ICloudSyncError.filesystem("cannot persist baseline: \(error)"), completed: updates, downloads: [], skipped: skipped)
            }
            updates.append(action.message)
        }

        // Identical pairs refresh the baseline too.
        for (name, digestValue) in plan.baselines where currentState[name] == nil || currentState[name] != digestValue {
            currentState[name] = digestValue
        }
        // Adoption commits the receipt and the downloaded sources in one manifest write.
        let receipt = ICloudAdoptionReceipt(scope: scope, batch: UUID())
        for item in incoming {
            let previous = state[item.name] ?? ""
            // An unfinished first upload provides no common ancestor for a download.
            let ancestor = previous.hasPrefix("creating:") ? "unconfirmed" : previous
            currentState[item.name] = "pending:\(ancestor):\(Self.digest(item.data)):\(receipt.batch.uuidString)"
        }
        do {
            try Self.saveState(currentState, to: statePath, filesystem: localFilesystem)
        } catch {
            return fail(ICloudSyncError.filesystem("cannot persist baseline: \(error)"), completed: updates, downloads: [], skipped: skipped)
        }

        let now = clock.now()
        let status: ICloudSyncStatus
        do {
            var waiting: [String] = []
            if !downloading.isEmpty {
                waiting.append("Waiting for iCloud downloads: " + downloading.sorted().joined(separator: ", ") + ".")
            }
            // Identical copies can still be waiting to upload from an earlier pass.
            let uploading = try plan.baselines.keys.sorted().filter { name in
                try !cloudFilesystem.uploadConfirmed(atPath: cloudRoot + "/" + name)
            }
            if !uploading.isEmpty {
                waiting.append("Waiting for iCloud upload confirmation: " + uploading.joined(separator: ", ") + ".")
            }
            if !waiting.isEmpty {
                status = .pending(now, waiting.joined(separator: " ") + " Check again when iCloud is online.")
            } else if plan.baselines.isEmpty {
                status = .failed(now, "No syncable sources found in this profile or folder.")
            } else {
                let completed = updates + plan.downloads.map(\.message)
                status = completed.isEmpty ? .upToDate(now) : .synced(now, completed)
            }
        } catch {
            return fail(ICloudSyncError.filesystem("Could not confirm iCloud upload. \(error.localizedDescription)"), completed: updates, downloads: [], skipped: skipped)
        }
        if !incoming.isEmpty || discoveredMain != nil {
            do {
                try await adoptDownloads(incoming, discoveredMain, receipt)
                updates += plan.downloads.map(\.message)
            } catch {
                return fail(error, completed: updates, downloads: [], skipped: skipped)
            }
        }
        lastStatus = status
        return ICloudSyncOutcome(status: status, updates: updates, downloads: incoming, skipped: skipped, discoveredMainConf: discoveredMain)
    }

    // MARK: internals

    private struct Pair {
        var name: String
        var data: Data?
        var cloudPath: String
    }

    private func fail(_ error: Error) -> ICloudSyncOutcome {
        fail(error, completed: [], downloads: [], skipped: [])
    }

    private func fail(
        _ error: Error,
        completed: [String],
        downloads: [ICloudSyncOutcome.Download],
        skipped: [String],
    ) -> ICloudSyncOutcome {
        let status = ICloudSyncStatus.failed(clock.now(), Self.describe(error))
        lastStatus = status
        return ICloudSyncOutcome(status: status, updates: completed, downloads: downloads, skipped: skipped)
    }

    public static func describe(_ error: Error) -> String {
        switch error as? ICloudSyncError {
        case .forbiddenSourceName(let name):
            return "\"\(name)\" is forbidden for sync (source paths only; never generated state or keys)"
        case .confTooLarge: return "conf source exceeds the 16 MiB sync limit"
        case .confLacksSections: return "automatic conf source lacks [General] or [Rule]"
        case .unsafeInclude(let include): return "unsafe relative conf include: \(include)"
        case .noSourcesConfigured: return "no local source files configured for sync"
        case .containerUnavailable: return "iCloud container unavailable"
        case .sourceTooLarge(let name): return "\(name) exceeds the 32 MiB sync limit"
        case .pathEscapesRoot(let path): return "source path escapes its root: \(path)"
        case .symlinkRefused(let path): return "refusing a symlink in source path: \(path)"
        case .deletedLocally(let name): return "\(name) was deleted locally; no automatic overwrite, resolve manually"
        case .deletedInCloud(let name): return "\(name) was deleted in iCloud; no automatic overwrite, resolve manually"
        case .conflict(let name): return "\(name) changed on both sides; neither copy was overwritten, merge manually"
        case .sourceDownloading(let path): return "iCloud source is awaiting download: \(path)"
        case .filesystem(let message): return message
        case nil: return error.localizedDescription
        }
    }

    // MARK: baseline state

    static func loadState(from path: String, filesystem: ICloudFilesystem) throws -> [String: String] {
        guard let data = try filesystem.contents(atPath: path) else { return [:] }
        return try JSONDecoder().decode([String: String].self, from: data)
    }

    static func saveState(_ state: [String: String], to path: String, filesystem: ICloudFilesystem) throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        try filesystem.writeAtomic(try encoder.encode(state), toPath: path)
    }

    // MARK: primitives

    static func digest(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    /// Port of internal/icloud.checkPath: every component from the root down
    /// must exist as a directory (or nothing yet), contain no symlink, and
    /// end in a regular file. Traversal cannot escape the root because the
    /// name was already validated, but the walk still refuses links.
    static func checkPath(root: String, path: String, filesystem: ICloudFilesystem) throws {
        guard path == root || path.hasPrefix(root + "/") else {
            throw ICloudSyncError.pathEscapesRoot(path)
        }
        let relative = path == root ? "" : String(path.dropFirst(root.count + 1))
        var current = root
        if !relative.isEmpty {
            for component in relative.split(separator: "/", omittingEmptySubsequences: false) {
                current += "/" + component
                if try filesystem.isSymbolicLink(atPath: current) {
                    throw ICloudSyncError.symlinkRefused(current)
                }
            }
        }
    }

}
