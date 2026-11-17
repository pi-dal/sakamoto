import XCTest
import Libbox
import SakamotoKit
@testable import Sakamoto

/// Bound shared recovery policy + real native service/command transport. The
/// fixture uses loopback only and never starts or stops an installed VPN.
@MainActor
final class NativeRecoveryIntegrationTests: XCTestCase {
    func testOneAutomaticRelaunchWaitsForAdmissionAndPreservesCrashBudget() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        func session() throws -> MobilecoreRecoverySession {
            var error: NSError?
            let value = try XCTUnwrap(MobilecoreNewRecoverySession(root.path, &error))
            if let error { throw error }
            return value
        }
        let predecessor = try session()
        let original = try checkedNativeValue { predecessor.begin(true, error: $0) }
        try predecessor.ready(original, instance: "unclean-predecessor", version: LibboxVersion())
        var waits: [Double] = []
        for expected in [2.0, 4.0, 8.0] {
            // Recreate the bridge owner, as happens when NetworkExtension
            // launches a new process over an unclean/failed private journal.
            let replacement = try session()
            let started = ProcessInfo.processInfo.systemUptime
            let ticket = try await TunnelRecoveryAdmission.begin(manual: false, isActive: { true },
                begin: { manual in try checkedNativeValue { replacement.begin(manual, error: $0) } },
                status: { try checkedNativeValue { replacement.statusJSON($0) } })
            let elapsed = ProcessInfo.processInfo.systemUptime - started
            waits.append(elapsed)
            XCTAssertGreaterThanOrEqual(elapsed, expected - 0.05)
            XCTAssertLessThan(elapsed, expected + 2)
            XCTAssertFalse(ticket.isEmpty)
            try replacement.ready(ticket, instance: "replacement", version: LibboxVersion())
            try replacement.failed(ticket)
        }
        let blocked = try session()
        let blockedAt = ProcessInfo.processInfo.systemUptime
        do {
            _ = try await TunnelRecoveryAdmission.begin(manual: false, isActive: { true },
                begin: { manual in try checkedNativeValue { blocked.begin(manual, error: $0) } },
                status: { try checkedNativeValue { blocked.statusJSON($0) } })
            XCTFail("Exhausted crash budget was reset")
        } catch { XCTAssertLessThan(ProcessInfo.processInfo.systemUptime - blockedAt, 1) }
        let repaired = try checkedNativeValue { blocked.begin(true, error: $0) }
        try blocked.failed(repaired)
        let stopping = try session()
        let gate = TunnelOperationGate()
        gate.start()
        let pending = Task {
            try await TunnelRecoveryAdmission.begin(manual: false, isActive: { gate.state != .stopping },
                begin: { manual in try checkedNativeValue { stopping.begin(manual, error: $0) } },
                status: { try checkedNativeValue { stopping.statusJSON($0) } })
        }
        try await Task.sleep(nanoseconds: 150_000_000)
        gate.stop(); try stopping.stop()
        do { _ = try await pending.value; XCTFail("Stop did not supersede admission") }
        catch is CancellationError { }
        let state = try JSONSerialization.jsonObject(with: Data(checkedNativeValue { stopping.statusJSON($0) }.utf8)) as? [String: Any]
        XCTAssertEqual(state?["held"] as? Bool, true)
        XCTAssertEqual(state?["attempts"] as? Int, 0)
        let evidence: [String: Any] = ["passed": true, "single_relaunch_wait_seconds": waits,
            "three_retry_budget_preserved": true, "blocked_fails_immediately": true,
            "stop_during_backoff": true, "native_version": LibboxVersion()]
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: evidence, options: [.prettyPrinted, .sortedKeys]), uniformTypeIdentifier: "public.json")
        attachment.name = "ios-automatic-relaunch"; attachment.lifetime = .keepAlways; add(attachment)
    }

    func testAdmissionCancellationAndInvalidJournalFailWithoutStartingCore() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        func session() throws -> MobilecoreRecoverySession {
            var error: NSError?
            let value = try XCTUnwrap(MobilecoreNewRecoverySession(root.path, &error))
            if let error { throw error }
            return value
        }
        let owner = try session()
        let ticket = try checkedNativeValue { owner.begin(true, error: $0) }
        try owner.failed(ticket)
        let canceled = Task {
            try await TunnelRecoveryAdmission.begin(manual: false, isActive: { true },
                begin: { manual in try checkedNativeValue { owner.begin(manual, error: $0) } },
                status: { try checkedNativeValue { owner.statusJSON($0) } })
        }
        try await Task.sleep(nanoseconds: 150_000_000)
        canceled.cancel()
        do { _ = try await canceled.value; XCTFail("Canceled startup was admitted") }
        catch is CancellationError { }
        let journal = root.appendingPathComponent("recovery/mobile/mobile-runtime.json")
        let before = try Data(contentsOf: journal)
        var state = try XCTUnwrap(JSONSerialization.jsonObject(with: before) as? [String: Any])
        XCTAssertEqual(state["attempts"] as? Int, 0)
        state["retry_at"] = ISO8601DateFormatter().string(from: Date().addingTimeInterval(86_400))
        try JSONSerialization.data(withJSONObject: state).write(to: journal, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: journal.path)
        let distant = try session()
        _ = try checkedNativeValue { distant.statusJSON($0) }
        let started = ProcessInfo.processInfo.systemUptime
        do {
            _ = try await TunnelRecoveryAdmission.begin(manual: false, isActive: { true },
                begin: { manual in try checkedNativeValue { distant.begin(manual, error: $0) } },
                status: { try checkedNativeValue { distant.statusJSON($0) } })
            XCTFail("Implausible deadline was accepted")
        } catch { XCTAssertLessThan(ProcessInfo.processInfo.systemUptime - started, 1) }
        let corrupt = Data("{broken".utf8)
        try corrupt.write(to: journal, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: journal.path)
        let replacement = try session()
        do {
            _ = try await TunnelRecoveryAdmission.begin(manual: false, isActive: { true },
                begin: { manual in try checkedNativeValue { replacement.begin(manual, error: $0) } },
                status: { try checkedNativeValue { replacement.statusJSON($0) } })
            XCTFail("Corrupt journal admitted automatic start")
        } catch { XCTAssertEqual(try Data(contentsOf: journal), corrupt) }
        let repaired = try await TunnelRecoveryAdmission.begin(manual: true, isActive: { true },
            begin: { manual in try checkedNativeValue { replacement.begin(manual, error: $0) } },
            status: { try checkedNativeValue { replacement.statusJSON($0) } })
        XCTAssertFalse(repaired.isEmpty)
        let attachment = XCTAttachment(string: "{\"passed\":true,\"task_cancellation\":true,\"distant_deadline_rejected\":true,\"corruption_preserved\":true,\"explicit_repair\":true}")
        attachment.name = "ios-admission-boundaries"; attachment.lifetime = .keepAlways; add(attachment)
    }

    func testDisconnectEvidenceSurvivesSuccessfulStartup() throws {
        let suite = "sakamoto-disconnect-scenario-" + UUID().uuidString
        let writer = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { writer.removePersistentDomain(forName: suite) }
        TunnelDiagnostics.record(stage: "VPN stopped", error: NSError(domain: "NEProviderStopReason", code: 4,
            userInfo: [NSLocalizedDescriptionKey: "Unrecoverable network change https://private.example/token?secret=fixture"]), defaults: writer)
        TunnelDiagnostics.clear(defaults: writer)
        let reader = try XCTUnwrap(UserDefaults(suiteName: suite))
        XCTAssertNil(TunnelDiagnostics.latest(defaults: reader))
        let retained = try XCTUnwrap(TunnelDiagnostics.history(defaults: reader).last)
        XCTAssertEqual(retained.stage, "VPN stopped")
        XCTAssertNil(retained.message.range(of: "private.example"))
        for index in 0..<50 {
            TunnelDiagnostics.recordEvent(stage: "Network reset", message: "fixture \(index)", defaults: writer)
        }
        let history = TunnelDiagnostics.history(defaults: reader)
        XCTAssertEqual(history.count, 24)
        XCTAssertEqual(history.last?.message, "fixture 49")
        let attachment = XCTAttachment(data: try JSONEncoder().encode(history), uniformTypeIdentifier: "public.json")
        attachment.name = "ios-disconnect-history"; attachment.lifetime = .keepAlways; add(attachment)
    }

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
