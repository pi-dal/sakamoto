import XCTest
@testable import Sakamoto

@MainActor
final class TailscaleDiagnosticsTests: XCTestCase {
    func testPingDisplaysActualTransportAndPrioritizesDirectEvidence() {
        typealias Outcome = TailscaleController.TailscalePingOutcome
        XCTAssertEqual(Outcome(isDirect: true, viaRelay: "peer", derpRegion: "hkg").pathDescription, "direct")
        XCTAssertEqual(Outcome(isDirect: false, viaRelay: "peer", derpRegion: "hkg").pathDescription, "peer-relay(peer)")
        XCTAssertEqual(Outcome(isDirect: false, derpRegion: "hkg").pathDescription, "DERP(hkg)")
        XCTAssertEqual(Outcome(isDirect: false, derpRegion: "#1").pathDescription, "DERP(#1)")
        XCTAssertEqual(Outcome(isDirect: false).pathDescription, "relayed")
    }
}
