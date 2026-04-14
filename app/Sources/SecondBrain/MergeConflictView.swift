import SwiftUI
import AppKit

enum MergeConflictResolution {
    case keepMine
    case useTheirs
    case cancel
}

struct MergeConflictView: View {
    let filename: String
    let theirs: String
    let mine: String
    let ancestor: String
    let onResolve: (MergeConflictResolution) -> Void

    var body: some View {
        VStack(spacing: 0) {
            VStack(spacing: 4) {
                Text("External changes detected")
                    .font(.headline)
                Text("\(filename) was modified outside the editor while you had unsaved changes.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
            .padding(.vertical, 12)

            HStack(spacing: 0) {
                diffPane(
                    title: "On Disk",
                    subtitle: "diff vs last saved",
                    tint: .orange,
                    original: ancestor,
                    modified: theirs
                )
                Divider()
                diffPane(
                    title: "Yours",
                    subtitle: "diff vs last saved",
                    tint: .blue,
                    original: ancestor,
                    modified: mine
                )
            }
            .frame(minHeight: 360)

            HStack {
                Button("Cancel") { onResolve(.cancel) }
                    .keyboardShortcut(.cancelAction)
                Spacer()
                Button("Use Theirs") { onResolve(.useTheirs) }
                Button("Keep Mine") { onResolve(.keepMine) }
                    .keyboardShortcut(.defaultAction)
            }
            .padding(12)
        }
        .frame(width: 1000, height: 560)
    }

    private func diffPane(title: String, subtitle: String, tint: Color, original: String, modified: String) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.subheadline.bold())
                    .foregroundStyle(tint)
                Text(subtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            .padding(8)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.secondary.opacity(0.08))

            Divider()

            DiffView(original: original, modified: modified)
        }
    }
}

@MainActor
final class MergeConflictController: NSObject, NSWindowDelegate {
    private var window: NSWindow?
    private var onResolve: ((MergeConflictResolution) -> Void)?

    func present(
        filename: String,
        theirs: String,
        mine: String,
        ancestor: String,
        completion: @escaping (MergeConflictResolution) -> Void
    ) {
        self.onResolve = completion
        let view = MergeConflictView(
            filename: filename,
            theirs: theirs,
            mine: mine,
            ancestor: ancestor
        ) { [weak self] resolution in
            self?.finish(with: resolution)
        }
        let hosting = NSHostingController(rootView: view)
        let win = NSWindow(contentViewController: hosting)
        win.title = "Merge Conflict — \(filename)"
        win.styleMask = [.titled, .closable]
        win.isReleasedWhenClosed = false
        win.center()
        win.delegate = self
        self.window = win
        win.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func finish(with resolution: MergeConflictResolution) {
        window?.close()
        window = nil
        let callback = onResolve
        onResolve = nil
        callback?(resolution)
    }

    func windowWillClose(_ notification: Notification) {
        if onResolve != nil {
            finish(with: .cancel)
        }
    }
}
