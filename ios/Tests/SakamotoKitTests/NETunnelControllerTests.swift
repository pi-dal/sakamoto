import NetworkExtension
import XCTest

@testable import SakamotoKit
@testable import SakamotoNE

/// Compile-contract and pure-logic tests for the NE transport. Runtime
/// tunnel behavior (startVPNTunnel, provider messages) needs an
/// entitlement-bearing app target and is intentionally not exercised here.
final class NETunnelControllerTests: XCTestCase {
    func testProviderProtocolCarriesOptions() throws {
        let options = TunnelStartOptions(configContent: "{\"log\":{}}", locale: nil)
        let protocolConfiguration = NETunnelController.makeProviderProtocol(
            bundleIdentifier: "test.sakamoto.provider",
            options: options
        )
        XCTAssertEqual(
            protocolConfiguration.providerBundleIdentifier,
            "test.sakamoto.provider"
        )
        XCTAssertTrue(protocolConfiguration is NETunnelProviderProtocol)
        let recovered = TunnelStartOptions(
            providerConfiguration: protocolConfiguration.providerConfiguration ?? [:]
        )
        XCTAssertEqual(recovered, options, "cold provider launch must recover configContent")
    }

    func testServiceStateMapping() {
        XCTAssertEqual(NETunnelController.serviceState(for: .connected), .running)
        XCTAssertEqual(NETunnelController.serviceState(for: .connecting), .starting)
        XCTAssertEqual(NETunnelController.serviceState(for: .reasserting), .starting)
        XCTAssertEqual(NETunnelController.serviceState(for: .disconnecting), .stopping)
        XCTAssertEqual(NETunnelController.serviceState(for: .disconnected), .stopped)
        XCTAssertEqual(NETunnelController.serviceState(for: .invalid), .unavailable)
    }

    func testControllerDoesNotClaimCoreCommanding() {
        // Compile-time honesty check: NETunnelController implements only the
        // NE transport. Core actions (mode/node/URL test) are CoreCommanding
        // and must come from the Libbox bridge.
        let controller: TunnelControlling = NETunnelController(
            providerBundleIdentifier: "test.sakamoto.provider"
        )
        _ = controller
        XCTAssertFalse(
            controller is CoreCommanding,
            "NE provider messages cannot reach the core; this must never hold"
        )
    }
}
