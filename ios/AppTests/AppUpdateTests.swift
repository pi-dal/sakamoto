import XCTest
import SwiftUI
@testable import Sakamoto

@MainActor
private final class ControlledUpdateChecker: IOSUpdateChecking {
    var started: XCTestExpectation?
    var requests: [(AppUpdateChannel, CheckedContinuation<IOSUpdateResult, Error>)] = []
    func check(_ channel: AppUpdateChannel) async throws -> IOSUpdateResult {
        try await withCheckedThrowingContinuation { continuation in
            requests.append((channel, continuation)); started?.fulfill()
        }
    }
}

@MainActor
final class AppUpdateTests: XCTestCase {
    func testRenderUpdatesForNativeReview() async throws {
        guard let directory = ProcessInfo.processInfo.environment["SAKAMOTO_UPDATE_REVIEW_DIR"] else { throw XCTSkip("Native capture output was not requested") }
        guard let scene = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene }).first else { throw XCTSkip("No scene") }
        struct Fixture: IOSUpdateChecking {
            let result: IOSUpdateResult
            func check(_ channel: AppUpdateChannel) async throws -> IOSUpdateResult { result }
        }
        let folder = URL(fileURLWithPath: directory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        for dark in [false, true] {
            let window = UIWindow(windowScene: scene)
            let model = AppUpdateModel(service: Fixture(result: IOSUpdateResult(version: dark ? nil : "0.1.99", notes: "Synthetic native review fixture", releaseURL: nil, minimumOS: nil)))
            model.channel = dark ? .appStore : .testFlight
            let host = UIHostingController(rootView: NavigationStack { AppUpdateView(model: model) }
                .environment(\.dynamicTypeSize, dark ? .xxxLarge : .large)
                .preferredColorScheme(dark ? .dark : .light))
            window.rootViewController = host; window.makeKeyAndVisible()
            try await Task.sleep(nanoseconds: 300_000_000)
            model.check()
            try await Task.sleep(nanoseconds: 300_000_000)
            let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in window.drawHierarchy(in: window.bounds, afterScreenUpdates: true) }
            let data = try XCTUnwrap(image.pngData()); XCTAssertGreaterThan(data.count, 20_000)
            let device = UIDevice.current.userInterfaceIdiom == .pad ? "tablet" : "phone"
            try data.write(to: folder.appendingPathComponent("\(device)-ios-updates\(dark ? "-dark-large" : "").png"))
            window.isHidden = true
        }
    }
    private func feed(_ rows: [(String, Bool, Bool)]) throws -> Data {
        try JSONSerialization.data(withJSONObject: rows.map { tag, draft, prerelease in
            ["tag_name": tag, "draft": draft, "prerelease": prerelease, "body": "Release notes"] as [String: Any]
        })
    }
    func testVersionComparisonIsNumericAndRejectsMalformedVersions() {
        XCTAssertGreaterThan(AppUpdateVersion("0.1.16")!, AppUpdateVersion("0.1.9")!)
        XCTAssertEqual(AppUpdateVersion("1.2"), AppUpdateVersion("1.2.0"))
        for value in ["", "1..2", "1.2.3.4", "1.2.rc", "1.2.3-beta", "999999999999999999999", "١.٢.٣"] { XCTAssertNil(AppUpdateVersion(value)) }
    }
    func testTestFlightFeedIncludesIOSPrereleasesAndIgnoresAndroidDraftsAndMalformedTags() throws {
        let data = try feed([("android-v9.9.9", false, false), ("ios-v0.1.9", false, true), ("ios-v0.1.16", false, true),
                             ("ios-v0.2.0", true, true), ("ios-v0.2.0-rc1", false, true), ("ios-v0.1.16/evil", false, true)])
        let latest = try XCTUnwrap(IOSUpdateRelease.latest(in: data))
        XCTAssertEqual(latest.version, "0.1.16")
        XCTAssertEqual(latest.pageURL.absoluteString, "https://github.com/pi-dal/sakamoto/releases/tag/ios-v0.1.16")
        XCTAssertNil(try IOSUpdateRelease.latest(in: feed([("android-v0.1.12", false, false)])))
    }
    func testAppStoreNotPublishedIsDifferentFromUpToDate() throws {
        let result = try IOSUpdateService.appStoreResult(from: Data(#"{"resultCount":0,"results":[]}"#.utf8))
        XCTAssertFalse(result.isPublished)
        XCTAssertFalse(result.isNewer(than: "0.1.16"))
        XCTAssertNil(result.releaseURL)
    }
    func testAppStoreLookupValidatesAppIdentityAndComparesStoreVersion() throws {
        let data = Data(#"{"resultCount":1,"results":[{"trackId":6820372642,"bundleId":"com.pidal.sakamoto","version":"0.1.17","minimumOsVersion":"16.0","releaseNotes":"Updated"}]}"#.utf8)
        let result = try IOSUpdateService.appStoreResult(from: data)
        XCTAssertTrue(result.isPublished)
        XCTAssertTrue(result.isNewer(than: "0.1.16"))
        XCTAssertFalse(result.isNewer(than: "0.1.17"))
        XCTAssertFalse(result.isNewer(than: "0.1.18"))
        XCTAssertEqual(result.minimumOS, "16.0")
        XCTAssertThrowsError(try IOSUpdateService.appStoreResult(from: Data(#"{"resultCount":1,"results":[{"trackId":123,"bundleId":"different.app","version":"99.0.0"}]}"#.utf8)))
    }
    func testSwitchingChannelRejectsLateResultFromCancelledRequest() async throws {
        let checker = ControlledUpdateChecker()
        let model = AppUpdateModel(service: checker)
        model.channel = .testFlight
        checker.started = expectation(description: "TestFlight check started")
        model.check()
        await fulfillment(of: [checker.started!], timeout: 2)
        model.channel = .appStore; model.changeChannel()
        checker.started = expectation(description: "App Store check started")
        model.check()
        await fulfillment(of: [checker.started!], timeout: 2)
        let fresh = IOSUpdateResult(version: "0.1.17", notes: "Store", releaseURL: IOSUpdateService.appStoreURL, minimumOS: nil)
        checker.requests[1].1.resume(returning: fresh)
        checker.requests[0].1.resume(returning: IOSUpdateResult(version: "9.9.9", notes: "Stale", releaseURL: nil, minimumOS: nil))
        // Await task scheduling through the main actor without wall-clock sleeps.
        for _ in 0..<10 { await Task.yield() }
        XCTAssertEqual(model.result, fresh)
        XCTAssertFalse(model.isChecking)
        XCTAssertNil(model.error)
    }
    func testCancelAndFailedRequestRemainRetryable() async throws {
        let checker = ControlledUpdateChecker()
        let model = AppUpdateModel(service: checker)
        checker.started = expectation(description: "Initial request")
        model.check()
        await fulfillment(of: [checker.started!], timeout: 2)
        model.stop()
        checker.requests[0].1.resume(throwing: URLError(.cancelled))
        for _ in 0..<10 { await Task.yield() }
        XCTAssertNil(model.error); XCTAssertFalse(model.isChecking)
        checker.started = expectation(description: "Retry request")
        model.check()
        await fulfillment(of: [checker.started!], timeout: 2)
        checker.requests[1].1.resume(throwing: IOSUpdateService.UpdateError.http(429))
        for _ in 0..<10 { await Task.yield() }
        XCTAssertTrue(model.error?.contains("limited") == true)
        XCTAssertFalse(model.isChecking)
    }
}
