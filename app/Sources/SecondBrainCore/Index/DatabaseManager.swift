import Foundation
import GRDB

public final class DatabaseManager: Sendable {
    private let dbPool: DatabasePool

    public init(path: String) throws {
        var config = Configuration()
        config.foreignKeysEnabled = true
        config.prepareDatabase { db in
            // Enable persistent WAL for multi-process access with Go CLI
            try db.execute(sql: "PRAGMA journal_mode = WAL")
        }
        dbPool = try DatabasePool(path: path, configuration: config)
        try migrate()
    }

    public var pool: DatabasePool { dbPool }

    private func migrate() throws {
        var migrator = DatabaseMigrator()

        migrator.registerMigration("v1") { db in
            try db.execute(sql: """
                CREATE TABLE IF NOT EXISTS documents (
                    id TEXT PRIMARY KEY,
                    path TEXT NOT NULL UNIQUE,
                    title TEXT NOT NULL DEFAULT '',
                    doc_type TEXT NOT NULL DEFAULT '',
                    status TEXT NOT NULL DEFAULT '',
                    created_at TEXT NOT NULL DEFAULT '',
                    modified_at TEXT NOT NULL DEFAULT '',
                    indexed_at TEXT NOT NULL DEFAULT '',
                    content_hash TEXT NOT NULL DEFAULT '',
                    frontmatter TEXT NOT NULL DEFAULT '{}'
                );

                CREATE INDEX IF NOT EXISTS idx_documents_path ON documents(path);
                CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(doc_type);

                CREATE TABLE IF NOT EXISTS chunks (
                    id TEXT PRIMARY KEY,
                    doc_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
                    heading_path TEXT NOT NULL,
                    level INTEGER NOT NULL DEFAULT 0,
                    content TEXT NOT NULL,
                    content_hash TEXT NOT NULL,
                    start_line INTEGER NOT NULL DEFAULT 0,
                    end_line INTEGER NOT NULL DEFAULT 0,
                    sort_order INTEGER NOT NULL DEFAULT 0
                );

                CREATE INDEX IF NOT EXISTS idx_chunks_doc ON chunks(doc_id);

                CREATE TABLE IF NOT EXISTS links (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    source_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
                    target_id TEXT,
                    target_raw TEXT NOT NULL,
                    heading TEXT NOT NULL DEFAULT '',
                    alias TEXT NOT NULL DEFAULT '',
                    resolved INTEGER NOT NULL DEFAULT 0
                );

                CREATE TABLE IF NOT EXISTS tags (
                    doc_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
                    tag TEXT NOT NULL,
                    PRIMARY KEY (doc_id, tag)
                );
            """)
        }

        try migrator.migrate(dbPool)
    }

    /// Fetch all documents ordered by modification date.
    public func allDocuments() throws -> [DocumentRecord] {
        try dbPool.read { db in
            try DocumentRecord.order(Column("modified_at").desc).fetchAll(db)
        }
    }

    /// Fetch a document by path.
    public func document(atPath path: String) throws -> DocumentRecord? {
        try dbPool.read { db in
            try DocumentRecord.filter(Column("path") == path).fetchOne(db)
        }
    }

    /// Full-text search using FTS5 (if available, falls back to LIKE).
    public func search(query: String, type: String? = nil, limit: Int = 20) throws -> [DocumentRecord] {
        try dbPool.read { db in
            var sql = "SELECT DISTINCT d.* FROM documents d"
            var args: [any DatabaseValueConvertible] = []
            var conditions: [String] = []

            if !query.isEmpty {
                // Simple LIKE search (FTS5 may not be available in all SQLite builds)
                conditions.append("(d.title LIKE ? OR d.frontmatter LIKE ?)")
                args.append("%\(query)%")
                args.append("%\(query)%")
            }

            if let type, !type.isEmpty {
                conditions.append("d.doc_type = ?")
                args.append(type)
            }

            if !conditions.isEmpty {
                sql += " WHERE " + conditions.joined(separator: " AND ")
            }

            sql += " ORDER BY d.modified_at DESC LIMIT ?"
            args.append(limit)

            return try DocumentRecord.fetchAll(db, sql: sql, arguments: StatementArguments(args))
        }
    }
    /// Fetch all link edges (source_id -> target_id).
    public func allLinks() throws -> [(source: String, target: String)] {
        try dbPool.read { db in
            let rows = try Row.fetchAll(db, sql: """
                SELECT source_id, target_id FROM links
                WHERE target_id IS NOT NULL
            """)
            return rows.compactMap { row in
                guard let source = row["source_id"] as String?,
                      let target = row["target_id"] as String? else { return nil }
                return (source: source, target: target)
            }
        }
    }

    /// Find documents that link to the given target name.
    public func backlinks(for targetName: String) throws -> [(path: String, title: String, linkText: String)] {
        try dbPool.read { db in
            let rows = try Row.fetchAll(db, sql: """
                SELECT d.path, d.title, l.target_raw
                FROM links l
                JOIN documents d ON d.id = l.source_id
                WHERE l.target_raw = ?
                LIMIT 50
            """, arguments: [targetName])

            return rows.map { row in
                (
                    path: row["path"] as String? ?? "",
                    title: row["title"] as String? ?? "",
                    linkText: row["target_raw"] as String? ?? ""
                )
            }
        }
    }
}

/// GRDB record for the documents table.
public struct DocumentRecord: Codable, FetchableRecord, PersistableRecord, Sendable {
    public static let databaseTableName = "documents"

    public var id: String
    public var path: String
    public var title: String
    public var docType: String
    public var status: String
    public var createdAt: String
    public var modifiedAt: String
    public var indexedAt: String
    public var contentHash: String
    public var frontmatter: String

    enum CodingKeys: String, CodingKey {
        case id, path, title
        case docType = "doc_type"
        case status
        case createdAt = "created_at"
        case modifiedAt = "modified_at"
        case indexedAt = "indexed_at"
        case contentHash = "content_hash"
        case frontmatter
    }
}
