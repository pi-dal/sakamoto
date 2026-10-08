import Foundation

// Filesystem seams for the iCloud sync store. Everything the sync pass does
// goes through these protocols, so unit tests run entirely in memory and the
// real iCloud integration is one thin adapter away — the pass logic itself
// never knows which one it is talking to.

// MARK: - Clock

public protocol ICloudSyncClock: Sendable {
    func now() -> Date
}

public struct ICloudSystemClock: ICloudSyncClock {
    public init() {}
    public func now() -> Date { Date() }
}

// MARK: - Filesystem

/// A minimal sync-oriented filesystem: read, atomic write, existence,
/// symlink refusal, directory creation. Paths are absolute strings resolved
/// by the caller (local support directory or cloud container root).
public protocol ICloudFilesystem: Sendable {
    /// File contents, or nil when the file does not exist. Throws on real
    /// I/O errors (never invents a replacement for an unreadable file —
    /// docs/icloud.md).
    func contents(atPath path: String) throws -> Data?

    func fileExists(atPath path: String) -> Bool

    /// Refusing symlinks is part of the contract (checkPath semantics): a
    /// source or cloud dependency must never be allowed to point outside
    /// its declared directory.
    func isSymbolicLink(atPath path: String) throws -> Bool

    /// Creates intermediate directories with private permissions (0700 —
    /// the port of os.MkdirAll(dir, 0700)).
    func ensureDirectory(atPath path: String) throws

    /// Atomic write: temp file + rename, private file mode (0600 — the port
    /// of icloud.atomic).
    func writeAtomic(_ data: Data, toPath path: String) throws
    /// Bounded source enumeration for first-device conf discovery.
    func sourceNames(under root: String) throws -> [String]
}

public extension ICloudFilesystem {
    func sourceNames(under root: String) throws -> [String] { [] }
}

/// In-memory implementation for tests and previews.
public final class InMemoryFilesystem: ICloudFilesystem, @unchecked Sendable {
    private let lock = NSLock()
    private var files: [String: Data] = [:]
    private var directories: Set<String> = []
    private var symlinks: [String: String] = [:]

    public init() {}

    public func contents(atPath path: String) throws -> Data? {
        lock.lock()
        defer { lock.unlock() }
        if let target = symlinks[path] {
            // Reading THROUGH a symlink is refused like the real adapter:
            // the store checks isSymbolicLink first; a raw read here keeps
            // tests honest about the refusal contract.
            throw ICloudSyncError.symlinkRefused(path + " -> " + target)
        }
        return files[path]
    }

    public func fileExists(atPath path: String) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return files[path] != nil || directories.contains(path) || symlinks[path] != nil
    }

    public func isSymbolicLink(atPath path: String) throws -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return symlinks[path] != nil
    }

    public func ensureDirectory(atPath path: String) throws {
        lock.lock()
        defer { lock.unlock() }
        var partial = ""
        for component in path.split(separator: "/", omittingEmptySubsequences: false) {
            partial += String(component)
            if !partial.isEmpty {
                directories.insert(partial)
            }
            partial += "/"
        }
    }

    public func writeAtomic(_ data: Data, toPath path: String) throws {
        lock.lock()
        defer { lock.unlock() }
        if symlinks[path] != nil {
            throw ICloudSyncError.symlinkRefused(path)
        }
        files[path] = data
        directories.insert((path as NSString).deletingLastPathComponent)
    }

    public func sourceNames(under root: String) throws -> [String] {
        lock.lock(); defer { lock.unlock() }
        return files.keys.filter { $0.hasPrefix(root + "/") }.map { String($0.dropFirst(root.count + 1)) }.filter(ICloudSyncPaths.isValidSourceName).sorted()
    }

    // --- Test helpers (not part of the protocol) ---

    public func createSymbolicLink(atPath path: String, toDestination target: String) {
        lock.lock()
        defer { lock.unlock() }
        symlinks[path] = target
    }

    public func removeFile(atPath path: String) {
        lock.lock()
        defer { lock.unlock() }
        files.removeValue(forKey: path)
    }

    public func allFiles() -> [String: Data] {
        lock.lock()
        defer { lock.unlock() }
        return files
    }
}

