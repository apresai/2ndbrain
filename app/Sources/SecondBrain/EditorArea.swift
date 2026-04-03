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
                    )
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

        return scrollView
    }

    func updateNSView(_ scrollView: NSScrollView, context: Context) {
        guard let textView = scrollView.documentView as? NSTextView else { return }
        if textView.string != text && !context.coordinator.isEditing {
            let selectedRanges = textView.selectedRanges
            textView.string = text
            textView.selectedRanges = selectedRanges
        }
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(text: $text)
    }

    @MainActor
    class Coordinator: NSObject, NSTextViewDelegate {
        var text: Binding<String>
        var isEditing = false
        private var debounceTimer: Timer?

        init(text: Binding<String>) {
            self.text = text
        }

        func textDidChange(_ notification: Notification) {
            guard let textView = notification.object as? NSTextView else { return }
            isEditing = true
            let currentString = textView.string

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
