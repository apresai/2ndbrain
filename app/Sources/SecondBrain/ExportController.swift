import AppKit
import UniformTypeIdentifiers
import WebKit
import SecondBrainCore

@MainActor
class ExportController {

    /// Export the current document as PDF using the preview's rendered HTML.
    static func exportPDF(html: String, suggestedName: String) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.pdf]
        panel.nameFieldStringValue = suggestedName.replacingOccurrences(of: ".md", with: ".pdf")
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        let webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 800, height: 600))
        webView.loadHTMLString(html, baseURL: nil)

        // Wait for content to load, then export PDF
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0) {
            webView.createPDF { result in
                switch result {
                case .success(let data):
                    try? data.write(to: url)
                case .failure(let error):
                    print("PDF export failed: \(error)")
                }
            }
        }
    }

    /// Export the current document as styled HTML.
    static func exportHTML(html: String, suggestedName: String) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.html]
        panel.nameFieldStringValue = suggestedName.replacingOccurrences(of: ".md", with: ".html")
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        try? html.write(to: url, atomically: true, encoding: .utf8)
    }

    /// Export the current document as plain markdown.
    static func exportMarkdown(content: String, suggestedName: String) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [UTType(filenameExtension: "md") ?? .plainText]
        panel.nameFieldStringValue = suggestedName
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        try? content.write(to: url, atomically: true, encoding: .utf8)
    }
}
