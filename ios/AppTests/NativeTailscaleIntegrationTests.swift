import XCTest
import Libbox
import SakamotoKit
import Darwin
@testable import Sakamoto

/// Real embedded endpoint -> libbox status stream -> the app's controller.
/// Failure cases: initial state misclassified as unknown, missed login link,
/// stale attachment after page dismissal, foreground resubscription failing.
@MainActor
final class NativeTailscaleIntegrationTests: XCTestCase {
    func testPendingControlStateSurvivesStatusReattachment() async throws {
        try AppServiceSetup.apply()
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root); try? AppServiceSetup.apply() }
        let setup = LibboxSetupOptions()
        setup.basePath = root.path; setup.workingPath = root.path; setup.tempPath = root.path
        setup.commandServerListenPort = try freePort()
        setup.commandServerSecret = UUID().uuidString
        var error: NSError?
        LibboxSetup(setup, &error)
        if let error { throw error }
        let platform = ObservationTestPlatform()
        let server = try XCTUnwrap(LibboxNewCommandServer(platform, platform, &error))
        if let error { throw error }
        let commanding = LibboxCoreCommanding()
        let controller = TailscaleController(commanding: commanding)
        defer { controller.cancel(); commanding.stop(); try? server.closeService(); server.close() }
        try server.start()
        // No external account or public control connection. The refused local
        // port leaves the real backend waiting for its first control response.
        let stateDirectory = root.appendingPathComponent("tailscale")
        try FileManager.default.createDirectory(at: stateDirectory, withIntermediateDirectories: true)
        // Disposable identity, never connected to a real tailnet. Existing
        // identities wait in NoState while control is unavailable.
        let prefs: [String: Any] = ["WantRunning": true, "ControlURL": "http://127.0.0.1:1",
            "Config": ["PrivateNodeKey": "privkey:" + String(repeating: "01", count: 32),
                       "UserProfile": ["ID": 1, "LoginName": "fixture@example.invalid"]]]
        let raw = try JSONSerialization.data(withJSONObject: prefs)
        let profile: [String: Any] = ["ID": "test", "Key": "profile-test", "Name": "fixture",
            "ControlURL": "http://127.0.0.1:1", "UserProfile": ["ID": 1, "LoginName": "fixture@example.invalid"]]
        let known = try JSONSerialization.data(withJSONObject: ["test": profile])
        let current = Data("profile-test".utf8)
        try JSONSerialization.data(withJSONObject: ["profile-test": raw.base64EncodedString(),
            "_profiles": known.base64EncodedString(), "_current-profile": current.base64EncodedString()])
            .write(to: stateDirectory.appendingPathComponent("tailscaled.state"))
        let config = #"{"log":{"level":"info"},"endpoints":[{"type":"tailscale","tag":"tailnet","control_url":"http://127.0.0.1:1"}],"outbounds":[{"type":"direct","tag":"direct"}]}"#
        try server.startOrReloadService(config, options: LibboxOverrideOptions())
        let instance = server.runningInstanceID()
        commanding.start(); controller.subscribe()
        try await eventually { controller.endpoints.first?.backendState.wireString == "NoState" }
        let pending = try XCTUnwrap(controller.endpoints.first)
        if case .unrecognized = pending.backendState { XCTFail("NoState must be recognized as initialization") }
        XCTAssertFalse(pending.backendState.running)
        controller.cancel()
        XCTAssertTrue(controller.endpoints.isEmpty)
        controller.subscribe()
        try await eventually { controller.endpoints.first?.backendState.wireString == "NoState" }
        XCTAssertEqual(server.runningInstanceID(), instance)
        let report: [String: Any] = ["wire_state": "NoState", "reattached": true, "same_core": true]
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys]), uniformTypeIdentifier: "public.json")
        attachment.name = "tailscale-native-pending-control"; attachment.lifetime = .keepAlways; add(attachment)
    }

    // Failure cases: runtime preparation misses old saved configs, duplicate
    // shortcuts after reconnect, or endpoint removal leaves dangling routes.
    func testPreparedTailnetConfigurationCanBeReappliedAndDisabled() throws {
        try AppServiceSetup.apply()
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let saved = try TailscaleEndpointProvisioning.standaloneConfiguration(options: TailscaleEndpointOptions())
        let prepared = try ProviderExperimentRunner.prepared(TunnelStartOptions(configContent: saved), root: root).0
        let reapplied = try ProviderExperimentRunner.prepared(TunnelStartOptions(configContent: prepared), root: root).0
        XCTAssertEqual(prepared, reapplied)
        var error: NSError?
        XCTAssertTrue(LibboxCheckConfig(prepared, &error))
        if let error { throw error }
        let document = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(prepared.utf8)) as? [String: Any])
        let route = try XCTUnwrap(document["route"] as? [String: Any])
        let rules = try XCTUnwrap(route["rules"] as? [[String: Any]])
        let sniffIndex = try XCTUnwrap(rules.firstIndex { $0["action"] as? String == "sniff" })
        XCTAssertTrue(rules[..<sniffIndex].contains { $0["outbound"] as? String == TailscaleEndpointOptions.defaultTag && $0["network"] as? String == "tcp" })
        let disabled = try TailscaleEndpointProvisioning.disable(in: prepared)
        error = nil
        XCTAssertTrue(LibboxCheckConfig(disabled, &error))
        if let error { throw error }
        let reenabled = try TailscaleEndpointProvisioning.enable(options: TailscaleEndpointOptions(), in: disabled)
        let restored = try ProviderExperimentRunner.prepared(TunnelStartOptions(configContent: reenabled), root: root).0
        error = nil
        XCTAssertTrue(LibboxCheckConfig(restored, &error))
        if let error { throw error }
        let report: [String: Any] = ["idempotent": prepared == reapplied, "shortcut_before_sniff": true, "disabled_core_config_valid": true, "reenabled_core_config_valid": true]
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys]), uniformTypeIdentifier: "public.json")
        attachment.name = "tailscale-native-packet-configuration"; attachment.lifetime = .keepAlways; add(attachment)
    }

    private func freePort() throws -> Int32 {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw POSIXError(.EIO) }
        defer { Darwin.close(descriptor) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        var size = socklen_t(MemoryLayout<sockaddr_in>.size)
        let result = withUnsafeMutablePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { socketAddress in
                guard Darwin.bind(descriptor, socketAddress, size) == 0 else { return Int32(-1) }
                return getsockname(descriptor, socketAddress, &size)
            }
        }
        guard result == 0 else { throw POSIXError(.EIO) }
        return Int32(UInt16(bigEndian: address.sin_port))
    }

    private func eventually(_ condition: () -> Bool) async throws {
        let end = Date().addingTimeInterval(8)
        while !condition() && Date() < end { try await Task.sleep(nanoseconds: 25_000_000) }
        XCTAssertTrue(condition(), "Embedded Tailscale status did not arrive")
    }
}
