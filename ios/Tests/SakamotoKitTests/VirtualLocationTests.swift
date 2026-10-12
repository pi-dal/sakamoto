import XCTest
@testable import SakamotoKit

final class VirtualLocationTests: XCTestCase {
    func testLocationIPCContainsCoordinatesButNoAuthority() throws {
        let settings = VirtualLocationSettings(enabled: true, latitude: -33.5, longitude: 151.25, accuracy: 15)
        let request = TunnelRequest.location(settings: settings)
        let data = try request.encode()
        XCTAssertEqual(try TunnelRequest(data: data), request)
        XCTAssertFalse(String(decoding: data, as: UTF8.self).contains("privateKey"))
        XCTAssertEqual(try TunnelRequest(data: TunnelRequest.location(settings: nil).encode()), .location(settings: nil))
    }
    func testInvalidCoordinatesCannotReachProvider() throws {
        for settings in [VirtualLocationSettings(latitude: .nan), VirtualLocationSettings(longitude: .infinity), VirtualLocationSettings(latitude: 91), VirtualLocationSettings(longitude: -181), VirtualLocationSettings(accuracy: 0), VirtualLocationSettings(accuracy: 1001)] {
            XCTAssertThrowsError(try TunnelRequest.location(settings: settings).encode())
        }
        let invalid = Data(#"{"action":"location","settingsJSON":"{\"enabled\":true,\"latitude\":91,\"longitude\":0,\"accuracy\":25}"}"#.utf8)
        XCTAssertThrowsError(try TunnelRequest(data: invalid))
    }
    func testSavedIntentDoesNotBecomeRuntimeEvidence() throws {
        let defaults = UserDefaults(suiteName: "sakamoto-location-test-\(UUID())")!
        defer { defaults.removeObject(forKey: VirtualLocationStore.key) }
        let settings = VirtualLocationSettings(enabled: true, latitude: 22.5, longitude: 114)
        try VirtualLocationStore.save(settings, defaults: defaults)
        XCTAssertEqual(VirtualLocationStore.load(defaults: defaults), settings)
        XCTAssertFalse(VirtualLocationSnapshot().enabled)
        defaults.set(Data("bad settings".utf8), forKey: VirtualLocationStore.key)
        XCTAssertFalse(VirtualLocationStore.load(defaults: defaults).enabled)
    }
    func testProviderEvidenceRoundTrips() throws {
        var evidence = VirtualLocationSnapshot(); evidence.enabled = true; evidence.requests = 2; evidence.patched = 1; evidence.tlsFailures = 1
        let response = TunnelResponse(ok: true, location: evidence)
        XCTAssertEqual(try TunnelResponse.decode(response.encode()).location, evidence)
    }
}
