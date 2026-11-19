import AppKit
import Observation
import SwiftUI

/// Renders the dynamic 18×18 menu bar status icon with clockwise indexing sweep,
/// pause indicator, and unconfigured orange badge.
@MainActor
enum StatusIcon {
    static func image(indexFraction: Double?, isPaused: Bool, geminiReady: Bool) -> NSImage {
        let size = NSSize(width: 18, height: 18)
        let img = NSImage(size: size, flipped: false) { _ in
            let symbolName = NSImage(systemSymbolName: "brain.head.profile", accessibilityDescription: "2ndbrain") != nil
                ? "brain.head.profile"
                : "sparkles"

            if let fraction = indexFraction {
                let clamped = max(0.02, min(1.0, fraction))
                let center = NSPoint(x: 9, y: 9)
                let radius: CGFloat = 7.4

                let track = NSBezierPath()
                track.appendArc(withCenter: center, radius: radius, startAngle: 0, endAngle: 360)
                track.lineWidth = 1.6
                NSColor.labelColor.withAlphaComponent(0.22).setStroke()
                track.stroke()

                let sweep = NSBezierPath()
                sweep.appendArc(
                    withCenter: center,
                    radius: radius,
                    startAngle: 90,
                    endAngle: 90 - 360 * CGFloat(clamped),
                    clockwise: true
                )
                sweep.lineWidth = 1.8
                sweep.lineCapStyle = .round
                NSColor.labelColor.setStroke()
                sweep.stroke()

                drawSymbol(symbolName, in: NSRect(x: 4.5, y: 4.5, width: 9, height: 9), tint: .labelColor)
            } else {
                drawSymbol(symbolName, in: NSRect(x: 1.5, y: 1.5, width: 14, height: 14), tint: .labelColor)

                if isPaused {
                    let badgeBg = NSRect(x: 10.5, y: 1.0, width: 7.0, height: 7.5)
                    NSColor.labelColor.withAlphaComponent(0.15).setFill()
                    NSBezierPath(roundedRect: badgeBg, xRadius: 1.5, yRadius: 1.5).fill()

                    NSColor.labelColor.setFill()
                    NSBezierPath(rect: NSRect(x: 11.7, y: 2.2, width: 1.6, height: 5.0)).fill()
                    NSBezierPath(rect: NSRect(x: 14.5, y: 2.2, width: 1.6, height: 5.0)).fill()
                } else if !geminiReady {
                    let dotRect = NSRect(x: 12.2, y: 1.5, width: 4.8, height: 4.8)
                    NSColor.systemOrange.setFill()
                    NSBezierPath(ovalIn: dotRect).fill()
                }
            }
            return true
        }
        // Only disable template mode when drawing the colored unconfigured badge.
        img.isTemplate = indexFraction != nil || isPaused || geminiReady
        return img
    }

    private static func drawSymbol(_ name: String, in rect: NSRect, tint: NSColor) {
        guard let base = NSImage(systemSymbolName: name, accessibilityDescription: "2ndbrain") else { return }
        let config = NSImage.SymbolConfiguration(paletteColors: [tint])
        let configured = base.withSymbolConfiguration(config) ?? base
        configured.draw(in: rect)
    }
}

/// Compact vault & Gemini status summary pinned at the top of the menu bar menu.
struct MenuStatusView: View {
    @Environment(AppState.self) private var appState

    private var statusBadge: (label: String, color: Color) {
        if appState.isIndexing {
            return ("Indexing", .accentColor)
        }
        if appState.pauseAutoSync {
            return ("Paused", .secondary)
        }
        if let ai = appState.aiStatus, ai.embedAvailable && ai.genAvailable {
            if ai.portabilityStatus == "dimension_break" {
                return ("Re-embed Needed", .orange)
            }
            return ("Ready", .green)
        }
        return ("Setup Needed", .orange)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Image(systemName: "brain.head.profile")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(Color.accentColor)
                Text(appState.vault?.rootURL.lastPathComponent ?? "No Vault Bound")
                    .font(.callout.weight(.semibold))
                    .lineLimit(1)
                Spacer(minLength: 4)
                Text(statusBadge.label)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(statusBadge.color)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(statusBadge.color.opacity(0.14), in: Capsule())
            }

