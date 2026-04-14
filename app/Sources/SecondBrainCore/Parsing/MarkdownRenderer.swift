import Foundation
import Markdown

public enum MarkdownRenderer {
    /// Renders markdown to HTML for the preview pane. When `editable` is true,
    /// the body is `contenteditable` and a Turndown.js bridge posts edits back
    /// via `window.webkit.messageHandlers.contentChanged.postMessage(md)`.
    public static func renderHTML(_ markdown: String, editable: Bool = false) -> String {
        let document = Document(parsing: markdown)
        var visitor = HTMLVisitor(editable: editable)
        return visitor.visit(document)
    }

    /// Extracts the heading outline from a markdown document.
    public static func extractOutline(_ markdown: String) -> [HeadingItem] {
        let document = Document(parsing: markdown)
        var visitor = OutlineVisitor()
        visitor.visit(document)
        return visitor.headings
    }
}

public struct HeadingItem: Identifiable {
    public let id = UUID()
    public let level: Int
    public let text: String
    public let range: SourceRange?
}

// MARK: - HTML Visitor

private struct HTMLVisitor: MarkupVisitor {
    typealias Result = String

    let editable: Bool

    mutating func defaultVisit(_ markup: any Markup) -> String {
        markup.children.map { visit($0) }.joined()
    }

    mutating func visitDocument(_ document: Document) -> String {
        let body = document.children.map { visit($0) }.joined()
        let contentEditableAttr = editable ? " contenteditable=\"true\"" : ""
        let editableCSS = editable ? """
        body:focus { outline: none; }
        body { cursor: text; min-height: 100vh; }
        .mermaid-diagram { contenteditable: false; pointer-events: none; }
        """ : ""
        let turndownBridge = editable ? """
        <script>
        \(TurndownJS.source)
        </script>
        <script>
        (function() {
          var turndownService = new TurndownService({
            headingStyle: 'atx',
            hr: '---',
            bulletListMarker: '-',
            codeBlockStyle: 'fenced',
            fence: '```',
            emDelimiter: '*',
            strongDelimiter: '**',
            linkStyle: 'inlined'
          });

          // Keep task list checkboxes
          turndownService.addRule('taskListItems', {
            filter: function(node) {
              return node.nodeName === 'INPUT' && node.getAttribute('type') === 'checkbox';
            },
            replacement: function(content, node) {
              return node.checked ? '[x] ' : '[ ] ';
            }
          });

          var debounceTimer = null;
          document.body.addEventListener('input', function() {
            clearTimeout(debounceTimer);
            debounceTimer = setTimeout(function() {
              try {
                var md = turndownService.turndown(document.body.innerHTML);
                window.webkit.messageHandlers.contentChanged.postMessage(md);
              } catch(e) {
                console.error('Turndown error:', e);
              }
            }, 300);
          });
        })();
        </script>
        """ : ""
        return """
        <!DOCTYPE html>
        <html>
        <head>
        <meta charset="utf-8">
        <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
            font-size: 15px;
            line-height: 1.6;
            color: var(--text);
            padding: 20px 32px;
            max-width: 800px;
            margin: 0 auto;
            --text: #1d1d1f;
            --code-bg: #f5f5f7;
            --border: #d2d2d7;
            --link: #0066cc;
        }
        @media (prefers-color-scheme: dark) {
            body { --text: #f5f5f7; --code-bg: #2c2c2e; --border: #48484a; --link: #4da3ff; background: #1c1c1e; }
        }
        h1, h2, h3, h4, h5, h6 { margin-top: 1.5em; margin-bottom: 0.5em; font-weight: 600; }
        h1 { font-size: 1.8em; border-bottom: 1px solid var(--border); padding-bottom: 0.3em; }
        h2 { font-size: 1.4em; border-bottom: 1px solid var(--border); padding-bottom: 0.2em; }
        code { font-family: "SF Mono", Menlo, monospace; font-size: 0.9em; background: var(--code-bg); padding: 2px 6px; border-radius: 4px; }
        pre { background: var(--code-bg); padding: 16px; border-radius: 8px; overflow-x: auto; }
        pre code { background: none; padding: 0; }
        a { color: var(--link); text-decoration: none; }
        a:hover { text-decoration: underline; }
        blockquote { border-left: 3px solid var(--border); margin-left: 0; padding-left: 16px; color: #6e6e73; }
        table { border-collapse: collapse; width: 100%; }
        th, td { border: 1px solid var(--border); padding: 8px 12px; text-align: left; }
        th { background: var(--code-bg); font-weight: 600; }
        img { max-width: 100%; }
        ul.task-list { list-style: none; padding-left: 0; }
        ul.task-list li { position: relative; padding-left: 1.5em; }
        ul.task-list li input[type="checkbox"] { position: absolute; left: 0; top: 0.3em; }
        \(editableCSS)
        </style>
        <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/katex@0.16.9/dist/katex.min.css"
          onerror="this.remove()">
        <script defer src="https://cdn.jsdelivr.net/npm/katex@0.16.9/dist/katex.min.js"
          onerror="this.remove()"></script>
        <script defer src="https://cdn.jsdelivr.net/npm/katex@0.16.9/dist/contrib/auto-render.min.js"
          onload="renderMathInElement(document.body, {delimiters: [{left: '$$', right: '$$', display: true}, {left: '$', right: '$', display: false}]});"
          onerror="this.remove()"></script>
        <script type="module">
          try {
            const { default: mermaid } = await import('https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.esm.min.mjs');
            mermaid.initialize({ startOnLoad: false, theme: window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'default' });
            document.querySelectorAll('pre code.language-mermaid').forEach(async (el) => {
              const pre = el.parentElement;
              const { svg } = await mermaid.render('mermaid-' + Math.random().toString(36).substr(2, 9), el.textContent);
              pre.outerHTML = '<div class="mermaid-diagram" contenteditable="false">' + svg + '</div>';
            });
          } catch(e) { /* offline — mermaid code blocks show as plain text */ }
        </script>
        </head>
        <body\(contentEditableAttr)>\(body)\(turndownBridge)</body>
        </html>
        """
    }

