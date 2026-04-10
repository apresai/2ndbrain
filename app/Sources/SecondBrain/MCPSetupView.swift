import SwiftUI
import AppKit

struct MCPSetupView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Image(systemName: "network")
                    .foregroundStyle(.secondary)
                Text("Connect AI Tools (MCP)")
                    .font(.title3)
                    .fontWeight(.medium)
                Spacer()
            }
            .padding(12)

            Divider()

            // Content
            if let text = appState.mcpSetupText {
                ScrollView {
                    Text(text)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                }
            } else {
                VStack(spacing: 12) {
                    ProgressView()
                    Text("Loading setup instructions...")
                        .foregroundStyle(.secondary)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }

            Divider()

            // Footer
            HStack {
                Spacer()
                Button("Copy All") {
                    if let text = appState.mcpSetupText {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(text, forType: .string)
                    }
                }
                .disabled(appState.mcpSetupText == nil)
                Button("Done") {
                    appState.mcpSetupText = nil
                    isPresented = false
                }
                .buttonStyle(.borderedProminent)
            }
            .padding(12)
        }
        .frame(width: 600, height: 480)
        .onAppear {
            Task { await appState.loadMCPSetup() }
        }
    }
}
