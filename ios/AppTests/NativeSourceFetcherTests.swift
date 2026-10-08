import XCTest
import Libbox
import SakamotoKit
@testable import Sakamoto

private final class SourceURLProtocol: URLProtocol {
    private static let lock = NSLock()
    private static var calls = 0
    static func reset() { lock.lock(); calls = 0; lock.unlock() }
    static var requestCount: Int { lock.lock(); defer { lock.unlock() }; return calls }
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        Self.lock.lock(); Self.calls += 1; let attempt = Self.calls; Self.lock.unlock()
        guard let url = request.url else { return }
        if url.host == "retry.example" && attempt == 1 {
            client?.urlProtocol(self, didFailWithError: URLError(.dnsLookupFailed)); return
        }
        let status = url.host == "missing.example" ? 404 : 200
        let response = HTTPURLResponse(url: url, statusCode: status, httpVersion: "HTTP/1.1", headerFields: nil)!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data("DOMAIN,remote.example,PROXY".utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@MainActor
final class NativeSourceFetcherTests: XCTestCase {
    private func generate(host: String) async throws -> String {
        var bundle = SourceBundle(); bundle.mainConf = "main.conf"
        bundle.files = ["nodes.txt":"trojan://test@example.com:443#node", "main.conf":"[Rule]\nRULE-SET,https://\(host)/private?token=hidden,PROXY\nFINAL,DIRECT"]
        let raw = String(decoding: try JSONEncoder().encode(bundle), as: UTF8.self)
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [SourceURLProtocol.self]
        let fetcher = NativeSourceFetcher(configuration: configuration)
        return try await Task.detached {
            var error: NSError?
            let result = MobilegenGenerateProfileWithFetcherJSON(raw, "", fetcher, &error)
            if let error { throw error }; return result
        }.value
    }
    func testNativeFetchBridgeRetriesThenCompilesRemoteRule() async throws {
        SourceURLProtocol.reset()
        let package = try await generate(host: "retry.example")
        let profile = try TunnelProfile.decodePackage(Data(package.utf8))
        XCTAssertFalse(profile.files["rules/proxy.srs", default: Data()].isEmpty)
        XCTAssertEqual(SourceURLProtocol.requestCount, 2)
    }
    func testMissingRemoteRuleReportsStatusAndKeepsCredentialsPrivate() async {
        SourceURLProtocol.reset()
        do { _ = try await generate(host: "missing.example"); XCTFail("404 accepted") }
        catch {
            let text = TunnelDiagnostics.sanitized(error.localizedDescription)
            XCTAssertTrue(text.contains("RULE-SET")); XCTAssertTrue(text.contains("missing.example")); XCTAssertTrue(text.contains("404"))
            XCTAssertFalse(text.contains("hidden")); XCTAssertFalse(text.contains("private"))
        }
        XCTAssertEqual(SourceURLProtocol.requestCount, 1)
    }
    func testOwnerSourceSnapshotCompilesWithNativeNetworkWhenProvisioned() async throws {
        let file = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0].appendingPathComponent("integration.sakamoto")
        guard FileManager.default.fileExists(atPath: file.path) else { throw XCTSkip("No private source fixture") }
        let profile = try TunnelProfile.decodePackage(Data(contentsOf: file))
        let raw = try XCTUnwrap(profile.sourceBundleJSON)
        try AppServiceSetup.apply()
        let package = try await Task.detached {
            var error: NSError?
            let result = MobilegenGenerateProfileWithFetcherJSON(raw, "", NativeSourceFetcher(), &error)
            if let error { throw error }; return result
        }.value
        let generated = try TunnelProfile.decodePackage(Data(package.utf8))
        XCTAssertFalse(generated.files.isEmpty)
        let suite = "sakamoto-native-source-" + UUID().uuidString
        let defaults = UserDefaults(suiteName: suite)!
        defer { defaults.removePersistentDomain(forName: suite) }
        let store = ConfigStore(persistence: defaults, keyStore: ConfigStoreTests.EmptyKeys())
        try store.installProfile(generated)
        XCTAssertNoThrow(try store.connectionContent())
    }
}
