import XCTest
@testable import SakamotoKit

// Settings semantics: the validated config-JSON merges must produce exactly
// what the host generator would (internal/gen shapes) and fail loudly on
// anything they cannot express.

final class SettingsOverridesTests: XCTestCase {
    // Minimal generated-config shape: sniff + hijack first, protections at
    // index 2 (gen.go), then normal routing rules.
    private let sample = """
    {
      "log": {"level": "info", "timestamp": true},
      "inbounds": [{"type": "tun", "tag": "tun-in", "strict_route": true, "stack": "mixed"}],
      "outbounds": [{"type": "direct", "tag": "direct"}],
      "route": {
        "rules": [
          {"action": "sniff"},
          {"protocol": "dns", "action": "hijack-dns"},
          {"protocol": "stun", "action": "reject"},
          {"ip_is_private": true, "action": "route", "outbound": "direct"}
        ],
        "final": "direct"
      }
    }
    """

    func testSetLogLevelValidLevels() throws {
        for level in ["error", "warn", "info", "debug", "INFO"] {
            let next = try SettingsOverrides.setLogLevel(level, in: sample)
            let parsed = try XCTUnwrap(ConfigSemanticsReader.read(next))
            XCTAssertEqual(parsed.logLevel, level.lowercased())
        }
    }

    func testSetLogLevelRejectsUnknown() {
        XCTAssertThrowsError(try SettingsOverrides.setLogLevel("trace", in: sample)) { error in
            XCTAssertEqual(
                error as? SettingsOverrideError,
                .unknownLogLevel("trace"),
                "trace is valid sing-box but outside the sakamoto vocabulary — reject loudly"
            )
        }
    }

    func testSetBlockQUICInsertsAtGeneratorPosition() throws {
        let withoutQUIC = try SettingsOverrides.setBlockSTUN(false, in: sample)
        let withQUIC = try SettingsOverrides.setBlockQUIC(true, in: withoutQUIC)
        let parsed = try XCTUnwrap(ConfigSemanticsReader.read(withQUIC))
        XCTAssertTrue(parsed.blockQUIC)
        XCTAssertFalse(parsed.blockSTUN)
        // Rule sits right after sniff + DNS hijack, matching the generator.
        let rules = try XCTUnwrap(JSONValueFactory.parse(withQUIC)?["route"]?["rules"]?.arrayValue)
        XCTAssertEqual(rules[2]["network"]?.stringValue, "udp")
        XCTAssertEqual(rules[2]["port"]?.intValue, 443)
        XCTAssertEqual(rules[2]["action"]?.stringValue, "reject")
    }

    func testSetBlockQUICIsIdempotentAndRemoves() throws {
        let on = try SettingsOverrides.setBlockQUIC(true, in: sample)
        let onAgain = try SettingsOverrides.setBlockQUIC(true, in: on)
        XCTAssertEqual(on, onAgain, "enabling twice must not duplicate the rule")
        let off = try SettingsOverrides.setBlockQUIC(false, in: on)
        let parsed = try XCTUnwrap(ConfigSemanticsReader.read(off))
        XCTAssertFalse(parsed.blockQUIC)
        let offAgain = try SettingsOverrides.setBlockQUIC(false, in: off)
        XCTAssertEqual(off, offAgain)
    }

    func testSetBlockSTUNRoundTrip() throws {
        let off = try SettingsOverrides.setBlockSTUN(false, in: sample)
        XCTAssertFalse(try XCTUnwrap(ConfigSemanticsReader.read(off)).blockSTUN)
        let on = try SettingsOverrides.setBlockSTUN(true, in: off)
        XCTAssertTrue(try XCTUnwrap(ConfigSemanticsReader.read(on)).blockSTUN)
    }

    func testInvalidConfigFailsLoudly() {
        XCTAssertThrowsError(try SettingsOverrides.setLogLevel("debug", in: "not json"))
        XCTAssertThrowsError(try SettingsOverrides.setBlockQUIC(true, in: "[1,2]"))
    }

    func testMissingRouteRulesIsAnError() throws {
        let noRules = """
        {"log": {"level": "info"}, "route": {"final": "direct"}}
        """
        XCTAssertThrowsError(try SettingsOverrides.setBlockQUIC(true, in: noRules)) { error in
            XCTAssertEqual(error as? SettingsOverrideError, .missingRouteRules)
        }
    }
}

final class ConfigSemanticsReaderTests: XCTestCase {
    func testReadsGeneratorFacts() throws {
        let config = """
        {
          "log": {"level": "warn"},
          "inbounds": [
            {"type": "direct", "tag": "protected-dns"},
            {"type": "tun", "tag": "tun-in", "strict_route": false, "stack": "system"}
          ],
          "outbounds": [{"type": "direct", "tag": "direct"}, {"type": "selector", "tag": "MainProxy"}],
          "endpoints": [{"type": "tailscale", "tag": "tailscale-in"}],
          "route": {
            "rules": [
              {"action": "sniff"},
              {"protocol": "dns", "action": "hijack-dns"},
              {"protocol": "stun", "action": "reject"},
              {"network": "udp", "port": 443, "action": "reject"}
            ],
            "final": "MainProxy"
          }
        }
        """
        let semantics = try XCTUnwrap(ConfigSemanticsReader.read(config))
        XCTAssertEqual(semantics.logLevel, "warn")
        XCTAssertTrue(semantics.blockSTUN)
        XCTAssertTrue(semantics.blockQUIC)
        XCTAssertEqual(semantics.routeFinal, "MainProxy")
        XCTAssertEqual(semantics.tunStack, "system")
        XCTAssertEqual(semantics.strictRoute, false)
        XCTAssertEqual(semantics.inboundCount, 2)
        XCTAssertEqual(semantics.outboundCount, 2)
        XCTAssertEqual(semantics.endpointCount, 1)
    }

    func testAbsentFieldsStayNil() throws {
        let semantics = try XCTUnwrap(ConfigSemanticsReader.read("{}"))
        XCTAssertNil(semantics.logLevel)
        XCTAssertNil(semantics.routeFinal)
        XCTAssertFalse(semantics.blockQUIC)
    }

    func testInvalidConfigReturnsNil() {
        XCTAssertNil(ConfigSemanticsReader.read("nope"))
    }
}
