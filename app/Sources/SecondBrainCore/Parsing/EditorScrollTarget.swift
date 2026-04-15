import Foundation

/// A request to scroll the editor to a specific location, consumed by the
/// `EditorArea` view's `updateNSView` method. Callers set this value on
/// `AppState.editorScrollTarget`; the editor resolves it, scrolls, and
/// clears the field.
///
/// All five variants eventually reduce to an NSRange via `LocationResolver`
/// before being handed to `NSTextView.scrollRangeToVisible`.
public enum EditorScrollTarget {
    /// Raw UTF-16 character offset into the full document text.
    case characterOffset(Int)
    /// Pre-computed NSRange (used by lint issues that carry ranges).
    case range(NSRange)
    /// A heading from the current outline; resolves to its source location
    /// plus the frontmatter offset.
    case heading(HeadingItem)
    /// 1-based line number in the full document text.
    case line(Int)
    /// Heading path string from a search result's `heading_path` field,
    /// e.g. "# Foo > ## Bar" or "Foo > Bar"; resolves against the current
    /// outline.
    case headingPath(String)
}
