import XCTest
@testable import GroqGo

final class GroqGoTests: XCTestCase {

    override func setUpWithError() throws {
        // Put setup code here
    }

    override func tearDownWithError() throws {
        // Put teardown code here
    }

    // MARK: - Message Tests

    func testMessageInitialization() throws {
        let message = Message(role: .user, content: "Hello")

        XCTAssertEqual(message.role, .user)
        XCTAssertEqual(message.content, "Hello")
        XCTAssertFalse(message.isStreaming)
        XCTAssertNotNil(message.id)
    }

    func testMessageRoles() throws {
        XCTAssertEqual(Message.Role.user.rawValue, "user")
        XCTAssertEqual(Message.Role.assistant.rawValue, "assistant")
        XCTAssertEqual(Message.Role.system.rawValue, "system")
        XCTAssertEqual(Message.Role.tool.rawValue, "tool")
    }

    func testMessageEquality() throws {
        let id = UUID()
        let timestamp = Date()

        let message1 = Message(id: id, role: .user, content: "Test", timestamp: timestamp)
        let message2 = Message(id: id, role: .user, content: "Test", timestamp: timestamp)

        XCTAssertEqual(message1, message2)
    }

    // MARK: - WSMessage Tests

    func testWSMessageEncoding() throws {
        let message = WSMessage(
            type: "chat",
            content: "Hello",
            model: "llama-3.3-70b-versatile",
            mode: "tools"
        )

        let encoder = JSONEncoder()
        let data = try encoder.encode(message)
        let json = try JSONSerialization.jsonObject(with: data) as? [String: Any]

        XCTAssertEqual(json?["type"] as? String, "chat")
        XCTAssertEqual(json?["content"] as? String, "Hello")
        XCTAssertEqual(json?["model"] as? String, "llama-3.3-70b-versatile")
        XCTAssertEqual(json?["mode"] as? String, "tools")
    }

    func testWSMessageDecoding() throws {
        let json = """
        {
            "type": "content",
            "content": "Test response"
        }
        """.data(using: .utf8)!

        let decoder = JSONDecoder()
        let message = try decoder.decode(WSMessage.self, from: json)

        XCTAssertEqual(message.type, "content")
        XCTAssertEqual(message.content, "Test response")
    }

    // MARK: - AIModel Tests

    func testAIModelDisplayNames() throws {
        XCTAssertEqual(AIModel.llama33.displayName, "Llama 3.3 70B")
        XCTAssertEqual(AIModel.claudeSonnet.displayName, "Claude Sonnet 4")
        XCTAssertEqual(AIModel.gpt4o.displayName, "GPT-4o")
    }

    func testAIModelRawValues() throws {
        XCTAssertEqual(AIModel.llama33.rawValue, "llama-3.3-70b-versatile")
        XCTAssertEqual(AIModel.claudeSonnet.rawValue, "claude-sonnet-4-20250514")
    }

    // MARK: - ChatMode Tests

    func testChatModeValues() throws {
        XCTAssertEqual(ChatMode.tools.rawValue, "tools")
        XCTAssertEqual(ChatMode.improve.rawValue, "improve")
        XCTAssertEqual(ChatMode.tools.displayName, "Tools")
        XCTAssertEqual(ChatMode.improve.displayName, "Improve")
    }
}
