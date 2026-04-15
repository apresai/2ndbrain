import Testing
import Foundation
import Markdown
@testable import SecondBrainCore

// MARK: - nsRange(for: SourceLocation, …)

@Test("nsRange: line 1, column 1 → offset 0")
func nsRangeLine1Col1() {
    let text = "hello world"
    let loc = SourceLocation(line: 1, column: 1, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    #expect(range?.location == 0)
    #expect(range?.length == 0)
}

@Test("nsRange: line 3, column 1 → skips first 2 lines including newlines")
func nsRangeLine3Col1() {
    let text = "first\nsecond\nthird"
    let loc = SourceLocation(line: 3, column: 1, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    // "first\n" = 6, "second\n" = 7 → offset 13
    #expect(range?.location == 13)
}

@Test("nsRange: handles CRLF line endings")
func nsRangeCRLF() {
    let text = "first\r\nsecond\r\nthird"
    let loc = SourceLocation(line: 3, column: 1, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    // "first\r\n" = 7, "second\r\n" = 8 → offset 15
    #expect(range?.location == 15)
}

@Test("nsRange: handles emoji in preceding lines (UTF-16 code unit math)")
func nsRangeWithEmoji() {
    // 🎉 is 2 UTF-16 code units. "🎉\n" = 3 units total.
    let text = "🎉\nsecond line"
    let loc = SourceLocation(line: 2, column: 1, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    #expect(range?.location == 3)
}

@Test("nsRange: handles CJK characters in preceding lines")
func nsRangeWithCJK() {
    // Each CJK character is 1 UTF-16 code unit but 3 UTF-8 bytes.
    // "你好\n" = 3 UTF-16 code units.
    let text = "你好\nsecond"
    let loc = SourceLocation(line: 2, column: 1, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    #expect(range?.location == 3)
}

@Test("nsRange: column > line length clamps to end of line")
func nsRangeColumnBeyondLine() {
    let text = "short\nlonger line"
    let loc = SourceLocation(line: 1, column: 99, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    // Should clamp to end of "short" (5 chars, offset 5)
    #expect(range?.location == 5)
}

@Test("nsRange: line beyond EOF returns nil")
func nsRangeLineBeyondEOF() {
    let text = "only line"
    let loc = SourceLocation(line: 5, column: 1, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    #expect(range == nil)
}

@Test("nsRange: negative or zero line returns nil")
func nsRangeInvalidLine() {
    let text = "some text"
    let loc = SourceLocation(line: 0, column: 1, source: nil)
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 0)
    #expect(range == nil)
}

@Test("nsRange: frontmatterLength = 100 adds 100 to offset")
func nsRangeWithFrontmatter() {
    let text = String(repeating: "x", count: 100) + "\n# heading\nbody"
    let loc = SourceLocation(line: 1, column: 1, source: nil) // Line 1 in the BODY
    let range = LocationResolver.nsRange(for: loc, in: text, frontmatterLength: 101)
    // Body starts at offset 101, line 1 column 1 → 101
    #expect(range?.location == 101)
}

// MARK: - nsRange(forLine:)

@Test("nsRangeForLine: line 1 returns range covering first line")
func nsRangeForLine1() {
    let text = "first\nsecond"
    let range = LocationResolver.nsRange(forLine: 1, in: text)
    #expect(range?.location == 0)
    #expect(range?.length == 5) // "first"
}

@Test("nsRangeForLine: line 2 returns range covering second line")
func nsRangeForLine2() {
    let text = "first\nsecond\nthird"
    let range = LocationResolver.nsRange(forLine: 2, in: text)
    #expect(range?.location == 6)
    #expect(range?.length == 6) // "second"
}

@Test("nsRangeForLine: empty document, line 1 returns zero range")
func nsRangeForLineEmpty() {
    let text = ""
    let range = LocationResolver.nsRange(forLine: 1, in: text)
    #expect(range?.location == 0)
    #expect(range?.length == 0)
}

@Test("nsRangeForLine: beyond EOF returns nil")
func nsRangeForLineBeyond() {
    let text = "only line"
    let range = LocationResolver.nsRange(forLine: 5, in: text)
    #expect(range == nil)
}

// MARK: - heading(matching:)

@Test("heading(matching:): exact single-level match")
func headingMatchExact() {
    let outline: [HeadingItem] = [
        makeHeading(level: 1, text: "Top"),
        makeHeading(level: 2, text: "Sub"),
    ]
    let match = LocationResolver.heading(matching: "Sub", in: outline)
    #expect(match?.text == "Sub")
}

@Test("heading(matching:): no match returns nil")
func headingMatchNone() {
    let outline: [HeadingItem] = [
        makeHeading(level: 1, text: "Top"),
    ]
    let match = LocationResolver.heading(matching: "Missing", in: outline)
    #expect(match == nil)
}

@Test("heading(matching:): duplicate text returns first occurrence")
func headingMatchDuplicate() {
    let first = makeHeading(level: 2, text: "Duplicate")
    let second = makeHeading(level: 2, text: "Duplicate")
    let outline = [first, second]
    let match = LocationResolver.heading(matching: "Duplicate", in: outline)
    #expect(match?.id == first.id)
}

@Test("heading(matching:): empty outline returns nil")
func headingMatchEmpty() {
    let match = LocationResolver.heading(matching: "anything", in: [])
    #expect(match == nil)
}

@Test("heading(matching:): normalizes heading-path prefix form")
func headingMatchPathForm() {
    // CLI `heading_path` comes as "# Foo > ## Bar"; caller may also pass "Foo > Bar".
    // Both should locate the deepest segment's heading.
    let outline: [HeadingItem] = [
        makeHeading(level: 1, text: "Foo"),
        makeHeading(level: 2, text: "Bar"),
    ]
    let hashMatch = LocationResolver.heading(matching: "# Foo > ## Bar", in: outline)
    let plainMatch = LocationResolver.heading(matching: "Foo > Bar", in: outline)
    #expect(hashMatch?.text == "Bar")
    #expect(plainMatch?.text == "Bar")
}

// MARK: - Helpers

private func makeHeading(level: Int, text: String, line: Int = 1) -> HeadingItem {
    let loc = SourceLocation(line: line, column: 1, source: nil)
    // Use a zero-width range at the heading start.
    let range = loc..<loc
    return HeadingItem(level: level, text: text, range: range)
}
