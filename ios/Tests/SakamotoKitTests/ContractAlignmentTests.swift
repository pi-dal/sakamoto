import XCTest

@testable import SakamotoKit

/// Locks the Swift vocabulary to ios/contract/vocabulary.json, which in turn
/// is generated from internal/core by pkg/mobilecore/contract_test.go.
/// Renaming or removing a vocabulary value on the Go side fails `go test`;
/// this file fails `swift test`. The macOS TUI wording cannot drift from the
/// iOS frontend through this chain.
final class ContractAlignmentTests: XCTestCase {
    private let contract: VocabularyContract = {
        let packageRoot = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent() // Tests/SakamotoKitTests/
            .deletingLastPathComponent() // Tests/
            .deletingLastPathComponent() // ios/
        let goldenURL = packageRoot
            .appendingPathComponent("contract")
            .appendingPathComponent("vocabulary.json")
        guard let data = try? Data(contentsOf: goldenURL) else {
            fatalError("missing golden contract at \(goldenURL.path)")
        }
        do {
            return try VocabularyContract.decode(data)
        } catch {
            fatalError("unreadable golden contract at \(goldenURL.path): \(error)")
        }
    }()

    func testContractShape() {
        XCTAssertEqual(contract.contractVersion, 1)
        XCTAssertEqual(contract.source, "github.com/pi-dal/sakamoto/pkg/mobilecore")
    }

    func testServiceStates() {
        XCTAssertEqual(ServiceState.allCases.map(\.rawValue), contract.serviceStates)
    }

    func testSessionPhases() {
        XCTAssertEqual(SessionPhase.allCases.map(\.rawValue), contract.sessionPhases)
    }

    func testProbeStates() {
        XCTAssertEqual(ProbeState.allCases.map(\.rawValue), contract.probeStates)
    }

    func testProbePaths() {
        XCTAssertEqual(ProbePaths.all, contract.probePaths)
        XCTAssertEqual(ProbePaths.all.count, 5)
    }

    func testRoutingModes() {
        XCTAssertEqual(RoutingMode.allCases.map(\.rawValue), contract.routingModes)
    }

    func testRoutingModeCycleIsDataMirrorOfGoCycle() {
        XCTAssertEqual(
            RoutingMode.cycleOrder.map(\.rawValue),
            contract.routingModeCycle,
            "routingModeCycle in vocabulary.json mirrors core.NextRoutingMode"
        )
        // Cycle semantics (data, not logic): Rule -> Global -> Direct.
        XCTAssertEqual(RoutingMode.cycleOrder.first, .rule)
        XCTAssertEqual(RoutingMode.cycleOrder.last, .direct)
    }

    func testNodeStatuses() {
        XCTAssertEqual(NodeStatus.allCases.map(\.rawValue), contract.nodeStatuses)
    }

    func testConfigStates() {
        XCTAssertEqual(ConfigState.allCases.map(\.rawValue), contract.configStates)
    }

    func testNoticeKinds() {
        XCTAssertEqual(NoticeKind.allCases.map(\.rawValue), contract.noticeKinds)
    }

    // MARK: Documented normalization (mobilecore rendering rules)

    func testFlexibleServiceStateNormalization() {
        XCTAssertEqual(ServiceState(flexible: "Running"), .running)
        XCTAssertEqual(ServiceState(flexible: "  stopping "), .stopping)
        // Unknown inputs normalize so a status label always renders
        // (mobilecore.SessionPhase: unknown service state -> "Stopped").
        XCTAssertEqual(ServiceState(flexible: "bogus"), .stopped)
        XCTAssertEqual(ServiceState(flexible: ""), .stopped)
    }

    func testFlexibleProbeStateNormalization() {
        XCTAssertEqual(ProbeState(flexible: "Reachable"), .reachable)
        XCTAssertEqual(ProbeState(flexible: "bogus"), .idle)
    }

    func testSessionPhaseIsStrict() {
        XCTAssertNotNil(SessionPhase(raw: "TUNRunning"))
        XCTAssertNil(SessionPhase(raw: "bogus"), "a rendered fact must not be guessed")
    }

    func testClashModeTransportEncoding() {
        // core/mode.go: the running core reports and accepts lowercase
        // clash mode strings.
        XCTAssertEqual(RoutingMode.rule.clashModeValue, "rule")
        XCTAssertEqual(RoutingMode.global.clashModeValue, "global")
        XCTAssertEqual(RoutingMode.direct.clashModeValue, "direct")
    }
}
