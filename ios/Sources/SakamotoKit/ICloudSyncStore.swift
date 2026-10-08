import CryptoKit
import Foundation

// The iCloud sync pass for the iOS app — a faithful port of
// internal/icloud/sync.go's conflict-safe algorithm onto the app's staged
// state, with the filesystem and clock injected so tests run entirely in
// memory (no iCloud account is ever touched by the unit tests).
//
// Model (mirrors the macOS daemon):
//   local side  — `localDirectory/sources/<name>` files this store owns
//                 (the app stages values in, adopts downloads out).
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

public actor ICloudSyncStore {

    public static let stateFileName = "icloud-state.json"
    public static let sourcesDirectoryName = "sources"
    private let localFilesystem: ICloudFilesystem
    private let cloudFilesystem: ICloudFilesystem
    private let container: ICloudContainer
    private let clock: ICloudSyncClock
    private let localDirectory: String

    private var settings: ICloudSyncSettings
    private var lastStatus: ICloudSyncStatus?

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

    /// Test/inspection seam: read back a locally staged source file (the
    /// download side lands here before the app adopts it).
    public func stagedLocalContent(name: String) -> Data? {
        let path = localDirectory + "/" + Self.sourcesDirectoryName + "/" + name
        return try? localFilesystem.contents(atPath: path)
    }

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
        validateDownload: @Sendable (String, Data) throws -> Void = { _, _ in }
    ) -> ICloudSyncOutcome {
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
        let directoryName = settings.directoryName.isEmpty ? "sakamoto" : settings.directoryName
        let cloudRoot = directoryURL?.path ?? containerURL
            .appendingPathComponent("Documents", isDirectory: true)
            .appendingPathComponent(directoryName, isDirectory: true).path

        // --- validate + resolve names ---------------------------------------

        var skipped: [String] = []
        var pairs: [Pair] = []
        for entry in staging {
            guard ICloudSyncPaths.isValidSourceName(entry.name) else {
                return fail(ICloudSyncError.forbiddenSourceName(entry.name))
            }
            guard entry.data.count <= ICloudSyncLimits.maxSourceBytes else {
                return fail(ICloudSyncError.sourceTooLarge(entry.name))
            }
            pairs.append(Pair(name: entry.name, data: entry.data, cloudPath: cloudRoot + "/" + entry.name))
        }
        for name in downloadNames where !staging.contains(where: { $0.name == name }) {
            guard ICloudSyncPaths.isValidSourceName(name) else { return fail(ICloudSyncError.forbiddenSourceName(name)) }
            pairs.append(Pair(name: name, data: nil, cloudPath: cloudRoot + "/" + name))
        }
        // Additional source paths must name staged entries; anything else is
        // reported as skipped — the pass never invents files.
        for additional in settings.additionalSourcePaths {
            if !staging.contains(where: { $0.name == additional }) && !downloadNames.contains(additional) {
                skipped.append(additional)
            }
        }
        guard !pairs.isEmpty else {
            return fail(ICloudSyncError.noSourcesConfigured)
        }

        // --- load baseline ---------------------------------------------------

        // A baseline belongs to one remote directory. Switching folders must
        // never reuse another directory's hash to choose an automatic upload.
        let scope = Self.digest(Data(cloudRoot.utf8))
        let statePath = localDirectory + "/directories/" + scope + "/" + Self.stateFileName
        let state: [String: String]
        do {
            state = try Self.loadState(from: statePath, filesystem: localFilesystem)
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
        var conflictNames: [String] = []

        for pair in pairs {
            // Symlink/traversal discipline on the cloud side (checkPath).
            do {
                try Self.checkPath(root: cloudRoot, path: pair.cloudPath, filesystem: cloudFilesystem)
            } catch {
                return fail(error)
            }

            let remote: Data?
            do {
                remote = try cloudFilesystem.contents(atPath: pair.cloudPath)
            } catch {
                return fail(ICloudSyncError.filesystem("cannot read cloud copy of \(pair.name): \(error)"))
            }

            if let remote {
                guard remote.count <= ICloudSyncLimits.maxSourceBytes else { return fail(ICloudSyncError.sourceTooLarge(pair.name)) }
                do { try validateDownload(pair.name, remote) }
                catch { return fail(ICloudSyncError.filesystem("invalid cloud source \(pair.name); current data kept")) }
            }
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
                if state[pair.name] != nil {
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

        if !conflictNames.isEmpty {
            let status = ICloudSyncStatus.conflict(clock.now(), conflictNames)
            lastStatus = status
            return ICloudSyncOutcome(
                status: status,
                updates: [],
                downloads: [],
                skipped: skipped,
            )
        }

        // --- execute: local staging, cloud copies, baseline per action -------

        var updates: [String] = []
        var downloads: [ICloudSyncOutcome.Download] = []
        var currentState = state

        let sourcesDirectory = localDirectory + "/" + Self.sourcesDirectoryName

        for action in plan.downloads {
            do {
                let localPath = sourcesDirectory + "/" + action.name
                try Self.writeLocal(action.data, to: localPath, filesystem: localFilesystem, root: localDirectory)
            } catch {
                return fail(error, completed: updates, downloads: downloads, skipped: skipped)
            }
            currentState[action.name] = Self.digest(action.data)
            do {
                try Self.saveState(currentState, to: statePath, filesystem: localFilesystem)
            } catch {
                return fail(ICloudSyncError.filesystem("cannot persist baseline: \(error)"), completed: updates, downloads: downloads, skipped: skipped)
            }
            downloads.append(.init(name: action.name, data: action.data))
            updates.append(action.message)
        }

        for action in plan.uploads {
            do {
                try Self.checkPath(root: cloudRoot, path: cloudRoot + "/" + action.name, filesystem: cloudFilesystem)
                try cloudFilesystem.ensureDirectory(atPath: cloudRoot)
                try cloudFilesystem.writeAtomic(action.data, toPath: cloudRoot + "/" + action.name)
            } catch {
                return fail(error, completed: updates, downloads: downloads, skipped: skipped)
            }
            currentState[action.name] = Self.digest(action.data)
            do {
                try Self.saveState(currentState, to: statePath, filesystem: localFilesystem)
            } catch {
                return fail(ICloudSyncError.filesystem("cannot persist baseline: \(error)"), completed: updates, downloads: downloads, skipped: skipped)
            }
            updates.append(action.message)
        }

        // Identical pairs refresh the baseline too.
        for (name, digestValue) in plan.baselines where currentState[name] == nil || currentState[name] != digestValue {
            currentState[name] = digestValue
        }
        do {
            try Self.saveState(currentState, to: statePath, filesystem: localFilesystem)
        } catch {
            return fail(ICloudSyncError.filesystem("cannot persist baseline: \(error)"), completed: updates, downloads: downloads, skipped: skipped)
        }

        let now = clock.now()
        let status: ICloudSyncStatus = updates.isEmpty ? .upToDate(now) : .synced(now, updates)
        lastStatus = status
        return ICloudSyncOutcome(status: status, updates: updates, downloads: downloads, skipped: skipped)
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

    private static func writeLocal(
        _ data: Data,
        to path: String,
        filesystem: ICloudFilesystem,
        root: String,
    ) throws {
        try checkPath(root: root, path: path, filesystem: filesystem)
        try filesystem.ensureDirectory(atPath: (path as NSString).deletingLastPathComponent)
        try filesystem.writeAtomic(data, toPath: path)
    }
}
