import XCTest
import NetworkExtension
@testable import SakamotoKit
@testable import SakamotoNE

final class AutomaticConnectionTests: XCTestCase {
    func testURLsBecomeDeduplicatedHosts() throws {
        XCTAssertEqual(try AutomaticConnectionSettings.normalizeDomains("https://Example.com/path?a=1\nexample.com, api.example.org"), ["example.com", "api.example.org"])
        for input in ["https://user:password@example.com", "ftp://example.com", "bad_host", "-bad.example"] {
            XCTAssertThrowsError(try AutomaticConnectionSettings.normalizeDomains(input))
        }
    }

    func testTriggerBudgetsAllowDuplicatesButRejectLargeLists() throws {
        let atLimit = (0..<AutomaticConnectionSettings.maxDomains).map { "d\($0).example" }.joined(separator: "\n")
        XCTAssertEqual(try AutomaticConnectionSettings.normalizeDomains(atLimit).count, AutomaticConnectionSettings.maxDomains)
        XCTAssertEqual(try AutomaticConnectionSettings.normalizeDomains(atLimit + "\nd0.example").count, AutomaticConnectionSettings.maxDomains)
        XCTAssertThrowsError(try AutomaticConnectionSettings.normalizeDomains(atLimit + "\noverflow.example"))
        XCTAssertThrowsError(try AutomaticConnectionSettings.normalizeDomains(String(repeating: "a", count: AutomaticConnectionSettings.maxInputBytes + 1)))
    }

    func testInvalidSettingsCannotEnableAnEmptyDomainRule() {
        XCTAssertThrowsError(try AutomaticConnectionSettings(mode: .domains).validated())
        XCTAssertThrowsError(try AutomaticConnectionSettings(mode: .domains, domains: ["example.com"], probeURL: "https://user:secret@example.org").validated())
    }

    func testRulesRoundTripThroughSystemPreferences() throws {
        let manager = NETunnelProviderManager()
        let settings = AutomaticConnectionSettings(mode: .domains, domains: ["example.com"], probeURL: "https://example.com/check")
        try AutomaticConnectionPolicy.apply(settings, to: manager)
        XCTAssertTrue(manager.isOnDemandEnabled)
        let evaluation = try XCTUnwrap(manager.onDemandRules?.first as? NEOnDemandRuleEvaluateConnection)
        let rule = try XCTUnwrap(evaluation.connectionRules?.first)
        XCTAssertEqual(rule.matchDomains, ["example.com"])
        XCTAssertEqual(rule.action, .connectIfNeeded)
        XCTAssertEqual(rule.probeURL?.absoluteString, settings.probeURL)
        XCTAssertEqual(AutomaticConnectionPolicy.settings(from: manager), settings)
        try AutomaticConnectionPolicy.apply(.init(), to: manager)
        XCTAssertFalse(manager.isOnDemandEnabled)
        XCTAssertTrue(manager.onDemandRules?.isEmpty == true)
    }

    func testNetworkConditionUsesIgnoreFallbackRatherThanDisconnectingManualVPN() throws {
        let rules = AutomaticConnectionPolicy.rules(for: .init(mode: .wifi))
        XCTAssertEqual(rules.count, 2)
        XCTAssertEqual(rules[0].interfaceTypeMatch, .wiFi)
        XCTAssertTrue(rules[1] is NEOnDemandRuleIgnore)
        XCTAssertTrue(AutomaticConnectionPolicy.rules(for: .init(mode: .anyNetwork)).first is NEOnDemandRuleConnect)
    }
}
