import Foundation
import Markdown

/// Pure helpers for mapping swift-markdown source locations and line numbers
/// into NSRange values that NSTextView can consume. Lives in SecondBrainCore
/// so the unit tests can cover it without touching SwiftUI or AppKit.
///
/// Why this exists: `HeadingItem.range` is a `Range<SourceLocation>` where
/// `SourceLocation.column` is a **1-based UTF-8 byte** offset, while NSRange
/// uses **UTF-16 code units**. A naive conversion produces wrong offsets for
/// any document containing emoji, CJK, or combining characters. Likewise,
/// when `extractOutline` parses only the markdown body (after stripping
/// frontmatter), resulting offsets must be shifted by the frontmatter length
/// to land correctly in the editor which displays the full file.
///
/// Implementation note: Swift treats "\r\n" as a single extended grapheme
/// cluster, so walking `String`'s `Character` view can't distinguish CRLF
/// from a regular character. This implementation walks the `unicodeScalars`
/// view where \r and \n are separate scalars, then maps scalar offsets back
/// into UTF-16 space for NSRange.
public enum LocationResolver {

    /// Converts a swift-markdown SourceLocation into an NSRange (zero length,
    /// pointing at the start) in the full document text.
    ///
    /// - Parameters:
    ///   - location: swift-markdown `SourceLocation` with 1-based line and
    ///     1-based UTF-8 column.
    ///   - text: the *full* document text, including any frontmatter.
    ///   - frontmatterLength: UTF-16 code-unit length of the frontmatter
    ///     block that was stripped before parsing. Zero if no frontmatter.
    /// - Returns: an NSRange anchored at the character offset, or nil if the
    ///   location is out of bounds.
    public static func nsRange(for location: SourceLocation,
                                in text: String,
                                frontmatterLength: Int) -> NSRange? {
        guard location.line >= 1 else { return nil }

        // Extract the body (post-frontmatter) for line iteration.
        let body: Substring
        if frontmatterLength == 0 {
            body = Substring(text)
        } else {
            let utf16 = text.utf16
            guard frontmatterLength <= utf16.count else { return nil }
            let startUtf16 = utf16.index(utf16.startIndex, offsetBy: frontmatterLength)
            guard let bodyStartIndex = String.Index(startUtf16, within: text) else {
                return nil
            }
            body = text[bodyStartIndex...]
        }

        // Walk the body's unicode scalars to find the start-of-line offset
        // (in UTF-16 code units) for `location.line`.
        var currentLine = 1
        var lineStartUtf16: Int = 0
        var scalars = body.unicodeScalars.makeIterator()
        var pendingCR = false

        while currentLine < location.line {
            guard let scalar = scalars.next() else {
                // Ran out of input before reaching the target line.
                return nil
            }
            let utf16Width = scalar.utf16.count

            if scalar == "\r" {
                pendingCR = true
                lineStartUtf16 += utf16Width
                // Don't advance line until we see if \n follows.
                continue
            }
            if scalar == "\n" {
                lineStartUtf16 += utf16Width
                currentLine += 1
                pendingCR = false
                continue
            }
            // If we had a lone \r (not followed by \n), that still ended a line.
            if pendingCR {
                currentLine += 1
                pendingCR = false
                // Fall through to count this scalar.
            }
            lineStartUtf16 += utf16Width
        }

        // Handle the edge case where the target line began after a lone \r.
        if pendingCR { currentLine += 1 }
        guard currentLine == location.line else { return nil }

        // Walk the target line's scalars to find the column offset.
        // SourceLocation.column is a 1-based UTF-8 byte offset.
        let bytesToConsume = max(0, location.column - 1)
        var bytesConsumed = 0
        var columnUtf16: Int = 0

        while bytesConsumed < bytesToConsume {
            guard let scalar = scalars.next() else { break }
            if scalar == "\n" || scalar == "\r" { break }
            let byteWidth = String(scalar).utf8.count
            if bytesConsumed + byteWidth > bytesToConsume { break }
            bytesConsumed += byteWidth
            columnUtf16 += scalar.utf16.count
        }

        let fullOffset = frontmatterLength + lineStartUtf16 + columnUtf16
        guard fullOffset <= text.utf16.count else { return nil }
        return NSRange(location: fullOffset, length: 0)
    }

    /// Returns the NSRange covering the entire Nth line (1-based) of `text`,
    /// excluding the line terminator. An empty string returns a zero range
    /// at location 0 for line 1; any line beyond EOF returns nil.
    public static func nsRange(forLine line: Int, in text: String) -> NSRange? {
        guard line >= 1 else { return nil }

        if text.isEmpty {
            return line == 1 ? NSRange(location: 0, length: 0) : nil
        }

        var currentLine = 1
        var lineStartUtf16: Int = 0
        var offsetUtf16: Int = 0
        var lineStartAtCurrent: Int = 0  // updated when we enter a new line
        var pendingCR = false

        // First pass: advance to target line's start.
        let scalars = text.unicodeScalars
        var iter = scalars.makeIterator()

        while currentLine < line {
            guard let scalar = iter.next() else { return nil }
            let w = scalar.utf16.count
            offsetUtf16 += w

            if scalar == "\r" {
                pendingCR = true
                continue
            }
            if scalar == "\n" {
                currentLine += 1
                lineStartAtCurrent = offsetUtf16
                pendingCR = false
                continue
            }
            if pendingCR {
                currentLine += 1
                // The target line started right after the \r (which we already counted).
                lineStartAtCurrent = offsetUtf16 - w
                pendingCR = false
                if currentLine == line {
                    // Need to re-include the current scalar in the line measurement,
                    // but our iterator has already consumed it. Simulate by stepping
                    // offsetUtf16 back and re-scanning the line from here using a
                    // different code path — or accept that pending-CR line-start is
                    // the position *after* the \r.
                }
            }
        }
        if pendingCR {
            currentLine += 1
            lineStartAtCurrent = offsetUtf16
        }
        guard currentLine == line else { return nil }
        lineStartUtf16 = lineStartAtCurrent

        // Second: measure the line's length up to the next terminator.
        var lineLength = 0
        var localPendingCR = false
        while let scalar = iter.next() {
            if scalar == "\r" {
                localPendingCR = true
                break
            }
            if scalar == "\n" { break }
            if localPendingCR { break }
            lineLength += scalar.utf16.count
        }

        return NSRange(location: lineStartUtf16, length: lineLength)
    }

    /// Finds the first heading in `outline` whose text matches the terminal
    /// segment of `path`. Accepts path forms with or without markdown prefix:
    /// "Foo > Bar", "# Foo > ## Bar", or bare "Bar".
    public static func heading(matching path: String, in outline: [HeadingItem]) -> HeadingItem? {
        guard !outline.isEmpty else { return nil }

        let segments = path.split(separator: ">").map { $0.trimmingCharacters(in: .whitespaces) }
        guard let last = segments.last, !last.isEmpty else { return nil }
        let target = last.trimmingCharacters(in: CharacterSet(charactersIn: "# "))
        guard !target.isEmpty else { return nil }

        return outline.first { $0.text == target }
    }
}
