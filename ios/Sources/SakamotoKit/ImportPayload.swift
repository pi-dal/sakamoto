import Foundation

/// Routes user-provided clipboard/QR text without fetching or persisting it.
public struct ImportPayload: Equatable, Sendable {
    public enum Kind: Equatable, Sendable { case node, url, generatedConfig, conf }
    public let kind: Kind
    public let text: String

    public static func detect(_ raw: String) throws -> ImportPayload {
        guard raw.utf8.count <= 1_048_576 else { throw InvalidPayload("Import is too large") }
        let text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "\u{FEFF}"))
            .trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { throw InvalidPayload("No importable text") }
        let kind: Kind
        if text.hasPrefix("{") { kind = .generatedConfig }
        else if text.hasPrefix("[") { kind = .conf }
        else {
            guard !text.contains("\n"), !text.contains("\r") else { throw InvalidPayload("Import one link at a time") }
            guard let separator = text.range(of: "://") else { throw InvalidPayload("Unsupported import") }
            let scheme = text[..<separator.lowerBound].lowercased()
            if scheme == "http" || scheme == "https" {
                guard let url = URL(string: text), let host = url.host, !host.isEmpty else { throw InvalidPayload("Invalid URL") }
                kind = .url
            } else {
                guard ["ss", "ssr", "vmess", "vless", "trojan", "hysteria", "hysteria2", "hy2", "tuic", "socks", "socks5", "anytls", "naive", "ssh"].contains(scheme) else {
                    throw InvalidPayload("Unsupported import")
                }
                kind = .node
            }
        }
        return ImportPayload(kind: kind, text: text)
    }

    public struct InvalidPayload: LocalizedError {
        public let message: String
        public init(_ message: String) { self.message = message }
        public var errorDescription: String? { message }
    }
}
