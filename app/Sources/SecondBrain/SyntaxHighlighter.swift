import AppKit

/// Applies syntax highlighting to fenced code blocks in an NSTextView.
/// Detects ```language blocks and colors keywords, strings, comments, and numbers.
class SyntaxHighlighter {
    // Semantic colors that adapt to light/dark mode
    private static let keywordColor = NSColor.systemPurple
    private static let stringColor = NSColor.systemGreen
    private static let commentColor = NSColor.systemGray
    private static let numberColor = NSColor.systemOrange
    private static let typeColor = NSColor.systemBlue
    private static let fenceColor = NSColor.tertiaryLabelColor

    // Cached regex patterns (compiled once)
    private static let fencePattern = try! NSRegularExpression(
        pattern: "^(`{3,})(\\w*)\\s*$", options: .anchorsMatchLines)
    private static let stringPattern = try! NSRegularExpression(
        pattern: "\"[^\"\\\\]*(\\\\.[^\"\\\\]*)*\"|'[^'\\\\]*(\\\\.[^'\\\\]*)*'")
    private static let numberPattern = try! NSRegularExpression(
        pattern: "\\b\\d+(\\.\\d+)?\\b")
    private static let slashCommentPattern = try! NSRegularExpression(
        pattern: "//.*$", options: .anchorsMatchLines)
    private static let hashCommentPattern = try! NSRegularExpression(
        pattern: "#.*$", options: .anchorsMatchLines)

    // Wikilink pattern: [[target]], [[target|alias]], or [[target#heading]]
    // Non-greedy inside the brackets. Requires both opening and closing
    // double brackets on the same line (no wrapping).
    private static let wikilinkPattern = try! NSRegularExpression(
        pattern: "\\[\\[([^\\[\\]\\n]+?)\\]\\]")

    // Common keywords by language family
    private static let cLikeKeywords: Set<String> = [
        "if", "else", "for", "while", "return", "break", "continue", "switch", "case",
        "default", "var", "let", "const", "func", "function", "class", "struct", "enum",
        "interface", "type", "import", "package", "defer", "go", "chan", "select",
        "async", "await", "try", "catch", "throw", "new", "delete", "nil", "null",
        "true", "false", "self", "this", "super", "public", "private", "static",
        "final", "override", "protocol", "extension", "guard", "where", "in", "range",
        "map", "filter", "reduce", "def", "elif", "except", "finally", "from", "as",
        "with", "yield", "lambda", "pass", "raise", "not", "and", "or", "is", "None",
        "True", "False", "export", "module", "require", "extends", "implements",
    ]

    private static let shellKeywords: Set<String> = [
        "if", "then", "else", "elif", "fi", "for", "while", "do", "done", "case", "esac",
        "function", "return", "exit", "echo", "export", "source", "local", "readonly",
        "set", "unset", "shift", "cd", "pwd", "test", "true", "false",
    ]

    // Cached keyword regex patterns (deterministic sort for stable regex)
    private static let cLikeKeywordPattern: NSRegularExpression = {
        try! NSRegularExpression(pattern: "\\b(" + cLikeKeywords.sorted().joined(separator: "|") + ")\\b")
    }()
    private static let shellKeywordPattern: NSRegularExpression = {
        try! NSRegularExpression(pattern: "\\b(" + shellKeywords.sorted().joined(separator: "|") + ")\\b")
    }()

    /// Apply syntax highlighting to all fenced code blocks in the text storage.
    static func highlight(_ textStorage: NSTextStorage, baseFont: NSFont) {
        let text = textStorage.string as NSString
        let fullRange = NSRange(location: 0, length: text.length)

        // Reset foreground color only for non-code-block text (preserve InlineMarkdownRenderer colors)
        // Code block colors will be set below; non-code text is handled by InlineMarkdownRenderer
        if fullRange.length > 0 {
            textStorage.addAttribute(.foregroundColor, value: NSColor.textColor, range: fullRange)
            // Clear any existing link attributes; we'll re-apply wikilinks below.
            textStorage.removeAttribute(.link, range: fullRange)
        }

        // Mark wikilinks as clickable links with a wikilink:// scheme.
        // NSTextView handles link clicks natively; the Coordinator's
        // textView(_:clickedOnLink:at:) delegate intercepts them and
        // routes the target through appState.openWikilink. Skips matches
        // that land inside a fenced code block (second pass below).
        applyWikilinks(textStorage, fullRange: fullRange)

        let matches = fencePattern.matches(in: text as String, range: fullRange)
        var i = 0
        while i < matches.count {
            let openMatch = matches[i]
            let openRange = openMatch.range
            let langRange = openMatch.range(at: 2)
            let language = langRange.length > 0 ? (text.substring(with: langRange) as String).lowercased() : ""

            // Find closing fence
            var closeRange: NSRange?
            for j in (i + 1)..<matches.count {
                let candidate = matches[j]
                if candidate.range.location > NSMaxRange(openRange) {
                    closeRange = candidate.range
                    i = j + 1
                    break
                }
            }

            guard let close = closeRange else {
                i += 1
                continue
            }

            // Color the fence markers
            textStorage.addAttribute(.foregroundColor, value: fenceColor, range: openRange)
            textStorage.addAttribute(.foregroundColor, value: fenceColor, range: close)

            // Get the code block content range
            let codeStart = NSMaxRange(openRange)
            let codeEnd = close.location
            guard codeEnd > codeStart else { continue }
            let codeRange = NSRange(location: codeStart, length: codeEnd - codeStart)
            let codeStr = text.substring(with: codeRange)

            highlightCode(codeStr, in: textStorage, offset: codeStart, language: language)
        }

        // If we didn't advance via close matching, move forward
        if i < matches.count {
            // Remaining unmatched fences — just color them
            for j in i..<matches.count {
                textStorage.addAttribute(.foregroundColor, value: fenceColor, range: matches[j].range)
            }
        }
    }

