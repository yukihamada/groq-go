import SwiftUI

/// Main content view with tab navigation
struct ContentView: View {
    @EnvironmentObject var viewModel: ChatViewModel
    @State private var selectedTab = 0

    var body: some View {
        TabView(selection: $selectedTab) {
            ChatView()
                .tabItem {
                    Label("Chat", systemImage: "message.fill")
                }
                .tag(0)

            SettingsView()
                .tabItem {
                    Label("Settings", systemImage: "gear")
                }
                .tag(1)
        }
        .tint(.purple)
        .onAppear {
            viewModel.connect()
        }
    }
}

#Preview {
    ContentView()
        .environmentObject(ChatViewModel())
}
