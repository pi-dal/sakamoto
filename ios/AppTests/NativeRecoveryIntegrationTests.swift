import XCTest
import Libbox
import SakamotoKit
@testable import Sakamoto

/// Bound shared recovery policy + real native service/command transport. The
/// fixture uses loopback only and never starts or stops an installed VPN.
@MainActor
final class NativeRecoveryIntegrationTests: XCTestCase {
    func testVerifiedRepairRetainsCoreAndStopRejectsLateStartup() async throws {
        XCTAssertEqual(LibboxVersion(), "1.14.2-sakamoto")
        XCTAssertEqual(MobilecoreRecoveryProtocol(), 1)
        try AppServiceSetup.apply()
        defer { try? AppServiceSetup.apply() }
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let setup = LibboxSetupOptions()
        setup.basePath = root.path; setup.workingPath = root.path; setup.tempPath = root.path
        setup.commandServerListenPort = 49291
        setup.commandServerSecret = UUID().uuidString
        var error: NSError?
        LibboxSetup(setup, &error)
        if let error { throw error }
        let session = try XCTUnwrap(MobilecoreNewRecoverySession(root.path, &error))
        if let error { throw error }
        let ticket = try checkedNativeValue { session.begin(true, error: $0) }
        let content = """
        {"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"MainProxy","outbounds":["direct"]}],"route":{"rules":[{"clash_mode":"global","action":"route","outbound":"MainProxy"},{"clash_mode":"direct","action":"route","outbound":"direct"}],"final":"MainProxy"},"experimental":{"clash_api":{"default_mode":"rule"}}}
        """
        try session.prepare(ticket, content: content, metadata: "{}", workingDirectory: root.path)
        let platform = ObservationTestPlatform()
        let server = try XCTUnwrap(LibboxNewCommandServer(platform, platform, &error))
        if let error { throw error }
        let commanding = LibboxCoreCommanding()
        defer { commanding.stop(); try? server.closeService(); server.close() }
        try server.start()
        try server.startOrReloadService(content, options: LibboxOverrideOptions())
        let instance = server.runningInstanceID()
        XCTAssertGreaterThan(instance, 0)
        try session.ready(ticket, instance: String(instance), version: LibboxVersion())
        commanding.start()
        let deadline = Date().addingTimeInterval(6)
        while !commanding.isAvailable && Date() < deadline { try await Task.sleep(nanoseconds: 25_000_000) }
        XCTAssertTrue(commanding.isAvailable)
        let verifiedTransport = commanding.isAvailable
        let decision = try JSONDecoder().decode(CoreRepairResult.self, from: Data(MobilecoreRepairDecisionJSON("Running", server.isServiceRunning(), false, false, true).utf8))
        XCTAssertEqual(decision.state, "healthy"); XCTAssertEqual(decision.action, "none"); XCTAssertTrue(decision.verified)
        XCTAssertEqual(server.runningInstanceID(), instance)
        try await commanding.setClashMode(.direct)
        XCTAssertThrowsError(try session.stable(ticket, instance: String(instance)))
        let gate = try XCTUnwrap(MobilecoreNewRuntimeGate())
        let late = gate.begin()
        gate.stop()
        XCTAssertFalse(gate.ready(late)); XCTAssertFalse(gate.current(late))
        try session.stop()
        XCTAssertThrowsError(try session.ready(ticket, instance: "late", version: LibboxVersion()))
        XCTAssertThrowsError(try checkedNativeValue { session.begin(false, error: $0) })
        XCTAssertThrowsError(try checkedNativeValue { session.begin(true, error: $0) })
        try server.closeService()
        let replacement = try XCTUnwrap(MobilecoreNewRecoverySession(root.path, &error))
        if let error { throw error }
        let successor = try checkedNativeValue { replacement.begin(true, error: $0) }
        XCTAssertNotEqual(successor, ticket)
        try replacement.failed(successor)
        let fallback = try checkedNativeValue { replacement.beginFallback($0) }
        try replacement.failed(fallback)
        XCTAssertThrowsError(try checkedNativeValue { replacement.begin(false, error: $0) })
        let evidence: [String: Any] = ["healthy_retained_instance": instance, "command_transport_verified": verifiedTransport, "late_start_rejected": true, "manual_stop_hold": true, "failed_fallback_blocks": true, "recovery_protocol": MobilecoreRecoveryProtocol(), "native_version": LibboxVersion()]
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: evidence, options: [.prettyPrinted, .sortedKeys]), uniformTypeIdentifier: "public.json")
        attachment.name = "mobile-core-recovery"; attachment.lifetime = .keepAlways; add(attachment)
    }
}
