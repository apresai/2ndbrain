import SwiftUI
import WebKit
import SecondBrainCore

struct EditorArea: View {
    @Environment(AppState.self) var appState
    @State private var showPreview = true

    var body: some View {
        @Bindable var state = appState

        if let tabIndex = validTabIndex {
            HSplitView {
                // Source editor
                MarkdownEditorView(
                    text: Binding(
                        get: { state.openDocuments[tabIndex].content },
                        set: { newValue in
                            state.openDocuments[tabIndex].content = newValue
                            state.openDocuments[tabIndex].isDirty = true
                            state.updateOutline()
                        }
                    ),
                    databaseManager: state.database,
                    typewriterMode: state.typewriterModeActive,
                    inlineRendering: state.inlineRenderingEnabled
                )
                .frame(minWidth: 300)

                if showPreview {
                    // Live HTML preview
                    MarkdownPreviewView(
                        html: previewHTML
                    )
                    .frame(minWidth: 300)
                }
            }
            .toolbar {
                ToolbarItem(placement: .automatic) {
                    Button {
                        showPreview.toggle()
                    } label: {
                        Image(systemName: showPreview ? "rectangle.split.2x1" : "rectangle")
                    }
                    .help(showPreview ? "Hide Preview" : "Show Preview")
                }
            }
        }
    }

    private var validTabIndex: Int? {
        let idx = appState.activeTabIndex
        guard idx >= 0, idx < appState.openDocuments.count else { return nil }
        return idx
    }

    private var previewHTML: String {
        guard let idx = validTabIndex else { return "" }
        let content = appState.openDocuments[idx].content
        let (_, body) = FrontmatterParser.parse(content)
        return MarkdownRenderer.renderHTML(body)
    }
}

// MARK: - Markdown Source Editor (NSTextView)

struct MarkdownEditorView: NSViewRepresentable {
    @Binding var text: String
    var databaseManager: DatabaseManager?
    var typewriterMode: Bool = false
    var inlineRendering: Bool = false

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSTextView.scrollableTextView()
        guard let textView = scrollView.documentView as? NSTextView else { return scrollView }

        textView.isEditable = true
        textView.isSelectable = true
        textView.allowsUndo = true
        textView.isRichText = false
        textView.font = NSFont.monospacedSystemFont(ofSize: 14, weight: .regular)
        textView.textColor = NSColor.textColor
        textView.backgroundColor = NSColor.textBackgroundColor
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.isAutomaticSpellingCorrectionEnabled = false
        textView.insertionPointColor = NSColor.controlAccentColor
        textView.textContainerInset = NSSize(width: 16, height: 16)

        // Line wrapping
        textView.isHorizontallyResizable = false
        textView.textContainer?.containerSize = NSSize(width: 0, height: CGFloat.greatestFiniteMagnitude)
        textView.textContainer?.widthTracksTextView = true

        textView.delegate = context.coordinator
        textView.string = text

        // Line number gutter
        let rulerView = LineNumberRulerView(textView: textView)
        scrollView.verticalRulerView = rulerView
        scrollView.hasVerticalRuler = true
        scrollView.rulersVisible = true