    mutating func visitHeading(_ heading: Heading) -> String {
        let content = heading.children.map { visit($0) }.joined()
        let tag = "h\(heading.level)"
        return "<\(tag)>\(content)</\(tag)>\n"
    }

    mutating func visitParagraph(_ paragraph: Paragraph) -> String {
        let content = paragraph.children.map { visit($0) }.joined()
        return "<p>\(content)</p>\n"
    }

    mutating func visitText(_ text: Text) -> String {
        escapeHTML(text.string)
    }

    mutating func visitEmphasis(_ emphasis: Emphasis) -> String {
        "<em>\(emphasis.children.map { visit($0) }.joined())</em>"
    }

    mutating func visitStrong(_ strong: Strong) -> String {
        "<strong>\(strong.children.map { visit($0) }.joined())</strong>"
    }

    mutating func visitInlineCode(_ inlineCode: InlineCode) -> String {
        "<code>\(escapeHTML(inlineCode.code))</code>"
    }

    mutating func visitCodeBlock(_ codeBlock: CodeBlock) -> String {
        let lang = codeBlock.language ?? ""
        let langAttr = lang.isEmpty ? "" : " class=\"language-\(lang)\""
        return "<pre><code\(langAttr)>\(escapeHTML(codeBlock.code))</code></pre>\n"
    }

    mutating func visitLink(_ link: Markdown.Link) -> String {
        let text = link.children.map { visit($0) }.joined()
        let dest = link.destination ?? ""
        return "<a href=\"\(escapeHTML(dest))\">\(text)</a>"
    }

    mutating func visitImage(_ image: Markdown.Image) -> String {
        let alt = image.children.map { visit($0) }.joined()
        let src = image.source ?? ""
        return "<img src=\"\(escapeHTML(src))\" alt=\"\(escapeHTML(alt))\">"
    }

    mutating func visitUnorderedList(_ list: UnorderedList) -> String {
        let items = list.children.map { visit($0) }.joined()
        return "<ul>\(items)</ul>\n"
    }

    mutating func visitOrderedList(_ list: OrderedList) -> String {
        let items = list.children.map { visit($0) }.joined()
        return "<ol>\(items)</ol>\n"
    }

    mutating func visitListItem(_ item: ListItem) -> String {
        let content = item.children.map { visit($0) }.joined()
        return "<li>\(content)</li>\n"
    }

    mutating func visitBlockQuote(_ blockQuote: BlockQuote) -> String {
        let content = blockQuote.children.map { visit($0) }.joined()
        return "<blockquote>\(content)</blockquote>\n"
    }

    mutating func visitThematicBreak(_ thematicBreak: ThematicBreak) -> String {
        "<hr>\n"
    }

    mutating func visitSoftBreak(_ softBreak: SoftBreak) -> String {
        "\n"
    }

    mutating func visitLineBreak(_ lineBreak: LineBreak) -> String {
        "<br>\n"
    }

    mutating func visitHTMLBlock(_ html: HTMLBlock) -> String {
        html.rawHTML
    }

    mutating func visitInlineHTML(_ html: InlineHTML) -> String {
        html.rawHTML
    }

    private func escapeHTML(_ string: String) -> String {
        string
            .replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
            .replacingOccurrences(of: "\"", with: "&quot;")
    }
}

// MARK: - Outline Visitor

private struct OutlineVisitor: MarkupWalker {
    var headings: [HeadingItem] = []

    mutating func visitHeading(_ heading: Heading) {
        let text = heading.plainText
        headings.append(HeadingItem(level: heading.level, text: text, range: heading.range))
    }
}
