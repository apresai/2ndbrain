import SwiftUI
import WebKit
import SecondBrainCore

struct EditorArea: View {
    @Environment(AppState.self) var appState
    @State private var editorMode: EditorMode = .source

    var body: some View {
        @Bindable var state = appState

        if let tabIndex = validTabIndex {
            Group {
                switch editorMode {
                case .source:
                    sourceEditor(tabIndex: tabIndex, state: state)
                case .split:
                    HSplitView {
                        sourceEditor(tabIndex: tabIndex, state: state)
                            .frame(minWidth: 300)
                        MarkdownPreviewView(html: { previewHTML }, appState: appState)
                            .frame(minWidth: 300)
                    }
                case .preview:
                    MarkdownPreviewView(
                        html: { editablePreviewHTML },
                        editable: true,
                        onContentChanged: { newBody in
                            appState.updateBodyOfCurrentDocument(newBody)
                        },
                        appState: appState
                    )
                }
            }
            .toolbar {
                ToolbarItemGroup(placement: .primaryAction) {
                    Button {
                        appState.showTemplatePicker = true
                    } label: {
                        Label("New", systemImage: "doc.badge.plus")
                    }
                    .disabled(appState.vault == nil)
                    .help("New Document")

                    Button {
                        appState.saveCurrentDocument()
                    } label: {
                        Label("Save", systemImage: "square.and.arrow.down")
                    }
                    .disabled(appState.currentDocument?.isDirty != true)
                    .help("Save Document")

                    Button {
                        guard let url = appState.currentDocument?.url,
                              let contentView = NSApp.keyWindow?.contentView else { return }
                        let picker = NSSharingServicePicker(items: [url])
                        let anchor = NSRect(x: contentView.bounds.maxX - 160, y: contentView.bounds.maxY, width: 1, height: 1)
                        picker.show(relativeTo: anchor, of: contentView, preferredEdge: .minY)
                    } label: {
                        Label("Share", systemImage: "square.and.arrow.up")
                    }
                    .disabled(appState.currentDocument == nil)
                    .help("Share")
                }

                ToolbarItem(placement: .automatic) {
                    Picker("Mode", selection: $editorMode) {
                        Label("Source", systemImage: "pencil.line")
                            .tag(EditorMode.source)
                        Label("Split", systemImage: "rectangle.split.2x1")
                            .tag(EditorMode.split)
                        Label("Preview", systemImage: "eye")
                            .tag(EditorMode.preview)
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 180)
                    .help("Editor mode: Source, Split, or Preview")
                }
            }
        }
    }

    @ViewBuilder
    private func sourceEditor(tabIndex: Int, state: AppState) -> some View {
        let readOnly = tabIndex < state.openDocuments.count && state.openDocuments[tabIndex].readOnly
        MarkdownEditorView(
            text: Binding(
                get: {
                    guard tabIndex < state.openDocuments.count else { return "" }
                    return state.openDocuments[tabIndex].content
                },
                set: { newValue in
                    guard tabIndex < state.openDocuments.count else { return }
                    guard !state.openDocuments[tabIndex].readOnly else { return }
                    state.openDocuments[tabIndex].content = newValue
                    state.openDocuments[tabIndex].isDirty = true
                    state.updateOutline()
                }
            ),
            databaseManager: state.database,
            typewriterMode: state.typewriterModeActive,
            inlineRendering: state.inlineRenderingEnabled,
            fontSize: state.editorFontSize,
            fontFamily: state.editorFontFamily,
            readOnly: readOnly,
            appState: state
        )
    }

    private var validTabIndex: Int? { appState.validTabIndex }

    private var previewHTML: String { renderedPreviewHTML(editable: false) }
    private var editablePreviewHTML: String { renderedPreviewHTML(editable: true) }

    private func renderedPreviewHTML(editable: Bool) -> String {
        guard let idx = validTabIndex else { return "" }
        let (_, body) = FrontmatterParser.parse(appState.openDocuments[idx].content)
        return injectFontOverrides(MarkdownRenderer.renderHTML(body, editable: editable))
    }

    private func injectFontOverrides(_ html: String) -> String {
        let cssFontFamily = appState.editorFontFamily == "System Mono"
            ? "-apple-system, BlinkMacSystemFont, sans-serif"
            : "\"\(appState.editorFontFamily)\", -apple-system, sans-serif"
        let fontOverride = "<style>body { font-size: \(Int(appState.editorFontSize))px !important; font-family: \(cssFontFamily) !important; }</style>"
        return html.replacingOccurrences(of: "</head>", with: "\(fontOverride)</head>")
    }
}

