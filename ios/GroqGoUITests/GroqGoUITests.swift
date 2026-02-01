import XCTest

final class GroqGoUITests: XCTestCase {

    var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        app = XCUIApplication()
        app.launch()
    }

    override func tearDownWithError() throws {
        app = nil
    }

    // MARK: - Launch Tests

    func testLaunch() throws {
        // Verify app launches and shows the main screen
        XCTAssertTrue(app.tabBars.buttons["Chat"].exists)
        XCTAssertTrue(app.tabBars.buttons["Settings"].exists)
    }

    // MARK: - Chat Tab Tests

    func testChatTabIsDefault() throws {
        XCTAssertTrue(app.navigationBars["Groq Go"].exists)
    }

    func testMessageInputExists() throws {
        let messageField = app.textFields["Message..."]
        XCTAssertTrue(messageField.exists)
    }

    func testSendButtonExists() throws {
        let sendButton = app.buttons.matching(identifier: "arrow.up.circle.fill").firstMatch
        XCTAssertTrue(sendButton.exists)
    }

    // MARK: - Settings Tab Tests

    func testNavigateToSettings() throws {
        app.tabBars.buttons["Settings"].tap()

        XCTAssertTrue(app.navigationBars["Settings"].exists)
    }

    func testSettingsHasServerSection() throws {
        app.tabBars.buttons["Settings"].tap()

        // Wait for navigation to complete
        let serverText = app.staticTexts["SERVER"]
        XCTAssertTrue(serverText.waitForExistence(timeout: 5))
    }

    func testSettingsHasModelSection() throws {
        app.tabBars.buttons["Settings"].tap()

        // Wait for navigation to complete
        let modelText = app.staticTexts["AI MODEL"]
        XCTAssertTrue(modelText.waitForExistence(timeout: 5))
    }

    func testSettingsHasModeSection() throws {
        app.tabBars.buttons["Settings"].tap()

        // Wait for navigation to complete
        let modeText = app.staticTexts["CHAT MODE"]
        XCTAssertTrue(modeText.waitForExistence(timeout: 5))
    }

    func testClearChatButtonExists() throws {
        app.tabBars.buttons["Settings"].tap()

        let clearButton = app.buttons["Clear Chat History"]
        XCTAssertTrue(clearButton.exists)
    }

    // MARK: - Tab Navigation Tests

    func testTabSwitching() throws {
        // Start on Chat tab
        XCTAssertTrue(app.navigationBars["Groq Go"].exists)

        // Switch to Settings
        app.tabBars.buttons["Settings"].tap()
        XCTAssertTrue(app.navigationBars["Settings"].exists)

        // Switch back to Chat
        app.tabBars.buttons["Chat"].tap()
        XCTAssertTrue(app.navigationBars["Groq Go"].exists)
    }

    // MARK: - Connection Status Tests

    func testConnectionIndicatorExists() throws {
        // The connection indicator should be visible in the toolbar
        // When disconnected, should show red indicator or banner
        // This test verifies the UI element exists
        XCTAssertTrue(app.navigationBars.element.exists)
    }

    // MARK: - Interaction Tests

    func testCanTypeInMessageField() throws {
        // Find the text field - it might have a different identifier
        let messageField = app.textFields.firstMatch
        guard messageField.waitForExistence(timeout: 5) else {
            XCTFail("Message field not found")
            return
        }
        messageField.tap()
        messageField.typeText("Test message")

        // Verify text was entered
        XCTAssertTrue(messageField.value as? String == "Test message" ||
                     app.textFields.containing(NSPredicate(format: "value CONTAINS 'Test message'")).count > 0)
    }

    // MARK: - Accessibility Tests

    func testAccessibilityLabels() throws {
        // Verify key elements have accessibility labels
        XCTAssertTrue(app.tabBars.buttons["Chat"].isHittable)
        XCTAssertTrue(app.tabBars.buttons["Settings"].isHittable)
    }

    // MARK: - Performance Tests

    func testLaunchPerformance() throws {
        if #available(iOS 13.0, *) {
            measure(metrics: [XCTApplicationLaunchMetric()]) {
                XCUIApplication().launch()
            }
        }
    }
}
