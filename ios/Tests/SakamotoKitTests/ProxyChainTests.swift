import XCTest
@testable import SakamotoKit

final class ProxyChainTests: XCTestCase {
    private let base = """
    {"outbounds":[{"tag":"MainProxy","type":"selector","outbounds":["ManualPick"]},{"tag":"ManualPick","type":"selector","outbounds":["entry","exit"],"default":"exit"},{"tag":"entry","type":"trojan"},{"tag":"exit","type":"socks"},{"tag":"direct","type":"direct"}],"route":{"rules":[{"domain_suffix":["proxy.example"],"outbound":"MainProxy"},{"domain_suffix":["direct.example"],"outbound":"direct"},{"action":"reject"}],"final":"MainProxy"},"dns":{"servers":[{"tag":"remote","detour":"MainProxy"}]}}
    """
    func testChainExcludesExitFromSelectorsAndPreservesDirectReject() throws {
        let raw = try ProxyChainSettings(enabled: true, exit: "exit").applying(to: base)
        let root = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(raw.utf8)) as? [String: Any])
        let out = try XCTUnwrap(root["outbounds"] as? [[String: Any]])
        XCTAssertEqual(out[1]["outbounds"] as? [String], ["entry"])
        XCTAssertEqual(out[1]["default"] as? String, "entry")
        XCTAssertEqual(out[3]["detour"] as? String, "MainProxy")
        let route = try XCTUnwrap(root["route"] as? [String: Any]); let rules = try XCTUnwrap(route["rules"] as? [[String: Any]])
        XCTAssertEqual(rules[0]["outbound"] as? String, "exit")
        XCTAssertEqual(rules[1]["outbound"] as? String, "direct")
        XCTAssertEqual(rules[2]["action"] as? String, "reject")
        XCTAssertEqual(route["final"] as? String, "exit")
        XCTAssertEqual(try ProxyChainSettings().applying(to: base), base, "disabling must use the unmodified snapshot")
    }
    func testRejectsIndirectCyclesAndMissingExit() {
        let cycle = base.replacingOccurrences(of: "\"tag\":\"entry\",\"type\":\"trojan\"", with: "\"tag\":\"entry\",\"type\":\"trojan\",\"detour\":\"MainProxy\"")
        XCTAssertThrowsError(try ProxyChainSettings(enabled: true, exit: "exit").applying(to: cycle))
        XCTAssertThrowsError(try ProxyChainSettings(enabled: true, exit: "missing").applying(to: base))
        XCTAssertThrowsError(try ProxyChainSettings(enabled: true, upstream: "exit", exit: "exit").applying(to: base))
    }
    func testImportedChainCanBeDetectedDisabledAndChanged() throws {
        let imported = try ProxyChainSettings(enabled: true, exit: "exit").applying(to: base)
        XCTAssertEqual(ProxyChainSettings.importedChain(in: imported), ProxyChainSettings(enabled: true, exit: "exit"))
        XCTAssertNil(ProxyChainSettings.importedChain(in: base))
        let disabled = try ProxyChainSettings.unchainedImportedConfig(imported)
        XCTAssertNil(ProxyChainSettings.importedChain(in: disabled))
        let root = try XCTUnwrap(try JSONSerialization.jsonObject(with: Data(disabled.utf8)) as? [String: Any])
        let out = try XCTUnwrap(root["outbounds"] as? [[String: Any]])
        XCTAssertNil(out.first { $0["tag"] as? String == "exit" }?["detour"])
        let route = try XCTUnwrap(root["route"] as? [String: Any])
        XCTAssertEqual(route["final"] as? String, "MainProxy")
        let rules = try XCTUnwrap(route["rules"] as? [[String: Any]])
        XCTAssertEqual(rules[0]["outbound"] as? String, "MainProxy")
        XCTAssertEqual(rules[1]["outbound"] as? String, "direct")
        XCTAssertEqual(rules[2]["action"] as? String, "reject")
        let dns = try XCTUnwrap(root["dns"] as? [String: Any])
        XCTAssertEqual((dns["servers"] as? [[String: Any]])?.first?["detour"] as? String, "MainProxy")
        let changed = try ProxyChainSettings(enabled: true, upstream: "entry", exit: "exit").applying(to: disabled)
        XCTAssertEqual(ProxyChainSettings.importedChain(in: changed), ProxyChainSettings(enabled: true, upstream: "entry", exit: "exit"))
    }
    func testProfileRoundTripRetainsChainIntent() throws {
        var profile = TunnelProfile(name: "Chain", config: base)
        profile.proxyChain = ProxyChainSettings(enabled: true, exit: "exit")
        let restored = try JSONDecoder().decode(TunnelProfile.self, from: JSONEncoder().encode(profile))
        XCTAssertEqual(restored.proxyChain, profile.proxyChain)
        XCTAssertEqual(restored.config, base)
    }
}
