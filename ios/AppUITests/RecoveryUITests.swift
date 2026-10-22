import XCTest

final class RecoveryUITests: XCTestCase {
    func testHomeCoreCheckDoesNotStartTheVPN() {
        let app = XCUIApplication()
        app.launchArguments = ["--editor-ui-test"]
        app.launch()
        defer { app.terminate() }
        let open = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.buttons["Open"]
        if open.exists { open.tap() }
        let home = app.tabBars.buttons["Home"]
        XCTAssertTrue(home.waitForExistence(timeout: 5)); home.tap()
        let repair = app.buttons["RepairVPN"]
        XCTAssertTrue(repair.waitForExistence(timeout: 5))
        XCTAssertTrue(repair.isEnabled)
        let entry = XCTAttachment(screenshot: app.screenshot())
        entry.name = "mobile-recovery-home"; entry.lifetime = .keepAlways
        add(entry)

        let details = app.buttons["Connection details"]
        for _ in 0..<6 where !details.isHittable { app.swipeUp() }
        XCTAssertTrue(details.isHittable); details.tap()
        let check = app.buttons["Check core only"]
        for _ in 0..<3 where !check.isHittable { app.swipeUp() }
        XCTAssertTrue(check.isHittable); check.tap()
        let outcome = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@ OR label == %@", "Core check unavailable.", "Core stopped; repair can start the saved profile")).firstMatch
        for _ in 0..<6 where !outcome.isHittable { app.swipeDown() }
        XCTAssertTrue(outcome.waitForExistence(timeout: 8))
        XCTAssertTrue(check.isEnabled)
        XCTAssertFalse(app.buttons["Reconnect VPN…"].exists)
        let result = XCTAttachment(screenshot: app.screenshot())
        result.name = "mobile-recovery-check-only"; result.lifetime = .keepAlways
        add(result)
    }
}
