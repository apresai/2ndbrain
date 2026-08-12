import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

struct PreferencesView: View {
    @Environment(AppState.self) var appState

    @State private var region = ""
    @State private var token = ""
    @State private var status: BedrockMachineStatus?
    @State private var loading = true
    @State private var saving = false
    @State private var message: String?

    var body: some View {
        Form {
            Section {
                Text("Machine-local Bedrock credentials. Saved to ~/.config/2nb/bedrock.json so the dashboard and a terminal 2nb share the same key. The token is never written into a vault.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }

            Section("AWS Bedrock") {
                TextField("Region", text: $region, prompt: Text("us-east-1"))
                    .textFieldStyle(.roundedBorder)

                SecureField("API key (bearer token)", text: $token)
                    .textFieldStyle(.roundedBorder)

                HStack {
                    Button(saving ? "Saving…" : "Save") {
                        Task { await save() }
                    }
                    .disabled(saving || (region.trimmingCharacters(in: .whitespaces).isEmpty && token.isEmpty))
                    .keyboardShortcut(.defaultAction)

                    Button("Clear token") {
                        Task { await clearToken() }
                    }
                    .disabled(saving || !(status?.tokenSet ?? false))

                    Spacer()
                }

                if let status {
                    Text(statusCaption(status))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }

                if let message {
                    Text(message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .formStyle(.grouped)
        .frame(minWidth: 440, minHeight: 280)
        .task { await reload() }
    }

    private func statusCaption(_ status: BedrockMachineStatus) -> String {
        let tokenLine: String
        if status.tokenSet {
            tokenLine = "Token set (\(status.tokenSource))"
        } else {
            tokenLine = "Token not set"
        }
        let regionLine = status.region.isEmpty
            ? "Region not set in file (vault ai.bedrock.region is used)"
            : "Region \(status.region)"
        if let err = status.error, !err.isEmpty {
            return "\(err). \(tokenLine). File: \(status.path)"
        }
        return "\(regionLine). \(tokenLine). File: \(status.path)"
    }

    private func reload() async {
        loading = true
        defer { loading = false }
        do {
            let st = try await appState.refreshBedrockMachineConfig()
            status = st
            if region.isEmpty {
                region = st.region
            }
        } catch {
            message = error.localizedDescription
        }
    }

    private func save() async {
        saving = true
        defer { saving = false }
        do {
            let trimmedToken = token.trimmingCharacters(in: .whitespacesAndNewlines)
            let st = try await appState.saveBedrockMachineConfig(
                region: region.trimmingCharacters(in: .whitespacesAndNewlines),
                token: trimmedToken.isEmpty ? nil : trimmedToken
            )
            status = st
            token = ""
            message = "Saved."
        } catch {
            presentSaveError(error.localizedDescription)
        }
    }

    private func clearToken() async {
        saving = true
        defer { saving = false }
        do {
            let st = try await appState.clearBedrockToken()
            status = st
            token = ""
            message = "Token cleared."
        } catch {
            presentSaveError(error.localizedDescription)
        }
    }

    private func presentSaveError(_ text: String) {
        message = text
        #if canImport(AppKit)
        let alert = NSAlert()
        alert.messageText = "Could not save Bedrock credentials"
        alert.informativeText = text
        alert.alertStyle = .warning
        alert.addButton(withTitle: "OK")
        alert.runModal()
        #endif
    }
}
