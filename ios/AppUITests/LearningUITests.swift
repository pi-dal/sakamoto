import XCTest

final class LearningUITests: XCTestCase {
    func testRecentChecksAreVisibleWithoutStartingTheVPN() {
        let app = XCUIApplication()
        app.launchArguments = ["--editor-ui-test"]
        app.launch()
        defer { app.terminate() }
        let settings = app.tabBars.buttons["Settings"]
        XCTAssertTrue(settings.waitForExistence(timeout: 5)); settings.tap()
        let experiment = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Experiment")).firstMatch
        XCTAssertTrue(experiment.waitForExistence(timeout: 5)); experiment.tap()
        let heading = app.staticTexts["Recent checks"]
        for _ in 0..<5 where !heading.isHittable { app.swipeUp() }
        XCTAssertTrue(heading.isHittable)
        XCTAssertTrue(app.staticTexts["No origin checks yet"].exists)
        let screenshot = XCTAttachment(screenshot: app.screenshot())
        screenshot.name = "experiment-recent-checks"; screenshot.lifetime = .keepAlways
        add(screenshot)
    }
}