// MARK: - Markdown Source Editor (NSTextView)

struct MarkdownEditorView: NSViewRepresentable {
    @Binding var text: String
    var databaseManager: DatabaseManager?
    var typewriterMode: Bool = false
    var inlineRendering: Bool = false
    var fontSize: CGFloat = 14
    var fontFamily: String = "System Mono"
    var readOnly: Bool = false
    // Passed explicitly so updateNSView can observe editorScrollTarget,
    // pendingFirstResponder, and outline without reaching into @Environment
    // (NSViewRepresentable doesn't propagate Environment reliably).
    var appState: AppState

    private func resolveFont(size: CGFloat? = nil) -> NSFont {
        let sz = size ?? fontSize
        if fontFamily == "System Mono" {
            return NSFont.monospacedSystemFont(ofSize: sz, weight: .regular)
        }
        return NSFont(name: fontFamily, size: sz)
            ?? NSFont.monospacedSystemFont(ofSize: sz, weight: .regular)
    }

    func makeNSView(context: Context) -> NSScrollView {
        let scrollView = NSTextView.scrollableTextView()
        guard let textView = scrollView.documentView as? NSTextView else { return scrollView }

        textView.isEditable = !readOnly
        textView.isSelectable = true
        textView.allowsUndo = true
        textView.isRichText = false
        textView.font = resolveFont()
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
        context.coordinator.fontSize = fontSize
        context.coordinator.fontFamily = fontFamily
        textView.string = text

        // Line number gutter
        let rulerView = LineNumberRulerView(textView: textView, fontSize: fontSize)
        scrollView.verticalRulerView = rulerView
        scrollView.hasVerticalRuler = true
        scrollView.rulersVisible = true

        return scrollView
    }

    func updateNSView(_ scrollView: NSScrollView, context: Context) {
        guard let textView = scrollView.documentView as? NSTextView else { return }
        let renderingChanged = context.coordinator.inlineRendering != inlineRendering
        let fontChanged = context.coordinator.fontSize != fontSize || context.coordinator.fontFamily != fontFamily
        context.coordinator.typewriterMode = typewriterMode
        context.coordinator.inlineRendering = inlineRendering
        context.coordinator.fontSize = fontSize
        context.coordinator.fontFamily = fontFamily
        context.coordinator.appState = appState
        textView.isEditable = !readOnly

        // Update font when size or family changed
        if fontChanged {
            let newFont = resolveFont()
            textView.font = newFont
            if let rulerView = scrollView.verticalRulerView as? LineNumberRulerView {
                rulerView.updateFontSize(fontSize)
            }
        }

        // Re-apply rendering when toggle or font changes
        if renderingChanged || fontChanged, let ts = textView.textStorage {
            let baseFont = resolveFont()
            ts.beginEditing()
            let fullRange = NSRange(location: 0, length: ts.length)
            ts.addAttribute(.font, value: baseFont, range: fullRange)
            ts.addAttribute(.foregroundColor, value: NSColor.textColor, range: fullRange)
            ts.removeAttribute(.strikethroughStyle, range: fullRange)
            ts.removeAttribute(.backgroundColor, range: fullRange)
            SyntaxHighlighter.highlight(ts, baseFont: baseFont)
            if inlineRendering {
                InlineMarkdownRenderer.render(ts, cursorLocation: textView.selectedRange().location, baseFontSize: fontSize)
            }
            ts.endEditing()
        }

        if textView.string != text && !context.coordinator.isEditing {
            let selectedRanges = textView.selectedRanges
            textView.string = text
            textView.selectedRanges = selectedRanges
        }

        // Apply pending scroll target (outline click, search result, lint
        // issue, etc.). Clears the field after applying so the next update
        // doesn't re-scroll on unrelated state changes.
        if let target = appState.editorScrollTarget {
            applyScrollTarget(target, textView: textView, fullText: textView.string)
            DispatchQueue.main.async {
                appState.editorScrollTarget = nil
            }
        }

        // Honor a pending first-responder request from openDocument, but
        // never steal focus from a live overlay. If any picker/panel is
        // up the user is interacting with it; applying makeFirstResponder
        // would yank focus into the editor mid-keystroke. Leave the flag
        // set — the next updateNSView cycle (triggered when the overlay's
        // own @Observable flag flips to false) will pick it up.
        if appState.pendingFirstResponder {
            let overlayActive = appState.showSearch
                || appState.showQuickOpen
                || appState.showCommandPalette
                || appState.showAskAI
                || appState.showTemplatePicker
                || appState.showSuggestLinks
                || appState.showPolish
            if !overlayActive {
                DispatchQueue.main.async {
                    scrollView.window?.makeFirstResponder(textView)
                    appState.pendingFirstResponder = false
                }
            }
        }
    }

