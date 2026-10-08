import XCTest

/// QA for the Mac app against the demo daemon (`demo/run.sh`), acting as agents through
/// the test agent API (see Demo.swift).
final class MacQAFlows: XCTestCase {
    var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
        app = XCUIApplication()
        app.launchArguments += ["-server", Demo.app.absoluteString]
        app.launch()
        app.activate()
    }

    override func tearDown() { app.terminate() }

    /// Text in the main window. (The menu bar menu lists waiting items too; skip it.)
    func text(_ s: String) -> XCUIElement {
        app.windows.firstMatch.staticTexts.containing(NSPredicate(format: "value CONTAINS %@ OR label CONTAINS %@", s, s)).firstMatch
    }

    func unique(_ s: String) -> String { "\(s) \(Int(Date().timeIntervalSince1970 * 1000) % 1_000_000)" }

    func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.windows.firstMatch.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }

    var input: XCUIElement { app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch }

    func sidebar(_ name: String) {
        let row = app.outlines.firstMatch.staticTexts[name]
        XCTAssertTrue(row.waitForExistence(timeout: 15), "sidebar lists \(name)")
        row.click()
    }

    func testSidebarAndProject() {
        XCTAssertTrue(app.outlines.firstMatch.staticTexts["Inbox"].waitForExistence(timeout: 15), "the sidebar has the Inbox")
        sidebar("Acme Storefront")
        XCTAssertTrue(text("Checkout ships this week").waitForExistence(timeout: 10), "the project page opens in the main pane")
        XCTAssertTrue(text("Priorities").exists && text("Merge order").exists, "with its plan")
        shot("mac-project")
        sidebar("Field App")
        XCTAssertTrue(text("Offline sync in progress").waitForExistence(timeout: 10), "switching projects")
    }

    func testAnswerQuestion() {
        let q = unique("Mac QA: which region?")
        let id = Demo.ask(pane: "w1:p1", q, options: ["EU (Recommended)", "US"])
        sidebar("Inbox")
        XCTAssertTrue(text(q).waitForExistence(timeout: 15), "the question arrives in the inbox")
        text(q).click()
        let us = app.buttons.containing(NSPredicate(format: "label CONTAINS 'US'")).firstMatch
        XCTAssertTrue(us.waitForExistence(timeout: 5))
        us.click()
        shot("mac-question")
        app.buttons["Submit answer"].click()
        XCTAssertTrue(text(q).waitForNonExistence(timeout: 10), "an answered question leaves Pending")
        XCTAssertEqual((Demo.item(id)?["answers"] as? [String: String])?[q], "US")
    }

    func testApproveReview() {
        let title = unique("Mac QA review: pricing page")
        let id = Demo.review(pane: "w2:p1", title: title)
        sidebar("Inbox")
        XCTAssertTrue(text(title).waitForExistence(timeout: 15))
        text(title).click()
        XCTAssertTrue(app.webViews.firstMatch.waitForExistence(timeout: 15), "the artifact renders")
        shot("mac-review")
        app.buttons["Approve"].click()
        XCTAssertTrue(text(title).waitForNonExistence(timeout: 10))
        XCTAssertEqual(Demo.item(id)?["state"] as? String, "approved")
    }

    /// Return sends, Shift-Return adds a line, Esc closes; the agent's reply arrives live.
    func testChatKeys() {
        sidebar("Acme Storefront")
        let compose = app.descendants(matching: .any)["Compose"]
        XCTAssertTrue(compose.waitForExistence(timeout: 10))
        compose.click()
        XCTAssertTrue(input.waitForExistence(timeout: 10), "the chat opens")
        let first = unique("Mac QA line one")
        input.click()
        input.typeText(first)
        input.typeKey(.return, modifierFlags: .shift)
        input.typeText("line two")
        XCTAssertNil(Demo.pendingMessage(pane: "w1:p1", text: first, tries: 2), "Shift-Return doesn't send")
        input.typeKey(.return, modifierFlags: [])
        let id = Demo.pendingMessage(pane: "w1:p1", text: first)
        XCTAssertNotNil(id, "Return sends")
        let sent = Demo.item(id ?? "")?["text"] as? String ?? ""
        XCTAssertTrue(sent.contains("\nline two"), "Shift-Return made a new line: \(sent)")
        Demo.reply(id!, "Mac reply received.")
        XCTAssertTrue(text("Mac reply received").waitForExistence(timeout: 10), "the reply arrives live")
        shot("mac-chat")
        app.typeKey(.escape, modifierFlags: [])
        XCTAssertTrue(compose.waitForExistence(timeout: 5), "Esc closes the chat")
    }

    func testSettingsWindow() {
        app.typeKey(",", modifierFlags: .command)
        let settings = app.windows.containing(NSPredicate(format: "title CONTAINS 'Settings' OR identifier CONTAINS 'Settings'")).firstMatch
        XCTAssertTrue(settings.waitForExistence(timeout: 5) || app.staticTexts["Server"].waitForExistence(timeout: 5), "Cmd-, opens Settings")
        XCTAssertTrue(text("Live").waitForExistence(timeout: 10), "the connection is live")
    }

    func testMenuBarItem() {
        let item = app.statusItems.firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 10), "Sidekick is in the menu bar")
        item.click()
        XCTAssertTrue(app.menuItems["Open Sidekick"].waitForExistence(timeout: 5), "its menu lists what's waiting and Open Sidekick")
        app.menuItems["Open Sidekick"].click()
    }
}
