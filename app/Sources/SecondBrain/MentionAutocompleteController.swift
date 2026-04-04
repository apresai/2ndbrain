import AppKit
import SwiftUI
import SecondBrainCore

enum TriggerMode {
    case atMention    // triggered by @
    case doubleBracket // triggered by [[
}

@MainActor
@Observable
class MentionAutocompleteState {
    var results: [DocumentRecord] = []
    var selectedIndex = 0
    var query = ""
    var isActive = false
}

@MainActor
class MentionAutocompleteController: NSObject {
    let state = MentionAutocompleteState()
    var databaseManager: DatabaseManager?
    var triggerLocation: Int?
    var triggerMode: TriggerMode = .atMention

    private var popover: NSPopover?
    private var hostingController: NSHostingController<MentionListView>?

    func activate(in textView: NSTextView, at location: Int, mode: TriggerMode) {
        triggerLocation = location
        triggerMode = mode
        state.query = ""
        state.selectedIndex = 0
        state.isActive = true

        loadResults(query: "")
        showPopover(in: textView, at: location)
    }

    func updateQuery(_ query: String) {
        state.query = query
        state.selectedIndex = 0
        loadResults(query: query)
    }

    func dismiss() {
        popover?.performClose(nil)
        popover = nil
        hostingController = nil
        triggerLocation = nil
        state.isActive = false
        state.results = []
        state.query = ""
    }

    func moveSelection(_ delta: Int) {
        let count = state.results.count
        guard count > 0 else { return }
        state.selectedIndex = max(0, min(count - 1, state.selectedIndex + delta))
    }

    func insertSelection(into textView: NSTextView) {
        guard let triggerLoc = triggerLocation,
              state.selectedIndex < state.results.count else {
            dismiss()
            return
        }

        let doc = state.results[state.selectedIndex]
        let title = doc.title.isEmpty ? doc.path.replacingOccurrences(of: ".md", with: "") : doc.title
        let replacement = "[[\(title)]]"

        // Calculate the range to replace: from trigger start to current cursor
        let cursorLocation = textView.selectedRange().location
        let triggerStart: Int
        switch triggerMode {
        case .atMention:
            triggerStart = triggerLoc // includes the @ character
        case .doubleBracket:
            triggerStart = triggerLoc // includes the first [
        }

        let replaceLength = cursorLocation - triggerStart
        guard replaceLength >= 0 else {
            dismiss()
            return
        }

        let replaceRange = NSRange(location: triggerStart, length: replaceLength)
        textView.insertText(replacement, replacementRange: replaceRange)
        dismiss()
    }

    // MARK: - Private

    private func loadResults(query: String) {
        guard let db = databaseManager else { return }
        do {
            if query.isEmpty {
                let all = try db.allDocuments()
                state.results = Array(all.prefix(15))
            } else {
                let searched = try db.search(query: query, limit: 15)
                // Refine with fuzzy match on title
                state.results = searched.filter { fuzzyMatch($0.title.lowercased(), query: query.lowercased()) || $0.title.localizedCaseInsensitiveContains(query) }
                if state.results.isEmpty {
                    state.results = searched
                }
            }
        } catch {
            state.results = []
        }
    }

    private func fuzzyMatch(_ string: String, query: String) -> Bool {
        var sIndex = string.startIndex
        for char in query {
            guard let found = string[sIndex...].firstIndex(of: char) else { return false }
            sIndex = string.index(after: found)
        }
        return true
    }

    private func showPopover(in textView: NSTextView, at charIndex: Int) {
        let listView = MentionListView(state: state)
        let hosting = NSHostingController(rootView: listView)
        hosting.preferredContentSize = NSSize(width: 300, height: 250)

        let pop = NSPopover()
        pop.contentViewController = hosting
        pop.behavior = .semitransient
        pop.animates = true

        // Position at the character location
        let glyphRange = textView.layoutManager?.glyphRange(forCharacterRange: NSRange(location: charIndex, length: 1), actualCharacterRange: nil) ?? NSRange(location: charIndex, length: 1)
        let rect = textView.layoutManager?.boundingRect(forGlyphRange: glyphRange, in: textView.textContainer!) ?? .zero
        let positionRect = NSRect(
            x: rect.origin.x + textView.textContainerInset.width,
            y: rect.origin.y + textView.textContainerInset.height,
            width: rect.width.isZero ? 1 : rect.width,
            height: rect.height
        )

        pop.show(relativeTo: positionRect, of: textView, preferredEdge: .maxY)

        self.popover = pop
        self.hostingController = hosting

        // Keep focus on text view
        textView.window?.makeFirstResponder(textView)
    }
}
