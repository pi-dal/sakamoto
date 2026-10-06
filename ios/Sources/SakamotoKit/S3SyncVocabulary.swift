import Foundation

public struct S3SyncSettings: Codable, Equatable, Sendable {
    public var enabled = false
    public var endpoint = ""
    public var region = "us-east-1"
    public var bucket = ""
    public var prefix = "sakamoto"
    public init() {}
}
public struct S3SyncCredentials: Codable, Sendable {
    public var accessKey: String
    public var secretKey: String
    public var sessionToken: String
    public init(accessKey: String = "", secretKey: String = "", sessionToken: String = "") {
        self.accessKey = accessKey; self.secretKey = secretKey; self.sessionToken = sessionToken
    }
    enum CodingKeys: String, CodingKey {
        case accessKey = "access_key", secretKey = "secret_key", sessionToken = "session_token"
    }
}
public struct SourceBundle: Codable, Sendable {
    public var version = 1
    public var mainConf: String = ""
    public var files: [String: String] = [:]
    public init() {}
    enum CodingKeys: String, CodingKey { case version, mainConf = "main_conf", files }
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decode(Int.self, forKey: .version)
        mainConf = try c.decodeIfPresent(String.self, forKey: .mainConf) ?? ""
        files = try c.decode([String: String].self, forKey: .files)
    }
}
public struct S3SyncResponse: Decodable, Sendable {
    public var bundle: SourceBundle
    public var baseline: S3Baseline
    public var downloads: [String]?
    public var uploaded: Bool
}
public struct S3Baseline: Codable, Sendable {
    public var target: String
    public var hashes: [String: String]
    public var mainConf: String?
    enum CodingKeys: String, CodingKey { case target, hashes, mainConf = "main_conf" }
}
public struct SyncPolicy: Codable, Sendable {
    public var match: String
    public var action: String
    public init(match: String, action: String) { self.match = match; self.action = action }
}
public struct SyncSubscription: Codable, Sendable {
    public var name: String
    public var url: String
    public var format: String
    public init(name: String, url: String, format: String) { self.name = name; self.url = url; self.format = format }
}
