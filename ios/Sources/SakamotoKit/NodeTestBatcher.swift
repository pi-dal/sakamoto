import Foundation

public enum NodeTestBatcher {
    public static func targets(_ groups: [GroupSnapshot]) -> [String] {
        let groupTags = Set(groups.map(\.tag))
        return Set(groups.flatMap(\.items).filter {
            !groupTags.contains($0.tag) && !["direct", "block", "dns", "selector", "urltest"].contains($0.kind) && $0.tag.lowercased() != "direct"
        }.map(\.tag)).sorted()
    }
    public struct Report: Sendable { public var requested: Int; public var failed: Int }
    public static func run(_ tags: [String], test: @escaping @Sendable (String) async throws -> Void) async -> Report {
        var report = Report(requested: 0, failed: 0)
        for start in stride(from: 0, to: tags.count, by: 4) {
            guard !Task.isCancelled else { break }
            let batch = Array(tags[start..<min(start + 4, tags.count)])
            report.requested += batch.count
            report.failed += await withTaskGroup(of: Bool.self, returning: Int.self) { group in
                for tag in batch { group.addTask { do { try await test(tag); return true } catch { return false } } }
                var failed = 0
                for await success in group { if !success { failed += 1 } }
                return failed
            }
        }
        return report
    }
}
