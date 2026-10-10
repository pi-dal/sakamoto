import XCTest
@testable import SakamotoKit

final class TunnelConfigurationReceiptTests: XCTestCase {
    func testCanonicalJSONDoesNotLoseConfirmationAfterRelaunch() throws {
        let id = UUID().uuidString
        let first = TunnelStartOptions(configContent: #"{"outbounds":[],"route":{"final":"direct"}}"#,
            locale: "en", profileID: id, experimentJSON: #"{"mode":"on","threshold":3}"#)
        let reordered = TunnelStartOptions(configContent: #"{"route":{"final":"direct"},"outbounds":[]}"#,
            locale: "zh", profileID: id, experimentJSON: #"{"threshold":3,"mode":"on"}"#, forceTailscaleDERP: false)
        XCTAssertTrue(try TunnelConfigurationReceipt(options: first).matches(reordered))
    }

    func testAReceiptCannotConfirmAnotherSelectionOrChangedPayload() throws {
        let original = TunnelStartOptions(configContent: #"{"route":{"final":"direct"}}"#,
            profileID: UUID().uuidString, experimentJSON: #"{"mode":"off"}"#)
        let receipt = try TunnelConfigurationReceipt(options: original)
        var changed = original
        changed.profileID = UUID().uuidString
        XCTAssertFalse(receipt.matches(changed))
        changed = original; changed.configContent = #"{"route":{"final":"proxy"}}"#
        XCTAssertFalse(receipt.matches(changed))
        changed = original; changed.experimentJSON = #"{"mode":"on"}"#
        XCTAssertFalse(receipt.matches(changed))
        changed = original; changed.forceTailscaleDERP = true
        XCTAssertFalse(receipt.matches(changed))
    }

    func testOnlyDigestAndIdentityArePersisted() throws {
        let suite = "sakamoto.receipt.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let options = TunnelStartOptions(configContent: #"{"password":"private-credential","server":"private.example"}"#,
            profileID: UUID().uuidString)
        TunnelConfigurationReceiptStore.record(options, defaults: defaults)
        let receipt = try XCTUnwrap(TunnelConfigurationReceiptStore.read(defaults: defaults))
        XCTAssertTrue(receipt.matches(options))
        let encoded = String(decoding: try JSONEncoder().encode(receipt), as: UTF8.self)
        XCTAssertFalse(encoded.contains("private-credential"))
        XCTAssertFalse(encoded.contains("private.example"))
        XCTAssertFalse(encoded.contains("configContent"))
        XCTAssertEqual(receipt.fingerprint.count, 64)
    }

    func testInvalidConfigurationCannotProduceConfirmation() {
        XCTAssertThrowsError(try TunnelConfigurationReceipt(options: .init(configContent: "invalid JSON")))
    }
}
