import Foundation
import Combine

/// ViewModel for managing chat state and interactions
@MainActor
final class ChatViewModel: ObservableObject {
    @Published var messages: [Message] = []
    @Published var inputText: String = ""
    @Published var isLoading: Bool = false
    @Published var selectedModel: AIModel = .llama33
    @Published var selectedMode: ChatMode = .tools
    @Published var serverURL: String = "ws://localhost:8080/ws"

    private let webSocketClient = WebSocketClient()
    private var cancellables = Set<AnyCancellable>()
    private var currentStreamingMessageID: UUID?

    var isConnected: Bool {
        webSocketClient.isConnected
    }

    var connectionError: Error? {
        webSocketClient.connectionError
    }

    init() {
        setupBindings()
        loadSettings()
    }

    private func setupBindings() {
        webSocketClient.messagePublisher
            .receive(on: DispatchQueue.main)
            .sink { [weak self] message in
                self?.handleMessage(message)
            }
            .store(in: &cancellables)

        webSocketClient.$isConnected
            .receive(on: DispatchQueue.main)
            .sink { [weak self] connected in
                if connected {
                    self?.isLoading = false
                }
            }
            .store(in: &cancellables)
    }

    private func loadSettings() {
        if let savedURL = UserDefaults.standard.string(forKey: "serverURL") {
            serverURL = savedURL
        }
        if let savedModel = UserDefaults.standard.string(forKey: "selectedModel"),
           let model = AIModel(rawValue: savedModel) {
            selectedModel = model
        }
        if let savedMode = UserDefaults.standard.string(forKey: "selectedMode"),
           let mode = ChatMode(rawValue: savedMode) {
            selectedMode = mode
        }
    }

    func saveSettings() {
        UserDefaults.standard.set(serverURL, forKey: "serverURL")
        UserDefaults.standard.set(selectedModel.rawValue, forKey: "selectedModel")
        UserDefaults.standard.set(selectedMode.rawValue, forKey: "selectedMode")
    }

    func connect() {
        if let url = URL(string: serverURL) {
            webSocketClient.serverURL = url
        }
        webSocketClient.connect()
    }

    func disconnect() {
        webSocketClient.disconnect()
    }

    func sendMessage() {
        let trimmedText = inputText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedText.isEmpty else { return }

        let userMessage = Message(role: .user, content: trimmedText)
        messages.append(userMessage)
        inputText = ""
        isLoading = true

        webSocketClient.sendChat(
            content: trimmedText,
            model: selectedModel.rawValue,
            mode: selectedMode.rawValue
        )
    }

    func clearMessages() {
        messages.removeAll()
        currentStreamingMessageID = nil
    }

    private func handleMessage(_ wsMessage: WSMessage) {
        switch wsMessage.type {
        case "system":
            if let content = wsMessage.content {
                let systemMessage = Message(role: .system, content: content)
                messages.append(systemMessage)
            }

        case "content":
            if let content = wsMessage.content {
                if let streamingID = currentStreamingMessageID,
                   let index = messages.firstIndex(where: { $0.id == streamingID }) {
                    messages[index].content += content
                } else {
                    let newMessage = Message(role: .assistant, content: content, isStreaming: true)
                    currentStreamingMessageID = newMessage.id
                    messages.append(newMessage)
                }
            }

        case "done":
            if let streamingID = currentStreamingMessageID,
               let index = messages.firstIndex(where: { $0.id == streamingID }) {
                messages[index].isStreaming = false
            }
            currentStreamingMessageID = nil
            isLoading = false

        case "tool_call":
            if let tool = wsMessage.tool {
                let toolContent = "Using tool: \(tool)"
                let toolMessage = Message(role: .tool, content: toolContent)
                messages.append(toolMessage)
            }

        case "tool_result":
            if let result = wsMessage.result {
                if let index = messages.lastIndex(where: { $0.role == .tool }) {
                    messages[index].content += "\n\(result)"
                }
            }

        case "error":
            if let error = wsMessage.error {
                let errorMessage = Message(role: .system, content: "Error: \(error)")
                messages.append(errorMessage)
            }
            isLoading = false

        default:
            break
        }
    }
}