        return scrollView
    }

    func updateNSView(_ scrollView: NSScrollView, context: Context) {
        guard let textView = scrollView.documentView as? NSTextView else { return }
        context.coordinator.typewriterMode = typewriterMode
        context.coordinator.inlineRendering = inlineRendering
        if textView.string != text && !context.coordinator.isEditing {
            let selectedRanges = textView.selectedRanges
            textView.string = text
            textView.selectedRanges = selectedRanges
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(text: $text, databaseManager: databaseManager)
    }

    @MainActor
    class Coordinator: NSObject, NSTextViewDelegate {
        var text: Binding<String>
        var isEditing = false
        var typewriterMode = false
        var inlineRendering = false
        let mentionController = MentionAutocompleteController()
        let slashController = SlashCommandController()
        private var debounceTimer: Timer?

        init(text: Binding<String>, databaseManager: DatabaseManager?) {
            self.text = text
            super.init()
            mentionController.databaseManager = databaseManager
        }

        func textView(_ textView: NSTextView, shouldChangeTextIn affectedCharRange: NSRange, replacementString: String?) -> Bool {
            guard let replacement = replacementString else { return true }

            if replacement == "@" && !mentionController.state.isActive {
                // Trigger after the @ is inserted
                let triggerLoc = affectedCharRange.location
                DispatchQueue.main.async { [weak self] in
                    self?.mentionController.activate(in: textView, at: triggerLoc, mode: .atMention)
                }
            } else if replacement == "[" && !mentionController.state.isActive {
                // Check if previous character is also [
                let loc = affectedCharRange.location
                if loc > 0 {
                    let prevRange = NSRange(location: loc - 1, length: 1)
                    let prevChar = (textView.string as NSString).substring(with: prevRange)
                    if prevChar == "[" {
                        let triggerLoc = loc - 1 // start from first [
                        DispatchQueue.main.async { [weak self] in
                            self?.mentionController.activate(in: textView, at: triggerLoc, mode: .doubleBracket)
                        }
                    }
                }
            }

            // Slash command trigger: "/" at start of a line
            if replacement == "/" && !slashController.state.isActive && !mentionController.state.isActive {
                let loc = affectedCharRange.location
                let text = textView.string as NSString
                // Check if at start of line
                let lineStart = (loc == 0) || (loc > 0 && text.substring(with: NSRange(location: loc - 1, length: 1)) == "\n")
                if lineStart {
                    DispatchQueue.main.async { [weak self] in
                        self?.slashController.activate(in: textView, at: loc)
                    }
                }
            }

            return true
        }

        func textView(_ textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
            // Handle slash commands
            if slashController.state.isActive {
                switch commandSelector {
                case #selector(NSResponder.moveDown(_:)):
                    slashController.moveSelection(1)
                    return true
                case #selector(NSResponder.moveUp(_:)):
                    slashController.moveSelection(-1)
                    return true
                case #selector(NSResponder.insertNewline(_:)), #selector(NSResponder.insertTab(_:)):
                    slashController.insertSelection(into: textView)
                    return true
                case #selector(NSResponder.cancelOperation(_:)):
                    slashController.dismiss()
                    return true
                default:
                    return false
                }
            }

            // Handle mention autocomplete
            guard mentionController.state.isActive else { return false }

            switch commandSelector {
            case #selector(NSResponder.moveDown(_:)):
                mentionController.moveSelection(1)
                return true
            case #selector(NSResponder.moveUp(_:)):
                mentionController.moveSelection(-1)
                return true
            case #selector(NSResponder.insertNewline(_:)):
                mentionController.insertSelection(into: textView)
                return true
            case #selector(NSResponder.cancelOperation(_:)):
                mentionController.dismiss()
                return true
            case #selector(NSResponder.insertTab(_:)):
                mentionController.insertSelection(into: textView)
                return true
            default:
                return false
            }
        }

        func textDidChange(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            isEditing = true
            let currentString = textView.string

            // Update slash command query
            if slashController.state.isActive, let triggerLoc = slashController.triggerLocation {
                let cursorLoc = textView.selectedRange().location
                let queryStart = triggerLoc + 1 // skip the /
                if cursorLoc < triggerLoc {
                    slashController.dismiss()
                } else if queryStart <= cursorLoc {
                    let query = (currentString as NSString).substring(with: NSRange(location: queryStart, length: cursorLoc - queryStart))
                    if query.contains("\n") {
                        slashController.dismiss()
                    } else {
                        slashController.updateQuery(query)
                    }
                }
            }

            // Update mention autocomplete query
            if mentionController.state.isActive, let triggerLoc = mentionController.triggerLocation {
                let cursorLoc = textView.selectedRange().location
                let queryStart: Int
                switch mentionController.triggerMode {
                case .atMention:
                    queryStart = triggerLoc + 1 // skip the @
                case .doubleBracket:
                    queryStart = triggerLoc + 2 // skip the [[
                }

                if cursorLoc < triggerLoc {
                    // Cursor moved before trigger — dismiss
                    mentionController.dismiss()
                } else if queryStart <= cursorLoc {
                    let query = (currentString as NSString).substring(with: NSRange(location: queryStart, length: cursorLoc - queryStart))
                    if query.contains("\n") || query.contains(" " + " ") {
                        mentionController.dismiss()
                    } else {
                        mentionController.updateQuery(query)
                    }
                } else {
                    // Deleted back past trigger
                    mentionController.dismiss()
                }
            }

            // Typewriter mode: center cursor vertically
            if typewriterMode, let scrollView = textView.enclosingScrollView {
                let cursorRange = textView.selectedRange()
                if let layoutManager = textView.layoutManager, let textContainer = textView.textContainer {
                    let glyphIndex = layoutManager.glyphIndexForCharacter(at: cursorRange.location)
                    let lineRect = layoutManager.lineFragmentRect(forGlyphAt: glyphIndex, effectiveRange: nil)
                    let cursorY = lineRect.origin.y + textView.textContainerInset.height
                    let visibleHeight = scrollView.contentView.bounds.height
                    let scrollY = max(0, cursorY - visibleHeight / 2)
                    scrollView.contentView.scroll(to: NSPoint(x: 0, y: scrollY))
                    scrollView.reflectScrolledClipView(scrollView.contentView)
                }
            }

            // Apply text rendering (syntax first, then inline — inline overrides non-code lines)
            if let ts = textView.textStorage {
                ts.beginEditing()
                SyntaxHighlighter.highlight(ts, baseFont: textView.font ?? NSFont.monospacedSystemFont(ofSize: 14, weight: .regular))
                if inlineRendering {
                    InlineMarkdownRenderer.render(ts, cursorLocation: textView.selectedRange().location)
                }
                ts.endEditing()
            }

            debounceTimer?.invalidate()
            debounceTimer = Timer.scheduledTimer(withTimeInterval: 0.15, repeats: false) { [weak self] _ in
                MainActor.assumeIsolated {
                    self?.text.wrappedValue = currentString
                    self?.isEditing = false
                }
            }
        }
    }
}

