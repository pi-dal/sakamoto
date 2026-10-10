import XCTest
@testable import SakamotoKit

final class TunnelProviderStartResolutionTests: XCTestCase {
    func testPreparedManualOnUsesCurrentConfigEvenWhenApplyIsPending() throws {
        let old = TunnelStartOptions(configContent: #"{"route":{"final":"old"}}"#, profileID: UUID().uuidString)
        let current = TunnelStartOptions(configContent: #"{"route":{"final":"current"}}"#, profileID: UUID().uuidString)
        let resolved = try TunnelStartOptions.resolveForProvider(startOptions: current.startTunnelOptions,
            providerConfiguration: old.providerConfiguration, requiresApply: true)
        XCTAssertEqual(resolved, current, "Manual On must carry its prepared payload through the provider's pending-Apply gate")
    }

    func testUnpreparedAutomaticStartStillCannotUseStaleConfig() {
        let saved = TunnelStartOptions(configContent: "{}")
        XCTAssertThrowsError(try TunnelStartOptions.resolveForProvider(startOptions: nil,
            providerConfiguration: saved.providerConfiguration, requiresApply: true))
    }

    func testMalformedExplicitConfigDoesNotFallBackToTheSavedConfig() {
        let saved = TunnelStartOptions(configContent: "{}")
        for value: NSObject in [NSNumber(value: 1), "" as NSString, "  " as NSString] {
            for pending in [false, true] {
                XCTAssertThrowsError(try TunnelStartOptions.resolveForProvider(startOptions: ["configContent": value],
                    providerConfiguration: saved.providerConfiguration, requiresApply: pending))
            }
        }
    }

    func testAnAppliedSavedConfigurationCanStartAutomatically() throws {
        let saved = TunnelStartOptions(configContent: "{}", profileID: UUID().uuidString)
        XCTAssertEqual(try TunnelStartOptions.resolveForProvider(startOptions: nil,
            providerConfiguration: saved.providerConfiguration, requiresApply: false), saved)
        XCTAssertThrowsError(try TunnelStartOptions.resolveForProvider(startOptions: nil,
            providerConfiguration: [:], requiresApply: false))
    }
}
