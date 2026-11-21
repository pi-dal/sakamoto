import Darwin
import Foundation

/// The app owns the exclusive lock until Apply finishes. The kernel releases it on process exit.
public final class ApplyLease {
    private let descriptor: Int32
    private let url: URL
    private let owner: Bool
    public let transaction: UUID

    // Only the device-local profile ID and digest cross the process boundary.
    public struct Rollback: Codable {
        public let reference: [String: String]?
        public init(reference: [String: String]?) { self.reference = reference }
    }
    public private(set) var rollback: Rollback?

    public init(root: URL, transaction: UUID, owner: Bool, rollback: Rollback? = nil) throws {
        self.transaction = transaction
        self.owner = owner
        url = root.appendingPathComponent("Apply-\(transaction.uuidString).lock")
        let flags = owner ? O_CREAT | O_EXCL | O_EXLOCK | O_RDWR : O_RDONLY
        descriptor = open(url.path, flags | O_NOFOLLOW | O_CLOEXEC | O_NONBLOCK, 0o600)
        guard descriptor >= 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        if owner {
            if let rollback {
                let data = try JSONEncoder().encode(rollback)
                let count = data.withUnsafeBytes { pwrite(descriptor, $0.baseAddress, $0.count, 0) }
                guard count == data.count else { throw CocoaError(.fileWriteUnknown) }
            }
            self.rollback = rollback
        } else {
            // Both processes now hold the inode. No persistent file is needed.
            unlink(url.path)
            guard ownerAlive else { throw CancellationError() }
            var bytes = [UInt8](repeating: 0, count: 1024)
            let count = bytes.withUnsafeMutableBytes { pread(descriptor, $0.baseAddress, $0.count, 0) }
            guard count >= 0 && count < bytes.count else { throw CocoaError(.fileReadCorruptFile) }
            if count > 0 { self.rollback = try JSONDecoder().decode(Rollback.self, from: Data(bytes.prefix(count))) }
        }
    }

    deinit {
        if owner { unlink(url.path) }
        close(descriptor)
    }

    private var ownerAlive: Bool {
        if flock(descriptor, LOCK_SH | LOCK_NB) == 0 { return false }
        return errno == EWOULDBLOCK
    }

    /// Claim the rollback slot while this transaction still owns the running profile.
    /// A competing apply can temporarily occupy the slot and then fail.
    @MainActor public func claimRollback<T>(whileOwned: () -> Bool, acquire: () -> T?) async -> T? {
        while !Task.isCancelled && whileOwned() {
            if let ticket = acquire() { return ticket }
            do { try await Task.sleep(nanoseconds: 25_000_000) }
            catch { return nil }
        }
        return nil
    }

    /// Observe the actual manifest after the writer leaves. Any read failure requires rollback.
    @MainActor public func observe(manifest: URL, finalized: () -> Bool = { false }, abandoned: () async -> Void) async {
        while ownerAlive {
            do { try await Task.sleep(nanoseconds: 50_000_000) }
            catch { return }
        }
        guard !Task.isCancelled, !finalized() else { return }
        struct Witness: Decodable { let appliedTransaction: UUID? }
        let witness = try? JSONDecoder().decode(Witness.self, from: Data(contentsOf: manifest))
        if witness?.appliedTransaction != transaction { await abandoned() }
    }
}
