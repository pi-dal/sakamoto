import XCTest
@testable import SakamotoKit

final class TunnelRuntimeTests: XCTestCase {
    func testIdentityIncludesRuleFilesEvenWhenJSONIsUnchanged() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let file = root.appendingPathComponent("rules.json")
        let options = TunnelStartOptions(configContent: #"{"route":{"rule_set":[{"type":"local","tag":"local","path":"rules.json"}]}}"#)
        try Data("first".utf8).write(to: file)
        let before = try TunnelRuntimeFingerprint.make(options: options, baseDirectory: root)
        XCTAssertEqual(before, try TunnelRuntimeFingerprint.make(options: options, baseDirectory: root))
        try Data("second".utf8).write(to: file)
        XCTAssertNotEqual(before, try TunnelRuntimeFingerprint.make(options: options, baseDirectory: root))
        try FileManager.default.removeItem(at: file)
        XCTAssertThrowsError(try TunnelRuntimeFingerprint.make(options: options, baseDirectory: root))
    }
    func testEquivalentConfigurationKeepsIdentityAndPolicyChangeDoesNot() throws {
        let root = FileManager.default.temporaryDirectory
        let options = TunnelStartOptions(configContent: #"{"outbounds":[],"route":{"final":"direct"}}"#, profileID: "one")
        let first = try TunnelRuntimeFingerprint.make(options: options, baseDirectory: root)
        var reformatted = options
        reformatted.configContent = "{ \"route\": {\"final\": \"direct\"}, \"outbounds\": [] }"
        XCTAssertEqual(first, try TunnelRuntimeFingerprint.make(options: reformatted, baseDirectory: root))
        reformatted.forceTailscaleDERP = true
        XCTAssertNotEqual(first, try TunnelRuntimeFingerprint.make(options: reformatted, baseDirectory: root))
    }
    func testStopInvalidatesApplyAndLateCompletionCannotUnlockReplacement() throws {
        let gate = TunnelOperationGate()
        let first = try XCTUnwrap(gate.beginApply())
        XCTAssertNil(gate.beginApply())
        XCTAssertTrue(gate.isCurrent(first))
        gate.stop()
        XCTAssertFalse(gate.isCurrent(first))
        XCTAssertNil(gate.beginApply())
        gate.start()
        let next = try XCTUnwrap(gate.beginApply())
        gate.finishApply(first)
        XCTAssertTrue(gate.isCurrent(next))
        XCTAssertNil(gate.beginApply())
        gate.finishApply(next)
        XCTAssertNotNil(gate.beginApply())
    }
}
