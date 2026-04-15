import Testing
import SecondBrainCore

@Test("renderHTML converts headings")
func renderHeadings() {
    let html = MarkdownRenderer.renderHTML("# H1\n## H2\n### H3")
    #expect(html.contains("<h1>"))
    #expect(html.contains("<h2>"))
    #expect(html.contains("<h3>"))
}

@Test("renderHTML converts bold and italic")
func renderInlineFormatting() {
    let html = MarkdownRenderer.renderHTML("**bold** and *italic*")
    #expect(html.contains("<strong>bold</strong>"))
    #expect(html.contains("<em>italic</em>"))
}

@Test("renderHTML converts code blocks")
func renderCodeBlock() {
    let html = MarkdownRenderer.renderHTML("```go\nfmt.Println(\"hello\")\n```")
    #expect(html.contains("<code"))
    #expect(html.contains("Println"))
}

@Test("renderHTML handles empty input without crashing")
func renderEmpty() {
    let html = MarkdownRenderer.renderHTML("")
    // May return empty string or minimal wrapper — just verify no crash
    #expect(html.count >= 0)
}

@Test("renderHTML converts links")
func renderLinks() {
    let html = MarkdownRenderer.renderHTML("[click](https://example.com)")
    #expect(html.contains("href"))
    #expect(html.contains("example.com"))
}

@Test("extractOutline returns heading hierarchy")
func extractOutlineBasic() {
    let outline = MarkdownRenderer.extractOutline("# Top\n## Sub\n### Deep\n## Another")
    #expect(outline.count == 4)
    #expect(outline[0].text == "Top")
    #expect(outline[0].level == 1)
    #expect(outline[1].text == "Sub")
    #expect(outline[1].level == 2)
    #expect(outline[2].level == 3)
}

@Test("extractOutline returns empty for no headings")
func extractOutlineNoHeadings() {
    let outline = MarkdownRenderer.extractOutline("Just a paragraph.\nNo headings here.")
    #expect(outline.isEmpty)
}

@Test("extractOutline populates SourceRange on each heading")
func extractOutlineHasRanges() {
    let outline = MarkdownRenderer.extractOutline("# Top\n## Sub")
    #expect(outline.count == 2)
    // swift-markdown populates ranges by default; our plan depends on this.
    // If this assertion fails, LocationResolver needs a line-scanning fallback.
    #expect(outline[0].range != nil, "H1 heading should have a SourceRange")
    #expect(outline[1].range != nil, "H2 heading should have a SourceRange")
    if let topRange = outline[0].range {
        #expect(topRange.lowerBound.line == 1, "Top heading is on line 1")
    }
    if let subRange = outline[1].range {
        #expect(subRange.lowerBound.line == 2, "Sub heading is on line 2")
    }
}

@Test("extractOutline: H1-H6 all captured with correct levels")
func extractOutlineAllLevels() {
    let md = "# H1\n## H2\n### H3\n#### H4\n##### H5\n###### H6"
    let outline = MarkdownRenderer.extractOutline(md)
    #expect(outline.count == 6)
    for (index, heading) in outline.enumerated() {
        #expect(heading.level == index + 1)
    }
}

@Test("extractOutline: fenced code block headings are NOT returned")
func extractOutlineSkipsCodeBlocks() {
    let md = "# Real\n\n```\n# fake heading in code\n```\n\n## Also real"
    let outline = MarkdownRenderer.extractOutline(md)
    #expect(outline.count == 2)
    #expect(outline[0].text == "Real")
    #expect(outline[1].text == "Also real")
}

@Test("extractOutline: heading with emoji preserves plain text")
func extractOutlineEmojiText() {
    let outline = MarkdownRenderer.extractOutline("# 🎉 Launch Day")
    #expect(outline.count == 1)
    #expect(outline[0].text.contains("🎉"))
    #expect(outline[0].text.contains("Launch Day"))
}

@Test("extractOutline: empty document returns empty array")
func extractOutlineEmptyDoc() {
    let outline = MarkdownRenderer.extractOutline("")
    #expect(outline.isEmpty)
}
