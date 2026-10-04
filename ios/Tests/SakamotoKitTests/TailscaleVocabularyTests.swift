import XCTest
@testable import SakamotoKit

// Tailscale vocabulary + config-injection contract. These pin the Swift
// mapping to the exact wire strings of the Libbox v1.14.2 Tailscale status
// stream (BackendState values come from tailscale/ipn.State.String()).

final class TailscaleVocabularyTests: XCTestCase {
    func testBackendStateWireStrings() {
        XCTAssertEqual(TailscaleBackendState(backendState: "Stopped"), .stopped)
        XCTAssertEqual(TailscaleBackendState(backendState: "Starting"), .starting)
        XCTAssertEqual(TailscaleBackendState(backendState: "NeedsLogin"), .needsLogin)
        XCTAssertEqual(TailscaleBackendState(backendState: "NeedsMachineAuth"), .needsMachineAuth)
        XCTAssertEqual(TailscaleBackendState(backendState: "Running"), .running)
    }

    func testBackendStateWhitespaceTolerant() {
        XCTAssertEqual(TailscaleBackendState(backendState: "  Running "), .running)
    }

    func testUnrecognizedStateCarriedVerbatim() {
        // A future core may add states; the mapping must surface the raw
        // value, never guess it into a known bucket.
        let state = TailscaleBackendState(backendState: "NeedsFutureAuth")
        XCTAssertEqual(state, .unrecognized("NeedsFutureAuth"))
        XCTAssertEqual(state.wireString, "NeedsFutureAuth")
        XCTAssertFalse(state.running)
        XCTAssertFalse(state.needsLoginFlow)
    }

    func testRunningAndLoginFlags() {
        XCTAssertTrue(TailscaleBackendState(backendState: "Running").running)
        XCTAssertFalse(TailscaleBackendState(backendState: "NeedsLogin").running)
        XCTAssertTrue(TailscaleBackendState(backendState: "NeedsLogin").needsLoginFlow)
        XCTAssertTrue(TailscaleBackendState(backendState: "NeedsMachineAuth").needsLoginFlow)
        XCTAssertFalse(TailscaleBackendState(backendState: "Running").needsLoginFlow)
    }

    func testRoundTripKnownStates() {
        for wire in ["Stopped", "Starting", "NeedsLogin", "NeedsMachineAuth", "Running"] {
            XCTAssertEqual(TailscaleBackendState(backendState: wire).wireString, wire)
        }
    }

    func testExitNodeCandidatesExcludeSelfAndCurrent() {
        let selfPeer = TailscalePeerSummary(
            stableID: "self", hostName: "self", dnsName: "self.tailnet.ts.net.", os: "ios",
            tailscaleIPs: ["100.64.0.1"], online: true, active: true, expired: false,
            exitNode: false, exitNodeOption: true, lastSeenUnixSeconds: 0
        )
        let currentExit = TailscalePeerSummary(
            stableID: "exit0", hostName: "exit0", dnsName: "exit0.tailnet.ts.net.", os: "linux",
            tailscaleIPs: ["100.64.0.2"], online: true, active: true, expired: false,
            exitNode: true, exitNodeOption: true, lastSeenUnixSeconds: 0
        )
        let candidate = TailscalePeerSummary(
            stableID: "exit1", hostName: "exit1", dnsName: "exit1.tailnet.ts.net.", os: "linux",
            tailscaleIPs: ["100.64.0.3"], online: true, active: false, expired: false,
            exitNode: false, exitNodeOption: true, lastSeenUnixSeconds: 0
        )
        let plain = TailscalePeerSummary(
            stableID: "laptop", hostName: "laptop", dnsName: "laptop.tailnet.ts.net.", os: "macOS",
            tailscaleIPs: ["100.64.0.4"], online: true, active: false, expired: false,
            exitNode: false, exitNodeOption: false, lastSeenUnixSeconds: 0
        )
        let endpoint = TailscaleEndpointSummary(
            endpointTag: "ts", backendState: .running, networkName: "tailnet.ts.net.",
            magicDNSSuffix: "tailnet.ts.net.", authURL: "",
            selfPeer: selfPeer, exitNodePeer: currentExit,
            peers: [selfPeer, currentExit, candidate, plain]
        )
        XCTAssertEqual(endpoint.exitNodeCandidates.map { $0.stableID }, ["exit1"])
    }

