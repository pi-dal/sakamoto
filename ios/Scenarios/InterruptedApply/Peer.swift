import Foundation
import CryptoKit

// Host peer for the disposable simulator. Own the provider end of the production
// lease in a process that survives the test app's deliberate exit.
@main struct ApplyPeer {
    struct Options: Decodable { var profileID: UUID; var configContent: String }
    struct Reload: Decodable { var transaction: UUID?; var requestID: UUID; var previous, next: Options }

    @MainActor static func main() async throws {
        let exchange = URL(fileURLWithPath: CommandLine.arguments[1])
        let group = exchange.deletingLastPathComponent()
        func wait(_ name: String) async throws {
            let deadline = Date().addingTimeInterval(90)
            while !FileManager.default.fileExists(atPath: exchange.appendingPathComponent(name).path) {
                guard Date() < deadline else { throw CocoaError(.fileReadUnknown) }
                try await Task.sleep(nanoseconds: 10_000_000)
            }
        }
        func activate(_ options: Options, _ phase: String) async throws {
            try Data(options.configContent.utf8).write(to: exchange.appendingPathComponent(phase + ".json"), options: .atomic)
            try await wait(phase + ".observed")
        }
        func receive() async throws -> Reload {
            try await wait("reload.json")
            let request = exchange.appendingPathComponent("reload.json")
            let reload = try JSONDecoder().decode(Reload.self, from: Data(contentsOf: request))
            try FileManager.default.removeItem(at: request)
            return reload
        }
        let contention = ProcessInfo.processInfo.environment["SAKAMOTO_LEASE_CONTENTION"] ?? "none"
        if contention == "double" {
            let b = try await receive()
            guard let bID = b.transaction else { exit(3) }
            let bLease = try ApplyLease(root: group, transaction: bID, owner: false)
            try await activate(b.previous, "baseline")
            try await activate(b.next, "candidate-1")
            try Data().write(to: exchange.appendingPathComponent(b.requestID.uuidString))
            func manifest(_ r: Reload) -> URL { group.appendingPathComponent("Profiles/\(r.next.profileID.uuidString)/profile.json") }
            var owner = bID
            let waiting = Task { @MainActor in
                await bLease.observe(manifest: manifest(b)) {
                    try? Data().write(to: exchange.appendingPathComponent("lease-busy"))
                    _ = await bLease.claimRollback(whileOwned: { owner == bID }, acquire: { nil as Bool? })
                }
            }
            try await wait("lease-busy")
            let c = try await receive()
            guard let cID = c.transaction, c.previous.profileID == b.next.profileID, c.next.profileID != b.next.profileID else { exit(6) }
            let cLease = try ApplyLease(root: group, transaction: cID, owner: false)
            guard let reference = cLease.rollback?.reference, let id = reference["profileID"], let digest = reference["payloadDigest"], UUID(uuidString: id) == b.previous.profileID else { exit(8) }
            let data = try Data(contentsOf: group.appendingPathComponent("AppliedVPN/\(id)-\(digest).json"))
            guard SHA256.hash(data: data).map({ String(format: "%02x", $0) }).joined() == digest else { exit(9) }
            let rollback = try JSONDecoder().decode(Options.self, from: data)
            owner = cID
            await waiting.value
            try await activate(c.next, "candidate-C")
            try Data().write(to: exchange.appendingPathComponent(c.requestID.uuidString))
            await cLease.observe(manifest: manifest(c)) {
                do { try await activate(rollback, "restored") } catch { exit(7) }
            }
            try Data().write(to: exchange.appendingPathComponent("committed"))
            return
        }
        for attempt in 1...3 {
            let reload = try await receive()
            guard let transaction = reload.transaction else { exit(3) }
            let lease = try ApplyLease(root: group, transaction: transaction, owner: false)
            if attempt == 1 { try await activate(reload.previous, "baseline") }
            try await activate(reload.next, "candidate-\(attempt)")
            try Data().write(to: exchange.appendingPathComponent(reload.requestID.uuidString))
            if attempt == 2 {
                let rollback = try await receive()
                guard rollback.transaction == nil else { exit(4) }
                try await activate(rollback.next, "retry-rollback")
                try Data().write(to: exchange.appendingPathComponent(rollback.requestID.uuidString))
                continue
            }
            let manifest = group.appendingPathComponent("Profiles/\(reload.next.profileID.uuidString)/profile.json")
            var reverted = false
            var owner = transaction
            var competitorRunning = contention != "none" && attempt == 1
            var checkedWhileBusy = false
            var competitor: Task<Void, Error>?
            if competitorRunning {
                competitor = Task { @MainActor in
                    try await wait("lease-busy")
                    if contention == "success" {
                        owner = UUID()
                        try await activate(reload.next, "takeover")
                    } else {
                        try await activate(reload.next, "competitor-failed")
                    }
                    competitorRunning = false
                }
            }
            await lease.observe(manifest: manifest) {
                func claim() -> Bool? {
                    if competitorRunning {
                        checkedWhileBusy = true
                        FileManager.default.createFile(atPath: exchange.appendingPathComponent("lease-busy").path, contents: nil)
                        return nil
                    }
                    return true
                }
                guard await lease.claimRollback(whileOwned: { owner == transaction }, acquire: claim) != nil else { return }
                reverted = true
                do { try await activate(reload.previous, "restored") }
                catch { exit(1) }
            }
            try await competitor?.value
            if attempt == 1 && contention != "none" && !checkedWhileBusy { exit(5) }
            guard reverted == (attempt == 1 && contention != "success") else { exit(2) }
            if attempt == 1 && contention == "success" {
                try Data().write(to: exchange.appendingPathComponent("retained-takeover"))
            }
        }
        try Data().write(to: exchange.appendingPathComponent("committed"))
    }
}
