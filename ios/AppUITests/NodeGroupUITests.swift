import XCTest

final class NodeGroupUITests: XCTestCase {
    private var app: XCUIApplication!
    override func setUp() {
        continueAfterFailure = false
        app = XCUIApplication()
        app.launchArguments = ["--editor-ui-test"]
        app.launch()
    }
    override func tearDown() { app.terminate() }
    private func openSources() {
        let button = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Nodes & sources")).firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: 8)); button.tap()
        XCTAssertTrue(app.buttons["SourceNode-Review A"].waitForExistence(timeout: 4))
    }
    private func attachment(_ name: String) {
        let item = XCTAttachment(screenshot: app.screenshot()); item.name = name; item.lifetime = .keepAlways; add(item)
    }
    func testDeleteConfirmsAndRemovesRowOnCurrentPage() {
        openSources()
        app.buttons["DeleteSourceNode-Review A"].tap()
        XCTAssertTrue(app.alerts["Delete source?"].waitForExistence(timeout: 3))
        attachment("delete-confirmation")
        app.alerts.buttons["Cancel"].tap()
        XCTAssertTrue(app.buttons["SourceNode-Review A"].exists)
        app.buttons["DeleteSourceNode-Review A"].tap()
        app.alerts.buttons["Delete"].tap()
        XCTAssertTrue(app.buttons["SourceNode-Review A"].waitForNonExistence(timeout: 4))
        XCTAssertTrue(app.buttons["SourceNode-Review B"].exists)
        XCTAssertTrue(app.navigationBars["Nodes & sources"].exists)
        attachment("source-deleted-on-same-page")
    }
    func testEditNodeServerSavesAndReopensWithNewValue() {
        openSources()
        app.buttons["SourceNode-Review A"].tap()
        app.buttons["Edit server, credentials & TLS"].tap()
        let server = app.textFields["NodeField-server"]
        XCTAssertTrue(server.waitForExistence(timeout: 3))
        server.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.5)).tap()
        server.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: (server.value as? String ?? "").count) + "edited.example")
        app.buttons["NodeSettingsSave"].tap()
        XCTAssertTrue(app.buttons["Edit server, credentials & TLS"].waitForExistence(timeout: 4))
        app.buttons["Edit server, credentials & TLS"].tap()
        XCTAssertEqual(app.textFields["NodeField-server"].value as? String, "edited.example")
        attachment("node-native-editor")
    }
    func testGroupMemberSelectionSavesAndReopens() {
        app.buttons["Proxy groups"].tap()
        XCTAssertTrue(app.buttons["Group-MainProxy"].waitForExistence(timeout: 3))
        app.buttons["Group-MainProxy"].tap()
        let member = app.buttons["GroupMember-Review A"]
        if !member.isHittable { app.swipeUp() }
        member.tap()
        app.buttons["GroupSave"].tap()
        XCTAssertTrue(app.buttons["Group-MainProxy"].waitForExistence(timeout: 4))
        app.buttons["Group-MainProxy"].tap()
        XCTAssertTrue(app.staticTexts["Selected members (1)"].exists)
        attachment("group-member-editor")
    }
}
