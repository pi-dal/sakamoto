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
        let preferences = UserDefaults.standard
        let keys = ["sakamoto.location.setupWizardSeen", "sakamoto.location.setupWizardStep", "sakamoto.location.cacheRecoveryActive", "sakamoto.location.cacheRecoveryStep", "sakamoto.location.guideFlow", "sakamoto.location.networkGuideStep"]
        let previous = keys.reduce(into: [String: Any]()) { values, key in values[key] = preferences.object(forKey: key) }
        let step = Int(ProcessInfo.processInfo.environment["SAKAMOTO_LOCATION_REVIEW_STEP"] ?? "0") ?? 0
        preferences.set(false, forKey: keys[0])
        preferences.set(step, forKey: keys[1])
        let recovery = ProcessInfo.processInfo.environment["SAKAMOTO_LOCATION_REVIEW_RECOVERY"] == "true"
        let flow = ProcessInfo.processInfo.environment["SAKAMOTO_LOCATION_REVIEW_FLOW"] ?? (recovery ? "activation" : "setup")
        preferences.set(false, forKey: keys[2])
        preferences.set(step, forKey: keys[3])
        preferences.set(flow, forKey: keys[4]); preferences.set(step, forKey: keys[5])
        defer {
            for key in keys {
                if let value = previous[key] { preferences.set(value, forKey: key) }
                else { preferences.removeObject(forKey: key) }
            }
        }
        let variant = ProcessInfo.processInfo.environment["SAKAMOTO_LOCATION_REVIEW_VARIANT"] ?? ""
        let content = NavigationStack { VirtualLocationView(tunnel: LocationControlTunnel(), supportsSimulation: true) }
            .transaction { $0.disablesAnimations = true }
            .environment(\.dynamicTypeSize, variant == "dark-large" ? .xxxLarge : .large)
            .preferredColorScheme(variant == "dark-large" ? .dark : nil)
        let host = UIHostingController(rootView: content)
        window.rootViewController = host; window.makeKeyAndVisible()
        for _ in 0..<20 {
            if let presented = host.presentedViewController, !presented.isBeingPresented, presented.transitionCoordinator == nil { break }
            try await Task.sleep(nanoseconds: 100_000_000)
        }
        let presented = try XCTUnwrap(host.presentedViewController)
        XCTAssertFalse(presented.isBeingPresented)
        XCTAssertNil(presented.transitionCoordinator)
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in window.drawHierarchy(in: window.bounds, afterScreenUpdates: true) }
        let data = try XCTUnwrap(image.pngData())
        XCTAssertGreaterThan(data.count, 20_000)
        let folder = URL(fileURLWithPath: directory); try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let device = UIDevice.current.userInterfaceIdiom == .pad ? "tablet" : "phone"
        let name = "\(device)-ios-\(flow)-step-\(step + 1)\(variant.isEmpty ? "" : "-" + variant).png"
        try data.write(to: folder.appendingPathComponent(name))
        window.isHidden = true
    }

    func testRenderLocationMapForNativeReview() async throws {
        guard let directory = ProcessInfo.processInfo.environment["SAKAMOTO_LOCATION_REVIEW_DIR"] else { throw XCTSkip("Only run when native screenshot output is requested") }
        guard let scene = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene }).first else { throw XCTSkip("No scene for rendering") }
        let points = [LocationWaypoint(latitude: 37.3349, longitude: -122.009), .init(latitude: 37.333, longitude: -122.007), .init(latitude: 37.331, longitude: -122.006)]
        let window = UIWindow(windowScene: scene)
        let variant = ProcessInfo.processInfo.environment["SAKAMOTO_LOCATION_REVIEW_VARIANT"] ?? ""
        let content = LocationMapPicker(isRoute: true, points: points, center: points[0], current: points[1]) { _ in }
            .environment(\.dynamicTypeSize, variant == "dark-large" ? .xxxLarge : .large)
            .preferredColorScheme(variant == "dark-large" ? .dark : nil)
        let host = UIHostingController(rootView: content)
        window.rootViewController = host; window.makeKeyAndVisible()
        try await Task.sleep(nanoseconds: 5_000_000_000)
        let image = UIGraphicsImageRenderer(bounds: window.bounds).image { _ in window.drawHierarchy(in: window.bounds, afterScreenUpdates: true) }
        let data = try XCTUnwrap(image.pngData()); XCTAssertGreaterThan(data.count, 20_000)
        let folder = URL(fileURLWithPath: directory); try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let device = UIDevice.current.userInterfaceIdiom == .pad ? "tablet" : "phone"
        let name = "\(device)-ios-map\(variant.isEmpty ? "" : "-" + variant).png"
        try data.write(to: folder.appendingPathComponent(name)); window.isHidden = true
    }
    func testDisabledSystemLocationOffersRecoveryWithoutStartingObservation() {
        let tunnel = LocationControlTunnel()
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true)
        model.locationServicesEnabled = false
        model.observeLocation()
        XCTAssertFalse(model.observing)
        XCTAssertTrue(model.observed?.contains("Fresh measurement paused") == true)
        XCTAssertEqual(model.locationServicesEnabled, false)
        XCTAssertNil(tunnel.settings)
    }
    func testMapPositionDoesNotChangeLiveSimulationUntilApplied() async {
        let tunnel = LocationControlTunnel()
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true)
        model.trustChecked = true
        model.locationServicesEnabled = false
        model.settings = VirtualLocationSettings(enabled: true, latitude: 0, longitude: 0)
        model.selectMapPosition(LocationWaypoint(latitude: 37.3349, longitude: -122.009))
        XCTAssertNil(tunnel.settings); XCTAssertEqual(model.settings.latitude, 0)
        XCTAssertEqual(model.mapPoint.latitude, 37.3349)
        await model.apply(enabled: true)
        XCTAssertEqual(tunnel.settings?.latitude, 37.3349)
        XCTAssertEqual(tunnel.settings?.longitude, -122.009)
        XCTAssertEqual(tunnel.reloads, 0)
        XCTAssertEqual(model.locationServicesEnabled, false)
        XCTAssertFalse(model.observing)
    }
    func testMapRouteEditingPreservesLiveRouteAndRejectsInvalidTimingAtStart() async {
        let tunnel = LocationControlTunnel()
        let live = VirtualLocationRoute(points: [.init(latitude: 0, longitude: 0), .init(latitude: 0, longitude: 1)], durationSeconds: 60)
        tunnel.evidence.route = live; tunnel.evidence.routeState = "Paused"
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true)
        model.settings.route = live; model.snapshot = tunnel.evidence; model.trustChecked = true
        model.routeDuration = "invalid"
        let edited = [LocationWaypoint(latitude: 37.3349, longitude: -122.009), .init(latitude: 37.333, longitude: -122.007)]
        model.selectMapRoute(edited)
        XCTAssertEqual(model.mapRoutePoints, edited)
        XCTAssertEqual(model.settings.route, live); XCTAssertEqual(model.snapshot.routeState, "Paused")
        XCTAssertNil(tunnel.settings)
        await model.startRoute()
        XCTAssertNil(tunnel.settings); XCTAssertNotNil(model.message)
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

    func testStopPersistsRestorationGuideOnlyAfterConfirmedControl() async throws {
        let name = "LocationGuide-\(UUID())"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        defaults.set(LocationGuideFlow.activation.rawValue, forKey: "sakamoto.location.guideFlow")
        defaults.set(3, forKey: "sakamoto.location.networkGuideStep")
        let tunnel = LocationControlTunnel()
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true, guideDefaults: defaults)
        model.settings = VirtualLocationSettings(enabled: true, latitude: 22.5, longitude: 114)
        model.latitude = "invalid unapplied edit"
        model.locationServicesEnabled = false
        tunnel.fail = true
        await model.stopForRestoration()
        XCTAssertFalse(model.restorationGuideRequested)
        XCTAssertEqual(defaults.string(forKey: "sakamoto.location.guideFlow"), "activation")
        XCTAssertTrue(model.settings.enabled)
        tunnel.fail = false
        await model.stopForRestoration()
        XCTAssertTrue(model.restorationGuideRequested)
        XCTAssertFalse(model.settings.enabled)
        XCTAssertEqual(defaults.string(forKey: "sakamoto.location.guideFlow"), "restoration")
        XCTAssertEqual(defaults.integer(forKey: "sakamoto.location.networkGuideStep"), 0)
        XCTAssertEqual(model.locationServicesEnabled, false)
        XCTAssertEqual(tunnel.reloads, 0)
        let reopened = try XCTUnwrap(UserDefaults(suiteName: name))
        XCTAssertEqual(reopened.string(forKey: "sakamoto.location.guideFlow"), "restoration")
    }
    func testUnexpectedEnabledReplyDoesNotOpenRestorationGuide() async {
        let tunnel = LocationControlTunnel(); tunnel.refuseStop = true
        let model = VirtualLocationModel(tunnel: tunnel, supportsSimulation: true)
        model.settings.enabled = true
        await model.stopForRestoration()
        XCTAssertFalse(model.restorationGuideRequested)
        XCTAssertTrue(model.settings.enabled)
        XCTAssertFalse(model.snapshotAvailable)
        XCTAssertTrue(model.message?.contains("not confirmed") == true)
    }
    func testEarlierRewritesDoNotConfirmCurrentVerification() {
        let model = VirtualLocationModel(tunnel: LocationControlTunnel(), supportsSimulation: true)
        model.vpnRunning = true; model.snapshotAvailable = true; model.locationServicesEnabled = true
        model.snapshot.enabled = true; model.snapshot.requests = 12; model.snapshot.patched = 10
        XCTAssertTrue(model.verificationHint(since: 10).contains("No new rewrite"))
        model.snapshot.patched = 11
        XCTAssertTrue(model.verificationHint(since: 10).contains("during this check"))
        model.snapshot.requests = 0; model.snapshot.patched = 0
        XCTAssertTrue(model.verificationHint(since: 0).contains("No location request"))
        model.locationServicesEnabled = false
        XCTAssertTrue(model.verificationHint(since: 0).contains("Enable Location Services"))
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
    var refuseStop = false
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
            self.settings = settings; evidence.enabled = refuseStop ? true : settings.enabled; evidence.route = settings.route
            evidence.latitude = settings.latitude; evidence.longitude = settings.longitude
            evidence.routeState = settings.route == nil ? nil : settings.route!.paused ? "Paused" : "Moving"
        }
        return evidence
    }
}
