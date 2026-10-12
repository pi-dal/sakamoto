import XCTest
import SakamotoKit
import SwiftUI
@testable import Sakamoto

@MainActor
final class VirtualLocationControlTests: XCTestCase {
    func testRenderVirtualLocationForNativeReview() async throws {
        guard let directory = ProcessInfo.processInfo.environment["SAKAMOTO_LOCATION_REVIEW_DIR"] else { throw XCTSkip("Only run when native screenshot output is requested") }
        guard let scene = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene }).first else { throw XCTSkip("No scene for rendering") }
        let window = UIWindow(windowScene: scene)
        let host = UIHostingController(rootView: NavigationStack { VirtualLocationView(tunnel: LocationControlTunnel()) })
        window.rootViewController = host; window.makeKeyAndVisible()
        try await Task.sleep(nanoseconds: 500_000_000)
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in window.drawHierarchy(in: window.bounds, afterScreenUpdates: true) }
        let data = try XCTUnwrap(image.pngData())
        XCTAssertGreaterThan(data.count, 20_000)
        let folder = URL(fileURLWithPath: directory); try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let name = UIDevice.current.userInterfaceIdiom == .pad ? "tablet-ios.png" : "phone-ios.png"
        try data.write(to: folder.appendingPathComponent(name))
        window.isHidden = true
    }

    func testStopIgnoresInvalidUnappliedCoordinateEditsAndDoesNotReloadVPN() async {
        let tunnel = LocationControlTunnel()
        let model = VirtualLocationModel(tunnel: tunnel)
        model.settings = VirtualLocationSettings(enabled: true, latitude: 22.5, longitude: 114, accuracy: 25)
        model.latitude = "invalid edit"; model.longitude = "999"; model.accuracy = "-1"
        await model.apply(enabled: false, useEditedCoordinates: false)
        XCTAssertEqual(tunnel.settings, VirtualLocationSettings(enabled: false, latitude: 22.5, longitude: 114, accuracy: 25))
        XCTAssertFalse(model.settings.enabled)
        XCTAssertEqual(tunnel.reloads, 0)
    }
    func testFailedControlDoesNotReportSuccess() async {
        let tunnel = LocationControlTunnel(); tunnel.fail = true
        let model = VirtualLocationModel(tunnel: tunnel)
        model.settings.enabled = true
        await model.apply(enabled: false, useEditedCoordinates: false)
        XCTAssertTrue(model.settings.enabled)
        XCTAssertNotNil(model.message)
        XCTAssertFalse(model.snapshot.enabled)
    }
}

private final class LocationControlTunnel: TunnelControlling, @unchecked Sendable {
    var settings: VirtualLocationSettings?
    var reloads = 0
    var fail = false
    func connect(options: TunnelStartOptions) async throws {}
    func disconnect() async throws {}
    func ping() async throws -> TunnelStateSnapshot { TunnelStateSnapshot(serviceState: .running) }
    func status() async throws -> ServiceState { .running }
    func reload(configContent: String) async throws { reloads += 1 }
    func observations() -> AsyncStream<TunnelObservation> { AsyncStream { $0.finish() } }
    func location(settings: VirtualLocationSettings?) async throws -> VirtualLocationSnapshot {
        if fail { throw TunnelProfile.InvalidProfile("provider unavailable") }
        self.settings = settings; return VirtualLocationSnapshot()
    }
}
