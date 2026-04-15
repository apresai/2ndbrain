import Foundation
import Yams

public enum FrontmatterParser {
    public static func parse(_ content: String) -> (frontmatter: [String: Any], body: String) {
        guard content.hasPrefix("---\n") || content.hasPrefix("---\r\n") else {
            return ([:], content)
        }

        let rest = String(content.dropFirst(4))
        guard let endIndex = rest.range(of: "\n---\n") else {
            return ([:], content)
        }

        let yamlString = String(rest[rest.startIndex..<endIndex.lowerBound])
        let bodyStart = rest[endIndex.upperBound...]

        do {
            if let parsed = try Yams.load(yaml: yamlString) as? [String: Any] {
                return (parsed, String(bodyStart))
            }
        } catch {
            // Malformed YAML - return raw content
        }

        return ([:], content)
    }

    /// Returns the length (in UTF-16 code units) of the leading frontmatter
    /// block in `content`, including the opening and closing `---` delimiters.
    /// Returns 0 when the content has no frontmatter. Used by LocationResolver
    /// to offset source locations that were computed against the post-
    /// frontmatter body, so they land correctly in the full document.
    public static func frontmatterLength(in content: String) -> Int {
        let (fm, body) = parse(content)
        guard !fm.isEmpty else { return 0 }
        // If parse fell back to returning the raw content as body (malformed),
        // body.utf16.count == content.utf16.count and the result is 0.
        return content.utf16.count - body.utf16.count
    }

    public static func serialize(frontmatter: [String: Any], body: String) -> String {
        var result = "---\n"
        if let yaml = try? Yams.dump(object: frontmatter, sortKeys: true) {
            result += yaml
        }
        result += "---\n"
        result += body
        return result
    }

    public static func loadDocument(from url: URL) throws -> MarkdownDocument {
        let content = try String(contentsOf: url, encoding: .utf8)
        let (fm, body) = parse(content)

        let id = fm["id"] as? String ?? UUID().uuidString
        let title = fm["title"] as? String ?? url.deletingPathExtension().lastPathComponent
        let docType = fm["type"] as? String ?? "note"
        let status = fm["status"] as? String ?? ""
        let tags = extractTags(from: fm)

        let createdAt = parseDate(fm["created"]) ?? Date()
        let modifiedAt = parseDate(fm["modified"]) ?? Date()

        return MarkdownDocument(
            id: id,
            path: url.path,
            title: title,
            docType: docType,
            status: status,
            tags: tags,
            createdAt: createdAt,
            modifiedAt: modifiedAt,
            frontmatterJSON: fmToJSON(fm),
            body: body
        )
    }

    private static func extractTags(from fm: [String: Any]) -> [String] {
        guard let raw = fm["tags"] else { return [] }
        if let arr = raw as? [String] { return arr }
        if let arr = raw as? [Any] { return arr.compactMap { $0 as? String } }
        return []
    }

    private static func parseDate(_ value: Any?) -> Date? {
        guard let str = value as? String else { return nil }
        let formatter = ISO8601DateFormatter()
        return formatter.date(from: str)
    }

    private static func fmToJSON(_ fm: [String: Any]) -> String {
        let sanitized = sanitizeForJSON(fm)
        guard JSONSerialization.isValidJSONObject(sanitized),
              let data = try? JSONSerialization.data(withJSONObject: sanitized, options: [.sortedKeys]),
              let str = String(data: data, encoding: .utf8) else { return "{}" }
        return str
    }

    private static func sanitizeForJSON(_ value: Any) -> Any {
        switch value {
        case let dict as [String: Any]:
            return dict.mapValues { sanitizeForJSON($0) }
        case let arr as [Any]:
            return arr.map { sanitizeForJSON($0) }
        case let date as Date:
            return ISO8601DateFormatter().string(from: date)
        case is String, is Bool, is Int, is Double, is Float:
            return value
        case is NSNull:
            return NSNull()
        default:
            return "\(value)"
        }
    }
}
