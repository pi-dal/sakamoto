import Darwin
import Foundation

/// Samples only at lifecycle/pressure events, never on a background timer.
public enum TunnelRuntimeEvidence {
    public static func memoryBytes() -> UInt64? {
        var info = task_vm_info_data_t()
        let capacity = MemoryLayout<task_vm_info_data_t>.size / MemoryLayout<integer_t>.size
        var count = mach_msg_type_number_t(capacity)
        let result = withUnsafeMutablePointer(to: &info) { pointer in
            pointer.withMemoryRebound(to: integer_t.self, capacity: capacity) {
                task_info(mach_task_self_, task_flavor_t(TASK_VM_INFO), $0, &count)
            }
        }
        return result == KERN_SUCCESS ? info.phys_footprint : nil
    }

    public static func record(stage: String, message: String, defaults: UserDefaults? = nil) {
        let footprint = memoryBytes().map(String.init) ?? "unavailable"
        TunnelDiagnostics.recordEvent(stage: stage, message: "\(message); footprint_bytes=\(footprint)", defaults: defaults)
    }

    public static func recordPreviousExit(_ statusJSON: String, defaults: UserDefaults? = nil) {
        guard statusJSON.utf8.count <= 8192,
              let value = try? JSONDecoder().decode(PreviousRuntime.self, from: Data(statusJSON.utf8)),
              value.phase == "starting" || value.phase == "running" else { return }
        // An unclean marker proves missing teardown, not jetsam or a crash.
        // Never persist the journal token or configuration with this evidence.
        TunnelDiagnostics.recordEvent(stage: "Previous runtime exit",
            message: "Unclean \(value.phase) marker; cause unknown; core_instance=\(value.instance ?? "unavailable")", defaults: defaults)
    }

    /// Read only the numeric reset event tail, never OOM config/log/heap
    /// snapshots. Called on lifecycle/foreground IPC demand, with no timer.
    public static func latestNativeMemoryReset(workingDirectory: URL) -> String? {
        let file = workingDirectory.appendingPathComponent("oom_draft/events.jsonl")
        guard let resources = try? file.resourceValues(forKeys: [.isSymbolicLinkKey, .isRegularFileKey]),
              resources.isSymbolicLink != true, resources.isRegularFile == true,
              let handle = try? FileHandle(forReadingFrom: file) else { return nil }
        defer { try? handle.close() }
        guard let size = try? handle.seekToEnd(),
              (try? handle.seek(toOffset: size > 65536 ? size - 65536 : 0)) != nil,
              let data = try? handle.read(upToCount: 65536) else { return nil }
        let formatter = ISO8601DateFormatter()
        for line in data.split(separator: 10).reversed() {
            guard let event = try? JSONDecoder().decode(MemoryReset.self, from: Data(line)),
                  event.t == "reset", event.reportOnly != true,
                  formatter.date(from: event.at) != nil,
                  let before = event.memoryBytes, before > 0 else { continue }
            let reason = event.reason == "threshold" || event.reason == "rate" ? event.reason! : "unknown"
            return "at=\(event.at); trigger=\(reason); before_bytes=\(before); after_bytes=\(event.memoryAfterBytes ?? 0); connections=\(max(0, event.connections ?? 0))"
        }
        return nil
    }

    private struct MemoryReset: Decodable {
        var t: String
        var at: String
        var reason: String?
        var memoryBytes: UInt64?
        var memoryAfterBytes: UInt64?
        var connections: Int?
        var reportOnly: Bool?
    }

    private struct PreviousRuntime: Decodable {
        var phase: String
        var instance: String?
    }
}
