import XCTest
import Libbox
import SakamotoKit
@testable import Sakamoto

@MainActor
final class NativeNetworkRecoveryIntegrationTests: XCTestCase {
    func testSystemStopsDoNotBecomeCrashesInTheNativeJournal() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        func owner() throws -> MobilecoreRecoverySession {
            var error: NSError?
            let result = try XCTUnwrap(MobilecoreNewRecoverySession(root.path, &error))
            if let error { throw error }
            return result
        }
        var current = try owner()
        var ticket = try checkedNativeValue { current.begin(true, error: $0) }
        let systemReasons = [3, 4, 8, 11, 12, 13, 15, 16]
        for reason in systemReasons + systemReasons {
            XCTAssertEqual(TunnelStopPolicy.action(reason: reason), .suspend)
            try current.ready(ticket, instance: "fixture", version: LibboxVersion())
            try current.suspend(ticket)
            XCTAssertThrowsError(try current.failed(ticket))
            current = try owner()
            ticket = try checkedNativeValue { current.begin(false, error: $0) }
            let state = try JSONSerialization.jsonObject(with: Data(checkedNativeValue { current.statusJSON($0) }.utf8)) as? [String: Any]
            XCTAssertEqual(state?["attempts"] as? Int, 0)
        }
        for reason in [1, 5, 9, 10] { XCTAssertEqual(TunnelStopPolicy.action(reason: reason), .hold) }
        for reason in [0, 2, 6, 7, 14, 17, 99] { XCTAssertEqual(TunnelStopPolicy.action(reason: reason), .failure) }
        try current.stop()
        let held = try owner()
        XCTAssertThrowsError(try checkedNativeValue { held.begin(false, error: $0) })
        let evidence: [String: Any] = ["passed": true, "system_stop_rounds": 16, "budget_unchanged": true,
            "manual_hold": true, "failure_reasons": [0, 2, 6, 7, 14, 17, 99], "native_version": LibboxVersion()]
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: evidence, options: [.prettyPrinted, .sortedKeys]), uniformTypeIdentifier: "public.json")
        attachment.name = "ios-system-stop-policy"; attachment.lifetime = .keepAlways; add(attachment)
    }

    func testFallbackStopsSelectingWhenModeChangesDuringAnAwait() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        var settings = ExperimentSettings()
        settings.fallbackEnabled = true
        settings.recoverAfter = 1
        settings.fallbacks = ["One": ["failed", "good"], "Two": ["failed", "good"]]
        var selections: [String] = []
        weak var observedRunner: ProviderExperimentRunner?
        let commands = ProviderExperimentRunner.RecoveryCommands(test: { _ in }, select: { selector, _ in
            selections.append(selector)
            // A native selection RPC suspends the actor. Model a user mode
            // change delivered before it returns to the recovery round.
            observedRunner?.clashMode("direct")
        }, settle: {}, advanceClock: {})
        let runner = try ProviderExperimentRunner(profileID: UUID().uuidString, settings: settings,
            content: #"{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}"#,
            root: root, recoveryCommands: commands) { _ in }
        observedRunner = runner
        runner.availability(true); runner.clashMode("rule")
        let fresh = Int64(Date().timeIntervalSince1970) + 10
        runner.updateGroups(["One", "Two"].map { .init(tag: $0, selected: "failed", items: [
            .init(tag: "failed", delay: 0, testedAt: fresh), .init(tag: "good", delay: 10, testedAt: fresh)]) })
        try runner.beginRecovery(manual: false)
        let deadline = Date().addingTimeInterval(2)
        while try ExperimentFiles.read(profileID: runner.profileID, root: root).status == "Recovery tests running", Date() < deadline {
            try await Task.sleep(nanoseconds: 10_000_000)
        }
        XCTAssertEqual(selections.count, 1, "Recovery selected another group after Rule mode ended")
        await runner.quiesce()
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: ["selections": selections.count, "expected": 1]), uniformTypeIdentifier: "public.json")
        attachment.name = "ios-fallback-mode-change"; attachment.lifetime = .keepAlways; add(attachment)
    }

    func testNativeMemoryResetEvidenceReadsOnlyBoundedNumericEvents() throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        let draft = root.appendingPathComponent("oom_draft")
        try FileManager.default.createDirectory(at: draft, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let events = draft.appendingPathComponent("events.jsonl")
        let content = String(repeating: "x", count: 70_000) + "\n" + #"{"t":"reset","at":"2026-11-16T00:00:00Z","reason":"threshold","memoryBytes":48000000,"memoryAfterBytes":32000000,"connections":7}"# + "\n" + #"{"t":"reset","at":"2026-11-16T00:00:01Z","reason":"rate","reportOnly":true,"memoryBytes":49000000}"# + "\n{partial"
        try Data(content.utf8).write(to: events)
        try Data("private config https://secret.example/token".utf8).write(to: draft.appendingPathComponent("config.json"))
        let evidence = try XCTUnwrap(TunnelRuntimeEvidence.latestNativeMemoryReset(workingDirectory: root))
        XCTAssertNotNil(evidence.range(of: "before_bytes=48000000"))
        XCTAssertNotNil(evidence.range(of: "connections=7"))
        XCTAssertNil(evidence.range(of: "49000000"))
        XCTAssertNil(evidence.range(of: "secret.example"))
        try FileManager.default.removeItem(at: events)
        try FileManager.default.createSymbolicLink(at: events, withDestinationURL: draft.appendingPathComponent("config.json"))
        XCTAssertNil(TunnelRuntimeEvidence.latestNativeMemoryReset(workingDirectory: root))
        let attachment = XCTAttachment(string: evidence)
        attachment.name = "ios-native-memory-reset-evidence"; attachment.lifetime = .keepAlways; add(attachment)
    }

    func testRuntimeEvidenceUsesMeasuredMemoryAndDoesNotGuessCrashCause() throws {
        let suite = "sakamoto-runtime-evidence-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: root) }
        var error: NSError?
        let owner = try XCTUnwrap(MobilecoreNewRecoverySession(root.path, &error))
        if let error { throw error }
        let ticket = try checkedNativeValue { owner.begin(true, error: $0) }
        try owner.ready(ticket, instance: "fixture-native-instance", version: LibboxVersion())
        TunnelRuntimeEvidence.recordPreviousExit(try checkedNativeValue { owner.statusJSON($0) }, defaults: defaults)
        let previous = try XCTUnwrap(TunnelDiagnostics.history(defaults: defaults).last)
        XCTAssertEqual(previous.stage, "Previous runtime exit")
        XCTAssertNotNil(previous.message.range(of: "cause unknown"))
        XCTAssertNil(previous.message.range(of: ticket))
        XCTAssertGreaterThan(try XCTUnwrap(TunnelRuntimeEvidence.memoryBytes()), 0)
        TunnelRuntimeEvidence.record(stage: "Memory warning", message: "fixture", defaults: defaults)
        let sample = try XCTUnwrap(TunnelDiagnostics.history(defaults: defaults).last)
        XCTAssertNotNil(sample.message.range(of: "footprint_bytes="))
        try owner.stop()
        let count = TunnelDiagnostics.history(defaults: defaults).count
        TunnelRuntimeEvidence.recordPreviousExit(try checkedNativeValue { owner.statusJSON($0) }, defaults: defaults)
        XCTAssertEqual(TunnelDiagnostics.history(defaults: defaults).count, count)
        let attachment = XCTAttachment(data: try JSONEncoder().encode(TunnelDiagnostics.history(defaults: defaults)), uniformTypeIdentifier: "public.json")
        attachment.name = "ios-runtime-memory-evidence"; attachment.lifetime = .keepAlways; add(attachment)
    }

    func testDNSRestoreFailuresRespectTheCapturedRuntime() async throws {
        var evidence: [[String: Any]] = []
        for scenario in ["success", "retry_success", "both_failed", "stopped", "replace_during_retry"] {
            var current = scenario != "stopped"
            var restoreCalls = 0
            var outcome = ""
            do {
                let result = try await TunnelNetworkSettingsTransaction.refresh(isCurrent: { current }, reapply: {
                    restoreCalls += 1
                    if scenario == "success" { return }
                    if scenario == "retry_success", restoreCalls == 2 { return }
                    if scenario == "replace_during_retry", restoreCalls == 2 { current = false }
                    throw NSError(domain: "DNSFixture", code: 2)
                })
                outcome = result.rawValue
            } catch is CancellationError { outcome = "superseded" }
            catch { outcome = "failed" }
            switch scenario {
            case "success": XCTAssertEqual(outcome, "restored"); XCTAssertEqual(restoreCalls, 1)
            case "retry_success": XCTAssertEqual(outcome, "recovered"); XCTAssertEqual(restoreCalls, 2)
            case "both_failed": XCTAssertEqual(outcome, "failed"); XCTAssertEqual(restoreCalls, 2)
            case "stopped": XCTAssertEqual(outcome, "superseded"); XCTAssertEqual(restoreCalls, 0)
            default: XCTAssertEqual(outcome, "superseded"); XCTAssertEqual(restoreCalls, 2)
            }
            evidence.append(["scenario": scenario, "outcome": outcome, "restore_calls": restoreCalls])
        }
        let attachment = XCTAttachment(data: try JSONSerialization.data(withJSONObject: evidence, options: [.prettyPrinted, .sortedKeys]), uniformTypeIdentifier: "public.json")
        attachment.name = "ios-dns-restore-transactions"; attachment.lifetime = .keepAlways; add(attachment)
    }
}