    private func applyScrollTarget(_ target: EditorScrollTarget,
                                    textView: NSTextView,
                                    fullText: String) {
        let frontmatterLength = FrontmatterParser.frontmatterLength(in: fullText)
        let resolved: NSRange?

        switch target {
        case .characterOffset(let n):
            resolved = (n >= 0 && n <= fullText.utf16.count) ? NSRange(location: n, length: 0) : nil
        case .range(let r):
            resolved = r
        case .heading(let h):
            if let sourceRange = h.range {
                resolved = LocationResolver.nsRange(
                    for: sourceRange.lowerBound,
                    in: fullText,
                    frontmatterLength: frontmatterLength
                )
            } else {
                resolved = nil
            }
        case .line(let l):
            resolved = LocationResolver.nsRange(forLine: l, in: fullText)
        case .headingPath(let path):
            guard let h = LocationResolver.heading(matching: path, in: appState.outline),
                  let sourceRange = h.range else {
                resolved = nil
                break
            }
            resolved = LocationResolver.nsRange(
                for: sourceRange.lowerBound,
                in: fullText,
                frontmatterLength: frontmatterLength
            )
        }

        guard let raw = resolved else {
            return
        }
        // Clamp to text bounds defensively — an outline parsed before an
        // edit may produce stale ranges.
        let maxLocation = fullText.utf16.count
        let clampedLoc = min(raw.location, maxLocation)
        let clampedLen = max(0, min(raw.length, maxLocation - clampedLoc))
        let clamped = NSRange(location: clampedLoc, length: clampedLen)

        textView.scrollRangeToVisible(clamped)
        textView.setSelectedRange(NSRange(location: clamped.location, length: 0))
        textView.showFindIndicator(for: clamped)
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
        var fontSize: CGFloat = 14
        var fontFamily: String = "System Mono"
        let mentionController = MentionAutocompleteController()
        let slashController = SlashCommandController()
        // Set by updateNSView so link-click delegate can route wikilinks
        // through the right AppState instance. Weak would be ideal but
        // AppState is a class and this file keeps a strong ref anyway
        // via the MarkdownEditorView struct — the coordinator's lifetime
        // is bounded by the same Representable.
        weak var appState: AppState?
        private var debounceTimer: Timer?

        init(text: Binding<String>, databaseManager: DatabaseManager?) {
            self.text = text
            super.init()
            mentionController.databaseManager = databaseManager
        }

        /// NSTextView delegate hook for link clicks. We use a custom
        /// `wikilink://` prefix in the syntax highlighter to mark
        /// [[wikilink]] ranges; intercept those here and route through
        /// AppState.openWikilink. Return true to indicate we handled the
        /// click ourselves.
        ///
        /// The link value is stored as an NSString (not URL) because
        /// Foundation's URL parser mangles path separators in the "host"
        /// portion — wikilinks with subdirectories would lose everything
        /// after the first `/`. We accept URL as a fallback for any
        /// other code path that may still store URLs here.
        func textView(_ textView: NSTextView, clickedOnLink link: Any, at charIndex: Int) -> Bool {
            let raw: String
            if let s = link as? String {
                raw = s
            } else if let s = link as? NSString {
                raw = s as String
            } else if let u = link as? URL {
                raw = u.absoluteString
            } else {
                return false
            }
            guard raw.hasPrefix("wikilink://") else { return false }
            let target = String(raw.dropFirst("wikilink://".count))
            appState?.openWikilink(target)
            return true
        }

        func resolvedFont() -> NSFont {
            if fontFamily == "System Mono" {
                return NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
            }
            return NSFont(name: fontFamily, size: fontSize)
                ?? NSFont.monospacedSystemFont(ofSize: fontSize, weight: .regular)
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
                let baseFont = resolvedFont()
                ts.beginEditing()
                SyntaxHighlighter.highlight(ts, baseFont: baseFont)
                if inlineRendering {
                    InlineMarkdownRenderer.render(ts, cursorLocation: textView.selectedRange().location, baseFontSize: fontSize)
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
    private var lineNumberFont: NSFont
    private let lineNumberColor = NSColor.tertiaryLabelColor

    init(textView: NSTextView, fontSize: CGFloat = 14) {
        self.textView = textView
        self.lineNumberFont = NSFont.monospacedSystemFont(ofSize: max(fontSize - 3, 9), weight: .regular)
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

    deinit {
        NotificationCenter.default.removeObserver(self)
    }

    func updateFontSize(_ size: CGFloat) {
        lineNumberFont = NSFont.monospacedSystemFont(ofSize: max(size - 3, 9), weight: .regular)
        needsDisplay = true
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
    let html: () -> String
    var editable: Bool = false
    var onContentChanged: ((String) -> Void)? = nil
    var appState: AppState

    func makeNSView(context: Context) -> WKWebView {
        let config = WKWebViewConfiguration()
        if editable {
            config.userContentController.add(context.coordinator, name: "contentChanged")
        }
        let webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = context.coordinator
        let initialHTML = html()
        webView.loadHTMLString(initialHTML, baseURL: nil)
        context.coordinator.lastHTML = initialHTML
        context.coordinator.onContentChanged = onContentChanged
        context.coordinator.appState = appState
        return webView
    }

    func updateNSView(_ webView: WKWebView, context: Context) {
        context.coordinator.onContentChanged = onContentChanged
        context.coordinator.appState = appState
        // In editable mode the WKWebView owns state — reloading would destroy
        // cursor and undo history. Content flows one-way via the JS bridge.
        if editable { return }
        let currentHTML = html()
        guard context.coordinator.lastHTML != currentHTML else { return }
        context.coordinator.lastHTML = currentHTML
        webView.loadHTMLString(currentHTML, baseURL: nil)
    }

    func makeCoordinator() -> PreviewCoordinator { PreviewCoordinator() }

    class PreviewCoordinator: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
        var lastHTML = ""
        var onContentChanged: ((String) -> Void)?
        weak var appState: AppState?

        func userContentController(
            _ userContentController: WKUserContentController,
            didReceive message: WKScriptMessage
        ) {
            guard message.name == "contentChanged",
                  let markdown = message.body as? String else { return }
            onContentChanged?(markdown)
        }

        // Route link clicks in the preview webview:
        // - wikilink:// → appState.openWikilink (stay in app)
        // - http/https → NSWorkspace (external browser)
        // - mailto:/tel:/other → NSWorkspace
        // - about:/no URL → allow (initial HTML load, in-page anchors)
        func webView(_ webView: WKWebView,
                     decidePolicyFor navigationAction: WKNavigationAction,
                     decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            guard let url = navigationAction.request.url else {
                decisionHandler(.allow)
                return
            }

            // Initial loadHTMLString comes through with about:blank — always allow.
            if url.scheme == "about" {
                decisionHandler(.allow)
                return
            }

            // In-page anchor navigation (href="#section"). WebKit handles
            // scrolling natively; allow it.
            if navigationAction.navigationType == .other && url.fragment != nil && url.host == nil {
                decisionHandler(.allow)
                return
            }

            if url.scheme == "wikilink" {
                let payload = url.host?.removingPercentEncoding
                    ?? url.absoluteString.replacingOccurrences(of: "wikilink://", with: "")
                Task { @MainActor in
                    self.appState?.openWikilink(payload)
                }
                decisionHandler(.cancel)
                return
            }

            // Block dangerous schemes defense-in-depth, even though
            // swift-markdown currently escapes raw HTML in preview output.
            // If that ever changes, a doc containing
            // `<a href="javascript:...">` must not execute script in our
            // webview, and `data:` URIs shouldn't load arbitrary content.
            if let scheme = url.scheme?.lowercased() {
                if scheme == "javascript" || scheme == "data" {
                    decisionHandler(.cancel)
                    return
                }
            }

            if navigationAction.navigationType == .linkActivated {
                // Any other link type opens in the default handler.
                NSWorkspace.shared.open(url)
                decisionHandler(.cancel)
                return
            }

            decisionHandler(.allow)
        }
    }
}

// MARK: - Editor Mode

enum EditorMode: String, CaseIterable {
    case source, split, preview
}
