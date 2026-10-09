import XCTest
import SwiftUI
import WidgetKit
import SakamotoKit
@testable import Sakamoto

@MainActor
final class WidgetLayoutTests: XCTestCase {
    func testAllFamiliesAtLargeTypeWithLongNodeNames() throws {
        let now = Date()
        let snapshot = SystemSurfaceSnapshot(serviceState: .running, phase: .unverified,
            selectedNode: "Reality-Vision-Azure-Japan-Long-Node-Name", routingMode: "Rule",
            latencyMS: 128, measuredAt: now, updatedAt: now)
        let cases: [(WidgetFamily, CGSize, String)] = [
            (.systemSmall, CGSize(width: 158, height: 158), "small"),
            (.systemMedium, CGSize(width: 338, height: 158), "medium"),
            (.systemLarge, CGSize(width: 338, height: 354), "large"),
            (.systemSmall, CGSize(width: 170, height: 170), "ipad-small"),
            (.systemMedium, CGSize(width: 360, height: 170), "ipad-medium")
        ]
        for (family, size, name) in cases {
            for dark in [false, true] {
                let content = VPNWidgetContent(snapshot: snapshot, date: now, family: family)
                    .padding(16)
                    .environment(\.dynamicTypeSize, .xxxLarge)
                    .environment(\.colorScheme, dark ? .dark : .light)
                    .background(dark ? Color.black : Color.white)
                    .ignoresSafeArea()
                let host = UIHostingController(rootView: content)
                let window = UIWindow(frame: CGRect(origin: .zero, size: size))
                window.rootViewController = host
                window.isHidden = false
                host.view.frame = window.bounds
                host.view.setNeedsLayout()
                host.view.layoutIfNeeded()
                let renderer = UIGraphicsImageRenderer(size: size)
                let image = renderer.image { _ in host.view.drawHierarchy(in: host.view.bounds, afterScreenUpdates: true) }
                XCTAssertEqual(image.size, size)
                let attachment = XCTAttachment(image: image)
                attachment.name = "widget-\(name)-\(dark ? "dark" : "light")-xxxlarge"
                attachment.lifetime = .keepAlways
                add(attachment)
                window.isHidden = true
                window.rootViewController = nil
            }
        }
    }

    func testDirectControlIntentsNeverRequestForeground() {
        XCTAssertFalse(ConnectTunnelIntent.openAppWhenRun)
        XCTAssertFalse(DisconnectTunnelIntent.openAppWhenRun)
        XCTAssertFalse(ToggleTunnelIntent.openAppWhenRun)
        XCTAssertFalse(TunnelStatusIntent.openAppWhenRun)
        if #available(iOS 18.0, *) { XCTAssertFalse(SetTunnelEnabledIntent.openAppWhenRun) }
    }
}
