import Testing
import Foundation
import SecondBrainCore

@Test("parse extracts YAML frontmatter and body")
func parseValidFrontmatter() {
    let input = "---\ntitle: Test Doc\ntype: note\nstatus: draft\n---\n# Hello World\n\nBody text."
    let (fm, body) = FrontmatterParser.parse(input)
    #expect(fm["title"] as? String == "Test Doc")
    #expect(fm["type"] as? String == "note")
    #expect(fm["status"] as? String == "draft")
    #expect(body.hasPrefix("# Hello World"))
}

@Test("parse returns raw content when no frontmatter")
func parseMissingFrontmatter() {
    let input = "Just plain text\nNo frontmatter here."
    let (fm, body) = FrontmatterParser.parse(input)
    #expect(fm.isEmpty)
    #expect(body == input)
}

@Test("parse returns raw content for unclosed frontmatter")
func parseUnclosedFrontmatter() {
    let input = "---\ntitle: Broken\nThis never closes"
    let (fm, body) = FrontmatterParser.parse(input)
    #expect(fm.isEmpty)
    #expect(body == input)
}

@Test("parse handles empty body after frontmatter")
func parseEmptyBody() {
    let input = "---\ntitle: Empty\n---\n"
    let (fm, body) = FrontmatterParser.parse(input)
    #expect(fm["title"] as? String == "Empty")
    #expect(body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
}

@Test("parse extracts tags array")
func parseTags() {
    let input = "---\ntitle: Tagged\ntags: [auth, security, jwt]\n---\n# Content"
    let (fm, _) = FrontmatterParser.parse(input)
    let tags = fm["tags"]
    #expect(tags != nil)
    if let arr = tags as? [String] {
        #expect(arr.contains("auth"))
        #expect(arr.contains("security"))
        #expect(arr.count == 3)
    }
}

@Test("serialize produces valid frontmatter + body")
func serializeRoundtrip() {
    let fm: [String: Any] = ["title": "Round Trip", "type": "note"]
    let body = "# Heading\n\nParagraph."
    let output = FrontmatterParser.serialize(frontmatter: fm, body: body)
    #expect(output.hasPrefix("---\n"))
    #expect(output.contains("title: Round Trip"))
    #expect(output.contains("# Heading"))
}

@Test("parse handles special characters in values")
func parseSpecialChars() {
    let input = "---\ntitle: \"It's a \\\"test\\\" with: colons\"\n---\n# Body"
    let (fm, _) = FrontmatterParser.parse(input)
    #expect(fm["title"] != nil)
}

@Test("parse does not crash on Obsidian template placeholders")
func parseTemplatePlaceholders() {
    // `date: {{date}}` parses as a YAML mapping keyed by a mapping; Yams.load
    // TRAPS on it (force-unwrap nil in its Constructor), which used to crash the
    // whole app while indexing a vault that contained template files. The
    // compose-based parser must survive it and still expose the simple fields.
    let input = "---\ntitle:\ndate: {{date}}\ntags: [daily]\nstatus: draft\n---\n# {{title}}\n"
    let (fm, body) = FrontmatterParser.parse(input)
    #expect(fm["status"] as? String == "draft")
    #expect((fm["tags"] as? [String])?.contains("daily") == true)
    #expect(body.contains("{{title}}"))
}

@Test("parse degrades gracefully on duplicate keys (no crash)")
func parseDuplicateKeys() {
    // Yams.load TRAPS on some malformed mappings; the compose-based parser must
    // never crash. Whether the composer rejects duplicate keys (empty frontmatter)
    // or keeps the last value is a Yams detail — either way it returns, and never
    // surfaces the shadowed first value.
    let input = "---\ntitle: First\ntitle: Second\nstatus: draft\n---\nBody"
    let (fm, body) = FrontmatterParser.parse(input)
    #expect(fm["title"] as? String != "First")
    #expect(body.contains("Body"))
}

@Test("parse preserves scalar types (string/int/bool) and null fallback")
func parseScalarTypes() {
    let input = "---\ntitle: Note\ncount: 5\npinned: true\nid:\n---\nBody"
    let (fm, _) = FrontmatterParser.parse(input)
    #expect(fm["title"] as? String == "Note")
    #expect(fm["count"] as? Int == 5)
    #expect(fm["pinned"] as? Bool == true)
    // An empty value resolves to NSNull (not ""), so `id as? String` is nil and
    // loadDocument falls back to a generated UUID rather than an empty id.
    #expect(fm["id"] as? String == nil)
}

@Test("loadDocument creates MarkdownDocument from URL")
func loadDocumentFromFile() throws {
    let tmp = FileManager.default.temporaryDirectory
        .appendingPathComponent("test-\(UUID().uuidString).md")
    let content = "---\nid: abc-123\ntitle: Test Note\ntype: adr\nstatus: proposed\ntags: [arch]\n---\n# Test Note\n\nBody."
    try content.write(to: tmp, atomically: true, encoding: .utf8)
    defer { try? FileManager.default.removeItem(at: tmp) }

    let doc = try FrontmatterParser.loadDocument(from: tmp)
    #expect(doc.id == "abc-123")
    #expect(doc.title == "Test Note")
    #expect(doc.docType == "adr")
    #expect(doc.status == "proposed")
    #expect(doc.tags.contains("arch"))
}
