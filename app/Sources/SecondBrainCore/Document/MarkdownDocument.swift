import Foundation

public struct MarkdownDocument: Identifiable, Sendable {
    public let id: String
    public var path: String
    public var title: String
    public var docType: String
    public var status: String
    public var tags: [String]
    public var createdAt: Date
    public var modifiedAt: Date
    public var frontmatterJSON: String  // Store as JSON string for Sendable
    public var body: String

    public init(
        id: String = UUID().uuidString,
        path: String = "",
        title: String = "",
        docType: String = "note",
        status: String = "draft",
        tags: [String] = [],
        createdAt: Date = Date(),
        modifiedAt: Date = Date(),
        frontmatterJSON: String = "{}",
        body: String = ""
    ) {
        self.id = id
        self.path = path
        self.title = title
        self.docType = docType
        self.status = status
        self.tags = tags
        self.createdAt = createdAt
        self.modifiedAt = modifiedAt
        self.frontmatterJSON = frontmatterJSON
        self.body = body
    }
}
