import AppKit

/// Applies inline markdown rendering to an NSTextView.
/// Active line shows raw markdown; all other lines render bold, italic, headings, etc.
/// This creates a hybrid "live preview" similar to Obsidian's reading view.
class InlineMarkdownRenderer {

    nonisolated(unsafe) private static let syntaxColor = NSColor.tertiaryLabelColor

    // Cached regex patterns
    private static let boldPattern = try! NSRegularExpression(pattern: "\\*\\*(.+?)\\*\\*")
    private static let italicPattern = try! NSRegularExpression(pattern: "(?<!\\*)\\*(?!\\*)(.+?)(?<!\\*)\\*(?!\\*)")
    private static let strikePattern = try! NSRegularExpression(pattern: "~~(.+?)~~")
    private static let codePattern = try! NSRegularExpression(pattern: "`([^`]+)`")

    /// Apply inline rendering to all lines except the active (cursor) line.
    /// The active line shows raw markdown for editing.
    static func render(_ textStorage: NSTextStorage, cursorLocation: Int, baseFontSize: CGFloat = 14) {
        let text = textStorage.string as NSString
        let fullRange = NSRange(location: 0, length: text.length)

        let baseFont = NSFont.monospacedSystemFont(ofSize: baseFontSize, weight: .regular)

        // Reset all text to base style
        textStorage.addAttribute(.font, value: baseFont, range: fullRange)
        textStorage.addAttribute(.foregroundColor, value: NSColor.textColor, range: fullRange)

        // Find the active line range (don't render inline on this line)
        let activeLine = text.lineRange(for: NSRange(location: cursorLocation, length: 0))

        // Scaled heading fonts
        let h1Font = NSFont.monospacedSystemFont(ofSize: round(baseFontSize * 24 / 14), weight: .bold)
        let h2Font = NSFont.monospacedSystemFont(ofSize: round(baseFontSize * 20 / 14), weight: .bold)
        let h3Font = NSFont.monospacedSystemFont(ofSize: round(baseFontSize * 17 / 14), weight: .semibold)
        let boldFont = NSFont.monospacedSystemFont(ofSize: baseFontSize, weight: .bold)
        let italicFont = baseFont.withTraits(.italic)

        // Process each line
        var lineStart = 0
        while lineStart < text.length {
            let lineRange = text.lineRange(for: NSRange(location: lineStart, length: 0))
            let line = text.substring(with: lineRange)

            // Skip the active line — show raw markdown there
            if NSIntersectionRange(lineRange, activeLine).length > 0 {
                lineStart = NSMaxRange(lineRange)
                continue
            }

            renderLine(line, in: textStorage, range: lineRange,
                       h1Font: h1Font, h2Font: h2Font, h3Font: h3Font,
                       boldFont: boldFont, italicFont: italicFont)
            lineStart = NSMaxRange(lineRange)
        }
    }

    private static func renderLine(_ line: String, in storage: NSTextStorage, range: NSRange,
                                   h1Font: NSFont, h2Font: NSFont, h3Font: NSFont,
                                   boldFont: NSFont, italicFont: NSFont?) {
        let trimmed = line.trimmingCharacters(in: .whitespaces)

        // Headings: render at larger font, dim the # markers
        if trimmed.hasPrefix("### ") {
            let markerLen = line.distance(from: line.startIndex, to: line.range(of: "### ")!.upperBound)
            storage.addAttribute(.font, value: h3Font, range: range)
            storage.addAttribute(.foregroundColor, value: syntaxColor,
                range: NSRange(location: range.location, length: markerLen))
        } else if trimmed.hasPrefix("## ") {
            let markerLen = line.distance(from: line.startIndex, to: line.range(of: "## ")!.upperBound)
            storage.addAttribute(.font, value: h2Font, range: range)
            storage.addAttribute(.foregroundColor, value: syntaxColor,
                range: NSRange(location: range.location, length: markerLen))
        } else if trimmed.hasPrefix("# ") {
            let markerLen = line.distance(from: line.startIndex, to: line.range(of: "# ")!.upperBound)
            storage.addAttribute(.font, value: h1Font, range: range)
            storage.addAttribute(.foregroundColor, value: syntaxColor,
                range: NSRange(location: range.location, length: markerLen))
        }

        // Bold: **text** → bold font, dim the ** markers
        for match in boldPattern.matches(in: line, range: NSRange(location: 0, length: line.count)) {
            let fullMatch = NSRange(location: range.location + match.range.location, length: match.range.length)
            let contentRange = NSRange(location: range.location + match.range(at: 1).location, length: match.range(at: 1).length)
            // Bold the content
            storage.addAttribute(.font, value: boldFont, range: contentRange)
            // Dim the ** markers
            let openMarker = NSRange(location: fullMatch.location, length: 2)
            let closeMarker = NSRange(location: NSMaxRange(fullMatch) - 2, length: 2)
            storage.addAttribute(.foregroundColor, value: syntaxColor, range: openMarker)
            storage.addAttribute(.foregroundColor, value: syntaxColor, range: closeMarker)
        }

        // Italic: *text* or _text_ → italic font (skip bold **)
        for match in italicPattern.matches(in: line, range: NSRange(location: 0, length: line.count)) {
            let fullMatch = NSRange(location: range.location + match.range.location, length: match.range.length)
            let contentRange = NSRange(location: range.location + match.range(at: 1).location, length: match.range(at: 1).length)
            if let italicFont {
                storage.addAttribute(.font, value: italicFont, range: contentRange)
            }
            storage.addAttribute(.foregroundColor, value: syntaxColor,
                range: NSRange(location: fullMatch.location, length: 1))
            storage.addAttribute(.foregroundColor, value: syntaxColor,
                range: NSRange(location: NSMaxRange(fullMatch) - 1, length: 1))
        }

        // Strikethrough: ~~text~~
        for match in strikePattern.matches(in: line, range: NSRange(location: 0, length: line.count)) {
            let contentRange = NSRange(location: range.location + match.range(at: 1).location, length: match.range(at: 1).length)
            storage.addAttribute(.strikethroughStyle, value: NSUnderlineStyle.single.rawValue, range: contentRange)
            let openMarker = NSRange(location: range.location + match.range.location, length: 2)
            let closeMarker = NSRange(location: range.location + NSMaxRange(match.range) - 2, length: 2)
            storage.addAttribute(.foregroundColor, value: syntaxColor, range: openMarker)
            storage.addAttribute(.foregroundColor, value: syntaxColor, range: closeMarker)
        }

        // Inline code: `code` → monospace with background tint, dim backticks
        for match in codePattern.matches(in: line, range: NSRange(location: 0, length: line.count)) {
            let fullMatch = NSRange(location: range.location + match.range.location, length: match.range.length)
            storage.addAttribute(.backgroundColor, value: NSColor.quaternaryLabelColor, range: fullMatch)
            storage.addAttribute(.foregroundColor, value: syntaxColor,
                range: NSRange(location: fullMatch.location, length: 1))
            storage.addAttribute(.foregroundColor, value: syntaxColor,
                range: NSRange(location: NSMaxRange(fullMatch) - 1, length: 1))
        }

        // Horizontal rule
        if trimmed == "---" || trimmed == "***" || trimmed == "___" {
            storage.addAttribute(.foregroundColor, value: syntaxColor, range: range)
        }
    }
}

extension NSFont {
    func withTraits(_ traits: NSFontDescriptor.SymbolicTraits) -> NSFont? {
        let descriptor = fontDescriptor.withSymbolicTraits(traits)
        return NSFont(descriptor: descriptor, size: pointSize)
    }
}
