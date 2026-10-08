import Foundation

/// Shared atomic nodes.txt import. Parsing errors never contain source links.
public enum NodeFileImport {
    public static func parse(_ data: Data, validate: (String) throws -> StagedNode) throws -> [StagedNode] {
        guard data.count <= ICloudSyncLimits.maxSourceBytes else { throw Failure("Node file exceeds the 32 MiB limit") }
        guard let text = String(data: data, encoding: .utf8) else { throw Failure("Node file must be UTF-8 text") }
        let textWithoutBOM = text.hasPrefix("\u{FEFF}") ? String(text.dropFirst()) : text
        var nodes: [StagedNode] = []
        var seen: Set<String> = []
        for (index, line) in textWithoutBOM.components(separatedBy: .newlines).enumerated() {
            let link = line.trimmingCharacters(in: .whitespaces)
            guard !link.isEmpty, !link.hasPrefix("#") else { continue }
            guard seen.insert(link).inserted else { continue }
            do { nodes.append(try validate(link)) }
            catch { throw Failure("Invalid node at line \(index + 1). No nodes were imported.") }
        }
        guard !nodes.isEmpty else { throw Failure("No node links found in the file") }
        return nodes
    }

    /// Manual imports add missing links; cloud adoption replaces the nodes half.
    public static func merging(_ nodes: [StagedNode], into book: NodesSourcesBook) -> NodesSourcesBook {
        var next = book
        var seen = Set(book.nodes.map(\.rawLink))
        next.nodes += nodes.filter { seen.insert($0.rawLink).inserted }
        return next
    }

    public struct Failure: LocalizedError {
        public let message: String
        public init(_ message: String) { self.message = message }
        public var errorDescription: String? { message }
    }
}
