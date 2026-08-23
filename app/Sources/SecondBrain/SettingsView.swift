import SwiftUI
import SecondBrainCore

/// The Settings window's tabs, selectable in code so a link anywhere in the
/// app can land the user on a SPECIFIC tab. Without a selection binding the
/// TabView opened on whatever tab macOS last restored, which made every
/// "open Settings" affordance a coin flip — the root of "I don't see how to
/// configure the bedrock key".
enum SettingsTab: String {
    case general, ai, advanced, integrations
}

/// Whether the enclosing SettingsView is the inline sidebar host rather than
/// the Cmd+, Settings window.
///
/// An environment key rather than an init parameter on each tab view, because
/// the consumers are leaf controls (the AI page's Return-key default action)
/// buried levels below the host: the key is stamped once, where the host knows
/// the answer, and read exactly where it matters, and its `false` default keeps
/// every existing construction site (the Settings scene, tests) behaving as the
/// window host with no call-site changes.
private struct SettingsHostIsInlineKey: EnvironmentKey {
    static let defaultValue = false
}

extension EnvironmentValues {
    var settingsHostIsInline: Bool {
        get { self[SettingsHostIsInlineKey.self] }
        set { self[SettingsHostIsInlineKey.self] = newValue }
    }
}

/// A Button that opens the Settings window ON a specific tab. Replaces
/// `SettingsLink`, which has no action hook to set the tab and so landed on
/// whatever tab macOS last restored. If the window is already open, setting
/// the selection switches the live window's tab.
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
            appState.settingsTab = tab
            if appState.vault != nil {
                // Vault bound: the dashboard sidebar hosts the same Settings
                // content as a first-class tab, so land there instead of
                // opening a second window over the dashboard.
                appState.showSettingsPane = true
            } else {
                // No vault: the main window is WelcomeView, which has no
                // sidebar, so the Cmd+, Settings window stays the host.
                openSettings()
            }
        }
    }
}

/// The macOS Settings window (Cmd+,).
///
/// Until now this held exactly two settings — a Bedrock region and token —
/// while 19 config keys lived in a dashboard tab, 13 of them behind a single
/// unlabeled "Advanced settings" disclosure. That is backwards on this
/// platform: a `Settings` scene wrapping a small `TabView` is where a Mac user
/// looks first, and it is the only surface that works with no vault bound.
///
/// Four tabs, in the order a user meets them: what vault, what AI, the knobs,
/// and which tools are wired up. Nothing was removed to get here — the tuning
/// knobs moved wholesale into Advanced, and the model catalog stayed in the
/// main window because a 980x700 browser does not belong in a settings sheet.
struct SettingsView: View {
    @Environment(AppState.self) var appState

    /// True when hosted inline as the dashboard sidebar's Settings tab; false
    /// (the default) in the Cmd+, Settings window. Both hosts share this one
    /// implementation and the one `AppState.settingsTab` selection; the flag
    /// gates only host-specific behavior: the fixed window frame below, and
    /// (via the `settingsHostIsInline` environment key) keyboard shortcuts
    /// that register window-global and so must stay out of the dashboard
    /// window.
    var isInline: Bool = false

    var body: some View {
        @Bindable var state = appState
        // `Tab(_:systemImage:content:)` is macOS 15+; this app deploys to 14,
        // so the tabs use the .tabItem form.
        let tabs = TabView(selection: $state.settingsTab) {
            SettingsGeneralView()
                .environment(appState)
                .tabItem { Label("General", systemImage: "gearshape") }
                .tag(SettingsTab.general)
            SettingsAIView()
                .environment(appState)
                .tabItem { Label("AI", systemImage: "sparkles") }
                .tag(SettingsTab.ai)
            SettingsAdvancedView()
                .environment(appState)
                .tabItem { Label("Advanced", systemImage: "slider.horizontal.3") }
                .tag(SettingsTab.advanced)
            SettingsIntegrationsView()
                .environment(appState)
                .tabItem { Label("Integrations", systemImage: "puzzlepiece.extension") }
                .tag(SettingsTab.integrations)
        }
        .environment(\.settingsHostIsInline, isInline)
        return Group {
            if isInline {
                // The split view's detail pane owns the size; a fixed frame
                // here would letterbox the content inside the dashboard.
                tabs
            } else {
                // Tall enough that the AI page's verdict lands on screen
                // without a scroll: a "Test everything" button whose answer is
                // below the fold half-defeats the point of having one button.
                tabs.frame(width: 660, height: 700)
            }
        }
    }
}

/// General: which vault, and the two version-bearing components a user can
/// update from here. Deliberately short.
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
                Text("The active vault follows Obsidian, so there is nothing to choose here. Open a different vault in Obsidian and this follows it.")
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
        }
        .formStyle(.grouped)
        .task { await reload() }
    }

    private func reload() async {
        // Single-flight: Settings renders in two hosts (the Cmd+, window and
        // the sidebar tab), so reloads can be requested while one is already
        // running; the Bool collapses re-entrant reloads within this host.
        if reloading { reloadPending = true; return }
        reloading = true
        defer {
            reloading = false
            // Coalesce, never drop: a reload requested while one was in flight
            // (a post-write refresh racing the .task load) re-runs once so the
            // view always ends on post-mutation data. Single-flight is per
            // INSTANCE by design: each host loads its own @State, and
            // concurrent read-only status reloads across hosts are harmless.
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

/// Advanced: every remaining `ai.*` knob, unchanged.
///
/// This wraps the existing AIAdvancedSettingsView rather than reimplementing
/// it. That view already routes every write through `2nb config set` so the
/// CLI owns validation and its error text surfaces per row; duplicating it here
/// would create a second place for those rules to drift.
struct SettingsAdvancedView: View {
    @Environment(AppState.self) var appState
    @State private var reloading = false
    @State private var reloadPending = false
    @State private var status: AIStatusInfo?
    @State private var models: [CatalogModelInfo] = []

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text("These have working defaults. Change them only when you have a reason — the captions name what each one actually affects.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                AIAdvancedSettingsView(aiStatus: status, models: models, onReload: reload)
                    .environment(appState)
            }
            .padding()
        }
        .task { await reload() }
    }

    private func reload() async {
        // Single-flight against dual-host reload stacking; see
        // SettingsGeneralView.reload.
        if reloading { reloadPending = true; return }
        reloading = true
        defer {
            reloading = false
            // Coalesce, never drop: a reload requested while one was in flight
            // (a post-write refresh racing the .task load) re-runs once so the
            // view always ends on post-mutation data. Single-flight is per
            // INSTANCE by design: each host loads its own @State, and
            // concurrent read-only status reloads across hosts are harmless.
            if reloadPending { reloadPending = false; Task { await reload() } }
        }
        status = try? await appState.fetchAIStatus()
        if let provider = status?.provider {
            models = (try? await appState.fetchModels(provider: provider)) ?? []
        }
    }
}
