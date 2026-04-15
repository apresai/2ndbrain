import Testing
import Foundation

// These tests exercise the wikilink regex pattern and the target-
// extraction logic used by AppState.openWikilink. The regex itself is
// private to SyntaxHighlighter (in the SecondBrain app target, not
// SecondBrainCore), so we duplicate the pattern here to validate its
// behavior. If the pattern changes, update both places.
//
// Rationale: this file tests the contract ("what should match"), not
// the specific regex string. The duplication is intentional and small.
private let wikilinkPattern = try! NSRegularExpression(
    pattern: "\\[\\[([^\\[\\]\\n]+?)\\]\\]"
)

private func firstMatch(in text: String) -> (full: String, inner: String)? {
    let range = NSRange(location: 0, length: (text as NSString).length)
    guard let match = wikilinkPattern.firstMatch(in: text, range: range) else {
        return nil
    }
    let full = (text as NSString).substring(with: match.range)
    let inner = (text as NSString).substring(with: match.range(at: 1))
    return (full, inner)
}

@Test("wikilink: simple [[target]]")
func wikilinkSimple() {
    let m = firstMatch(in: "see [[Hello Auth]] for info")
    #expect(m?.inner == "Hello Auth")
}

@Test("wikilink: alias form [[target|alias]]")
func wikilinkAlias() {
    let m = firstMatch(in: "see [[Hello Auth|the auth doc]]")
    #expect(m?.inner == "Hello Auth|the auth doc")
}

@Test("wikilink: heading form [[target#heading]]")
func wikilinkHeading() {
    let m = firstMatch(in: "[[Hello Auth#Setup]]")
    #expect(m?.inner == "Hello Auth#Setup")
}

@Test("wikilink: heading and alias [[target#heading|alias]]")
func wikilinkHeadingAlias() {
    let m = firstMatch(in: "[[Hello Auth#Setup|start here]]")
    #expect(m?.inner == "Hello Auth#Setup|start here")
}

@Test("wikilink: empty [[]] does not match")
func wikilinkEmpty() {
    let m = firstMatch(in: "empty [[]] here")
    #expect(m == nil)
}

@Test("wikilink: unclosed [[foo does not match")
func wikilinkUnclosed() {
    let m = firstMatch(in: "[[unclosed")
    #expect(m == nil)
}

@Test("wikilink: nested brackets [[[[foo]]]] finds inner wikilink")
func wikilinkNested() {
    // The pattern greedy-matches the innermost well-formed [[target]]
    // pair. [[[[foo]]]] contains [[foo]] as a valid wikilink target,
    // which is what NSRegularExpression.firstMatch finds. This is
    // acceptable behavior — users don't write quadruple-bracketed text
    // in practice.
    let m = firstMatch(in: "[[[[foo]]]]")
    #expect(m?.inner == "foo")
}

@Test("wikilink: unicode target [[日本語]]")
func wikilinkUnicode() {
    let m = firstMatch(in: "[[日本語]]")
    #expect(m?.inner == "日本語")
}

@Test("wikilink: spaces in target [[my note]]")
func wikilinkSpaces() {
    let m = firstMatch(in: "text [[my note]] text")
    #expect(m?.inner == "my note")
}

@Test("wikilink: multiple wikilinks on one line")
func wikilinkMultiple() {
    let text = "[[first]] and [[second]]"
    let range = NSRange(location: 0, length: (text as NSString).length)
    let matches = wikilinkPattern.matches(in: text, range: range)
    #expect(matches.count == 2)
}

@Test("wikilink: target with punctuation inside")
func wikilinkPunct() {
    let m = firstMatch(in: "[[c++ notes]]")
    #expect(m?.inner == "c++ notes")
}

// Target-parsing logic that openWikilink uses to split alias/heading
// from the raw match. Duplicated here so the test doesn't need to
// instantiate AppState.
private func splitTarget(_ raw: String) -> (name: String, heading: String?) {
    let withoutAlias = raw.split(separator: "|").first.map(String.init) ?? raw
    let parts = withoutAlias.split(separator: "#", maxSplits: 1).map(String.init)
    let name = parts[0].trimmingCharacters(in: .whitespaces)
    let heading = parts.count == 2 ? parts[1].trimmingCharacters(in: .whitespaces) : nil
    return (name, heading)
}

@Test("splitTarget: simple target has no heading")
func splitTargetSimple() {
    let (name, heading) = splitTarget("Hello Auth")
    #expect(name == "Hello Auth")
    #expect(heading == nil)
}

@Test("splitTarget: target#heading splits correctly")
func splitTargetHeading() {
    let (name, heading) = splitTarget("Hello Auth#Setup")
    #expect(name == "Hello Auth")
    #expect(heading == "Setup")
}

@Test("splitTarget: target|alias drops alias")
func splitTargetAlias() {
    let (name, heading) = splitTarget("Hello Auth|the doc")
    #expect(name == "Hello Auth")
    #expect(heading == nil)
}

@Test("splitTarget: target#heading|alias drops alias, keeps heading")
func splitTargetHeadingAlias() {
    let (name, heading) = splitTarget("Hello Auth#Setup|start here")
    #expect(name == "Hello Auth")
    #expect(heading == "Setup")
}
