import XCTest

/// End-to-end flows against the live dl server and the Trial test project.
/// Each test is run on its own (`-only-testing:`) as the agent side progresses.
final class SidekickFlows: XCTestCase {
    var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = true
        app = XCUIApplication()
        // Set TEST_RUNNER_SK_DICTATION_AUDIO=<wav> for xcodebuild to feed hold-to-talk a clip.
        if let clip = ProcessInfo.processInfo.environment["SK_DICTATION_AUDIO"],
           FileManager.default.fileExists(atPath: clip), name.contains("Hold") {
            app.launchArguments += ["-debugDictationAudio", clip]
        }
        app.launch()
    }

    func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }

    func openProject(_ name: String) {
        let row = app.staticTexts[name]
        XCTAssertTrue(row.waitForExistence(timeout: 20), "project \(name)")
        row.tap()
        sleep(2)
    }

    func openInbox(_ title: String) {
        app.tabBars.buttons["Inbox"].tap()
        let item = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", title)).firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 20), "inbox item \(title)")
        item.tap()
    }

    // MARK: Composer

    func testComposerSend() {
        openProject("Trial")
        let plus = app.descendants(matching: .any)["Compose"]
        XCTAssertTrue(plus.waitForExistence(timeout: 5))
        plus.tap()
        sleep(1)
        shot("composer-open")
        let field = app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch
        field.tap()
        field.typeText(ProcessInfo.processInfo.environment["SK_MESSAGE"] ?? "confirmed. Please run all four steps now.")
        app.buttons["Send"].tap()
        let delivered = app.staticTexts["Delivered · waiting for a reply"]
        XCTAssertTrue(delivered.waitForExistence(timeout: 15), "message delivered")
        shot("composer-sent")
        // Wait for the agent's reply to replace the waiting line.
        let deadline = Date().addingTimeInterval(240)
        while delivered.exists && Date() < deadline { sleep(5) }
        XCTAssertFalse(delivered.exists, "reply arrived")
        shot("composer-replied")
    }

    func testNoDoubleSend() {
        openProject("Trial")
        app.descendants(matching: .any)["Compose"].tap()
        sleep(1)
        let text = "Sidekick dedupe check \(Int(Date().timeIntervalSince1970)): reply with just ok."
        let field = app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch
        field.tap()
        field.typeText(text)
        app.buttons["Send"].tap()
        sleep(4)
        shot("after-send")
        let bubbles = app.staticTexts.matching(NSPredicate(format: "label == %@", text))
        XCTAssertEqual(bubbles.count, 1, "the message shows exactly once")
        XCTAssertFalse(app.tabBars.firstMatch.isHittable, "the chat is full screen: no tab bar")
        app.buttons["Close"].tap()
        sleep(1)
        XCTAssertTrue(app.tabBars.firstMatch.isHittable, "closing the chat brings the tab bar back")
    }

    /// An unread reply sits in the Inbox; opening it shows the conversation and marks it read.
    func testReplyInInbox() {
        app.tabBars.buttons["Inbox"].tap()
        let reply = app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'inbox reply ok'")).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 20), "unread reply is in the inbox")
        shot("reply-in-inbox")
        reply.tap()
        let field = app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the conversation opens, ready to answer")
        sleep(2)
        shot("reply-opened")
        app.buttons["Close"].tap()
        app.navigationBars.buttons.firstMatch.tap()
        sleep(3)
        shot("reply-read")
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS 'inbox reply ok'")).firstMatch.exists, "read replies leave Pending")
    }

    func testComposerHoldToTalk() {
        openProject("Trial")
        let plus = app.descendants(matching: .any)["Compose"]
        XCTAssertTrue(plus.waitForExistence(timeout: 5))
        // The test harness plays speech on the Mac while this holds.
        plus.press(forDuration: 7)
        sleep(4)
        shot("hold-released")
        let field = app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), "composer opened after hold")
        let text = (field.value as? String) ?? ""
        XCTAssertFalse(text.isEmpty || text.hasPrefix("Ask or tell"), "transcript landed: \(text)")
    }

    func testThreadMessage() {
        openProject("Trial")
        app.swipeUp()
        let row = app.buttons.containing(NSPredicate(format: "label CONTAINS 'Sidekick smoke-test thread'")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "thread row")
        row.tap()
        sleep(1)
        shot("thread-expanded")
        app.buttons["Open thread"].firstMatch.tap()
        sleep(3)
        shot("thread-page")
        app.descendants(matching: .any)["Compose"].tap()
        sleep(1)
        let field = app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch
        field.tap()
        field.typeText("Which haiku did you write? Paste it here.")
        app.buttons["Send"].tap()
        let delivered = app.staticTexts["Delivered · waiting for a reply"]
        XCTAssertTrue(delivered.waitForExistence(timeout: 15), "message delivered to thread")
        let deadline = Date().addingTimeInterval(240)
        while delivered.exists && Date() < deadline { sleep(5) }
        XCTAssertFalse(delivered.exists, "thread replied")
        shot("thread-replied")
    }

    // MARK: Project page

    func testProjectPage() {
        openProject("Trial")
        shot("project-top")
        app.swipeUp()
        sleep(1)
        shot("project-middle")
        app.swipeUp()
        sleep(1)
        shot("project-bottom")
    }

    func testMerchMakerThreads() {
        openProject("Merch Maker")
        app.swipeUp()
        sleep(1)
        shot("mm-threads")
        let row = app.staticTexts["Draft collections: in-context design review"]
        if row.waitForExistence(timeout: 5) {
            row.tap()
            sleep(1)
            shot("mm-thread-expanded")
            app.buttons["Open thread"].firstMatch.tap()
            sleep(3)
            shot("mm-thread-page")
        }
    }

    // MARK: Inbox and artifacts

    func testInbox() {
        app.tabBars.buttons["Inbox"].tap()
        sleep(3)
        shot("inbox")
    }

    func testArtifactHTML() {
        openInbox("Landing page v2")
        let js = app.webViews.staticTexts.containing(NSPredicate(format: "label BEGINSWITH 'JavaScript ran'")).firstMatch
        XCTAssertTrue(js.waitForExistence(timeout: 15), "page JS ran")
        shot("artifact-html")
        app.webViews.links["details page"].tap()
        XCTAssertTrue(app.webViews.staticTexts["Details"].waitForExistence(timeout: 10), "same-origin link stays in app")
        shot("artifact-html-details")
        XCTAssertTrue(app.buttons["Approve"].exists, "still on the review screen")
    }

    func testArtifactImage() { artifact("Product shot", "artifact-image") }
    func testArtifactAudio() { artifact("Voice note", "artifact-audio") }
    func testArtifactMarkdown() { artifact("Weekly report", "artifact-markdown") }
    func testArtifactPDF() { artifact("Contract draft", "artifact-pdf") }

    func testArtifactVideo() {
        openInbox("Demo video")
        sleep(4)
        shot("artifact-video")
        let video = app.webViews.firstMatch
        video.tap()
        sleep(3)
        shot("artifact-video-playing")
    }

    func artifact(_ title: String, _ name: String) {
        openInbox(title)
        sleep(5)
        shot(name)
    }

    func testRefine() {
        openInbox("Product shot")
        sleep(2)
        app.descendants(matching: .any).matching(identifier: "Refine").firstMatch.tap()
        sleep(1)
        shot("refine-open")
        app.typeText("Make the circle brighter and crop tighter.")
        shot("refine-typed")
        app.buttons["Send changes"].tap()
        sleep(3)
        shot("refine-sent")
    }

    func testApproveImage() {
        openInbox("Voice note")
        sleep(2)
        app.buttons["Approve"].tap()
        sleep(3)
        shot("approved")
    }

    func testRejectPDF() {
        openInbox("Contract draft")
        sleep(2)
        app.buttons["Reject"].firstMatch.tap()
        sleep(1)
        shot("reject-confirm")
        // The confirmation's own Reject button is the last one on screen.
        let rejects = app.buttons.matching(identifier: "Reject")
        XCTAssertGreaterThan(rejects.count, 1, "confirmation shown")
        rejects.element(boundBy: rejects.count - 1).tap()
        sleep(3)
        shot("rejected")
    }

    // MARK: Agent-driven items

    func testAnswerQuestion() {
        openInbox("haiku")
        sleep(1)
        shot("question")
        app.buttons.containing(NSPredicate(format: "label CONTAINS[c] 'ocean'")).firstMatch.tap()
        shot("question-picked")
        app.buttons["Submit answer"].tap()
        sleep(3)
        shot("question-answered")
    }

    func testApproveProposal() {
        openInbox("smoke-test thread")
        sleep(2)
        shot("proposal")
        app.buttons["Approve"].tap()
        sleep(3)
        shot("proposal-approved")
    }
}
