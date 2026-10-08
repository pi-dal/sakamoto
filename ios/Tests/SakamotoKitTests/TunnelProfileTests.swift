import XCTest
@testable import SakamotoKit

final class TunnelProfileTests: XCTestCase {
    private let config = """
    {"inbounds":[{"type":"tun","tag":"tun"}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"rules":[],"final":"direct","rule_set":[{"type":"local","tag":"rules","format":"binary","path":"/mac/private/rules/main.srs"}]}}
    """
    func testPortablePackageResolvesRulesUnderSelectedProfile() throws {
        let package = TunnelProfile.Package(name: "Main", config: config, files: ["rules/main.srs": Data([1, 2, 3])])
        let profile = try TunnelProfile.decodePackage(JSONEncoder().encode(package))
        let prepared = try profile.preparedConfig(ruleDirectory: URL(fileURLWithPath: "/group/Profiles/main"))
        let root = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(prepared.utf8)) as? [String: Any])
        let route = try XCTUnwrap(root["route"] as? [String: Any])
        let sets = try XCTUnwrap(route["rule_set"] as? [[String: Any]])
        XCTAssertEqual(sets.first?["path"] as? String, "/group/Profiles/main/rules/main.srs")
    }
    func testJSONWithMissingRulesCannotBeConnected() {
        XCTAssertThrowsError(try TunnelProfile(name: "Bad", config: config).preparedConfig(ruleDirectory: URL(fileURLWithPath: "/group")))
    }
    func testUnsafeRuleNamesAndNoTunAreRejected() throws {
        let package = TunnelProfile.Package(name: "Bad", config: config, files: ["rules/../main.srs": Data([1])])
        XCTAssertThrowsError(try TunnelProfile.decodePackage(JSONEncoder().encode(package)))
        XCTAssertThrowsError(try TunnelProfile(name: "Bad", config: "{\"inbounds\":[],\"route\":{}}").preparedConfig(ruleDirectory: URL(fileURLWithPath: "/group")))
        XCTAssertFalse(TunnelProfile.validFileName("rules/../../secret"))
        XCTAssertFalse(TunnelProfile.validFileName("rules/.hidden"))
    }
    func testConfGraphRequiresEveryIncludeAndRejectsCycles() throws {
        let main = Data("[General]\ninclude = child.conf\n[Rule]\nFINAL,DIRECT".utf8)
        XCTAssertThrowsError(try ICloudSyncPaths.validateConfGraph(main: "conf/main.conf", files: ["conf/main.conf": main]))
        try ICloudSyncPaths.validateConfGraph(main: "conf/main.conf", files: ["conf/main.conf": main, "conf/child.conf": Data("[Rule]\nFINAL,DIRECT".utf8)])
        XCTAssertThrowsError(try ICloudSyncPaths.validateConfGraph(main: "conf/main.conf", files: ["conf/main.conf": main, "conf/child.conf": Data("[General]\ninclude = main.conf".utf8)]))
    }

    func testDiagnosticsNeverStoresLinkAuthorityOrToken() {
        let text = TunnelDiagnostics.sanitized("failed https://user:password@example.com/path?token=secret and vless://uuid@example.com:443")
        XCTAssertFalse(text.contains("password"))
        XCTAssertFalse(text.contains("secret"))
        XCTAssertFalse(text.contains("uuid"))
    }
}
