import XCTest

@testable import SakamotoKit

final class ProviderIPCTests: XCTestCase {
    func testPingRoundTrip() throws {
        let data = try TunnelRequest.ping.encode()
        XCTAssertEqual(try TunnelRequest(data: data), .ping)
    }

    func testReloadConfigRoundTrip() throws {
        let request = TunnelRequest.reloadConfig(content: "{\"outbounds\":[]}")
        let data = try request.encode()
        XCTAssertEqual(try TunnelRequest(data: data), request)
    }

    func testEncodingIsDeterministicJSON() throws {
        let a = try TunnelRequest.reloadConfig(content: "x").encode()
        let b = try TunnelRequest.reloadConfig(content: "x").encode()
        XCTAssertEqual(a, b)
        let json = String(decoding: a, as: UTF8.self)
        XCTAssertTrue(json.contains("\"action\":\"reloadConfig\""))
        XCTAssertTrue(json.contains("\"configContent\":\"x\""))
    }

    func testUnknownActionIsLoud() {
        let data = Data("{\"action\":\"futureAction\"}".utf8)
        XCTAssertThrowsError(try TunnelRequest(data: data)) { error in
            guard case DecodingError.dataCorrupted = error else {
                return XCTFail("expected dataCorrupted, got \(error)")
            }
        }
    }

    func testMissingActionIsLoud() {
        XCTAssertThrowsError(try TunnelRequest(data: Data("{}".utf8)))
    }

    func testReloadConfigWithoutContentIsLoud() {
        let data = Data("{\"action\":\"reloadConfig\"}".utf8)
        XCTAssertThrowsError(try TunnelRequest(data: data))
    }

    // MARK: TunnelResponse

    func testResponseRoundTripWithState() throws {
        let response = TunnelResponse(
            ok: true,
            state: TunnelStateSnapshot(serviceState: .running, detail: nil)
        )
        let decoded = try TunnelResponse.decode(try response.encode())
        XCTAssertEqual(decoded, response)
        XCTAssertEqual(decoded.state?.serviceState, .running)
    }

    func testFailureResponse() throws {
        let response = TunnelResponse.failure("start service: boom")
        XCTAssertEqual(response.ok, false)
        XCTAssertEqual(response.error, "start service: boom")
        let decoded = try TunnelResponse.decode(try response.encode())
        XCTAssertEqual(decoded, response)
    }

    func testSnapshotServiceStateIsStrictlyDecoded() throws {
        let good = Data("{\"ok\":true,\"state\":{\"serviceState\":\"Running\"}}".utf8)
        XCTAssertEqual(try TunnelResponse.decode(good).state?.serviceState, .running)

        let unknown = Data("{\"ok\":true,\"state\":{\"serviceState\":\"Exploded\"}}".utf8)
        XCTAssertThrowsError(
            try TunnelResponse.decode(unknown),
            "an unknown ServiceState must fail loudly, not normalize"
        )
    }
}

final class TunnelStartOptionsTests: XCTestCase {
    private let config = "{\"log\":{\"level\":\"info\"}}"

    func testProviderConfigurationRoundTrip() {
        let options = TunnelStartOptions(configContent: config, locale: "zh-CN")
        let recovered = TunnelStartOptions(
            providerConfiguration: options.providerConfiguration
        )
        XCTAssertEqual(recovered, options)
    }

    func testProviderConfigurationWithoutConfigContentIsNil() {
        XCTAssertNil(TunnelStartOptions(providerConfiguration: ["locale": "zh-CN"]))
        XCTAssertNil(TunnelStartOptions(providerConfiguration: [:]))
    }

    func testStartTunnelOptionsRoundTrip() {
        let options = TunnelStartOptions(configContent: config)
        let dict = options.startTunnelOptions
        XCTAssertEqual(dict["configContent"] as? String, config)
        XCTAssertNil(dict["locale"])
        XCTAssertEqual(TunnelStartOptions(startTunnelOptions: dict), options)
    }

    func testStartTunnelOptionsWithoutConfigContentIsNil() {
        XCTAssertNil(TunnelStartOptions(startTunnelOptions: ["locale": "en" as NSString]))
    }
}
