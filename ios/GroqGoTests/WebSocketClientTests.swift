import XCTest
import Combine
@testable import GroqGo

final class WebSocketClientTests: XCTestCase {
    var cancellables: Set<AnyCancellable>!

    override func setUpWithError() throws {
        cancellables = Set<AnyCancellable>()
    }

    override func tearDownWithError() throws {
        cancellables = nil
    }

    // MARK: - Initialization Tests

    func testClientInitialization() throws {
        let client = WebSocketClient()

        XCTAssertFalse(client.isConnected)
        XCTAssertNil(client.connectionError)
        XCTAssertNotNil(client.serverURL)
    }

    func testDefaultServerURL() throws {
        // Clear saved URL
        UserDefaults.standard.removeObject(forKey: "serverURL")

        let client = WebSocketClient()
        XCTAssertEqual(client.serverURL.absoluteString, "ws://localhost:8080/ws")
    }

    func testServerURLPersistence() throws {
        let testURL = URL(string: "ws://test.example.com/ws")!

        let client1 = WebSocketClient()
        client1.serverURL = testURL

        // Create new client to test persistence
        let client2 = WebSocketClient()
        XCTAssertEqual(client2.serverURL.absoluteString, testURL.absoluteString)

        // Clean up
        UserDefaults.standard.removeObject(forKey: "serverURL")
    }

    // MARK: - Connection State Tests

    func testDisconnectSetsState() throws {
        let client = WebSocketClient()
        client.disconnect()

        XCTAssertFalse(client.isConnected)
    }

    // MARK: - Message Publisher Tests

    func testMessagePublisherExists() throws {
        let client = WebSocketClient()
        let expectation = expectation(description: "Publisher should be accessible")

        client.messagePublisher
            .sink { _ in }
            .store(in: &cancellables)

        expectation.fulfill()
        wait(for: [expectation], timeout: 1.0)
    }

    // MARK: - WebSocket Error Tests

    func testWebSocketErrorDescriptions() throws {
        XCTAssertEqual(
            WebSocketError.notConnected.errorDescription,
            "Not connected to server"
        )
        XCTAssertEqual(
            WebSocketError.maxReconnectAttemptsReached.errorDescription,
            "Failed to reconnect after multiple attempts"
        )
    }
}
