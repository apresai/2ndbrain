import XCTest
import SecondBrainCore

final class ReadOnlyPreviewTests: XCTestCase {
    func testPreviewIsReadOnly() throws {
        let md = """
        # Hello World

        This is a test paragraph with **bold** and *italic*.

        - Item 1
        - Item 2

        ## Code Example

        ```swift
        let x = 42
        ```
        """

        let html = MarkdownRenderer.renderHTML(md)
        XCTAssertFalse(html.contains("contenteditable=\"true\""), "Preview must never be contenteditable — would re-introduce WYSIWYG gibberish bug")
        XCTAssertFalse(html.contains("TurndownService"), "Turndown bridge must not be injected")
        XCTAssertFalse(html.contains("contentChanged"), "No HTML→markdown message bridge")
        XCTAssertTrue(html.contains("<h1>Hello World</h1>"), "Should render heading")
        XCTAssertTrue(html.contains("<strong>bold</strong>"), "Should render bold")
    }
}