    func testCapabilitiesAreExplicit() {
        // The supported list is exactly what Libbox v1.14.2 exposes; the
        // unsupported list must stay populated so the UI can explain absent
        // features instead of hiding them.
        XCTAssertEqual(
            Set(TailscaleCapabilities.supportedActions.map { $0.rawValue }),
            ["observe-status", "set-exit-node", "clear-exit-node-selection", "logout", "ping-peer"]
        )
        XCTAssertTrue(TailscaleCapabilities.unsupported.contains { $0.capability == .taildrop })
        XCTAssertTrue(TailscaleCapabilities.unsupported.contains { $0.capability == .tailscaleSSH })
        XCTAssertTrue(TailscaleCapabilities.unsupported.contains { $0.capability == .externalCLIProbe })
        // Every capability in the unsupported list must carry a reason.
        XCTAssertTrue(TailscaleCapabilities.unsupported.allSatisfy { !$0.reason.isEmpty })
    }

    func testPeerDisplayNamePrefersDNSLabel() {
        let peer = TailscalePeerSummary(
            stableID: "s", hostName: "bare-host", dnsName: "bare-host.tailnet.ts.net.", os: "ios",
            tailscaleIPs: [], online: false, active: false, expired: false,
            exitNode: false, exitNodeOption: false, lastSeenUnixSeconds: 0
        )
        XCTAssertEqual(peer.displayName, "bare-host")
        let noDNS = TailscalePeerSummary(
            stableID: "s2", hostName: "bare-host", dnsName: "", os: "ios",
            tailscaleIPs: [], online: false, active: false, expired: false,
            exitNode: false, exitNodeOption: false, lastSeenUnixSeconds: 0
        )
        XCTAssertEqual(noDNS.displayName, "bare-host")
    }
}

final class TailscaleConfigInjectionTests: XCTestCase {
    private let config = """
    {
      "endpoints": [
        {"type": "tailscale", "state_directory": "ts"},
        {"type": "another"}
      ],
      "route": {"rules": []}
    }
    """

    func testInjectsIntoTailscaleEndpointOnly() throws {
        let injected = try TailscaleConfigInjection.inject(authKey: "tsauth-key-1", into: config)
        let data = try XCTUnwrap(injected.data(using: .utf8))
        let object = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let endpoints = try XCTUnwrap(object["endpoints"] as? [[String: Any]])
        XCTAssertEqual(endpoints[0]["auth_key"] as? String, "tsauth-key-1")
        XCTAssertEqual(endpoints[0]["type"] as? String, "tailscale")
        XCTAssertNil(endpoints[1]["auth_key"])
        // Non-endpoint parts preserved.
        XCTAssertNotNil(object["route"])
    }

    func testKeychainWinsOverConfigTextKey() throws {
        let withKey = """
        {"endpoints": [{"type": "tailscale", "auth_key": "stale"}]}
        """
        let injected = try TailscaleConfigInjection.inject(authKey: "fresh", into: withKey)
        XCTAssertTrue(injected.contains("\"fresh\""))
        XCTAssertFalse(injected.contains("stale"))
    }

    func testEmptyKeyReturnsConfigUnchanged() throws {
        let out = try TailscaleConfigInjection.inject(authKey: "", into: config)
        XCTAssertEqual(out, config)
    }

    func testRejectsConfigWithoutTailscaleEndpoint() {
        XCTAssertThrowsError(try TailscaleConfigInjection.inject(
            authKey: "k", into: "{\"endpoints\": [{\"type\": \"wireguard\"}]}"
        )) { error in
            XCTAssertEqual(error as? TailscaleConfigError, .noTailscaleEndpoint)
        }
        XCTAssertThrowsError(try TailscaleConfigInjection.inject(
            authKey: "k", into: "{\"route\": {}}"
        )) { error in
            XCTAssertEqual(error as? TailscaleConfigError, .noTailscaleEndpoint)
        }
    }

    func testRejectsNonObjectRoot() {
        XCTAssertThrowsError(try TailscaleConfigInjection.inject(authKey: "k", into: "[1,2]")) { error in
            XCTAssertEqual(error as? TailscaleConfigError, .rootNotObject)
        }
    }

    func testRejectsMalformedJSON() {
        XCTAssertThrowsError(try TailscaleConfigInjection.inject(authKey: "k", into: "{not json"))
    }
}
