import Foundation
import Combine

/// WebSocket client for communicating with the groq-go server
final class WebSocketClient: NSObject, ObservableObject {
    @Published private(set) var isConnected = false
    @Published private(set) var connectionError: Error?

    private var webSocketTask: URLSessionWebSocketTask?
    private var urlSession: URLSession!
    private let messageSubject = PassthroughSubject<WSMessage, Never>()
    private var reconnectAttempts = 0
    private let maxReconnectAttempts = 5
    private var reconnectWorkItem: DispatchWorkItem?

    var messagePublisher: AnyPublisher<WSMessage, Never> {
        messageSubject.eraseToAnyPublisher()
    }

    var serverURL: URL {
        didSet {
            UserDefaults.standard.set(serverURL.absoluteString, forKey: "serverURL")
        }
    }

    override init() {
        if let savedURL = UserDefaults.standard.string(forKey: "serverURL"),
           let url = URL(string: savedURL) {
            self.serverURL = url
        } else {
            self.serverURL = URL(string: "ws://localhost:8080/ws")!
        }
        super.init()
        self.urlSession = URLSession(configuration: .default, delegate: self, delegateQueue: .main)
    }

    func connect() {
        guard webSocketTask == nil || webSocketTask?.state != .running else { return }

        disconnect()

        webSocketTask = urlSession.webSocketTask(with: serverURL)
        webSocketTask?.resume()

        receiveMessage()
    }

    func disconnect() {
        reconnectWorkItem?.cancel()
        webSocketTask?.cancel(with: .goingAway, reason: nil)
        webSocketTask = nil
        DispatchQueue.main.async {
            self.isConnected = false
        }
    }

    func send(_ message: WSMessage) {
        guard isConnected else {
            connectionError = WebSocketError.notConnected
            return
        }

        do {
            let data = try JSONEncoder().encode(message)
            let string = String(data: data, encoding: .utf8) ?? ""
            webSocketTask?.send(.string(string)) { [weak self] error in
                if let error = error {
                    DispatchQueue.main.async {
                        self?.connectionError = error
                    }
                }
            }
        } catch {
            connectionError = error
        }
    }

    func sendChat(content: String, model: String, mode: String) {
        let message = WSMessage(
            type: "chat",
            content: content,
            model: model,
            mode: mode
        )
        send(message)
    }

    private func receiveMessage() {
        webSocketTask?.receive { [weak self] result in
            guard let self = self else { return }

            switch result {
            case .success(let message):
                switch message {
                case .string(let text):
                    self.handleMessage(text)
                case .data(let data):
                    if let text = String(data: data, encoding: .utf8) {
                        self.handleMessage(text)
                    }
                @unknown default:
                    break
                }
                self.receiveMessage()

            case .failure(let error):
                DispatchQueue.main.async {
                    self.connectionError = error
                    self.isConnected = false
                }
                self.scheduleReconnect()
            }
        }
    }

    private func handleMessage(_ text: String) {
        guard let data = text.data(using: .utf8),
              let message = try? JSONDecoder().decode(WSMessage.self, from: data) else {
            return
        }

        DispatchQueue.main.async {
            self.messageSubject.send(message)
        }
    }

    private func scheduleReconnect() {
        guard reconnectAttempts < maxReconnectAttempts else {
            connectionError = WebSocketError.maxReconnectAttemptsReached
            return
        }

        reconnectAttempts += 1
        let delay = Double(reconnectAttempts) * 2.0

        reconnectWorkItem = DispatchWorkItem { [weak self] in
            self?.connect()
        }

        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: reconnectWorkItem!)
    }
}

extension WebSocketClient: URLSessionWebSocketDelegate {
    func urlSession(_ session: URLSession, webSocketTask: URLSessionWebSocketTask, didOpenWithProtocol protocol: String?) {
        DispatchQueue.main.async {
            self.isConnected = true
            self.connectionError = nil
            self.reconnectAttempts = 0
        }
    }

    func urlSession(_ session: URLSession, webSocketTask: URLSessionWebSocketTask, didCloseWith closeCode: URLSessionWebSocketTask.CloseCode, reason: Data?) {
        DispatchQueue.main.async {
            self.isConnected = false
        }
        scheduleReconnect()
    }
}

enum WebSocketError: LocalizedError {
    case notConnected
    case maxReconnectAttemptsReached

    var errorDescription: String? {
        switch self {
        case .notConnected:
            return "Not connected to server"
        case .maxReconnectAttemptsReached:
            return "Failed to reconnect after multiple attempts"
        }
    }
}
