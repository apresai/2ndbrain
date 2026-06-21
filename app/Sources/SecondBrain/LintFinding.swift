import Foundation

/// A lint finding classified from `2nb lint`'s human-readable `message`, so the
/// Validation tab can offer the right remediation per finding. The CLI emits
/// these messages in fixed formats (see `cli/internal/cli/lint.go`); this is the
/// pure, view-free parser for them, unit-tested against every shape.
enum LintFinding: Equatable {
    /// `broken wikilink: [[TARGET]]` — a `[[target]]` that resolves to no note.
    case brokenLink(target: String)
    /// `missing required field 'FIELD' for type 'TYPE'`.
    case missingField(field: String, type: String)
    /// `field 'FIELD' value 'VALUE' not in [a b c]`.
    case invalidEnum(field: String, value: String, allowed: [String])
    /// `parse error: ...` — malformed frontmatter; only manual fix applies.
    case parseError
    /// Anything else the classifier doesn't recognize.
    case other

    static func classify(message: String) -> LintFinding {
        if let target = brokenLinkTarget(message) {
            return .brokenLink(target: target)
        }
        if let (field, type) = missingField(message) {
            return .missingField(field: field, type: type)
        }
        if let invalid = invalidEnum(message) {
            return .invalidEnum(field: invalid.field, value: invalid.value, allowed: invalid.allowed)
        }
        if message.hasPrefix("parse error:") {
            return .parseError
        }
        return .other
    }

    // MARK: - Parsers (format mirrors lint.go message strings)

    private static func brokenLinkTarget(_ m: String) -> String? {
        let prefix = "broken wikilink: [["
        guard m.hasPrefix(prefix), m.hasSuffix("]]") else { return nil }
        let target = String(m.dropFirst(prefix.count).dropLast(2))
        return target.isEmpty ? nil : target
    }

    private static func missingField(_ m: String) -> (field: String, type: String)? {
        guard m.hasPrefix("missing required field '"), m.contains("' for type '") else { return nil }
        // Split on the single-quote delimiters: the field and type values are
        // the 2nd and 4th components. Enum/field names never contain a quote.
        let parts = m.components(separatedBy: "'")
        guard parts.count >= 4 else { return nil }
        return (parts[1], parts[3])
    }

    private static func invalidEnum(_ m: String) -> (field: String, value: String, allowed: [String])? {
        guard m.hasPrefix("field '"), m.contains("' value '"), m.contains("' not in ["), m.hasSuffix("]") else {
            return nil
        }
        let parts = m.components(separatedBy: "'")
        guard parts.count >= 4 else { return nil }
        let field = parts[1]
        let value = parts[3]
        // The allowed set is Go's fmt %v of a []string: `[a b c]` (space-joined).
        guard let open = m.range(of: "[", options: .backwards) else { return nil }
        let inside = m[m.index(after: open.lowerBound)..<m.index(before: m.endIndex)]
        let allowed = inside.split(separator: " ").map(String.init)
        guard !allowed.isEmpty else { return nil }
        return (field, value, allowed)
    }
}
