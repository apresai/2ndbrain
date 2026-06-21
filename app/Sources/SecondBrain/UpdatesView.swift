import SwiftUI
import SecondBrainCore

/// Decoded `2nb update --json`. Mirrors the Go `cli.UpdateStatus` contract: a
/// drift in field names/casing fails `UpdateInfoDecodeTests`.
struct UpdateInfo: Decodable, Sendable {
    let current: String
    let latest: String?
    let updateAvailable: Bool
    let checked: Bool
    let detail: String?

    enum CodingKeys: String, CodingKey {
        case current, latest, checked, detail
        case updateAvailable = "update_available"
    }
}

/// Dashboard "Updates" tab: shows the app, CLI, and Obsidian-plugin versions
/// against the latest published release (via `2nb update --json`), with one-click
/// upgrades for the CLI and plugin and the copy-paste command for the app (which
/// can't cleanly replace itself while running).
struct UpdatesView: View {
    @Environment(AppState.self) var appState

    @State private var info: UpdateInfo?
    @State private var checking = false
    @State private var pluginVersion: String?
    @State private var upgrading: String?
    @State private var actionMessage: String?
    @State private var actionIsError = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                Divider()

                if checking && info == nil {
                    ProgressView("Checking for updates…").padding(.vertical, 8)
                } else if let info, info.checked, let latest = info.latest {
                    componentList(latest: latest)
                } else {
                    Text(info?.detail ?? "Couldn't check for updates. Check your connection and try again.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }

                if let actionMessage {
                    Text(actionMessage)
                        .font(.caption)
                        .foregroundStyle(actionIsError ? .red : .secondary)
                }
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task { await refresh() }
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Updates").font(.title2).fontWeight(.semibold)
                if let latest = info?.latest {
                    Text("Latest release: \(latest)")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer()
            Button(checking ? "Checking…" : "Check now") {
                Task { await refresh() }
            }
            .disabled(checking)
        }
    }

    @ViewBuilder
    private func componentList(latest: String) -> some View {
        VStack(spacing: 10) {
            componentRow(name: "SecondBrain app", current: appVersion, latest: latest) {
                // The running app can't cleanly replace its own bundle, so offer
                // the command rather than a one-click self-upgrade.
                copyableCommand("brew upgrade --cask apresai/tap/secondbrain")
            }
            componentRow(name: "2nb CLI", current: appState.cliVersion, latest: latest) {
                if let brew = BrewLocator.resolve() {
                    Button(upgrading == "cli" ? "Updating…" : "Update CLI") {
                        Task { await upgradeCLI(brew: brew) }
                    }
                    .disabled(upgrading != nil)
                    .controlSize(.small)
                } else {
                    copyableCommand("brew upgrade apresai/tap/twonb")
                }
            }
            componentRow(name: "Obsidian plugin", current: pluginVersion, latest: latest) {
                Button(upgrading == "plugin" ? "Updating…" : "Update plugin") {
                    Task { await upgradePlugin() }
                }
                .disabled(upgrading != nil || appState.vault == nil)
                .controlSize(.small)
            }
        }
    }

    @ViewBuilder
    private func componentRow<Action: View>(
        name: String,
        current: String?,
        latest: String,
        @ViewBuilder action: () -> Action
    ) -> some View {
        let outdated = CLIVersion.isOlder(cli: current, thanApp: latest)
        HStack(alignment: .top, spacing: 10) {
            Circle()
                .fill(outdated ? Color.orange : Color.green)
                .frame(width: 8, height: 8)
                .padding(.top, 5)
            VStack(alignment: .leading, spacing: 2) {
                Text(name).font(.callout).fontWeight(.medium)
                Text(current.map { "\($0)\(outdated ? "  →  \(latest)" : "  (up to date)")" } ?? "not detected")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if outdated { action() }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 8))
    }

    private func copyableCommand(_ cmd: String) -> some View {
        Text(cmd)
            .font(.caption.monospaced())
            .textSelection(.enabled)
            .foregroundStyle(.secondary)
    }

    // MARK: - Actions

    private func refresh() async {
        checking = true
        defer { checking = false }
        pluginVersion = appState.vault.flatMap { ObsidianPlugin.installedVersion(vaultRoot: $0.rootURL) }
        await appState.refreshCLIVersion()
        info = await appState.checkForUpdates()
    }

    private func upgradeCLI(brew: String) async {
        upgrading = "cli"
        actionMessage = nil
        defer { upgrading = nil }
        do {
            try await appState.upgradeCLI(brewPath: brew)
            actionIsError = false
            actionMessage = "CLI updated to \(appState.cliVersion ?? "the latest version")."
            info = await appState.checkForUpdates()
        } catch {
            actionIsError = true
            actionMessage = "CLI update failed: \(error.localizedDescription)"
        }
    }

    private func upgradePlugin() async {
        guard let vaultRoot = appState.vault?.rootURL else { return }
        upgrading = "plugin"
        actionMessage = nil
        defer { upgrading = nil }
        do {
            try await appState.installObsidianPlugin()
            pluginVersion = ObsidianPlugin.installedVersion(vaultRoot: vaultRoot)
            actionIsError = false
            actionMessage = "Obsidian plugin updated. Reload Obsidian to pick it up."
        } catch {
            actionIsError = true
            actionMessage = "Plugin update failed: \(error.localizedDescription)"
        }
    }
}
