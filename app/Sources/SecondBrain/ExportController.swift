import AppKit
import UniformTypeIdentifiers
import WebKit
import SecondBrainCore
import os

private let log = Logger(subsystem: "dev.apresai.2ndbrain", category: "export")

@MainActor
class ExportController {

    /// Prevent the delegate from being deallocated before the callback fires.
    /// WKNavigationDelegate is a weak reference, so without this the delegate
    /// and webView would be freed immediately after exportPDF returns.
    private static var activeExport: PDFExportDelegate?

    /// Export the current document as PDF using the preview's rendered HTML.
    /// Uses WKNavigationDelegate to wait for full render before capturing.
    static func exportPDF(html: String, suggestedName: String) {
        log.info("PDF export started for \(suggestedName)")
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.pdf]
        panel.nameFieldStringValue = suggestedName.replacingOccurrences(of: ".md", with: ".pdf")
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else {
            log.debug("PDF export cancelled by user")
            return
        }

        let delegate = PDFExportDelegate(outputURL: url) {
            Self.activeExport = nil
        }
        activeExport = delegate

        let webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 800, height: 600))
        webView.navigationDelegate = delegate
        delegate.webView = webView
        log.info("Loading HTML for PDF render to \(url.lastPathComponent)")
        webView.loadHTMLString(html, baseURL: nil)
    }

    /// Export the current document as styled HTML.
    static func exportHTML(html: String, suggestedName: String) {
        log.info("HTML export started for \(suggestedName)")
        let panel = NSSavePanel()
        panel.allowedContentTypes = [.html]
        panel.nameFieldStringValue = suggestedName.replacingOccurrences(of: ".md", with: ".html")
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        do {
            try html.write(to: url, atomically: true, encoding: .utf8)
            log.info("HTML exported to \(url.lastPathComponent)")
        } catch {
            log.error("HTML export failed: \(error.localizedDescription)")
            let alert = NSAlert(error: error)
            alert.runModal()
        }
    }

    /// Export the current document as plain markdown.
    static func exportMarkdown(content: String, suggestedName: String) {
        log.info("Markdown export started for \(suggestedName)")
        let panel = NSSavePanel()
        panel.allowedContentTypes = [UTType(filenameExtension: "md") ?? .plainText]
        panel.nameFieldStringValue = suggestedName
        panel.canCreateDirectories = true

        guard panel.runModal() == .OK, let url = panel.url else { return }

        do {
            try content.write(to: url, atomically: true, encoding: .utf8)
            log.info("Markdown exported to \(url.lastPathComponent)")
        } catch {
            log.error("Markdown export failed: \(error.localizedDescription)")
            let alert = NSAlert(error: error)
            alert.runModal()
        }
    }
}

/// Waits for WKWebView to finish loading, then captures PDF.
private class PDFExportDelegate: NSObject, WKNavigationDelegate {
    let outputURL: URL
    let onComplete: () -> Void
    var webView: WKWebView? // strong ref to keep webView alive

    init(outputURL: URL, onComplete: @escaping () -> Void) {
        self.outputURL = outputURL
        self.onComplete = onComplete
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        log.debug("WKWebView finished loading for PDF capture")
        // Small delay for JS rendering (Mermaid/KaTeX) after DOM load
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [self] in
            webView.createPDF { result in
                Task { @MainActor [self] in
                    switch result {
                    case .success(let data):
                        do {
                            try data.write(to: outputURL)
                            log.info("PDF exported to \(self.outputURL.lastPathComponent) (\(data.count) bytes)")
                        } catch {
                            log.error("PDF write failed: \(error.localizedDescription)")
                            let alert = NSAlert(error: error)
                            alert.runModal()
                        }
                    case .failure(let error):
                        log.error("PDF capture failed: \(error.localizedDescription)")
                        let alert = NSAlert()
                        alert.messageText = "PDF Export Failed"
                        alert.informativeText = error.localizedDescription
                        alert.runModal()
                    }
                    self.webView = nil
                    self.onComplete()
                }
            }
        }
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
        log.error("WKWebView navigation failed: \(error.localizedDescription)")
        Task { @MainActor [self] in
            let alert = NSAlert()
            alert.messageText = "PDF Export Failed"
            alert.informativeText = error.localizedDescription
            alert.runModal()
            self.webView = nil
            self.onComplete()
        }
    }
}
