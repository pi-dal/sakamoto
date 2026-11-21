import Network
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
    func testCredentialEditorsPreserveOriginalAfterCancelAndRelaunch() {
        app.terminate()
        app.launchArguments = []
        app.launchEnvironment["SAKAMOTO_CREDENTIAL_UI_SUITE"] = UUID().uuidString
        app.launch()
        func tap(_ label: String) {
            let button = app.buttons[label].firstMatch
            for _ in 0..<5 where !button.isHittable { app.swipeUp() }
            XCTAssertTrue(button.isHittable, label)
            button.tap()
        }
        tap("Tailscale")
        tap("Edit auth key…")
        let input = app.secureTextFields.firstMatch
        input.tap()
        input.typeText("fixture-replacement")
        tap("Store")
        XCTAssertTrue(app.staticTexts["Keychain save failed"].waitForExistence(timeout: 3))
        tap("Cancel")
        app.terminate()
        app.launchArguments = ["--credential-ui-cold"]
        app.launch()
        tap("S3 Sync")
        tap("Edit credentials…")
        XCTAssertTrue(app.staticTexts["Could not read S3 credentials. Try again."].waitForExistence(timeout: 3))
        XCTAssertFalse(app.secureTextFields["Access key"].exists)
        tap("Edit credentials…")
        let field = app.secureTextFields["Secret key"]
        XCTAssertTrue(field.waitForExistence(timeout: 3))
        field.tap()
        field.typeText("-discard")
        tap("Cancel")
        app.terminate()
        app.launch()
        tap("S3 Sync")
        tap("Edit credentials…")
        tap("Edit credentials…")
        XCTAssertTrue(app.secureTextFields["Secret key"].waitForExistence(timeout: 3))
        tap("Cancel")
    }

    func testLegacyOnlyKeySurvivesDeleteFailureAndCanBeDeletedAfterRelaunch() {
        app.terminate()
        app.launchEnvironment["SAKAMOTO_CREDENTIAL_UI_SUITE"] = UUID().uuidString
        func launch(_ phase: String) {
            app.launchEnvironment["SAKAMOTO_KEY_DELETE_PHASE"] = phase
            app.launchArguments = []
            app.launch()
            XCTAssertTrue(app.buttons["Tailscale"].waitForExistence(timeout: 8))
        }
        func confirmDelete() {
            let open = app.buttons["Delete stored key…"]
            for _ in 0..<6 where !open.isHittable { app.swipeUp() }
            open.tap()
            app.buttons["Delete key"].tap()
        }
        launch("fail")
        app.buttons["Tailscale"].tap()
        confirmDelete()
        let error = app.staticTexts["Keychain deletion failed"]
        for _ in 0..<6 where !error.isHittable { app.swipeDown() }
        XCTAssertTrue(error.exists)
        app.terminate()
        launch("retry")
        app.buttons["Tailscale"].tap()
        confirmDelete()
        for _ in 0..<6 where !error.isHittable { app.swipeDown() }
        XCTAssertTrue(error.exists)
        confirmDelete()
        let success = app.staticTexts["Stored auth key deleted"]
        for _ in 0..<6 where !success.isHittable { app.swipeDown() }
        XCTAssertTrue(success.isHittable)
        XCTAssertFalse(error.exists)
        app.terminate()
        launch("deleted")
    }

    func testTailscaleDraftsFollowSelectedProfile() {
        app.terminate()
        app.launchEnvironment["SAKAMOTO_TAILNET_PROFILE_SUITE"] = UUID().uuidString
        app.launchArguments = []
        app.launch()
        app.buttons["Tailscale"].tap()
        app.buttons["Endpoint options"].tap()
        XCTAssertEqual(app.textFields["Hostname (optional)"].value as? String, "phone-a")
        XCTAssertEqual(app.textFields["Exit node (name or IP)"].value as? String, "exit-a")
        XCTAssertEqual(app.switches["Accept tailnet subnet routes"].value as? String, "1")
        XCTAssertEqual(app.switches["Route LAN access via exit node"].value as? String, "1")
        app.tabBars.buttons["Home"].tap()
        app.buttons["Configuration, Fixture A"].tap()
        app.buttons["Fixture B"].tap()
        app.tabBars.buttons["Settings"].tap()
        let toggle = app.switches["Built-in Tailscale endpoint"]
        XCTAssertEqual(toggle.value as? String, "0")
        toggle.coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.5)).tap()
        app.buttons["Apply & connect"].tap()
        XCTAssertTrue(app.staticTexts["applied to the running provider"].waitForExistence(timeout: 8))
        app.terminate()
        app.launchArguments = ["--tailnet-profile-cold"]
        app.launch()
        XCTAssertTrue(app.buttons["Tailscale"].waitForExistence(timeout: 8))
    }

    private func selectLocalFile(_ name: String) {
        let browse = app.tabBars["DOC.browsingModeTabBar"].buttons["Browse"]
        XCTAssertTrue(browse.waitForExistence(timeout: 8))
        browse.tap()
        let local = app.cells["DOC.sidebar.item.On My iPhone"]
        let file = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", name)).firstMatch
        if !file.waitForExistence(timeout: 2) {
            if !local.waitForExistence(timeout: 2) { browse.tap() }
            XCTAssertTrue(local.waitForExistence(timeout: 5))
            local.tap()
        }
        XCTAssertTrue(file.waitForExistence(timeout: 8))
        file.tap()
    }

    func testCommentOnlyManualNodesInShadowrocketExport() throws {
        app.terminate()
        app.launchEnvironment["SAKAMOTO_TAILNET_PROFILE_SUITE"] = UUID().uuidString
        app.launchArguments = ["--empty-profile-fixture"]
        app.launch()
        app.tabBars.buttons["Config"].tap()
        app.buttons["Add sources"].tap()
        app.buttons["From Files…"].tap()
        selectLocalFile("sr-only")
        func openAdvanced() {
            let advanced = app.buttons["Advanced"]
            for _ in 0..<5 where !advanced.isHittable { app.swipeUp() }
            advanced.tap()
        }
        func verifyMembers(_ stage: String) throws {
            app.buttons["Edit configuration JSON…"].tap()
            let editor = app.textViews.firstMatch
            XCTAssertTrue(editor.waitForExistence(timeout: 5))
            let config = try XCTUnwrap(editor.value as? String)
            let root = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(config.utf8)) as? [String: Any])
            let rows = try XCTUnwrap(root["outbounds"] as? [[String: Any]])
            let group = try XCTUnwrap(rows.first { $0["tag"] as? String == "OthersAuto" })
            XCTAssertEqual(group["outbounds"] as? [String], ["Imported"], stage)
            app.buttons["Cancel"].tap()
        }
        openAdvanced()
        try verifyMembers("import")
        for pass in 1...2 {
            let generate = app.buttons["Generate without connecting"]
            XCTAssertTrue(generate.isEnabled)
            generate.tap()
            try verifyMembers("generation \(pass)")
        }
        app.terminate()
        app.launch()
        app.tabBars.buttons["Config"].tap()
        openAdvanced()
        try verifyMembers("restart")
    }

    func testNodeFileImportNoticeFollowsSelection() {
        app.terminate()
        app.launchArguments = []
        app.launchEnvironment["SAKAMOTO_TAILNET_PROFILE_SUITE"] = UUID().uuidString
        app.launch()
        app.tabBars.buttons["Config"].tap()
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Nodes & sources")).firstMatch.tap()
        func importNodes(_ filename: String, feedback: String) {
            app.buttons["Add source"].tap()
            app.buttons["Import nodes.txt"].tap()
            selectLocalFile(filename)
            let notice = app.staticTexts[feedback]
            for _ in 0..<5 where !notice.isHittable { app.swipeUp() }
            XCTAssertTrue(notice.isHittable)
        }
        let addedTwo = "Imported 2 node links. Existing nodes and sources were kept."
        let failed = "Invalid node at line 2. No nodes were imported."
        let duplicate = "All node links are already present. Added 0 nodes."
        importNodes("NoticeFixtureA", feedback: addedTwo)
        importNodes("NoticeFixtureInvalid", feedback: failed)
        XCTAssertFalse(app.staticTexts[addedTwo].exists)
        XCTAssertFalse(app.buttons["SourceNode-PartialImport"].exists)
        importNodes("NoticeFixtureA", feedback: duplicate)
        XCTAssertFalse(app.staticTexts[failed].exists)
        importNodes("NoticeFixtureCRLF", feedback: failed)
        XCTAssertFalse(app.staticTexts[duplicate].exists)
        let a = app.buttons["SourceNode-NoticeOnlyA"]
        for _ in 0..<5 where !a.isHittable { app.swipeDown() }
        XCTAssertTrue(a.isHittable)
        XCTAssertFalse(app.buttons["SourceNode-PartialImport"].exists)
        XCTAssertFalse(app.buttons["SourceNode-PartialCRLF"].exists)
        app.tabBars.buttons["Home"].tap()
        app.buttons["Configuration, Fixture A"].tap()
        app.buttons["Fixture B"].tap()
        app.tabBars.buttons["Config"].tap()
        XCTAssertTrue(app.buttons["SourceNode-RealityLink"].waitForExistence(timeout: 6))
        XCTAssertFalse(a.exists)
        for _ in 0..<3 { app.swipeUp() }
        XCTAssertFalse(app.staticTexts["Imported 2 node links. Existing nodes and sources were kept."].exists)
        XCTAssertFalse(app.staticTexts[duplicate].exists)
        XCTAssertFalse(app.staticTexts[failed].exists)
        importNodes("NoticeFixtureB", feedback: "Imported 1 node link. Existing nodes and sources were kept.")
        let b = app.buttons["SourceNode-NoticeOnlyB"]
        for _ in 0..<5 where !b.isHittable { app.swipeDown() }
        XCTAssertTrue(b.isHittable)
        XCTAssertFalse(a.exists)
    }

    func testImportDetailsFollowSelectedSource() {
        app.terminate()
        app.launchArguments = []
        app.launchEnvironment["SAKAMOTO_TAILNET_PROFILE_SUITE"] = UUID().uuidString
        app.launch()
        app.tabBars.buttons["Config"].tap()
        func importFile(_ name: String, expectingFailure: Bool = false) {
            app.buttons["Add sources"].tap()
            app.buttons["From Files…"].tap()
            selectLocalFile(name)
            if expectingFailure {
                XCTAssertTrue(hasLabel(name))
            } else {
                let imported = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ AND label CONTAINS %@", "Rule source", name)).firstMatch
                XCTAssertTrue(imported.waitForExistence(timeout: 8))
            }
        }
        func switchTo(_ name: String, from current: String) {
            app.tabBars.buttons["Home"].tap()
            app.buttons["Configuration, Fixture " + current].tap()
            app.buttons["Fixture " + name].tap()
            app.tabBars.buttons["Config"].tap()
        }
        switchTo("B", from: "A")
        importFile("ReportFixtureB")
        switchTo("A", from: "B")
        importFile("ReportFixtureA")
        let privateName = "PrivateFailedFixtureA"
        importFile(privateName, expectingFailure: true)
        let advanced = app.buttons["Advanced"]
        for _ in 0..<5 where !advanced.isHittable { app.swipeUp() }
        advanced.tap()
        app.buttons["Import details"].tap()
        func hasLabel(_ value: String) -> Bool {
            app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", value)).firstMatch.waitForExistence(timeout: 2)
        }
        XCTAssertTrue(hasLabel(privateName))
        XCTAssertTrue(hasLabel("192.0.2.31"))
        XCTAssertTrue(hasLabel("DIRECT"))
        switchTo("B", from: "A")
        XCTAssertTrue(hasLabel("ReportFixtureB"))
        XCTAssertTrue(hasLabel("192.0.2.32"))
        XCTAssertTrue(hasLabel("PROXY"))
        XCTAssertFalse(hasLabel("192.0.2.31"))
        XCTAssertFalse(hasLabel("DIRECT"))
        XCTAssertFalse(hasLabel(privateName))
        app.navigationBars.buttons.firstMatch.tap()
        app.navigationBars.buttons.firstMatch.tap()
        for _ in 0..<5 where !app.buttons["Add sources"].isHittable { app.swipeDown() }
        XCTAssertTrue(app.buttons["Add sources"].isHittable)
        XCTAssertFalse(hasLabel(privateName))
    }

    func testCancelledSourceFormsStayWithProfile() throws {
        let ready = expectation(description: "loopback listener ready")
        let unexpectedConnection = expectation(description: "cancelled source contacted")
        unexpectedConnection.isInverted = true
        let parameters = NWParameters.tcp
        parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        let listener = try NWListener(using: parameters)
        listener.stateUpdateHandler = { if case .ready = $0 { ready.fulfill() } }
        listener.newConnectionHandler = { connection in
            unexpectedConnection.fulfill()
            connection.cancel()
        }
        listener.start(queue: DispatchQueue(label: "source-form-endpoint"))
        defer { listener.cancel() }
        wait(for: [ready], timeout: 5)
        let address = "https://127.0.0.1:\(try XCTUnwrap(listener.port).rawValue)/source?token=fixture-only-a"
        app.terminate()
        app.launchEnvironment["SAKAMOTO_TAILNET_PROFILE_SUITE"] = UUID().uuidString
        app.launchArguments = ["--source-form-profile"]
        app.launch()
        app.tabBars.buttons["Config"].tap()
        func openURLForm() {
            app.buttons["Add sources"].tap()
            app.buttons["From URL…"].tap()
        }
        func openSubscription() {
            app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Nodes & sources")).firstMatch.tap()
            app.buttons["Add source"].tap()
            app.buttons["AddSubscriptionFromMenu"].tap()
        }
        openURLForm()
        let url = app.textFields["Shadowrocket .conf URL"]
        func enter(_ text: String, in field: XCUIElement) {
            XCTAssertTrue(field.waitForExistence(timeout: 5))
            for _ in 0..<3 {
                field.coordinate(withNormalizedOffset: CGVector(dx: 0.15, dy: 0.5)).tap()
                if app.keyboards.firstMatch.waitForExistence(timeout: 2) { break }
            }
            app.typeText(text)
            XCTAssertEqual(field.value as? String, text)
        }
        enter(address, in: url)
        app.buttons["Cancel"].tap()
        openSubscription()
        let name = app.textFields["Name"]
        let source = app.textFields["https://subscription URL"]
        enter("Only A", in: name)
        enter(address, in: source)
        app.buttons["Cancel"].tap()
        app.navigationBars.buttons.firstMatch.tap()
        app.tabBars.buttons["Home"].tap()
        app.buttons["Configuration, Fixture A"].tap()
        app.buttons["Fixture B"].tap()
        app.tabBars.buttons["Config"].tap()
        openURLForm()
        XCTAssertEqual(url.value as? String, url.placeholderValue)
        XCTAssertFalse(app.buttons["Import"].isEnabled)
        app.buttons["Cancel"].tap()
        openSubscription()
        XCTAssertEqual(name.value as? String, name.placeholderValue)
        XCTAssertEqual(source.value as? String, source.placeholderValue)
        XCTAssertFalse(app.buttons["Add"].isEnabled)
        app.buttons["Cancel"].tap()
        app.terminate()
        app.launchArguments = ["--source-form-cold"]
        app.launch()
        XCTAssertTrue(app.buttons["Tailscale"].waitForExistence(timeout: 8))
        wait(for: [unexpectedConnection], timeout: 1)
    }

    func testExperimentDraftAndUnreadableHistoryFollowProfile() {
        app.terminate()
        app.launchEnvironment["SAKAMOTO_TAILNET_PROFILE_SUITE"] = UUID().uuidString
        app.launchArguments = ["--experiment-profile"]
        app.launch()
        app.buttons["Experiment"].tap()
        let priority = app.buttons["Edit priority chain"]
        func reveal(_ element: XCUIElement) {
            for _ in 0..<8 where !element.isHittable { app.swipeUp() }
        }
        reveal(priority)
        priority.tap()
        let group = app.textFields["Selector group tag"]
        group.tap(); group.typeText("MainProxy\n")
        let members = app.textFields["Priority tags, comma-separated"]
        members.tap(); members.typeText("Allowed,Imported\n")
        XCTAssertTrue(app.buttons["Save priority chain"].isEnabled)
        let failures = app.buttons["AddRecentFailures"]
        reveal(failures); failures.tap()
        XCTAssertTrue(app.staticTexts["only-a.invalid"].exists)
        app.buttons["Cancel"].tap()
        app.tabBars.buttons["Home"].tap()
        app.buttons["Configuration, Fixture A"].tap()
        app.buttons["Fixture B"].tap()
        app.tabBars.buttons["Settings"].tap()
        for _ in 0..<5 where !priority.isHittable { app.swipeDown() }
        reveal(priority); priority.tap()
        XCTAssertEqual(group.value as? String, "Selector group tag")
        XCTAssertEqual(members.value as? String, "Priority tags, comma-separated")
        XCTAssertFalse(app.buttons["Save priority chain"].isEnabled)
        let unavailable = app.staticTexts["Experiment history is unavailable."]
        reveal(unavailable)
        XCTAssertTrue(unavailable.isHittable)
        XCTAssertFalse(failures.exists)
        XCTAssertFalse(app.staticTexts["only-a.invalid"].exists)
        app.terminate()
        app.launchArguments = ["--experiment-profile-cold"]
        app.launch()
        XCTAssertTrue(app.buttons["Experiment"].waitForExistence(timeout: 8))
    }

    func testChainExitDraftStaysWithItsProfile() {
        app.terminate()
        app.launchEnvironment["SAKAMOTO_TAILNET_PROFILE_SUITE"] = UUID().uuidString
        app.launchArguments = []
        app.launch()
        app.buttons["Configuration details"].tap()
        app.buttons["Proxy chain"].tap()
        let field = app.textFields["Node share link"]
        field.tap()
        field.typeText("trojan://fixture-a-secret@a-exit.invalid:443#AExit")
        field.typeText("\n")
        XCTAssertTrue(app.buttons["Add exit & generate"].isEnabled)
        app.tabBars.buttons["Home"].tap()
        app.buttons["Configuration, Fixture A"].tap()
        app.buttons["Fixture B"].tap()
        app.tabBars.buttons["Settings"].tap()
        XCTAssertEqual(field.value as? String, "Node share link")
        XCTAssertFalse(app.buttons["Add exit & generate"].isEnabled)
        field.tap()
        field.typeText("trojan://fixture-b-secret@b-exit.invalid:443#BExit\n")
        app.buttons["Add exit & generate"].tap()
        XCTAssertTrue(app.staticTexts["Exit added. Choose it above and save the chain."].waitForExistence(timeout: 15))
        app.terminate()
        app.launchArguments = ["--chain-profile-cold"]
        app.launch()
        XCTAssertTrue(app.buttons["Configuration details"].waitForExistence(timeout: 8))
    }

    func testVLESSChainExitSavesReopensAndDisables() {
        app.terminate()
        app.launchArguments = ["--editor-ui-test", "--chain-ui-test"]
        app.launch()
        let open = app.buttons["Proxy chain"]
        XCTAssertTrue(open.waitForExistence(timeout: 6))
        if !open.isHittable { app.swipeUp() }
        open.tap()
        let picker = app.buttons["ChainExit"]
        XCTAssertTrue(picker.waitForExistence(timeout: 4)); picker.tap()
        app.buttons["Review B"].tap()
        app.switches["ChainEnabled"].coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.5)).tap()
        XCTAssertEqual(app.switches["ChainEnabled"].value as? String, "1")
        app.buttons["ChainSave"].tap()
        XCTAssertTrue(app.staticTexts["Saved. Apply changes in Config."].waitForExistence(timeout: 4))
        attachment("vless-chain-saved")
        app.navigationBars.buttons.firstMatch.tap()
        app.buttons["Proxy chain"].tap()
        XCTAssertEqual(app.switches["ChainEnabled"].value as? String, "1")
        XCTAssertTrue(app.buttons["ChainExit"].label.contains("Review B"))
        app.switches["ChainEnabled"].coordinate(withNormalizedOffset: CGVector(dx: 0.92, dy: 0.5)).tap()
        XCTAssertEqual(app.switches["ChainEnabled"].value as? String, "0")
        app.buttons["ChainSave"].tap()
        XCTAssertTrue(app.staticTexts["Saved. Apply changes in Config."].waitForExistence(timeout: 4))
        attachment("vless-chain-disabled")
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
    func testPolicyDomainAndSubdomainsSaveAndReopen() {
        let routing = app.buttons["Routing rules"]
        XCTAssertTrue(routing.waitForExistence(timeout: 5))
        if !routing.isHittable { app.swipeUp() }
        routing.tap()
        app.buttons["Edit custom overrides"].tap()
        app.buttons["Add rule…"].tap()
        app.buttons["PolicyMatchType"].tap()
        app.buttons["Domain & all subdomains"].tap()
        let value = app.textFields["PolicyMatchValue"]
        XCTAssertTrue(value.waitForExistence(timeout: 3))
        value.tap(); value.typeText("aihubmix.com")
        app.buttons["PolicySave"].tap()
        let rule = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "PolicyRule-")).firstMatch
        XCTAssertTrue(rule.waitForExistence(timeout: 4))
        XCTAssertTrue(rule.label.contains("suffix:aihubmix.com"))
        rule.tap()
        XCTAssertEqual(app.textFields["PolicyMatchValue"].value as? String, "aihubmix.com")
        XCTAssertTrue(app.buttons["PolicyMatchType"].label.contains("Domain & all subdomains"))
        attachment("policy-domain-and-subdomains")
    }

    func testRealityAutoMemberCanBeExcludedWithoutChangingManualPick() {
        app.terminate()
        app.launchArguments.append("--reality-group-ui-test")
        app.launch()
        app.buttons["Proxy groups"].tap()
        app.buttons["Group-RealityAuto"].tap()
        let remove = app.buttons["Remove Review A from group"]
        XCTAssertTrue(remove.waitForExistence(timeout: 3))
        for _ in 0..<3 where !remove.isHittable { app.swipeUp() }
        remove.tap()
        app.buttons["GroupSave"].tap()
        XCTAssertTrue(app.buttons["Group-RealityAuto"].waitForExistence(timeout: 4))
        app.buttons["Group-RealityAuto"].tap()
        XCTAssertTrue(app.staticTexts["Selected members (1)"].exists)
        attachment("reality-auto-excluded-member")
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.buttons["Group-ManualPick"].tap()
        XCTAssertTrue(app.staticTexts["Selected members (2)"].exists)
        attachment("manual-pick-keeps-excluded-node")
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
