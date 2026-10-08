import XCTest
@testable import SakamotoKit

final class ExperimentSettingsTests: XCTestCase {
    private let config = """
    {"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"chain-exit","outbounds":["node"]}],"route":{"rules":[{"action":"reject","domain":["blocked.example"]},{"rule_set":["rs-proxy"],"outbound":"chain-exit"}],"final":"direct"},"custom":{"keep":true}}
    """

    func testToggleUsesExplicitProxyAndPreservesRules() throws {
        let enabled = try SettingsOverrides.setExperiment(true, in: config)
        XCTAssertTrue(SettingsOverrides.experimentEnabled(in: enabled))
        XCTAssertEqual(ConfigSemanticsReader.read(enabled)?.routeFinal, "chain-exit")
        let before = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(config.utf8)) as? NSDictionary)
        let after = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(enabled.utf8)) as? NSDictionary)
        XCTAssertEqual(before["custom"] as? NSDictionary, after["custom"] as? NSDictionary)
        XCTAssertEqual((before["route"] as? NSDictionary)?["rules"] as? NSArray, (after["route"] as? NSDictionary)?["rules"] as? NSArray)
        let disabled = try SettingsOverrides.setExperiment(false, in: enabled)
        XCTAssertEqual(ConfigSemanticsReader.read(disabled)?.routeFinal, "direct")
        XCTAssertFalse(SettingsOverrides.experimentEnabled(in: disabled))
    }

    func testNoProxyGuessingOrMissingOutbound() {
        XCTAssertThrowsError(try SettingsOverrides.setExperiment(true, in: "{\"outbounds\":[],\"route\":{\"rules\":[],\"final\":\"direct\"}}"))
        XCTAssertThrowsError(try SettingsOverrides.setExperiment(true, in: config.replacingOccurrences(of: "\"tag\":\"chain-exit\"", with: "\"tag\":\"other\"")))
    }
}
