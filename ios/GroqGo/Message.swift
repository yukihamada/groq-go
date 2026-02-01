import Foundation

/// Represents a chat message
struct Message: Identifiable, Codable, Equatable {
    let id: UUID
    let role: Role
    var content: String
    let timestamp: Date
    var isStreaming: Bool

    enum Role: String, Codable {
        case user
        case assistant
        case system
        case tool
    }

    init(id: UUID = UUID(), role: Role, content: String, timestamp: Date = Date(), isStreaming: Bool = false) {
        self.id = id
        self.role = role
        self.content = content
        self.timestamp = timestamp
        self.isStreaming = isStreaming
    }
}

/// WebSocket message format matching the server
struct WSMessage: Codable {
    let type: String
    var content: String?
    var tool: String?
    var args: String?
    var result: String?
    var error: String?
    var model: String?
    var mode: String?

    enum CodingKeys: String, CodingKey {
        case type, content, tool, args, result, error, model, mode
    }
}

/// Available AI models
enum AIModel: String, CaseIterable, Identifiable {
    case llama33 = "llama-3.3-70b-versatile"
    case llama31 = "llama-3.1-8b-instant"
    case claudeSonnet = "claude-sonnet-4-20250514"
    case claude35Sonnet = "claude-3-5-sonnet-20241022"
    case gpt4o = "gpt-4o"
    case gpt4oMini = "gpt-4o-mini"

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .llama33: return "Llama 3.3 70B"
        case .llama31: return "Llama 3.1 8B"
        case .claudeSonnet: return "Claude Sonnet 4"
        case .claude35Sonnet: return "Claude 3.5 Sonnet"
        case .gpt4o: return "GPT-4o"
        case .gpt4oMini: return "GPT-4o Mini"
        }
    }
}

/// Chat mode
enum ChatMode: String, CaseIterable, Identifiable {
    case tools
    case improve

    var id: String { rawValue }

    var displayName: String {
        switch self {
        case .tools: return "Tools"
        case .improve: return "Improve"
        }
    }
}
