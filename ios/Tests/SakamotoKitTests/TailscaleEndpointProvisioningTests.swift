import XCTest
@testable import SakamotoKit

// Tailscale endpoint provisioning: the "enable built-in Tailscale" flow
// must write minimal legal endpoint JSON (docs/configuration/endpoint/
// tailscale.md, v1.14.2), validate operator input, and never touch the
// auth key (Keychain-only, injected at start time).

final class TailscaleEndpointProvisioningTests: XCTestCase {
    private let plainConfig = """
    {
      "log": {"level": "info"},
      "outbounds": [{"type": "direct", "tag": "direct"}, {"type": "selector", "tag": "MainProxy"}],
      "route": {"rules": [], "final": "direct"}
    }
    """

    func testEnableAppendsMinimalEndpoint() throws {
        let enabled = try TailscaleEndpointProvisioning.enable(options: TailscaleEndpointOptions(), in: plainConfig)
        XCTAssertTrue(TailscaleEndpointProvisioning.isEnabled(in: enabled))
        let root = try XCTUnwrap(JSONValueFactory.parse(enabled))
        let endpoints = try XCTUnwrap(root["endpoints"]?.arrayValue)
        XCTAssertEqual(endpoints.count, 1)
        XCTAssertEqual(endpoints[0]["type"]?.stringValue, "tailscale")
        XCTAssertEqual(endpoints[0]["tag"]?.stringValue, TailscaleEndpointOptions.defaultTag)
        XCTAssertNil(endpoints[0]["hostname"], "unset options must stay absent (core defaults)")
        XCTAssertEqual(endpoints[0]["accept_routes"]?.boolValue, false)
    }

    func testEnableWithFullOptions() throws {
        let options = TailscaleEndpointOptions(
            hostname: "iphone-pi", acceptRoutes: true,
            exitNode: "home-exit", exitNodeAllowLANAccess: true
        )
        let enabled = try TailscaleEndpointProvisioning.enable(options: options, in: plainConfig)
        let described = try XCTUnwrap(TailscaleEndpointProvisioning.describe(in: enabled))
        XCTAssertEqual(described, options)
    }

    func testEnableUpdatesExistingEndpointInPlace() throws {
        let first = try TailscaleEndpointProvisioning.enable(options: TailscaleEndpointOptions(), in: plainConfig)
        let updated = try TailscaleEndpointProvisioning.enable(
            options: TailscaleEndpointOptions(hostname: "renamed"), in: first
        )
        let root = try XCTUnwrap(JSONValueFactory.parse(updated))
        let endpoints = try XCTUnwrap(root["endpoints"]?.arrayValue)
        XCTAssertEqual(endpoints.count, 1, "enable is an update for an existing endpoint, not a duplicate")
        XCTAssertEqual(endpoints[0]["hostname"]?.stringValue, "renamed")
    }

    func testDisableRemovesOnlyTailscaleEndpoints() throws {
        var config = plainConfig
        // A non-tailscale endpoint must survive disable.
        let withWireGuard = try TailscaleEndpointProvisioning.enable(options: TailscaleEndpointOptions(), in: config)
        var parsed = try XCTUnwrap(JSONValueFactory.parse(withWireGuard))
        _ = parsed
        config = withWireGuard
        // Hand-add a wireguard endpoint alongside.
        let data = try XCTUnwrap(config.data(using: .utf8))
        var root = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        var endpoints = try XCTUnwrap(root["endpoints"] as? [[String: Any]])
        endpoints.append(["type": "wireguard", "tag": "wg-ep"])
        root["endpoints"] = endpoints
        let serialized = String(data: try JSONSerialization.data(withJSONObject: root), encoding: .utf8)!
        let disabled = try TailscaleEndpointProvisioning.disable(in: serialized)
        XCTAssertFalse(TailscaleEndpointProvisioning.isEnabled(in: disabled))
        let after = try XCTUnwrap(JSONValueFactory.parse(disabled))
        XCTAssertEqual(after["endpoints"]?.arrayValue?.first?["type"]?.stringValue, "wireguard")
    }

    func testDisableWithoutEndpointsIsNoOp() throws {
        XCTAssertEqual(try TailscaleEndpointProvisioning.disable(in: plainConfig), plainConfig)
    }

    func testValidationRejectsBadInput() {
        XCTAssertThrowsError(try TailscaleEndpointProvisioning.enable(
            options: TailscaleEndpointOptions(tag: "has space"), in: plainConfig
        ))
        XCTAssertThrowsError(try TailscaleEndpointProvisioning.enable(
            options: TailscaleEndpointOptions(tag: ""), in: plainConfig
        ))
        XCTAssertThrowsError(try TailscaleEndpointProvisioning.enable(
            options: TailscaleEndpointOptions(hostname: "under_score"), in: plainConfig
        ))
        XCTAssertThrowsError(try TailscaleEndpointProvisioning.enable(
            options: TailscaleEndpointOptions(hostname: "-lead"), in: plainConfig
        ))
        XCTAssertThrowsError(try TailscaleEndpointProvisioning.enable(
            options: TailscaleEndpointOptions(exitNode: "bad node\nname"), in: plainConfig
        ))
    }

    func testTagCollisionIsRejected() throws {
        let withEndpoint = try TailscaleEndpointProvisioning.enable(options: TailscaleEndpointOptions(), in: plainConfig)
        // Colliding with the existing tailscale tag via a different tag that
        // matches an outbound tag must fail.
        XCTAssertThrowsError(try TailscaleEndpointProvisioning.enable(
            options: TailscaleEndpointOptions(tag: "MainProxy"), in: withEndpoint
        )) { error in
            XCTAssertEqual(error as? TailscaleEndpointError, .tagCollision("MainProxy"))
        }
    }

    func testAuthKeyInjectionInterplay() throws {
        // The full on-device chain: enable the endpoint, then the start-time
        // injection can attach the Keychain key. The injection WITHOUT an
        // endpoint stays a blocking error.
        let withEndpoint = try TailscaleEndpointProvisioning.enable(options: TailscaleEndpointOptions(), in: plainConfig)
        let injected = try TailscaleConfigInjection.inject(authKey: "tsauth-secret", into: withEndpoint)
        let root = try XCTUnwrap(JSONValueFactory.parse(injected))
        XCTAssertEqual(root["endpoints"]?.arrayValue?.first?["auth_key"]?.stringValue, "tsauth-secret")

        XCTAssertThrowsError(try TailscaleConfigInjection.inject(authKey: "tsauth-secret", into: plainConfig)) { error in
            XCTAssertEqual(error as? TailscaleConfigError, .noTailscaleEndpoint)
        }
        // No key + no endpoint: unchanged, no error (Tailscale simply unused).
        XCTAssertEqual(try TailscaleConfigInjection.inject(authKey: "", into: plainConfig), plainConfig)
    }
}
