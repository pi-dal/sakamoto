import Foundation
import CryptoKit

/// Identity of an applied runtime, including file-backed rule contents. Hash
/// files incrementally to stay within the Network Extension memory budget.
public enum TunnelRuntimeFingerprint {
    public static func make(options: TunnelStartOptions, baseDirectory: URL) throws -> String {
        let receipt = try TunnelConfigurationReceipt(options: options)
        var digest = SHA256()
        digest.update(data: Data(receipt.fingerprint.utf8))
        let root = try JSONSerialization.jsonObject(with: Data(options.configContent.utf8)) as? [String: Any]
        let route = root?["route"] as? [String: Any]
        let sets = route?["rule_set"] as? [[String: Any]] ?? []
        for path in Set(sets.filter { $0["type"] as? String == "local" }.compactMap { $0["path"] as? String }).sorted() {
            // Template paths may refer to multiple tags. Conservatively reload
            // instead of accepting an incomplete identity for these configs.
            guard !path.contains("{tag}") else { throw FingerprintError.templatePath }
            let url = path.hasPrefix("/") ? URL(fileURLWithPath: path) : baseDirectory.appendingPathComponent(path)
            digest.update(data: Data(path.utf8))
            let file = try FileHandle(forReadingFrom: url)
            defer { try? file.close() }
            var fileDigest = SHA256()
            while let chunk = try file.read(upToCount: 64 * 1024), !chunk.isEmpty {
                fileDigest.update(data: chunk)
            }
            digest.update(data: Data(fileDigest.finalize()))
        }
        return digest.finalize().map { String(format: "%02x", $0) }.joined()
    }
    private enum FingerprintError: Error { case templatePath }
}

/// A native stop can arrive off the main actor while configuration application
/// is suspended. Tickets prevent that application or its rollback from later
/// resurrecting the stopped tunnel. Only one apply transaction runs at a time.
public final class TunnelOperationGate: @unchecked Sendable {
    private let lock = NSLock()
    private var generation: UInt64 = 0
    private var applying = false
    private var stopping = false

    public init() {}
    public func beginApply() -> UInt64? {
        lock.lock(); defer { lock.unlock() }
        guard !applying, !stopping else { return nil }
        applying = true
        generation &+= 1
        return generation
    }
    public func isCurrent(_ ticket: UInt64) -> Bool {
        lock.lock(); defer { lock.unlock() }
        return !stopping && applying && generation == ticket
    }
    public func finishApply(_ ticket: UInt64) {
        lock.lock(); defer { lock.unlock() }
        if generation == ticket { applying = false }
    }
    public func stop() {
        lock.lock(); defer { lock.unlock() }
        stopping = true; applying = false; generation &+= 1
    }
    public func start() {
        lock.lock(); defer { lock.unlock() }
        stopping = false; applying = false; generation &+= 1
    }
    /// Publish final observations under the same short lock as stop invalidation.
    public func publishIfActive(_ operation: () -> Void) -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard !stopping, !applying else { return false }
        operation(); return true
    }
    public var state: ServiceState? {
        lock.lock(); defer { lock.unlock() }
        return stopping ? .stopping : (applying ? .starting : nil)
    }
}
