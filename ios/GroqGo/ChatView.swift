import SwiftUI

/// Main chat interface view
struct ChatView: View {
    @EnvironmentObject var viewModel: ChatViewModel
    @FocusState private var isInputFocused: Bool
    @State private var showingModelPicker = false

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                connectionStatusBar
                messageList
                inputArea
            }
            .navigationTitle("Groq Go")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    connectionIndicator
                }
                ToolbarItem(placement: .topBarTrailing) {
                    modelButton
                }
            }
        }
    }

    // MARK: - Connection Status Bar

    private var connectionStatusBar: some View {
        Group {
            if !viewModel.isConnected {
                HStack {
                    Image(systemName: "wifi.slash")
                    Text("Disconnected")
                    Spacer()
                    Button("Reconnect") {
                        viewModel.connect()
                    }
                    .buttonStyle(.bordered)
                    .tint(.white)
                }
                .font(.caption)
                .padding(.horizontal)
                .padding(.vertical, 8)
                .background(Color.red.opacity(0.9))
                .foregroundColor(.white)
            }
        }
    }

    // MARK: - Message List

    private var messageList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 12) {
                    ForEach(viewModel.messages) { message in
                        MessageBubble(message: message)
                            .id(message.id)
                    }

                    if viewModel.isLoading {
                        LoadingIndicator()
                            .id("loading")
                    }
                }
                .padding()
            }
            .onChange(of: viewModel.messages.count) { _, _ in
                withAnimation {
                    if let lastMessage = viewModel.messages.last {
                        proxy.scrollTo(lastMessage.id, anchor: .bottom)
                    } else if viewModel.isLoading {
                        proxy.scrollTo("loading", anchor: .bottom)
                    }
                }
            }
        }
        .background(Color(.systemGroupedBackground))
    }

    // MARK: - Input Area

    private var inputArea: some View {
        VStack(spacing: 0) {
            Divider()
            HStack(spacing: 12) {
                TextField("Message...", text: $viewModel.inputText, axis: .vertical)
                    .textFieldStyle(.plain)
                    .lineLimit(1...5)
                    .focused($isInputFocused)
                    .padding(12)
                    .background(Color(.systemBackground))
                    .clipShape(RoundedRectangle(cornerRadius: 20))
                    .overlay(
                        RoundedRectangle(cornerRadius: 20)
                            .stroke(Color(.separator), lineWidth: 1)
                    )
                    .onSubmit {
                        sendMessage()
                    }

                sendButton
            }
            .padding()
            .background(Color(.secondarySystemBackground))
        }
    }

    private var sendButton: some View {
        Button(action: sendMessage) {
            Image(systemName: "arrow.up.circle.fill")
                .font(.system(size: 32))
                .foregroundStyle(canSend ? Color.purple : Color.gray)
        }
        .disabled(!canSend)
    }

    private var canSend: Bool {
        !viewModel.inputText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty &&
        viewModel.isConnected &&
        !viewModel.isLoading
    }

    // MARK: - Toolbar Items

    private var connectionIndicator: some View {
        Circle()
            .fill(viewModel.isConnected ? Color.green : Color.red)
            .frame(width: 10, height: 10)
    }

    private var modelButton: some View {
        Button(action: { showingModelPicker = true }) {
            HStack(spacing: 4) {
                Text(viewModel.selectedModel.displayName)
                    .font(.caption)
                Image(systemName: "chevron.down")
                    .font(.caption2)
            }
        }
        .confirmationDialog("Select Model", isPresented: $showingModelPicker) {
            ForEach(AIModel.allCases) { model in
                Button(model.displayName) {
                    viewModel.selectedModel = model
                    viewModel.saveSettings()
                }
            }
        }
    }

    // MARK: - Actions

    private func sendMessage() {
        guard canSend else { return }
        viewModel.sendMessage()
        isInputFocused = false
    }
}

// MARK: - Message Bubble

struct MessageBubble: View {
    let message: Message

    var body: some View {
        HStack {
            if message.role == .user {
                Spacer(minLength: 60)
            }

            VStack(alignment: message.role == .user ? .trailing : .leading, spacing: 4) {
                Text(message.content)
                    .padding(12)
                    .background(backgroundColor)
                    .foregroundColor(textColor)
                    .clipShape(RoundedRectangle(cornerRadius: 16))

                if message.isStreaming {
                    HStack(spacing: 4) {
                        ProgressView()
                            .scaleEffect(0.6)
                        Text("Streaming...")
                            .font(.caption2)
                            .foregroundColor(.secondary)
                    }
                }
            }

            if message.role != .user {
                Spacer(minLength: 60)
            }
        }
    }

    private var backgroundColor: Color {
        switch message.role {
        case .user:
            return .purple
        case .assistant:
            return Color(.systemBackground)
        case .system:
            return .orange.opacity(0.2)
        case .tool:
            return .blue.opacity(0.2)
        }
    }

    private var textColor: Color {
        switch message.role {
        case .user:
            return .white
        default:
            return .primary
        }
    }
}

// MARK: - Loading Indicator

struct LoadingIndicator: View {
    @State private var isAnimating = false

    var body: some View {
        HStack(spacing: 8) {
            ForEach(0..<3) { index in
                Circle()
                    .fill(Color.purple.opacity(0.6))
                    .frame(width: 8, height: 8)
                    .scaleEffect(isAnimating ? 1.0 : 0.5)
                    .animation(
                        .easeInOut(duration: 0.6)
                        .repeatForever()
                        .delay(Double(index) * 0.2),
                        value: isAnimating
                    )
            }
        }
        .padding()
        .onAppear {
            isAnimating = true
        }
    }
}

#Preview {
    ChatView()
        .environmentObject(ChatViewModel())
}
