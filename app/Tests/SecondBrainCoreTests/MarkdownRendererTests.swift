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
