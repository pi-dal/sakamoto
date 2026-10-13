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
    private func replace(_ field: XCUIElement, with text: String) {
        field.press(forDuration: 1.2)
        let selectAll = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "Select All")).firstMatch
        XCTAssertTrue(selectAll.waitForExistence(timeout: 3))
        selectAll.tap()
        field.typeText(text)
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
        let server = app.textFields["NodeField-server"]
        XCTAssertTrue(server.waitForExistence(timeout: 3))
        let remark = app.textFields["NodeField-tag"]
        replace(remark, with: "Renamed node")
        replace(server, with: "192.0.2.8")
        app.buttons["NodeSettingsSave"].tap()
        XCTAssertTrue(app.buttons["SourceNode-Review A"].waitForExistence(timeout: 4))
        XCTAssertTrue(app.buttons["SourceNode-Review A"].label.contains("Renamed node"))
        app.buttons["SourceNode-Review A"].tap()
        XCTAssertEqual(app.textFields["NodeField-server"].value as? String, "192.0.2.8")
        XCTAssertEqual(app.textFields["NodeField-tag"].value as? String, "Renamed node")
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
