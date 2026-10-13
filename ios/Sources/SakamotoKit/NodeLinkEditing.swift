import Foundation

/// Edit source endpoints without exposing or rebuilding their credentials/query options.
public enum NodeLinkEditing {
    public struct Endpoint: Equatable {
        public var remark: String
        public var server: String
        public var port: Int?
        public init(remark: String, server: String, port: Int?) { self.remark = remark; self.server = server; self.port = port }
    }
    private static func decode(_ text: String) -> Data? {
        let normalized = text.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        return Data(base64Encoded: normalized + String(repeating: "=", count: (4 - normalized.count % 4) % 4))
    }
    private static func vmess(_ raw: String) -> [String: Any]? {
        guard raw.lowercased().hasPrefix("vmess://"), let data = decode(String(raw.dropFirst(8))), let json = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any], json["add"] is String else { return nil }
        return json
    }
    private static func components(_ raw: String) throws -> URLComponents {
        guard var url = URLComponents(string: raw), let scheme = url.scheme else { throw TunnelProfile.InvalidProfile("This node could not be opened.") }
        if ["vmess", "vless", "socks", "socks5"].contains(scheme.lowercased()), url.user == nil, let authority = raw.components(separatedBy: "://").last?.components(separatedBy: "?").first?.components(separatedBy: "#").first,
           let data = decode(authority), let decoded = String(data: data, encoding: .utf8), decoded.contains("@"), var expanded = URLComponents(string: scheme + "://" + decoded) {
            expanded.percentEncodedQuery = url.percentEncodedQuery; expanded.percentEncodedFragment = url.percentEncodedFragment; url = expanded
        }
        guard url.host?.isEmpty == false else { throw TunnelProfile.InvalidProfile("This node is missing its server.") }
        return url
    }
    public static func endpoint(_ raw: String) throws -> Endpoint {
        if let json = vmess(raw), let host = json["add"] as? String {
            let port = (json["port"] as? NSNumber)?.intValue ?? Int(json["port"] as? String ?? "") ?? 443
            return Endpoint(remark: json["ps"] as? String ?? host, server: host, port: port)
        }
        let url = try components(raw)
        let query = url.queryItems ?? []
        let host = (url.host ?? "").trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
        let name = url.fragment.flatMap { $0.isEmpty ? nil : $0 } ?? query.first { $0.name == "remarks" }?.value ?? host
        let range = query.contains { $0.name == "mport" && $0.value?.isEmpty == false }
        return Endpoint(remark: name, server: host, port: range ? nil : url.port ?? (["socks", "socks5"].contains(url.scheme ?? "") ? 1080 : 443))
    }
    public static func updating(_ raw: String, remark: String, server: String, port: Int?) throws -> String {
        let name = remark.trimmingCharacters(in: .whitespacesAndNewlines), host = server.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !name.contains(where: { $0.isNewline || $0 == "\0" }), !host.isEmpty, !host.contains(where: { $0.isWhitespace }), !host.contains("://"), !host.contains("/") else { throw TunnelProfile.InvalidProfile("Enter a remark and an IP address or hostname.") }
        if let port, !(1...65535).contains(port) { throw TunnelProfile.InvalidProfile("Port must be 1–65535.") }
        if var json = vmess(raw) {
            json["ps"] = name; json["add"] = host
            if let port { json["port"] = String(port) }
            return "vmess://" + (try JSONSerialization.data(withJSONObject: json, options: [.sortedKeys])).base64EncodedString()
        }
        var url = try components(raw)
        url.host = host.contains(":") && !host.hasPrefix("[") ? "[\(host)]" : host
        if let port { url.port = port }
        url.fragment = name
        guard let edited = url.string else { throw TunnelProfile.InvalidProfile("Enter a valid IP address or hostname.") }
        return edited
    }
}
