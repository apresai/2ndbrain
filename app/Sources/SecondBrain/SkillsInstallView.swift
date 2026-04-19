import SwiftUI

struct SkillsInstallView: View {
    @Environment(AppState.self) var appState
    @Binding var isPresented: Bool

    private let agents = [
        "Claude Code", "Cursor", "Windsurf", "GitHub Copilot",
        "Kiro", "Cline", "Roo Code", "JetBrains Junie",
    ]

    var body: some View {
        VStack(spacing: 0) {
            // Header
            HStack {
                Image(systemName: "sparkles")
                    .foregroundStyle(.secondary)
                Text("Install AI Agent Skills")
                    .font(.title3)
                    .fontWeight(.medium)
                Spacer()
            }
            .padding(12)

            Divider()

            // Content
            VStack(alignment: .leading, spacing: 12) {
                Text("Installs SKILL.md files so AI coding agents know how to use 2ndbrain — CLI commands, MCP tools, and document format.")
                    .font(.callout)
                    .foregroundStyle(.secondary)

                VStack(alignment: .leading, spacing: 6) {
                    ForEach(agents, id: \.self) { agent in
                        HStack(spacing: 8) {
                            Image(systemName: "checkmark.circle")
                                .foregroundStyle(.green)
                                .font(.caption)
                            Text(agent)
                                .font(.body)
                        }
                    }
                }
                .padding(.vertical, 4)

                Text("Project-level install in your vault root. Use `2nb skills install --user` in Terminal for global install.")
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            .frame(maxHeight: .infinity, alignment: .top)

            // Result
            if let result = appState.skillsInstallResult {
                Divider()
                Text(result)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .padding(12)
            }

            Divider()

            // Footer
            HStack {
                if appState.isInstallingSkills {
                    ProgressView()
                        .controlSize(.small)
                    Text("Installing...")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button("Close") {
                    appState.skillsInstallResult = nil
                    isPresented = false
                }
                .keyboardShortcut(.cancelAction)
                if appState.skillsInstallResult == nil {
                    Button("Install All") {
                        Task { await appState.installSkills() }
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(appState.isInstallingSkills)
                }
            }
            .padding(12)
        }
        .frame(width: 420, height: 400)
    }
}