// MARK: - Line Number Ruler

class LineNumberRulerView: NSRulerView {
    private weak var textView: NSTextView?
    private let lineNumberFont = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
    private let lineNumberColor = NSColor.tertiaryLabelColor

    init(textView: NSTextView) {
        self.textView = textView
        super.init(scrollView: textView.enclosingScrollView!, orientation: .verticalRuler)
        self.ruleThickness = 40
        self.clientView = textView

        NotificationCenter.default.addObserver(
            self, selector: #selector(textDidChange),
            name: NSText.didChangeNotification, object: textView
        )
        NotificationCenter.default.addObserver(
            self, selector: #selector(boundsDidChange),
            name: NSView.boundsDidChangeNotification,
            object: textView.enclosingScrollView?.contentView
        )
    }

    required init(coder: NSCoder) {
        fatalError("init(coder:) not implemented")
    }

    @objc private func textDidChange(_ notification: Notification) {
        needsDisplay = true
    }

    @objc private func boundsDidChange(_ notification: Notification) {
        needsDisplay = true
    }

    override func drawHashMarksAndLabels(in rect: NSRect) {
        guard let textView = textView,
              let layoutManager = textView.layoutManager,
              let textContainer = textView.textContainer else { return }

        let visibleRect = textView.visibleRect
        let glyphRange = layoutManager.glyphRange(forBoundingRect: visibleRect, in: textContainer)
        let charRange = layoutManager.characterRange(forGlyphRange: glyphRange, actualGlyphRange: nil)

        let text = textView.string as NSString
        let inset = textView.textContainerInset

        let attrs: [NSAttributedString.Key: Any] = [
            .font: lineNumberFont,
            .foregroundColor: lineNumberColor,
        ]

        // Count line number at the start of visible range
        var lineNumber = 1
        text.substring(to: charRange.location).enumerateLines { _, _ in
            lineNumber += 1
        }

        var index = charRange.location
        while index < NSMaxRange(charRange) {
            let lineRange = text.lineRange(for: NSRange(location: index, length: 0))
            let glyphIndex = layoutManager.glyphIndexForCharacter(at: lineRange.location)

            var lineRect = layoutManager.lineFragmentRect(forGlyphAt: glyphIndex, effectiveRange: nil)
            lineRect.origin.y += inset.height - visibleRect.origin.y

            let numStr = "\(lineNumber)" as NSString
            let strSize = numStr.size(withAttributes: attrs)
            let drawPoint = NSPoint(
                x: ruleThickness - strSize.width - 6,
                y: lineRect.origin.y + (lineRect.height - strSize.height) / 2
            )
            numStr.draw(at: drawPoint, withAttributes: attrs)

            lineNumber += 1
            index = NSMaxRange(lineRange)
        }
    }
}

// MARK: - Markdown Preview (WKWebView)
// Note: The HTML is generated from the user's own local markdown files (trusted content).
// This is a local-only editor with no external content injection.

struct MarkdownPreviewView: NSViewRepresentable {
    let html: String

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        let webView = WKWebView(frame: .zero, configuration: config)
        webView.loadHTMLString(html, baseURL: nil)
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        // Full reload on each update - simple and safe for local content
        webView.loadHTMLString(html, baseURL: nil)
    }
}
