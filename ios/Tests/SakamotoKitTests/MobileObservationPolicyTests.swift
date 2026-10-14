import XCTest
@testable import SakamotoKit

final class MobileObservationPolicyTests: XCTestCase {
    func testDisabledAndExplicitModeNeedNoMonitorButEnabledPoliciesDo() {
        var settings = ExperimentSettings()
        XCTAssertFalse(settings.requiresMonitoring)
        settings.mode = .on
        XCTAssertFalse(settings.requiresMonitoring)
        settings.mode = .auto
        XCTAssertTrue(settings.learningEnabled)
        XCTAssertTrue(settings.requiresMonitoring)
        settings.mode = .off; settings.fallbackEnabled = true
        XCTAssertFalse(settings.learningEnabled)
        XCTAssertTrue(settings.requiresMonitoring)
        settings.fallbackEnabled = false; settings.cfRegionBlock = true
        XCTAssertTrue(settings.learningEnabled)
    }

    func testNativeBatchesAndNestedDuplicateMembersDefineResultWindow() {
        let seven = (0..<7).map { ExperimentGroup.Item(tag: "n\($0)", delay: 0, testedAt: 0) }
        let eleven = (0..<11).map { ExperimentGroup.Item(tag: "n\($0)", delay: 0, testedAt: 0) }
        let groups = [ExperimentGroup(tag: "seven", selected: "n0", items: seven),
                      ExperimentGroup(tag: "eleven", selected: "n0", items: eleven),
                      ExperimentGroup(tag: "nested", selected: "seven", items: [.init(tag: "seven", delay: 0, testedAt: 0), .init(tag: "eleven", delay: 0, testedAt: 0)])]
        XCTAssertEqual(ExperimentGroup.resultWaitSeconds(tags: ["seven"], groups: groups), 20)
        XCTAssertEqual(ExperimentGroup.resultWaitSeconds(tags: ["eleven", "nested"], groups: groups), 35)
        XCTAssertEqual(ExperimentGroup.resultWaitSeconds(tags: ["missing"], groups: groups), 20)
        let cycle = [ExperimentGroup(tag: "a", selected: "b", items: [.init(tag: "b", delay: 0, testedAt: 0)]), ExperimentGroup(tag: "b", selected: "a", items: [.init(tag: "a", delay: 0, testedAt: 0)])]
        XCTAssertEqual(ExperimentGroup.resultWaitSeconds(tags: ["a"], groups: cycle), 20)
    }

    func testStableProbeBackoffPreservesLearningFreshnessAndResetsOnFailure() {
        var schedule = ExperimentProbeSchedule()
        XCTAssertEqual(schedule.delaySeconds(learning: false), 30)
        for expected in [60, 120, 300, 300] {
            schedule.record(healthy: true)
            XCTAssertEqual(schedule.delaySeconds(learning: false), expected)
            XCTAssertLessThanOrEqual(schedule.delaySeconds(learning: true), 60)
        }
        schedule.record(healthy: false)
        XCTAssertEqual(schedule.delaySeconds(learning: false), 30)
        schedule.record(healthy: true); schedule.reset()
        XCTAssertEqual(schedule.delaySeconds(learning: true), 30)
    }
}