/// Inspect entries without following dangling links. Missing entries are
/// allowed during preflight; other I/O failures must still stop the sync.
private func isFilesystemSymbolicLink(atPath path: String, fileManager: FileManager) throws -> Bool {
    do {
        let attributes = try fileManager.attributesOfItem(atPath: path)
        return attributes[.type] as? FileAttributeType == .typeSymbolicLink
    } catch let error as NSError {
        if error.domain == NSCocoaErrorDomain,
           error.code == NSFileNoSuchFileError || error.code == NSFileReadNoSuchFileError {
            return false
        }
        throw error
    }
}

private func filesystemSourceNames(under root: String, fileManager: FileManager) throws -> [String] {
    guard fileManager.fileExists(atPath: root) else { return [] }
    guard try !isFilesystemSymbolicLink(atPath: root, fileManager: fileManager) else { throw ICloudSyncError.symlinkRefused(root) }
    var result: [String] = []
    var visited = 0
    func visit(_ relative: String, depth: Int) throws {
        guard depth <= ICloudSyncLimits.maxIncludeDepth else { throw ICloudSyncError.filesystem("source folder exceeds discovery depth") }
        let directory = relative.isEmpty ? root : root + "/" + relative
        for name in try fileManager.contentsOfDirectory(atPath: directory) {
            visited += 1
            guard visited <= 1024 else { throw ICloudSyncError.filesystem("source folder exceeds discovery limit") }
            let source = relative.isEmpty ? name : relative + "/" + name
            guard ICloudSyncPaths.isValidSourceName(source) else { continue }
            let path = root + "/" + source
            guard try !isFilesystemSymbolicLink(atPath: path, fileManager: fileManager) else { throw ICloudSyncError.symlinkRefused(path) }
            var isDirectory: ObjCBool = false
            guard fileManager.fileExists(atPath: path, isDirectory: &isDirectory) else { continue }
            if isDirectory.boolValue { try visit(source, depth: depth + 1) }
            else if source.lowercased().hasSuffix(".conf") {
                result.append(source)
                guard result.count <= ICloudSyncLimits.maxIncludeFiles else { throw ICloudSyncError.filesystem("too many conf sources") }
            }
        }
    }
    try visit("", depth: 0)
    return result.sorted()
}

// MARK: - Local adapter (real files, no iCloud)

/// Real-files implementation used for the LOCAL side (app-support staging of
/// the baseline state). Cloud files get the coordinator-based adapter below.
public final class LocalFilesystem: ICloudFilesystem, @unchecked Sendable {
    private let fileManager: FileManager

    public init(fileManager: FileManager = .default) {
        self.fileManager = fileManager
    }

    public func sourceNames(under root: String) throws -> [String] {
        try filesystemSourceNames(under: root, fileManager: fileManager)
    }

    public func contents(atPath path: String) throws -> Data? {
        // exists-then-read (Data(contentsOf:) throws) so a real I/O failure
        // is reported instead of faking "missing".
        guard fileManager.fileExists(atPath: path) else { return nil }
        return try Data(contentsOf: URL(fileURLWithPath: path))
    }

    public func fileExists(atPath path: String) -> Bool {
        fileManager.fileExists(atPath: path)
    }

    public func isSymbolicLink(atPath path: String) throws -> Bool {
        try isFilesystemSymbolicLink(atPath: path, fileManager: fileManager)
    }

