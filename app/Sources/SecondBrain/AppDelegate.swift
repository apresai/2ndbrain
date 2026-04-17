import AppKit
import os

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let log = Logger(subsystem: "dev.apresai.2ndbrain", category: "appdelegate")

    func applicationDidFinishLaunching(_ notification: Notification) {
        renameFileMenuToNotes()

        // SwiftUI rebuilds the menu when scene state changes and may reset
        // our rename back to "File". Re-apply on every menu-tracking cycle.
        NotificationCenter.default.addObserver(
            forName: NSMenu.didBeginTrackingNotification,
            object: nil,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.renameFileMenuToNotes()
            }
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
