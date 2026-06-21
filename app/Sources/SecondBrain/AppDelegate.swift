import AppKit
import os
import SecondBrainCore

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let log = Logger(subsystem: "dev.apresai.2ndbrain", category: "appdelegate")
    private var menuTrackingObserver: NSObjectProtocol?

    func applicationDidFinishLaunching(_ notification: Notification) {
        // launchd starts the app with a minimal PATH (no /opt/homebrew/bin),
        // which every child process inherits. Repair it before the first CLI
        // call so the bundled 2nb's exec.LookPath("2nb") — e.g. `skills doctor`'s
        // on-PATH check — reflects the user's real shell PATH instead of falsely
        // reporting "2nb is NOT on your shell PATH". Runs before onAppear's
        // `2nb vault set` and any later status/doctor probe.
        let toolPATH = CLIPath.ensureToolPATH()
        log.debug("augmented child-process PATH: \(toolPATH, privacy: .public)")

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
