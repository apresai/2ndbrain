import AppKit
import os
import SecondBrainCore

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let log = Logger(subsystem: "dev.apresai.2ndbrain", category: "appdelegate")
    private var menuTrackingObserver: NSObjectProtocol?
    private var windowCloseObserver: NSObjectProtocol?
    private weak var appState: AppState?
    private var configuredAppState = false
    private var statusMenuController: StatusMenuController?
    private var quickSearchController: QuickSearchPanelController?
    private var automationController: GUIAutomationController?

    var quickSearchPanelController: QuickSearchPanelController? {
        quickSearchController
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // launchd starts the app with a minimal PATH (no /opt/homebrew/bin),
        // which every child process inherits. Repair it before the first CLI
        // call so the bundled 2nb's exec.LookPath("2nb") — e.g. `skills doctor`'s
        // on-PATH check — reflects the user's real shell PATH instead of falsely
        // reporting "2nb is NOT on your shell PATH". Runs before any later
        // status/doctor probe the app spawns.
        let toolPATH = CLIPath.ensureToolPATH()
        log.debug("augmented child-process PATH: \(toolPATH, privacy: .public)")

        // Clear the quarantine xattr the installer applied to the bundled 2nb so
        // Gatekeeper can't block it as a quarantined, ticket-less standalone
        // binary when we spawn it (a standalone Mach-O can't carry a stapled
        // notarization ticket, so a quarantined one needs an online check that
        // fails offline → "Apple could not verify '2nb' … Move to Trash"). Must
        // run before the first CLI spawn (the first one is .onAppear → openVault).
        let quarantineStrip = CLIPath.prepareBundledCLI()
        log.debug("bundled 2nb quarantine strip: \(quarantineStrip.map { $0 ? "cleared" : "FAILED (bundle not writable?)" } ?? "n/a (no bundled CLI)", privacy: .public)")

        renameFileMenuToNotes()

        // SwiftUI rebuilds the menu when scene state changes and may reset
        // our rename back to "File". Re-apply on every menu-tracking cycle.
        menuTrackingObserver = NotificationCenter.default.addObserver(
            forName: NSMenu.didBeginTrackingNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.renameFileMenuToNotes()
            }
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let token = menuTrackingObserver {
            NotificationCenter.default.removeObserver(token)
        }
        automationController?.stop()
        quickSearchController?.hide()
        statusMenuController?.tearDown()
    }

    // MARK: - Dashboard window lifecycle

    /// The app keeps running in the menu bar after its window closes. Stated
    /// explicitly so the posture is a decision, and so every path below that
    /// brings the window back is known to be required.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    /// Dock click, `open -a`, or a relaunch of the running app. SwiftUI's
    /// implicit reopen goes through the New Window command, which this app
    /// removes, so a closed dashboard never returned: the process was alive,
    /// had no window, and looked hung. Recreate it here.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if dashboardWindow() != nil {
            showMainWindow()
            return true
        }
        showMainWindow()
        return false
    }

    /// Front the dashboard window, creating it when it has been closed.
    func showMainWindow() {
        NSApp.activate(ignoringOtherApps: true)
        if let window = dashboardWindow() {
            if window.isMiniaturized { window.deminiaturize(nil) }
            window.makeKeyAndOrderFront(nil)
            return
        }
        guard let open = appState?.openMainWindowAction else {
            log.error("dashboard window is closed and no openWindow action was captured")
            return
        }
        appState?.actionTracker.record(
            category: .navigation,
            action: "window.reopen",
            phase: .succeeded,
            summary: "Reopened the dashboard window"
        )
        open()
    }

    private func dashboardWindow() -> NSWindow? {
        NSApp.windows.first(where: MainWindow.isDashboard)
    }

    /// A route request (menu command, status item) made with no dashboard
    /// window open has nothing to consume it. Reopen the window; its
    /// ContentView drains the request on appear.
    private func watchPendingRoutes() {
        guard let appState else { return }
        withObservationTracking {
            _ = appState.hasPendingDashboardRoute
        } onChange: { [weak self] in
            Task { @MainActor [weak self] in
                guard let self, let appState = self.appState else { return }
                if appState.hasPendingDashboardRoute, self.dashboardWindow() == nil {
                    self.showMainWindow()
                }
                self.watchPendingRoutes()
            }
        }
    }

    private func observeDashboardClose() {
        windowCloseObserver = NotificationCenter.default.addObserver(
            forName: NSWindow.willCloseNotification,
            object: nil,
            queue: .main
        ) { [weak self] note in
            guard let window = note.object as? NSWindow else { return }
            Task { @MainActor [weak self] in
                guard MainWindow.isDashboard(window) else { return }
                self?.appState?.actionTracker.record(
                    category: .navigation,
                    action: "window.close",
                    phase: .succeeded,
                    summary: "Closed the dashboard window (app stays in the menu bar)"
                )
            }
        }
    }

    func configure(appState: AppState) {
        if self.appState == nil {
            self.appState = appState
            watchPendingRoutes()
            observeDashboardClose()
        }
        guard !configuredAppState,
              ProcessInfo.processInfo.environment["2NB_TEST"] != "1" else {
            return
        }
        configuredAppState = true
        let quickSearch = QuickSearchPanelController(appState: appState)
        self.quickSearchController = quickSearch
        self.statusMenuController = StatusMenuController(
            appState: appState,
            onOpenMainWindow: { [weak self] in self?.showMainWindow() }
        ) {
            quickSearch.toggle()
        }
        if let controlDir = LaunchOptions.current.guiControlDir {
            let controller = GUIAutomationController(controlDir: controlDir, appState: appState, appDelegate: self)
            controller.start()
            self.automationController = controller
        }
        HotkeyManager.shared.onTrigger = { [weak self] in
            self?.quickSearchController?.toggle()
        }
        HotkeyManager.shared.registerDefaultIfNeeded()
    }

    func prepareMenusForInteraction() {
        renameFileMenuToNotes()
        if let mainMenu = NSApp.mainMenu {
            updateMenusRecursively(mainMenu)
        }
    }

    private func updateMenusRecursively(_ menu: NSMenu) {
        menu.delegate?.menuNeedsUpdate?(menu)
        menu.update()
        for item in menu.items {
            if let sub = item.submenu {
                updateMenusRecursively(sub)
            }
        }
    }

    func toggleQuickSearch() {
        quickSearchController?.toggle()
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        renameFileMenuToNotes()
    }

    private func renameFileMenuToNotes() {
        guard let mainMenu = NSApp.mainMenu else { return }
        for item in mainMenu.items {
            if item.title == "Notes" { return }
            if item.title == "File" {
                item.title = "Notes"
                item.submenu?.title = "Notes"
                log.debug("Renamed File → Notes")
                return
            }
        }
    }
}
