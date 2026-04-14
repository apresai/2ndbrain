import XCTest
import SecondBrainCore

final class EditablePreviewTests: XCTestCase {
    func testEditableHTMLGenerated() throws {
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

        let html = MarkdownRenderer.renderHTML(md, editable: true)
        XCTAssertTrue(html.contains("contenteditable=\"true\""), "Body should be contenteditable")
        XCTAssertTrue(html.contains("TurndownService"), "Should include Turndown.js")
        XCTAssertTrue(html.contains("messageHandlers"), "Should include WKScriptMessageHandler bridge")
        XCTAssertTrue(html.contains("contentChanged"), "Should post contentChanged messages")

        let readOnlyHTML = MarkdownRenderer.renderHTML(md)
        XCTAssertFalse(readOnlyHTML.contains("contenteditable=\"true\""), "Read-only should not be contenteditable=true")
        XCTAssertFalse(readOnlyHTML.contains("TurndownService"), "Read-only should not include Turndown")
    }
}
