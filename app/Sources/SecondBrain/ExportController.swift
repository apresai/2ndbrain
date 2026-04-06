import AppKit
import UniformTypeIdentifiers
import WebKit
import SecondBrainCore

@MainActor
class ExportController {

    /// Export the current document as PDF using the preview's rendered HTML.
    /// Uses WKNavigationDelegate to wait for full render before capturing.
    static func exportPDF(html: String, suggestedName: String) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.pdf]
        panel.nameFieldStringValue = suggestedName.replacingOccurrences(of: ".md", with: ".pdf")
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        let delegate = PDFExportDelegate(outputURL: url)
        let webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 800, height: 600))
        webView.navigationDelegate = delegate
        // Hold a strong reference to prevent deallocation before callback
        delegate.webView = webView
        webView.loadHTMLString(html, baseURL: nil)
    }

    /// Export the current document as styled HTML.
    static func exportHTML(html: String, suggestedName: String) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.html]
        panel.nameFieldStringValue = suggestedName.replacingOccurrences(of: ".md", with: ".html")
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        do {
            try html.write(to: url, atomically: true, encoding: .utf8)
        } catch {
            let alert = NSAlert(error: error)
            alert.runModal()
        }
    }

    /// Export the current document as plain markdown.
    static func exportMarkdown(content: String, suggestedName: String) {
        let panel = NSSavePanel()
        panel.allowedContentTypes = [UTType(filenameExtension: "md") ?? .plainText]
        panel.nameFieldStringValue = suggestedName
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        do {
            try content.write(to: url, atomically: true, encoding: .utf8)
        } catch {
            let alert = NSAlert(error: error)
            alert.runModal()
        }
    }
}

/// Waits for WKWebView to finish loading, then captures PDF.
private class PDFExportDelegate: NSObject, WKNavigationDelegate {
    let outputURL: URL
    var webView: WKWebView? // strong ref to keep webView alive

    init(outputURL: URL) {
        self.outputURL = outputURL
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        // Small delay for JS rendering (Mermaid/KaTeX) after DOM load
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [self] in
            webView.createPDF { result in
                Task { @MainActor [self] in
                    switch result {
                    case .success(let data):
                        do {
                            try data.write(to: outputURL)
                        } catch {
                            let alert = NSAlert(error: error)
                            alert.runModal()
                        }
                    case .failure(let error):
                        let alert = NSAlert()
                        alert.messageText = "PDF Export Failed"
                        alert.informativeText = error.localizedDescription
                        alert.runModal()
                    }
                    self.webView = nil // release
                }
            }
        }
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        Task { @MainActor [self] in
            let alert = NSAlert()
            alert.messageText = "PDF Export Failed"
            alert.informativeText = error.localizedDescription
            alert.runModal()
            self.webView = nil
        }
    }
}
