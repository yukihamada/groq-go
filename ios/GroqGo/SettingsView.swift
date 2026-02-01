import SwiftUI

/// Settings view for configuring the app
struct SettingsView: View {
    @EnvironmentObject var viewModel: ChatViewModel
    @State private var tempServerURL: String = ""
    @State private var showingClearConfirmation = false

    var body: some View {
        NavigationStack {
            Form {
                serverSection
                modelSection
                modeSection
                dataSection
                aboutSection
            }
            .navigationTitle("Settings")
            .onAppear {
                tempServerURL = viewModel.serverURL
            }
        }
    }

    // MARK: - Server Section

    private var serverSection: some View {
        Section {
            HStack {
                Circle()
                    .fill(viewModel.isConnected ? Color.green : Color.red)
                    .frame(width: 10, height: 10)
                Text(viewModel.isConnected ? "Connected" : "Disconnected")
            }

            TextField("Server URL", text: $tempServerURL)
                .textContentType(.URL)
                .autocapitalization(.none)
                .autocorrectionDisabled()
                .keyboardType(.URL)

            Button("Connect") {
                viewModel.serverURL = tempServerURL
                viewModel.saveSettings()
                viewModel.disconnect()
                viewModel.connect()
            }
            .disabled(tempServerURL.isEmpty)
        } header: {
            Text("Server")
        } footer: {
            Text("Enter the WebSocket URL of your groq-go server")
        }
    }

    // MARK: - Model Section

    private var modelSection: some View {
        Section {
            Picker("Model", selection: $viewModel.selectedModel) {
                ForEach(AIModel.allCases) { model in
                    Text(model.displayName).tag(model)
                }
            }
            .onChange(of: viewModel.selectedModel) { _, _ in
                viewModel.saveSettings()
            }
        } header: {
            Text("AI Model")
        }
    }

    // MARK: - Mode Section

    private var modeSection: some View {
        Section {
            Picker("Mode", selection: $viewModel.selectedMode) {
                ForEach(ChatMode.allCases) { mode in
                    Text(mode.displayName).tag(mode)
                }
            }
            .pickerStyle(.segmented)
            .onChange(of: viewModel.selectedMode) { _, _ in
                viewModel.saveSettings()
            }
        } header: {
            Text("Chat Mode")
        } footer: {
            Text(modeDescription)
        }
    }

    private var modeDescription: String {
        switch viewModel.selectedMode {
        case .tools:
            return "Tools mode allows the AI to use various tools like file operations and web browsing."
        case .improve:
            return "Improve mode is for self-improvement and code modification tasks."
        }
    }

    // MARK: - Data Section

    private var dataSection: some View {
        Section {
            Button(role: .destructive) {
                showingClearConfirmation = true
            } label: {
                Label("Clear Chat History", systemImage: "trash")
            }
            .confirmationDialog(
                "Clear all messages?",
                isPresented: $showingClearConfirmation,
                titleVisibility: .visible
            ) {
                Button("Clear", role: .destructive) {
                    viewModel.clearMessages()
                }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("This action cannot be undone.")
            }
        } header: {
            Text("Data")
        }
    }

    // MARK: - About Section

    private var aboutSection: some View {
        Section {
            HStack {
                Text("Version")
                Spacer()
                Text("1.0.0")
                    .foregroundColor(.secondary)
            }

            Link(destination: URL(string: "https://github.com/groq-go")!) {
                HStack {
                    Text("GitHub")
                    Spacer()
                    Image(systemName: "arrow.up.right.square")
                        .foregroundColor(.secondary)
                }
            }
        } header: {
            Text("About")
        }
    }
}

#Preview {
    SettingsView()
        .environmentObject(ChatViewModel())
}
