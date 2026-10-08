import XCTest

/// End-to-end QA against the demo daemon (`demo/run.sh`): every flow creates its own
/// items through the test agent API, acting as the agents, so tests are independent
/// and nothing touches real projects.
///
/// Demo panes: Acme coordinator w1:p1, threads t-0012 w2:p1 (checkout), t-0014 w3:p1,
/// t-0015 w4:p1 (search), Field App coordinator w9:p1.
final class QAFlows: XCTestCase {
    var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
        app = XCUIApplication()
        app.launchArguments += ["-server", Demo.app.absoluteString]
        if name.contains("Offline") { app.launchArguments = ["-server", "http://127.0.0.1:1"] }
        if name.contains("Attachment"), let file = ProcessInfo.processInfo.environment["SK_ATTACH_FILE"] {
            app.launchArguments += ["-debugAttach", file]
        }
        if name.contains("Hold"), let clip = ProcessInfo.processInfo.environment["SK_DICTATION_AUDIO"] {
            app.launchArguments += ["-debugDictationAudio", clip]
        }
        app.launch()
    }

    // MARK: Helpers

    func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }

    func text(_ s: String) -> XCUIElement {
        app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    func button(_ s: String) -> XCUIElement {
        app.buttons.containing(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    var input: XCUIElement { app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch }

    func openProject(_ name: String) {
        app.tabBars.buttons["Projects"].tap()
        XCTAssertTrue(text(name).waitForExistence(timeout: 15), "project \(name) listed")
        text(name).tap()
    }

    func openInbox(_ title: String) {
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(text(title).waitForExistence(timeout: 15), "inbox shows \(title)")
        text(title).tap()
    }

    func unique(_ s: String) -> String { "\(s) \(Int(Date().timeIntervalSince1970 * 1000) % 1_000_000)" }

    // MARK: Projects and plan

    func testProjectsAndPlan() {
        XCTAssertTrue(text("Checkout ships this week").waitForExistence(timeout: 15), "project headline in the list")
        XCTAssertTrue(text("Offline sync in progress").exists)
        shot("qa-projects")
        openProject("Acme Storefront")
        for s in ["One-page checkout is done", "Focus", "Ship one-page checkout before the spring sale", "Priorities",
                  "Product image CDN", "Merge order", "Payment provider upgrade", "Up next", "Order tracking emails"] {
            if !text(s).exists { app.swipeUp() }
            XCTAssertTrue(text(s).waitForExistence(timeout: 5), "project page shows \(s)")
        }
        XCTAssertTrue(app.staticTexts["Review"].exists && app.staticTexts["Building"].exists, "stage chips")
        shot("qa-plan")
        // The Threads list row (not the plan's row of the same name) expands in place.
        let row = app.buttons.containing(NSPredicate(format: "label CONTAINS 'Product image CDN' AND label CONTAINS 'In progress'")).firstMatch
        for _ in 0..<4 where !row.isHittable { app.swipeUp() }
        XCTAssertTrue(row.waitForExistence(timeout: 5), "the thread is listed under Threads")
        row.tap()
        XCTAssertTrue(text("three sizes").waitForExistence(timeout: 5), "an expanded thread shows its summary")
        shot("qa-thread-expanded")
        app.buttons["Open thread"].firstMatch.tap()
        XCTAssertTrue(app.buttons["Show the full report"].waitForExistence(timeout: 5) || text("three sizes").exists, "Open thread opens it")
    }

    /// What Sidekick observes is applied to the plan: work pointing at a thread that
    /// doesn't exist is flagged, unlisted open threads are shown, and the page says
    /// it may be out of date.
    func testPlanFreshness() {
        Demo.request(Demo.agent.appending(path: "v1/plan"), "POST", ["origin": Demo.origin("w9:p1"), "plan": [
            "focus": "Offline sync for the inspection pilot",
            "work": [["title": "Photo uploads", "stage": "building", "thread": "t-0099"]],
        ]])
        openProject("Field App")
        XCTAssertTrue(text("Sidekick: thread not found").waitForExistence(timeout: 10), "a missing thread is flagged")
        if !text("Not in the plan").exists { app.swipeUp() }
        XCTAssertTrue(text("Not in the plan").waitForExistence(timeout: 5), "unlisted open threads are shown")
        XCTAssertTrue(text("Offline sync").exists)
        XCTAssertTrue(app.descendants(matching: .any)["plan.freshness"].label.contains("May be out of date"), "the page says it may be stale")
        shot("qa-plan-freshness")
    }

    func testThreadPage() {
        openProject("Acme Storefront")
        // A plan row with a thread opens that thread.
        button("One-page checkout").tap()
        XCTAssertTrue(text("Checkout is one page now").waitForExistence(timeout: 10), "thread summary")
        XCTAssertTrue(text("test-notes.md").exists && text("timings.csv").exists, "library files listed")
        app.swipeUp()
        let more = app.buttons["Show the full report"]
        XCTAssertTrue(more.waitForExistence(timeout: 5), "long reports start collapsed")
        XCTAssertFalse(text("Step 40").exists)
        more.tap()
        app.swipeUp()
        XCTAssertTrue(text("Step").waitForExistence(timeout: 5), "the full report expands")
        shot("qa-thread-report")
        app.swipeDown(); app.swipeDown(); app.swipeDown()
        text("test-notes.md").tap()
        XCTAssertTrue(text("All 14 checkout tests pass").waitForExistence(timeout: 10), "a markdown library file renders")
    }

    // MARK: Questions

    func testQuestionAnswer() {
        let q = unique("QA: which shipping carrier?")
        app.tabBars.buttons["Inbox"].tap()
        let id = Demo.ask(pane: "w1:p1", q, options: ["UPS (Recommended)::fastest", "FedEx", "USPS::cheapest"])
        XCTAssertTrue(text(q).waitForExistence(timeout: 10), "a new question arrives live, without refreshing")
        text(q).tap()
        XCTAssertTrue(button("USPS").waitForExistence(timeout: 5))
        shot("qa-question")
        button("USPS").tap()
        app.buttons["Submit answer"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10), "submitting goes back to the inbox")
        XCTAssertFalse(text(q).waitForExistence(timeout: 2), "an answered question leaves Pending")
        XCTAssertEqual(Demo.item(id)?["state"] as? String, "answered")
        XCTAssertEqual((Demo.item(id)?["answers"] as? [String: String])?[q], "USPS")
        app.buttons["Resolved"].tap()
        XCTAssertTrue(text(q).waitForExistence(timeout: 5), "it shows under Resolved")
        shot("qa-resolved")
    }

    func testQuestionWithContext() {
        let q = unique("QA: which logo should ship?")
        let id = Demo.askWithContext(pane: "w1:p1", q, options: ["Logo A", "Logo B"])
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(text(q).waitForExistence(timeout: 10))
        XCTAssertTrue(text("Question · with context").exists, "the inbox says it has context")
        text(q).tap()
        let card = app.descendants(matching: .any)["question.context"]
        XCTAssertTrue(card.waitForExistence(timeout: 10), "the context card is in the question")
        XCTAssertTrue(app.webViews.staticTexts.containing(NSPredicate(format: "label CONTAINS 'bolder square mark'")).firstMatch.waitForExistence(timeout: 15), "the page renders inline")
        shot("qa-question-context")
        button("Full screen").tap()
        XCTAssertTrue(app.webViews.staticTexts.containing(NSPredicate(format: "label CONTAINS 'Logo B'")).firstMatch.waitForExistence(timeout: 10), "full screen shows the page")
        app.navigationBars.buttons.firstMatch.tap()
        app.swipeUp()
        button("Logo B").tap()
        app.buttons["Submit answer"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10))
        XCTAssertEqual((Demo.item(id)?["answers"] as? [String: String])?[q], "Logo B")
    }

    func testQuestionOtherReturnAndLinks() {
        let q = unique("QA: what should the banner say? See https://example.com/brand")
        _ = Demo.ask(pane: "w1:p1", q, options: ["Spring sale::https://example.com/spring"])
        openInbox("what should the banner say")
        XCTAssertTrue(app.links.containing(NSPredicate(format: "label CONTAINS 'example.com/brand'")).firstMatch.waitForExistence(timeout: 5), "links in the question are links")
        input.tap()
        input.typeText("Sale ends Sunday\n")
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10), "Return submits a typed answer")
    }

    /// A note typed after picking an option goes with it; picking after typing keeps the note.
    func testQuestionOptionWithNote() {
        let q = unique("QA: ship the spring banner?")
        let id = Demo.ask(pane: "w1:p1", q, options: ["Ship it", "Hold"])
        openInbox("ship the spring banner")
        input.tap()
        input.typeText("Use the warmer photo")
        button("Ship it").tap()
        app.buttons["Submit answer"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10))
        XCTAssertEqual((Demo.item(id)?["answers"] as? [String: String])?[q], "Ship it. Note from the user: Use the warmer photo")
    }

    // MARK: Reviews and proposals

    func testReviewApprove() {
        let title = unique("QA review: hero banner")
        let id = Demo.review(pane: "w2:p1", title: title)
        openInbox(title)
        XCTAssertTrue(app.webViews.staticTexts.containing(NSPredicate(format: "label CONTAINS 'hero banner'")).firstMatch.waitForExistence(timeout: 15), "the artifact page renders full screen")
        shot("qa-review")
        app.buttons["Approve"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10))
        XCTAssertEqual(Demo.item(id)?["state"] as? String, "approved")
    }

    func testReviewRefine() {
        let title = unique("QA review: footer")
        let id = Demo.review(pane: "w2:p1", title: title)
        openInbox(title)
        app.descendants(matching: .any).matching(identifier: "Refine").firstMatch.tap()
        input.tap()
        input.typeText("Make the links bigger")
        shot("qa-refine")
        app.buttons["Send changes"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10))
        XCTAssertEqual(Demo.item(id)?["state"] as? String, "changes")
        XCTAssertEqual(Demo.item(id)?["comment"] as? String, "Make the links bigger")
    }

    func testReviewReject() {
        let title = unique("QA review: popup")
        let id = Demo.review(pane: "w2:p1", title: title)
        openInbox(title)
        app.buttons["Reject"].firstMatch.tap()
        let confirm = app.buttons.matching(identifier: "Reject")
        XCTAssertGreaterThan(confirm.count, 1, "rejecting asks first")
        confirm.element(boundBy: confirm.count - 1).tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10))
        XCTAssertEqual(Demo.item(id)?["state"] as? String, "rejected")
    }

    func testProposalApproveAndDecline() {
        let a = unique("QA proposal: loyalty points"), b = unique("QA proposal: dark mode")
        let ida = Demo.propose(pane: "w1:p1", title: a), idb = Demo.propose(pane: "w1:p1", title: b)
        openInbox("loyalty points")
        XCTAssertTrue(text("customers keep asking").waitForExistence(timeout: 5), "a proposal shows why")
        app.buttons["Approve"].tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10))
        text("dark mode").tap()
        app.buttons["Decline"].firstMatch.tap()
        let confirm = app.buttons.matching(identifier: "Decline")
        confirm.element(boundBy: confirm.count - 1).tap()
        XCTAssertTrue(app.staticTexts["Inbox"].waitForExistence(timeout: 10))
        XCTAssertEqual(Demo.item(ida)?["state"] as? String, "approved")
        XCTAssertEqual(Demo.item(idb)?["state"] as? String, "rejected")
    }

    func testSwipes() {
        let r = unique("QA swipe review"), q = unique("QA swipe question")
        let rid = Demo.review(pane: "w2:p1", title: r)
        let qid = Demo.ask(pane: "w1:p1", q, options: ["Yes (Recommended)", "No"])
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(text(r).waitForExistence(timeout: 10))
        text(r).swipeRight()
        if app.buttons["Approve"].waitForExistence(timeout: 2) { app.buttons["Approve"].tap() }
        XCTAssertTrue(text(r).waitForNonExistence(timeout: 10))
        text(q).swipeRight()
        if button("Yes (Recommended)").waitForExistence(timeout: 2) { button("Yes (Recommended)").tap() }
        XCTAssertTrue(text(q).waitForNonExistence(timeout: 10))
        XCTAssertEqual(Demo.item(rid)?["state"] as? String, "approved")
        XCTAssertEqual((Demo.item(qid)?["answers"] as? [String: String])?[q], "Yes (Recommended)")
    }

    // MARK: Chat

    func testChatRoundTrip() {
        openProject("Acme Storefront")
        app.descendants(matching: .any)["Compose"].tap()
        let msg = unique("QA chat: is checkout ready?")
        input.tap()
        input.typeText(msg)
        app.buttons["composer.send"].tap()
        XCTAssertTrue(text(msg).waitForExistence(timeout: 10), "the message shows")
        XCTAssertTrue(app.staticTexts["Delivered · waiting for a reply"].exists)
        let id = Demo.pendingMessage(pane: "w1:p1", text: msg)
        XCTAssertNotNil(id, "the coordinator received the message")
        Demo.reply(id!, "Yes, checkout is ready: CI is green.")
        XCTAssertTrue(text("checkout is ready: CI is green").waitForExistence(timeout: 10), "the reply arrives live")
        shot("qa-chat")
        app.buttons["Close"].tap()

        // A second reply while the chat is closed: one inbox row for the conversation.
        let id2 = Demo.sendMessage(project: "acme", "QA chat follow-up")
        Demo.reply(id2, "Follow-up answered.")
        let id3 = Demo.sendMessage(project: "acme", "QA chat follow-up 2")
        Demo.reply(id3, "Second follow-up answered.")
        app.tabBars.buttons["Inbox"].tap()
        XCTAssertTrue(text("Second follow-up answered").waitForExistence(timeout: 10), "the conversation row shows the newest reply")
        XCTAssertTrue(text("2 new").exists, "and how many are new")
        XCTAssertFalse(text("Follow-up answered.").exists, "older replies don't get their own rows")
        text("Second follow-up answered").tap()
        XCTAssertTrue(input.waitForExistence(timeout: 10), "opening the row opens the conversation")
        app.buttons["Close"].tap()
        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(text("Second follow-up answered").waitForNonExistence(timeout: 10), "a read conversation leaves Pending")
    }

    func testChatPickProjectAndReturn() {
        app.descendants(matching: .any)["Compose"].tap()
        XCTAssertTrue(text("coordinator").waitForExistence(timeout: 5))
        button("coordinator").tap()
        app.buttons["Field App"].tap()
        XCTAssertTrue(text("Field App · coordinator").waitForExistence(timeout: 5), "the project picker switches the recipient")
        let msg = unique("QA picker: sync status?")
        input.tap()
        input.typeText(msg + "\n")
        XCTAssertTrue(text(msg).waitForExistence(timeout: 10), "Return sends")
        XCTAssertNotNil(Demo.pendingMessage(pane: "w9:p1", text: msg), "it went to Field App's coordinator")
    }

    func testThreadChat() {
        openProject("Acme Storefront")
        button("Product image CDN").tap() // the plan row opens the thread
        XCTAssertTrue(text("three sizes").waitForExistence(timeout: 10))
        app.descendants(matching: .any)["Compose"].tap()
        XCTAssertTrue(text("Product image CDN").waitForExistence(timeout: 5), "the chat is addressed to the thread")
        let msg = unique("QA thread chat")
        input.tap()
        input.typeText(msg + "\n")
        let id = Demo.pendingMessage(pane: "w3:p1", text: msg)
        XCTAssertNotNil(id, "the thread received it")
        Demo.reply(id!, "Thread reply received.")
        XCTAssertTrue(text("Thread reply received").waitForExistence(timeout: 10))
    }

    func testAttachment() {
        openProject("Acme Storefront")
        app.descendants(matching: .any)["Compose"].tap()
        XCTAssertTrue(button("Remove").waitForExistence(timeout: 10), "the file waits in the tray")
        sleep(2)
        input.tap()
        let msg = unique("QA attachment")
        input.typeText(msg)
        app.buttons["composer.send"].tap()
        XCTAssertTrue(button("Attachment ").waitForExistence(timeout: 10), "the sent message shows its attachment")
        let id = Demo.pendingMessage(pane: "w1:p1", text: msg)
        let atts = (Demo.item(id ?? "")?["attachments"] as? [[String: Any]]) ?? []
        XCTAssertEqual(atts.count, 1, "the server stored the attachment")
        let url = URL(string: atts.first?["url"] as? String ?? "")!
        var status = 0
        let done = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: url) { _, resp, _ in status = (resp as? HTTPURLResponse)?.statusCode ?? 0; done.signal() }.resume()
        done.wait()
        XCTAssertEqual(status, 200, "the stored file is served")
    }

    func testHoldToTalk() {
        openProject("Acme Storefront")
        app.descendants(matching: .any)["Compose"].press(forDuration: 3)
        XCTAssertTrue(input.waitForExistence(timeout: 15))
        let deadline = Date().addingTimeInterval(15)
        while ((input.value as? String) ?? "").hasPrefix("Ask") && Date() < deadline { sleep(1) }
        XCTAssertTrue(((input.value as? String) ?? "").lowercased().contains("smoke test"), "the transcript lands in the input: \(input.value ?? "")")
    }

    // MARK: Plan links, live updates, settings, offline

    func testBlockedWorkLinksToQuestion() {
        let q = unique("QA ranking: best sellers or closest match?")
        _ = Demo.ask(pane: "w4:p1", q, options: ["Best sellers", "Closest match"])
        openProject("Acme Storefront")
        let link = text("Waiting on your answer")
        for _ in 0..<3 where !link.exists { app.swipeUp() }
        XCTAssertTrue(link.waitForExistence(timeout: 10), "blocked work shows it waits on the user")
        button("Search suggestions").tap()
        XCTAssertTrue(text("ranking").waitForExistence(timeout: 10), "tapping it opens the question")
        XCTAssertTrue(app.buttons["Submit answer"].exists)
    }

    func badge() -> Int {
        let v = app.tabBars.buttons["Inbox"].value as? String ?? ""
        return Int(v.filter(\.isNumber)) ?? 0
    }

    func testLiveBadge() {
        _ = app.tabBars.buttons["Inbox"].waitForExistence(timeout: 10)
        sleep(2)
        let before = badge()
        _ = Demo.ask(pane: "w1:p1", unique("QA badge"), options: ["A", "B"])
        let deadline = Date().addingTimeInterval(10)
        var after = before
        while after <= before && Date() < deadline {
            sleep(1)
            after = badge()
        }
        XCTAssertGreaterThan(after, before, "the Inbox badge updates live")
    }

    /// A push (sent by the harness with `simctl push` once this question exists) shows a
    /// banner; tapping it opens the question, from the background or the foreground.
    func testPushDeepLink() {
        let q = unique("QA push: approve the new font?")
        _ = Demo.ask(pane: "w1:p1", q, options: ["Yes", "No"])
        if ProcessInfo.processInfo.environment["SK_PUSH_FROM_BACKGROUND"] != nil { XCUIDevice.shared.press(.home) }
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let banner = springboard.descendants(matching: .any)
            .matching(NSPredicate(format: "label CONTAINS 'approve the new font'")).firstMatch
        XCTAssertTrue(banner.waitForExistence(timeout: 60), "the push arrives as a banner")
        shot("qa-push-banner")
        banner.tap()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10))
        XCTAssertTrue(app.buttons["Submit answer"].waitForExistence(timeout: 10), "tapping it opens the question")
        XCTAssertTrue(text("approve the new font").exists)
        shot("qa-push-opened")
    }

    func testLaunchState() {
        sleep(4)
        shot("qa-launch")
        XCTAssertTrue(app.staticTexts["Sidekick"].exists, "the app launches on the Projects list")
    }

    func testSettings() {
        app.staticTexts["Sidekick"].press(forDuration: 1.2)
        XCTAssertTrue(app.staticTexts["Settings"].waitForExistence(timeout: 5) || app.navigationBars["Settings"].exists, "long-pressing the title opens Settings")
        XCTAssertTrue(text("Live").exists, "the connection is live")
        shot("qa-settings")
        app.buttons["Done"].tap()
    }

    func testOffline() {
        XCTAssertTrue(text("Can't reach").waitForExistence(timeout: 20), "an unreachable server says so")
        shot("qa-offline")
    }
}

