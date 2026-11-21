import CryptoKit
import Foundation
import Darwin

/// System VPN preferences hold an ID and digest. The payload is device-local.
enum ProviderProfileStore {
    private static func file(for id: String, revision: String? = nil) throws -> URL {
        guard UUID(uuidString: id) != nil,
              let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: SystemSurfaceStore.groupIdentifier) else {
            throw TunnelStartOptions.ResolutionError.missingConfiguration
        }
        var folder = container.appendingPathComponent("AppliedVPN", isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try folder.setResourceValues(values)
        if let revision {
            guard revision.utf8.count == 64, revision.utf8.allSatisfy({ (48...57).contains($0) || (97...102).contains($0) }) else { throw TunnelStartOptions.ResolutionError.needsApply }
            return folder.appendingPathComponent(id + "-" + revision + ".json")
        }
        return folder.appendingPathComponent(id + ".json")
    }

    private static func digest(_ data: Data) -> String {
        SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
    }

    // Serialize staging and pruning across the app and the packet extension.
    private struct Revisions: Codable {
        var staged: String?
        var confirmed: String?
        var rollback: String?
        var transaction: UUID?
        var beforeTransaction: String?
        var finalizedTransaction: UUID?

        func manifestCommitted(for id: String) throws -> Bool {
            guard let transaction else { return true }
            if finalizedTransaction == transaction { return true }
            let container = try file(for: id).deletingLastPathComponent().deletingLastPathComponent()
            let manifest = container.appendingPathComponent("Profiles/\(id)/profile.json")
            let witness: TunnelProfile
            do { witness = try JSONDecoder().decode(TunnelProfile.self, from: Data(contentsOf: manifest)) }
            catch { throw TunnelStartOptions.ResolutionError.unverifiedInstalledProfile }
            guard witness.id == id else { throw TunnelStartOptions.ResolutionError.unverifiedInstalledProfile }
            return witness.appliedTransaction == transaction
        }

        func committedRevision(for id: String) throws -> String? {
            try manifestCommitted(for: id) ? confirmed : beforeTransaction
        }
    }

    private static func update(_ id: String, finalizing transaction: UUID? = nil, afterWrite: () throws -> Void = {}, _ body: (URL, inout Revisions) throws -> Void) throws {
        let legacy = try file(for: id)
        let base = legacy.deletingPathExtension()
        let fd = open(base.appendingPathExtension("lock").path, O_CREAT | O_RDWR, 0o600)
        guard fd >= 0 else { throw CocoaError(.fileWriteUnknown) }
        defer { close(fd) }
        guard flock(fd, LOCK_EX) == 0 else { throw CocoaError(.fileWriteUnknown) }
        let index = base.appendingPathExtension("revisions")
        let original = FileManager.default.fileExists(atPath: index.path) ? try Data(contentsOf: index) : nil
        var revisions = try original.map { try JSONDecoder().decode(Revisions.self, from: $0) } ?? Revisions()
        try body(legacy, &revisions)
        let protection: Data.WritingOptions = [.atomic, .completeFileProtectionUntilFirstUserAuthentication]
        try JSONEncoder().encode(revisions).write(to: index, options: protection)
        do { try afterWrite() }
        catch {
            if let original { try original.write(to: index, options: protection) }
            else { try FileManager.default.removeItem(at: index) }
            throw error
        }
        if let transaction, revisions.transaction == transaction, (try? revisions.manifestCommitted(for: id)) == true {
            revisions.finalizedTransaction = transaction
            // The manifest already committed. This redundant witness lets it be repaired later.
            try? JSONEncoder().encode(revisions).write(to: index, options: protection)
        }
    }

