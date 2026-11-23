import SwiftUI
import SecondBrainCore
#if canImport(AppKit)
import AppKit
#endif

/// The Settings window's tabs, selectable in code so any button across the
/// dashboard can open Settings on the intended tab.
enum SettingsTab: String {
    case ai, integrations, general
}

/// A Button that opens the Cmd+, Settings window on a specific tab.
struct OpenSettingsTabButton: View {
    @Environment(AppState.self) var appState
    @Environment(\.openSettings) private var openSettings
    let title: String
    let tab: SettingsTab

    init(_ title: String, tab: SettingsTab) {
        self.title = title
        self.tab = tab
    }

    var body: some View {
        Button(title) {
            appState.noteSettingsOpened(tab: tab)
            openSettings()
        }
    }
}

/// The macOS Settings window (Cmd+,) — single host for all configuration:
/// Gemini credentials & models, AI client integrations, and General/Updates.
struct SettingsView: View {
    @Environment(AppState.self) var appState

    var body: some View {
        @Bindable var state = appState
        // `Tab(_:systemImage:content:)` is macOS 15+; this app deploys to 14,
        // so the tabs use the .tabItem form.
        TabView(selection: $state.settingsTab) {
            SettingsPane(minHeight: 520) {
                SettingsAIView()
                    .environment(appState)
            }
            .tabItem { Label("AI & Models", systemImage: "sparkles") }
            .tag(SettingsTab.ai)

            SettingsPane(minHeight: 340) {
                SettingsIntegrationsView()
                    .environment(appState)
            }
            .tabItem { Label("Integrations", systemImage: "cpu") }
            .tag(SettingsTab.integrations)

            SettingsPane(minHeight: 380) {
                SettingsGeneralView()
                    .environment(appState)
            }
            .tabItem { Label("General", systemImage: "gearshape") }
            .tag(SettingsTab.general)
        }
        .onChange(of: state.settingsTab) { _, newValue in
            appState.actionTracker.record(
                category: .navigation,
                action: "settings.tab",
                phase: .succeeded,
                summary: "Switched Settings tab to \(newValue.rawValue)",
                metadata: ["tab": newValue.rawValue]
            )
        }
    }
}

private struct SettingsHeightKey: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        let next = nextValue()
        if next > 0 { value = next }
    }
}

/// Auto-sizes each Settings tab vertically to its grouped `Form` content
/// while capping at the screen's visible frame so tabs fit cleanly.
private struct SettingsPane<Content: View>: View {
    let minHeight: CGFloat
    @ViewBuilder let content: () -> Content
    @State private var measuredHeight: CGFloat = 0

    private var maxHeight: CGFloat {
        max(minHeight, (NSScreen.main?.visibleFrame.height ?? 820) - 140)
    }

    private var clampedHeight: CGFloat {
        let target = measuredHeight > 0 ? measuredHeight : minHeight
        return min(max(target, minHeight), maxHeight)
    }

    /// The window is sized to the content, so a scroller is needed only when
    /// the content is taller than the screen allows. Showing one otherwise
    /// ("Always show scroll bars", or mid-animation while the frame is still
    /// shorter than the content) narrows the content, rewraps its text,
    /// changes the measured height, and re-sizes the window again.
    private var needsScroller: Bool {
        measuredHeight > maxHeight + 1
    }

    var body: some View {
        ScrollView(.vertical) {
            content()
                .scrollDisabled(true)
                .fixedSize(horizontal: false, vertical: true)
                .background(
                    GeometryReader { proxy in
                        Color.clear.preference(key: SettingsHeightKey.self, value: proxy.size.height)
                    }
                )
        }
        .scrollIndicators(needsScroller ? .automatic : .never)
        .onPreferenceChange(SettingsHeightKey.self) { height in
            if height > 0, abs(height - measuredHeight) > 1 {
                measuredHeight = height
            }
        }
        .frame(width: 640, height: clampedHeight)
        .animation(.easeInOut(duration: 0.16), value: clampedHeight)
    }
}

/// General: active Obsidian vault, Obsidian plugin install/update, and updates.
struct SettingsGeneralView: View {
    @Environment(AppState.self) var appState
    @State private var reloading = false
    @State private var reloadPending = false
    @State private var pluginVersion: String?
    @State private var busy = false
    @State private var message: String?

    var body: some View {
        Form {
            Section("Vault") {
                if let vault = appState.vault {
                    LabeledContent("Name", value: vault.rootURL.lastPathComponent)
                    LabeledContent("Path") {
                        Text(vault.rootURL.path)
                            .font(.caption.monospaced())
                            .textSelection(.enabled)
                    }
                } else {
                    Text("No vault bound. 2ndbrain follows the vault Obsidian has open.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text("The active vault follows Obsidian automatically, or can be switched from the Vault menu.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Section("Obsidian plugin") {
                LabeledContent("Installed", value: pluginVersion ?? "unknown")
                Button(busy ? "Installing…" : "Install or update plugin") {
                    Task { await installPlugin() }
                }
                .disabled(busy || appState.vault == nil)
            }

            if let message {
                Section {
                    Text(message).font(.caption).foregroundStyle(.secondary)
                }
            }

            Section("Quick Search & Menu Bar") {
                let hotkey = HotkeyManager.shared
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Global shortcut")
                        Text("Opens the Quick Ask & Search HUD from any Space or fullscreen app.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    VStack(alignment: .trailing, spacing: 6) {
                        ShortcutRecorder(
                            combo: hotkey.combo,
                            apply: { hotkey.apply($0) },
                            listening: { hotkey.setListening($0) }
                        )
                        Button("Reset to ⌃⇧Space") {
                            hotkey.resetToDefault()
                        }
                        .controlSize(.small)
                        .disabled(hotkey.combo?.spec == KeyCombo.defaultSpec)
                    }
                }

                Toggle(
                    "Pause automatic background indexing",
                    isOn: Binding(
                        get: { appState.pauseAutoSync },
                        set: { appState.setAutoSyncPaused($0) }
                    )
                )

                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("Setup guide")
                        Text("Re-run the 3-step vault, Gemini, and shortcut walkthrough.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Run First-Time Setup Guide…") {
                        appState.showOnboarding = true
                    }
                }
            }

            Section {
                UpdatesView(embedded: true)
                    .environment(appState)
            }
        }
        .formStyle(.grouped)
        .task { await reload() }
    }

    private func reload() async {
        if reloading { reloadPending = true; return }
        reloading = true
        defer {
            reloading = false
            if reloadPending { reloadPending = false; Task { await reload() } }
        }
        pluginVersion = appState.vault.flatMap { ObsidianPlugin.installedVersion(vaultRoot: $0.rootURL) }
    }

    private func installPlugin() async {
        busy = true
        defer { busy = false }
        do {
            try await appState.installObsidianPlugin()
            message = "Plugin installed. Enable it in Obsidian's Community plugins if this is the first install."
            await reload()
        } catch {
            message = error.localizedDescription
        }
    }
}
