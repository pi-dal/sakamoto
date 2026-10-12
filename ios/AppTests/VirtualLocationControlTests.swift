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
    func testRouteHoldUsesRuntimePointDespiteInvalidEdits() async {
        let tunnel = LocationControlTunnel()
        tunnel.evidence.enabled = true; tunnel.evidence.latitude = 22.55; tunnel.evidence.longitude = 114.05
        let route = VirtualLocationRoute(points: [.init(latitude: 22.5, longitude: 114), .init(latitude: 22.6, longitude: 114.1)], durationSeconds: 60)
        tunnel.evidence.route = route; tunnel.evidence.routeState = "Moving"
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true)
        model.trustChecked = true; model.settings = VirtualLocationSettings(enabled: true, latitude: 22.5, longitude: 114, accuracy: 15, route: route)
        model.latitude = "invalid"; model.longitude = "999"; model.accuracy = "invalid"
        await model.holdRoute()
        XCTAssertNil(tunnel.settings?.route)
        XCTAssertEqual(tunnel.settings?.latitude, 22.55)
        XCTAssertEqual(tunnel.settings?.longitude, 114.05)
        XCTAssertEqual(tunnel.settings?.accuracy, 15)
        XCTAssertTrue(model.settings.enabled); XCTAssertEqual(tunnel.reloads, 0)
    }
    func testPauseAndResumePreserveRouteIdentity() async {
        let tunnel = LocationControlTunnel()
        let route = VirtualLocationRoute(points: [.init(latitude: 0, longitude: 0), .init(latitude: 0, longitude: 1)], durationSeconds: 60)
        tunnel.evidence.route = route; tunnel.evidence.routeState = "Moving"
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true)
        model.trustChecked = true; model.snapshot = tunnel.evidence
        model.settings = VirtualLocationSettings(enabled: true, route: route); model.accuracy = "invalid edit"
        await model.pauseRoute(true)
        XCTAssertEqual(tunnel.settings?.route?.id, route.id); XCTAssertEqual(tunnel.settings?.route?.paused, true)
        await model.pauseRoute(false)
        XCTAssertEqual(tunnel.settings?.route?.id, route.id); XCTAssertEqual(tunnel.settings?.route?.paused, false)
        XCTAssertEqual(tunnel.reloads, 0)
    }
    func testReopenedPageRecoversLiveRouteFromProvider() async {
        let tunnel = LocationControlTunnel()
        tunnel.evidence.route = VirtualLocationRoute(points: [.init(latitude: 0, longitude: 0), .init(latitude: 0, longitude: 1)], durationSeconds: 60)
        let model = VirtualLocationModel(tunnel: tunnel)
        await model.refresh(); XCTAssertEqual(model.settings.route, tunnel.evidence.route)
    }
    func testFailedRouteStartDoesNotReportMovement() async {
        let tunnel = LocationControlTunnel(); tunnel.fail = true
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true)
        model.trustChecked = true; model.routeText = "0, 0\n0, 1"; model.accuracy = "25"
        await model.startRoute(); XCTAssertNil(model.settings.route); XCTAssertNil(model.snapshot.route); XCTAssertNotNil(model.message)
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
    var evidence = VirtualLocationSnapshot()
    func connect(options: TunnelStartOptions) async throws {}
    func disconnect() async throws {}
    func ping() async throws -> TunnelStateSnapshot { TunnelStateSnapshot(serviceState: .running) }
    func status() async throws -> ServiceState { .running }
    func reload(configContent: String) async throws { reloads += 1 }
    func observations() -> AsyncStream<TunnelObservation> { AsyncStream { $0.finish() } }
    func location(settings: VirtualLocationSettings?) async throws -> VirtualLocationSnapshot {
        if fail { throw TunnelProfile.InvalidProfile("provider unavailable") }
        if let settings {
            self.settings = settings; evidence.enabled = settings.enabled; evidence.route = settings.route
            evidence.latitude = settings.latitude; evidence.longitude = settings.longitude
            evidence.routeState = settings.route == nil ? nil : settings.route!.paused ? "Paused" : "Moving"
        }
        return evidence
    }
}