    public func ensureDirectory(atPath path: String) throws {
        try fileManager.createDirectory(
            atPath: path,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700],
        )
    }

    public func writeAtomic(_ data: Data, toPath path: String) throws {
        try ensureDirectory(atPath: (path as NSString).deletingLastPathComponent)
        let tmp = path + ".tmp-\(UUID().uuidString)"
        try data.write(to: URL(fileURLWithPath: tmp), options: .atomic)
        // Private file mode, mirroring os.CreateTemp + rename in icloud.atomic.
        try fileManager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: tmp)
        if fileManager.fileExists(atPath: path) {
            _ = try fileManager.replaceItemAt(
                URL(fileURLWithPath: path),
                withItemAt: URL(fileURLWithPath: tmp),
            )
        } else {
            try fileManager.moveItem(atPath: tmp, toPath: path)
        }
    }
}

// MARK: - iCloud container

/// The one real iCloud touchpoint: resolving the ubiquity container. Returns
/// nil when no iCloud account is signed in or the container is not
/// entitled — the store renders that as .unavailable without touching local
/// functionality.
public protocol ICloudContainer: Sendable {
    func containerURL() -> URL?
}

public struct UbiquityICloudContainer: ICloudContainer {
    /// Placeholder container id (mirrors ios/README.md signing notes):
    /// `iCloud.com.pidal.sakamoto`. Replace together with the entitlements
    /// and a real Team.
    public static let placeholderContainerID = "iCloud.com.pidal.sakamoto"

    private let containerID: String?

    public init(containerID: String? = UbiquityICloudContainer.placeholderContainerID) {
        self.containerID = containerID
    }

    public func containerURL() -> URL? {
        FileManager.default.url(forUbiquityContainerIdentifier: containerID)
    }
}

/// Coordinator-based filesystem for files inside the iCloud container.
/// NSFileCoordinator coordinates with other processes/devices that present
/// the same files (the system's file presenter machinery), which is required
/// etiquette for ubiquitous files; writes stay atomic with private modes.
public final class CloudFilesystem: ICloudFilesystem, @unchecked Sendable {
    private let fileManager: FileManager
    private let coordinator: NSFileCoordinator

    public init(fileManager: FileManager = .default, coordinator: NSFileCoordinator = NSFileCoordinator()) {
        self.fileManager = fileManager
        self.coordinator = coordinator
    }

    public func sourceNames(under root: String) throws -> [String] {
        try filesystemSourceNames(under: root, fileManager: fileManager)
    }

    public func contents(atPath path: String) throws -> Data? {
        guard fileManager.fileExists(atPath: path) else { return nil }
        var readError: NSError?
        var data: Data?
        var thrown: Error?
        coordinator.coordinate(
            readingItemAt: URL(fileURLWithPath: path),
            options: .withoutChanges,
            error: &readError,
        ) { url in
            do {
                data = try Data(contentsOf: url)
            } catch {
                thrown = error
            }
        }
        if let thrown { throw thrown }
        if let readError { throw ICloudSyncError.filesystem(readError.localizedDescription) }
        return data
    }

    public func fileExists(atPath path: String) -> Bool {
        fileManager.fileExists(atPath: path)
    }

    public func isSymbolicLink(atPath path: String) throws -> Bool {
        try isFilesystemSymbolicLink(atPath: path, fileManager: fileManager)
    }

    public func ensureDirectory(atPath path: String) throws {
        try fileManager.createDirectory(
            atPath: path,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700],
        )
    }

    public func writeAtomic(_ data: Data, toPath path: String) throws {
        try ensureDirectory(atPath: (path as NSString).deletingLastPathComponent)
        var writeError: NSError?
        var thrown: Error?
        let url = URL(fileURLWithPath: path)
        coordinator.coordinate(
            writingItemAt: url,
            options: .forReplacing,
            error: &writeError,
        ) { target in
            do {
                // Data.write(options: .atomic) = temp + rename; then force
                // the private mode the sync contract promises (0600).
                try data.write(to: target, options: .atomic)
                try self.fileManager.setAttributes(
                    [.posixPermissions: 0o600],
                    ofItemAtPath: target.path,
                )
            } catch {
                thrown = error
            }
        }
        if let thrown { throw thrown }
        if let writeError { throw ICloudSyncError.filesystem(writeError.localizedDescription) }
    }
}