    static func save(_ input: TunnelStartOptions) throws -> [String: Any] {
        var options = input
        options.configContent = try TailscaleConfigInjection.withoutAuthKey(input.configContent)
        guard let id = options.profileID else { throw TunnelStartOptions.ResolutionError.needsApply }
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        let data = try encoder.encode(options)
        let revision = digest(data)
        try update(id) { _, revisions in
            try data.write(to: file(for: id, revision: revision), options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            revisions.staged = revision
        }
        return ["profileID": id, "payloadDigest": revision]
    }

    static func confirm(_ input: TunnelStartOptions, reference: [String: Any], previous: [String: Any]?, transaction: UUID?, afterWrite: () throws -> Void = {}) throws -> Bool {
        guard let id = input.profileID, let revision = reference["payloadDigest"] as? String,
              reference["profileID"] as? String == id else { return false }
        var accepted = false
        try update(id, finalizing: transaction, afterWrite: { if accepted { try afterWrite() } }) { legacy, revisions in
            guard revisions.staged == revision, try load(reference) == input else { return }
            accepted = true
            let previousConfirmed = try revisions.committedRevision(for: id)
            revisions.beforeTransaction = transaction == nil ? nil : previousConfirmed
            revisions.transaction = transaction
            if reference["controllerTransaction"] as? Bool == true {
                guard let transaction, reference["applyTransaction"] as? String == transaction.uuidString else { throw TunnelStartOptions.ResolutionError.needsApply }
                revisions.finalizedTransaction = transaction
            }
            if revisions.confirmed != revision {
                revisions.rollback = revisions.confirmed
                if previous?["profileID"] as? String == id, let prior = previous?["payloadDigest"] as? String, prior != revision {
                    revisions.rollback = prior
                }
                revisions.confirmed = revision
            }
            let folder = legacy.deletingLastPathComponent()
            let keep = Set([revision, revisions.rollback, previousConfirmed].compactMap { $0 })
            for url in try FileManager.default.contentsOfDirectory(at: folder, includingPropertiesForKeys: nil) {
                let name = url.deletingPathExtension().lastPathComponent
                guard url.pathExtension == "json", name.hasPrefix(id + "-") else { continue }
                let hash = String(name.dropFirst(id.count + 1))
                if hash.count == 64 && !keep.contains(hash) { try FileManager.default.removeItem(at: url) }
            }
            if FileManager.default.fileExists(atPath: legacy.path), !keep.contains(digest(try Data(contentsOf: legacy))) {
                try FileManager.default.removeItem(at: legacy)
            }
        }
        return accepted
    }

    private static func revisions(for id: String) throws -> Revisions? {
        let index = try file(for: id).deletingPathExtension().appendingPathExtension("revisions")
        let data: Data
        do { data = try Data(contentsOf: index) }
        catch let error as CocoaError where error.code == .fileReadNoSuchFile { return nil }
        return try JSONDecoder().decode(Revisions.self, from: data)
    }

    static func finalized(_ transaction: UUID, for id: String) throws -> Bool {
        try revisions(for: id)?.finalizedTransaction == transaction
    }

    static func interruptedApply(for id: String) throws -> Bool {
        guard let revisions = try revisions(for: id), revisions.transaction != nil else { return false }
        return try !revisions.manifestCommitted(for: id)
    }

    // Persist the predecessor alongside the candidate in the same NE preference write.
    // Its profile ID can differ from the candidate's, even on the very first Apply of B.
    static func provisionalReference(_ candidate: [String: Any], transaction: UUID, previous: [String: Any]?) throws -> [String: Any] {
        var result = candidate
        result["applyTransaction"] = transaction.uuidString
        // An unidentified legacy VPN is migrated to an empty, on-demand-disabled protocol.
        if let previous, !previous.isEmpty {
            guard let id = previous["profileID"] as? String, let digest = previous["payloadDigest"] as? String else {
                throw TunnelStartOptions.ResolutionError.needsApply
            }
            let predecessor = ["profileID": id, "payloadDigest": digest]
            _ = try load(predecessor)
            result["previousProfile"] = predecessor
        }
        return result
    }

    static func rollbackReference(for reference: [String: Any]) throws -> [String: Any]? {
        if let raw = reference["applyTransaction"] {
            guard let value = raw as? String, let transaction = UUID(uuidString: value), let id = reference["profileID"] as? String else { throw TunnelStartOptions.ResolutionError.needsApply }
            var witness = try revisions(for: id) ?? Revisions()
            witness.transaction = transaction
            let resolved: [String: Any]
            let committed = reference["controllerTransaction"] as? Bool == true
                ? witness.finalizedTransaction == transaction && witness.confirmed == reference["payloadDigest"] as? String
                : try witness.manifestCommitted(for: id)
            if committed {
                guard let digest = reference["payloadDigest"] as? String else { throw TunnelStartOptions.ResolutionError.needsApply }
                resolved = ["profileID": id, "payloadDigest": digest]
            } else {
                guard let previous = reference["previousProfile"] as? [String: String] else { return nil }
                resolved = previous
            }
            _ = try load(resolved)
            return resolved
        }
        guard let id = reference["profileID"] as? String, let revisions = try revisions(for: id) else { return reference }
        if let digest = try revisions.committedRevision(for: id) {
            let committed: [String: Any] = ["profileID": id, "payloadDigest": digest]
            _ = try load(committed)
            return committed
        }
        return revisions.transaction == nil ? reference : nil
    }

    static func confirmed(for id: String) throws -> TunnelStartOptions? {
        guard let revisions = try revisions(for: id) else { return nil }
        guard let revision = try revisions.committedRevision(for: id) else {
            if revisions.transaction != nil { throw TunnelStartOptions.ResolutionError.needsApply }
            return nil
        }
        return try load(["profileID": id, "payloadDigest": revision])
    }

    static func load(_ ref: [String: Any]) throws -> TunnelStartOptions {
        guard let id = ref["profileID"] as? String, let revision = ref["payloadDigest"] as? String else { throw TunnelStartOptions.ResolutionError.needsApply }
        let versioned = try file(for: id, revision: revision)
        // Existing installations used a single file. Its saved digest still has to match.
        let path = FileManager.default.fileExists(atPath: versioned.path) ? versioned : try file(for: id)
        #if DEBUG
        TunnelStartOptions.observePayloadRead?()
        #endif
        let data = try Data(contentsOf: path)
        guard digest(data) == revision else { throw TunnelStartOptions.ResolutionError.needsApply }
        let options = try JSONDecoder().decode(TunnelStartOptions.self, from: data)
        guard options.profileID == id else { throw TunnelStartOptions.ResolutionError.needsApply }
        return options
    }
}