    /// Applies `.link` attributes to every [[wikilink]] in the text that
    /// isn't inside a fenced code block. Uses a custom `wikilink://`
    /// scheme; the editor's click delegate intercepts it. This keeps the
    /// brackets themselves unstyled (just tinted) so the source remains
    /// readable while still being clickable.
    private static func applyWikilinks(_ textStorage: NSTextStorage, fullRange: NSRange) {
        let text = textStorage.string
        let nsText = text as NSString

        // Collect code-block ranges so we can skip matches inside them.
        var codeBlockRanges: [NSRange] = []
        let fenceMatches = fencePattern.matches(in: text, range: fullRange)
        var i = 0
        while i < fenceMatches.count {
            let open = fenceMatches[i].range
            var close: NSRange?
            for j in (i + 1)..<fenceMatches.count {
                let cand = fenceMatches[j].range
                if cand.location > NSMaxRange(open) {
                    close = cand
                    i = j + 1
                    break
                }
            }
            if let close {
                codeBlockRanges.append(NSRange(location: open.location,
                                                length: NSMaxRange(close) - open.location))
            } else {
                i += 1
            }
        }

        let linkColor = NSColor.linkColor
        for match in wikilinkPattern.matches(in: text, range: fullRange) {
            let range = match.range
            // Skip matches inside code blocks.
            let inCode = codeBlockRanges.contains { cb in
                range.location >= cb.location && NSMaxRange(range) <= NSMaxRange(cb)
            }
            if inCode { continue }

            // Extract the inner target (group 1).
            guard match.numberOfRanges >= 2 else { continue }
            let innerRange = match.range(at: 1)
            let inner = nsText.substring(with: innerRange)

            // Store the link as a raw string, not a URL. Using URL here
            // runs into Foundation's URL parser treating path separators
            // as host/path boundaries — wikilinks with subdirectories
            // (e.g. [[projects/q2]]) lose everything after the first
            // `/` because URL.host returns only "projects". NSAttributedString
            // .link accepts NSString, and textView(_:clickedOnLink:at:)
            // receives it as Any, so the click handler can recover the
            // target verbatim by stripping the "wikilink://" prefix.
            let linkValue = "wikilink://\(inner)" as NSString
            textStorage.addAttribute(.link, value: linkValue, range: range)
            textStorage.addAttribute(.foregroundColor, value: linkColor, range: range)
            textStorage.addAttribute(.underlineStyle,
                                      value: NSUnderlineStyle.single.rawValue,
                                      range: range)
        }
    }

    private static func highlightCode(_ code: String, in storage: NSTextStorage, offset: Int, language: String) {
        let nsCode = code as NSString
        let codeRange = NSRange(location: 0, length: nsCode.length)

        // Comments (single-line)
        let commentPattern: NSRegularExpression
        switch language {
        case "bash", "sh", "zsh", "python", "py", "yaml", "yml", "toml":
            commentPattern = hashCommentPattern
        default:
            commentPattern = slashCommentPattern
        }

        for match in commentPattern.matches(in: code, range: codeRange) {
            storage.addAttribute(.foregroundColor, value: commentColor,
                range: NSRange(location: match.range.location + offset, length: match.range.length))
        }

        for match in stringPattern.matches(in: code, range: codeRange) {
            storage.addAttribute(.foregroundColor, value: stringColor,
                range: NSRange(location: match.range.location + offset, length: match.range.length))
        }

        for match in numberPattern.matches(in: code, range: codeRange) {
            storage.addAttribute(.foregroundColor, value: numberColor,
                range: NSRange(location: match.range.location + offset, length: match.range.length))
        }

        let keywordPat = (language == "bash" || language == "sh" || language == "zsh")
            ? shellKeywordPattern : cLikeKeywordPattern
        for match in keywordPat.matches(in: code, range: codeRange) {
            storage.addAttribute(.foregroundColor, value: keywordColor,
                range: NSRange(location: match.range.location + offset, length: match.range.length))
        }
    }
}