            if let ai = appState.aiStatus {
                Text("\(ai.documentCount) notes · \(ai.embeddingCount) embedded · \(ai.genModel)")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            } else {
                Text(appState.vault == nil ? "Bind an Obsidian vault to get started" : "Checking vault status…")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            if appState.isIndexing {
                ProgressView(value: appState.indexFraction ?? 0.08)
                    .progressViewStyle(.linear)
                if let ep = appState.embeddingProgress, ep.total > 0 {
                    let etaSuffix = ep.remainingSeconds.map { " · ~\(Formatters.eta($0)) left" } ?? ""
                    Text("\(ep.current)/\(ep.total)\(etaSuffix)")
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .frame(width: 260, alignment: .leading)
    }
}

/// Persistent macOS menu bar item (`NSStatusItem`) providing one-click access to
/// the Quick Ask & Search HUD, vault sync, auto-sync pause, and Gemini Settings.
@MainActor
final class StatusMenuController: NSObject, NSMenuDelegate {
    private let appState: AppState
    private let onToggleQuickSearch: () -> Void
    private var statusItem: NSStatusItem?
    private let menu = NSMenu()
    private var isActive = true

    init(appState: AppState, onToggleQuickSearch: @escaping () -> Void) {
        self.appState = appState
        self.onToggleQuickSearch = onToggleQuickSearch
        super.init()

        guard ProcessInfo.processInfo.environment["2NB_TEST"] != "1" else {
            isActive = false
            return
        }

        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        item.button?.toolTip = "2ndbrain"
        item.menu = menu
        menu.delegate = self
        self.statusItem = item
        refreshIconAndObserve()
    }

    func tearDown() {
        isActive = false
        if let statusItem {
            NSStatusBar.system.removeStatusItem(statusItem)
            self.statusItem = nil
        }
    }

    private func refreshIconAndObserve() {
        guard isActive else { return }
        withObservationTracking {
            let geminiReady = (appState.aiStatus?.embedAvailable == true)
                && (appState.aiStatus?.genAvailable == true)
                && (appState.aiStatus?.portabilityStatus != "dimension_break")
            let fraction = appState.indexFraction
            let paused = appState.pauseAutoSync
            statusItem?.button?.image = StatusIcon.image(
                indexFraction: fraction,
                isPaused: paused,
                geminiReady: geminiReady
            )
        } onChange: { [weak self] in
            Task { @MainActor [weak self] in
                self?.refreshIconAndObserve()
            }
        }
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()

        let headerItem = NSMenuItem()
        let hosting = NSHostingView(rootView: MenuStatusView().environment(appState))
        let fitting = hosting.fittingSize
        hosting.frame = NSRect(x: 0, y: 0, width: 260, height: max(68, fitting.height))
        headerItem.view = hosting
        menu.addItem(headerItem)

        menu.addItem(.separator())

        let shortcutHint = HotkeyManager.shared.combo?.display ?? "⌃⇧Space"
        let quickSearch = NSMenuItem(
            title: "Quick Ask & Search… (\(shortcutHint))",
            action: #selector(toggleQuickSearchAction),
            keyEquivalent: ""
        )
        quickSearch.target = self
        menu.addItem(quickSearch)

        let openApp = NSMenuItem(title: "Open 2ndbrain", action: #selector(openMainWindowAction), keyEquivalent: "")
        openApp.target = self
        menu.addItem(openApp)

        let openObsidian = NSMenuItem(title: "Open Vault in Obsidian", action: #selector(openObsidianAction), keyEquivalent: "")
        openObsidian.target = self
        openObsidian.isEnabled = appState.vault != nil
        menu.addItem(openObsidian)

        menu.addItem(.separator())

        let needsReembed = appState.aiStatus?.portabilityStatus == "dimension_break"
        let syncTitle: String
        if appState.isIndexing {
            syncTitle = "Indexing Vault…"
        } else if needsReembed {
            syncTitle = "Re-embed Vault Now"
        } else {
            syncTitle = "Sync Index Now"
        }
        let syncItem = NSMenuItem(
            title: syncTitle,
            action: #selector(syncIndexAction),
            keyEquivalent: ""
        )
        syncItem.target = self
        syncItem.isEnabled = appState.vault != nil && !appState.isIndexing
        menu.addItem(syncItem)

        let pauseItem = NSMenuItem(
            title: appState.pauseAutoSync ? "Resume Auto-Sync" : "Pause Auto-Sync",
            action: #selector(togglePauseAction),
            keyEquivalent: ""
        )
        pauseItem.target = self
        menu.addItem(pauseItem)

        menu.addItem(.separator())

        let settingsItem = NSMenuItem(title: "Gemini Settings…", action: #selector(openGeminiSettingsAction), keyEquivalent: "")
        settingsItem.target = self
        menu.addItem(settingsItem)

        menu.addItem(.separator())

        let quitItem = NSMenuItem(title: "Quit 2ndbrain", action: #selector(quitAction), keyEquivalent: "q")
        quitItem.target = self
        menu.addItem(quitItem)
    }

    @objc private func toggleQuickSearchAction() {
        onToggleQuickSearch()
    }

    @objc private func openMainWindowAction() {
        NSApp.activate(ignoringOtherApps: true)
        if let main = NSApp.windows.first(where: { !($0 is QuickSearchPanelWindow) && $0.canBecomeMain }) {
            main.deminiaturize(nil)
            main.makeKeyAndOrderFront(nil)
        } else {
            NSApp.windows.first?.makeKeyAndOrderFront(nil)
        }
    }

    @objc private func openObsidianAction() {
        appState.openVaultInObsidian()
    }

    @objc private func syncIndexAction() {
        if appState.aiStatus?.portabilityStatus == "dimension_break" {
            appState.startForceReembed()
        } else {
            appState.startIndex()
        }
    }

    @objc private func togglePauseAction() {
        appState.setAutoSyncPaused(!appState.pauseAutoSync)
    }

    @objc private func openGeminiSettingsAction() {
        appState.requestOpenSettings(tab: .ai)
    }

    @objc private func quitAction() {
        NSApp.terminate(nil)
    }
}
