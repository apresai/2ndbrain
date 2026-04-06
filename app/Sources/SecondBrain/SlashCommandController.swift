import AppKit
import SwiftUI

struct SlashCommand: Identifiable {
    let id = UUID()
    let title: String
    let icon: String
    let template: String
}

@Observable
class SlashCommandState {
    var commands: [SlashCommand] = []
    var filteredCommands: [SlashCommand] = []
    var selectedIndex = 0
    var query = ""
    var isActive = false
}

@MainActor
class SlashCommandController: NSObject {
    let state = SlashCommandState()
    var triggerLocation: Int?
    private var popover: NSPopover?
    private var hostingController: NSHostingController<SlashCommandListView>?

    private static let allCommands: [SlashCommand] = [
        SlashCommand(title: "Heading 1", icon: "textformat.size.larger", template: "# "),
        SlashCommand(title: "Heading 2", icon: "textformat.size", template: "## "),
        SlashCommand(title: "Heading 3", icon: "textformat.size.smaller", template: "### "),
        SlashCommand(title: "Bullet List", icon: "list.bullet", template: "- "),
        SlashCommand(title: "Numbered List", icon: "list.number", template: "1. "),
        SlashCommand(title: "Checkbox", icon: "checkmark.square", template: "- [ ] "),
        SlashCommand(title: "Code Block", icon: "chevron.left.forwardslash.chevron.right", template: "```\n\n```"),
        SlashCommand(title: "Table", icon: "tablecells", template: "| Column 1 | Column 2 | Column 3 |\n|----------|----------|----------|\n| | | |\n"),
        SlashCommand(title: "Wikilink", icon: "link", template: "[["),
        SlashCommand(title: "Blockquote", icon: "text.quote", template: "> "),
        SlashCommand(title: "Horizontal Rule", icon: "minus", template: "---\n"),
        SlashCommand(title: "Bold", icon: "bold", template: "****"),
        SlashCommand(title: "Italic", icon: "italic", template: "__"),
        SlashCommand(title: "Mermaid Diagram", icon: "arrow.triangle.branch", template: "```mermaid\ngraph TD\n    A --> B\n```"),
        SlashCommand(title: "Math Block", icon: "function", template: "$$\n\n$$"),
    ]

    func activate(in textView: NSTextView, at location: Int) {
        triggerLocation = location
        state.query = ""
        state.commands = Self.allCommands
        state.filteredCommands = Self.allCommands
        state.selectedIndex = 0
        state.isActive = true
        showPopover(in: textView, at: location)
    }

    func updateQuery(_ query: String) {
        state.query = query
        if query.isEmpty {
            state.filteredCommands = state.commands
        } else {
            let lowered = query.lowercased()
            state.filteredCommands = state.commands.filter {
                $0.title.lowercased().contains(lowered)
            }
        }
        state.selectedIndex = 0
    }

    func moveSelection(_ delta: Int) {
        let count = state.filteredCommands.count
        guard count > 0 else { return }
        state.selectedIndex = max(0, min(count - 1, state.selectedIndex + delta))
    }

    func insertSelection(into textView: NSTextView) {
        guard let triggerLoc = triggerLocation,
              state.selectedIndex < state.filteredCommands.count else {
            dismiss()
            return
        }

        let command = state.filteredCommands[state.selectedIndex]
        let cursorLoc = textView.selectedRange().location
        let replaceRange = NSRange(location: triggerLoc, length: cursorLoc - triggerLoc)

        textView.insertText(command.template, replacementRange: replaceRange)
        dismiss()
    }

    func dismiss() {
        popover?.close()
        popover = nil
        hostingController = nil
        triggerLocation = nil
        state.isActive = false
        state.query = ""
    }

    private func showPopover(in textView: NSTextView, at charIndex: Int) {
        let listView = SlashCommandListView(state: state)
        let hosting = NSHostingController(rootView: listView)
        hosting.preferredContentSize = NSSize(width: 260, height: 300)

        let pop = NSPopover()
        pop.contentViewController = hosting
        pop.behavior = .semitransient

        guard let layoutManager = textView.layoutManager,
              let textContainer = textView.textContainer else { return }

        let textLength = (textView.string as NSString).length
        let safeLength = charIndex < textLength ? 1 : 0
        let glyphRange = layoutManager.glyphRange(forCharacterRange: NSRange(location: charIndex, length: safeLength), actualCharacterRange: nil)
        var rect = layoutManager.boundingRect(forGlyphRange: glyphRange, in: textContainer)
        rect.origin.x += textView.textContainerInset.width
        rect.origin.y += textView.textContainerInset.height

        pop.show(relativeTo: rect, of: textView, preferredEdge: .maxY)
        self.popover = pop
        self.hostingController = hosting
    }
}

struct SlashCommandListView: View {
    @Bindable var state: SlashCommandState

    var body: some View {
        List(Array(state.filteredCommands.enumerated()), id: \.element.id) { index, command in
            HStack(spacing: 8) {
                Image(systemName: command.icon)
                    .frame(width: 20)
                    .foregroundStyle(.secondary)
                Text(command.title)
                    .fontWeight(index == state.selectedIndex ? .semibold : .regular)
            }
            .padding(.vertical, 2)
            .listRowBackground(index == state.selectedIndex ? Color.accentColor.opacity(0.15) : Color.clear)
        }
        .listStyle(.plain)
    }
}
