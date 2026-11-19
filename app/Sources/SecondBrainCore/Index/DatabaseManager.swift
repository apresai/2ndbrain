import Foundation
import GRDB

public final class DatabaseManager: Sendable {
    private let dbPool: DatabasePool

    public init(path: String) throws {
        var config = Configuration()
        config.foreignKeysEnabled = true
        // Wait up to 5s on a locked DB instead of failing immediately. The Go
        // CLI and every MCP server open the same index with busy_timeout=5000;
        // matching it here means a concurrent write from the CLI doesn't surface
        // as a hard "database is locked" error in the app, and the GUI's reads
        // don't starve a `2nb vault checkpoint` running alongside.
        config.busyMode = .timeout(5)
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

    /// Total number of indexed documents.
    public func documentCount() throws -> Int {
        try dbPool.read { db in
            try Int.fetchOne(db, sql: "SELECT COUNT(*) FROM documents") ?? 0
        }
    }

    /// Search-first note picker query across `documents.title`, `documents.path`,
    /// and `chunks_fts` (falling back to `chunks.content LIKE` if `chunks_fts` is
    /// absent or a query fails FTS5 parsing). Empty query returns most recently
    /// modified notes up to `limit`.
    public func searchNotes(query: String, limit: Int = 50) throws -> [NotePickerItem] {
        let boundedLimit = max(1, min(limit, 500))
        let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)

        return try dbPool.read { db in
            if trimmed.isEmpty {
                let rows = try Row.fetchAll(db, sql: """
                    SELECT id, path, title, doc_type, status, modified_at
                    FROM documents
                    ORDER BY modified_at DESC, path ASC
                    LIMIT ?
                """, arguments: [boundedLimit])
                return rows.map { row in
                    NotePickerItem(
                        id: row["id"] as String? ?? "",
                        path: row["path"] as String? ?? "",
                        title: row["title"] as String? ?? "",
                        docType: row["doc_type"] as String? ?? "",
                        status: row["status"] as String? ?? "",
                        modifiedAt: row["modified_at"] as String? ?? "",
                        snippet: nil,
                        matchSource: "recent"
                    )
                }
            }

            var results: [NotePickerItem] = []
            var seenIDs = Set<String>()
            let likePattern = "%\(Self.escapeLike(trimmed))%"

            // 1. Exact/title/path matches first.
            let titlePathRows = try Row.fetchAll(db, sql: """
                SELECT id, path, title, doc_type, status, modified_at,
                       CASE
                           WHEN LOWER(path) = LOWER(?) OR LOWER(title) = LOWER(?) THEN 0
                           WHEN title LIKE ? ESCAPE '\\' THEN 1
                           ELSE 2
                       END AS rank
                FROM documents
                WHERE title LIKE ? ESCAPE '\\' OR path LIKE ? ESCAPE '\\'
                ORDER BY rank ASC, modified_at DESC, path ASC
                LIMIT ?
            """, arguments: [trimmed, trimmed, likePattern, likePattern, likePattern, boundedLimit])

            for row in titlePathRows {
                let id = row["id"] as String? ?? ""
                guard !id.isEmpty, seenIDs.insert(id).inserted else { continue }
                let rank = row["rank"] as Int? ?? 2
                results.append(NotePickerItem(
                    id: id,
                    path: row["path"] as String? ?? "",
                    title: row["title"] as String? ?? "",
                    docType: row["doc_type"] as String? ?? "",
                    status: row["status"] as String? ?? "",
                    modifiedAt: row["modified_at"] as String? ?? "",
                    snippet: nil,
                    matchSource: rank <= 1 ? "title" : "path"
                ))
            }

            if results.count >= boundedLimit {
                return Array(results.prefix(boundedLimit))
            }

            // 2. Body search via FTS5 `chunks_fts` when present.
            let hasFTS = (try? Int.fetchOne(
                db,
                sql: "SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'chunks_fts'"
            )) != nil

            let ftsQuery = Self.ftsMatchQuery(for: trimmed)
            if hasFTS && !ftsQuery.isEmpty {
                do {
                    let ftsRows = try Row.fetchAll(db, sql: """
                        WITH matched AS (
                            SELECT d.id, d.path, d.title, d.doc_type, d.status, d.modified_at,
                                   SUBSTR(c.content, 1, 160) AS snippet,
                                   bm25(chunks_fts) AS score,
                                   c.sort_order AS sort_order
                            FROM chunks_fts
                            JOIN chunks c ON c.rowid = chunks_fts.rowid
                            JOIN documents d ON d.id = c.doc_id
                            WHERE chunks_fts MATCH ?
                        ),
                        ranked AS (
                            SELECT id, path, title, doc_type, status, modified_at, snippet, score,
                                   ROW_NUMBER() OVER (PARTITION BY id ORDER BY score ASC, sort_order ASC) AS rn
                            FROM matched
                        )
                        SELECT id, path, title, doc_type, status, modified_at, snippet
                        FROM ranked
                        WHERE rn = 1
                        ORDER BY score ASC, modified_at DESC, path ASC
                        LIMIT ?
                    """, arguments: [ftsQuery, boundedLimit])

                    for row in ftsRows {
                        let id = row["id"] as String? ?? ""
                        guard !id.isEmpty, seenIDs.insert(id).inserted else { continue }
                        results.append(NotePickerItem(
                            id: id,
                            path: row["path"] as String? ?? "",
                            title: row["title"] as String? ?? "",
                            docType: row["doc_type"] as String? ?? "",
                            status: row["status"] as String? ?? "",
                            modifiedAt: row["modified_at"] as String? ?? "",
                            snippet: Self.cleanSnippet(row["snippet"] as String?),
                            matchSource: "content"
                        ))
                        if results.count >= boundedLimit {
                            return Array(results.prefix(boundedLimit))
                        }
                    }
                } catch {
                    // Fall through to LIKE content search below.
                }
            }

            // 3. Fallback / substring content LIKE search across chunks.
            let contentRows = try Row.fetchAll(db, sql: """
                WITH ranked AS (
                    SELECT d.id, d.path, d.title, d.doc_type, d.status, d.modified_at,
                           SUBSTR(c.content, 1, 160) AS snippet,
                           ROW_NUMBER() OVER (PARTITION BY d.id ORDER BY c.sort_order ASC) AS rn
                    FROM chunks c
                    JOIN documents d ON d.id = c.doc_id
                    WHERE c.content LIKE ? ESCAPE '\\'
                )
                SELECT id, path, title, doc_type, status, modified_at, snippet
                FROM ranked
                WHERE rn = 1
                ORDER BY modified_at DESC, path ASC
                LIMIT ?
            """, arguments: [likePattern, boundedLimit])

            for row in contentRows {
                let id = row["id"] as String? ?? ""
                guard !id.isEmpty, seenIDs.insert(id).inserted else { continue }
                results.append(NotePickerItem(
                    id: id,
                    path: row["path"] as String? ?? "",
                    title: row["title"] as String? ?? "",
                    docType: row["doc_type"] as String? ?? "",
                    status: row["status"] as String? ?? "",
                    modifiedAt: row["modified_at"] as String? ?? "",
                    snippet: Self.cleanSnippet(row["snippet"] as String?),
                    matchSource: "content"
                ))
                if results.count >= boundedLimit {
                    break
                }
            }

            return Array(results.prefix(boundedLimit))
        }
    }

    /// Convert a user query into a safe FTS5 MATCH expression matching `cli/internal/search`.
    public static func ftsMatchQuery(for query: String) -> String {
        let rawWords = query.split(whereSeparator: { $0.isWhitespace }).map(String.init)
        var parts: [String] = []
        for raw in rawWords {
            var cleaned = raw
                .replacingOccurrences(of: "\"", with: "")
                .replacingOccurrences(of: "*", with: "")
            while let last = cleaned.last, "?!.,;:".contains(last) {
                cleaned.removeLast()
            }
            let lower = cleaned.lowercased()
            guard !lower.isEmpty else { continue }
            let needsQuotes = lower.unicodeScalars.contains { scalar in
                !CharacterSet.alphanumerics.contains(scalar) && scalar != "_"
            }
            if needsQuotes {
                parts.append("\"\(lower)\"")
            } else {
                parts.append(lower)
            }
        }
        return parts.joined(separator: " ")
    }

    private static func escapeLike(_ raw: String) -> String {
        raw
            .replacingOccurrences(of: "\\", with: "\\\\")
            .replacingOccurrences(of: "%", with: "\\%")
            .replacingOccurrences(of: "_", with: "\\_")
    }

    private static func cleanSnippet(_ raw: String?) -> String? {
        guard let raw else { return nil }
        let collapsed = raw
            .replacingOccurrences(of: "\r\n", with: " ")
            .replacingOccurrences(of: "\n", with: " ")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return collapsed.isEmpty ? nil : collapsed
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
    /// Count the chunks associated with a given document ID.
    public func chunkCount(forDocID docID: String) throws -> Int {
        try dbPool.read { db in
            try Int.fetchOne(db, sql: "SELECT COUNT(*) FROM chunks WHERE doc_id = ?", arguments: [docID]) ?? 0
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

    /// Fetch all tags with their document counts.
    public func allTags() throws -> [(tag: String, count: Int)] {
        try dbPool.read { db in
            let rows = try Row.fetchAll(db, sql: """
                SELECT tag, COUNT(*) as cnt FROM tags GROUP BY tag ORDER BY cnt DESC
            """)
            return rows.map { row in
                (tag: row["tag"] as String? ?? "", count: row["cnt"] as Int? ?? 0)
            }
        }
    }

    /// Fetch documents that have a specific tag.
    public func documentsWithTag(_ tag: String) throws -> [DocumentRecord] {
        try dbPool.read { db in
            try DocumentRecord.fetchAll(db, sql: """
                SELECT d.* FROM documents d
                JOIN tags t ON t.doc_id = d.id
                WHERE t.tag = ?
                ORDER BY d.modified_at DESC
            """, arguments: [tag])
        }
    }

    /// Fetch documents that have ALL of the specified tags (intersection).
    public func documentsWithAllTags(_ tags: [String]) throws -> [DocumentRecord] {
        guard !tags.isEmpty else { return [] }
        let placeholders = tags.map { _ in "?" }.joined(separator: ", ")
        return try dbPool.read { db in
            try DocumentRecord.fetchAll(db, sql: """
                SELECT d.* FROM documents d
                JOIN tags t ON t.doc_id = d.id
                WHERE t.tag IN (\(placeholders))
                GROUP BY d.id
                HAVING COUNT(DISTINCT t.tag) = ?
                ORDER BY d.modified_at DESC
            """, arguments: StatementArguments(tags + [tags.count]))
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

    /// Fetch all unresolved links (source -> raw target name, no matching document).
    public func unresolvedLinks() throws -> [(source: String, rawTarget: String)] {
        try dbPool.read { db in
            let rows = try Row.fetchAll(db, sql: """
                SELECT source_id, target_raw FROM links
                WHERE target_id IS NULL AND resolved = 0
            """)
            return rows.compactMap { row in
                guard let src = row["source_id"] as String?,
                      let raw = row["target_raw"] as String? else { return nil }
                return (source: src, rawTarget: raw)
            }
        }
    }

    /// Tags for a single document.
    public func tagsForDoc(id: String) throws -> [String] {
        try dbPool.read { db in
            try String.fetchAll(db, sql: "SELECT tag FROM tags WHERE doc_id = ? ORDER BY tag", arguments: [id])
        }
    }

    /// Map of doc_id -> [tag] for every document in the vault. One query instead
    /// of N, so a global graph with thousands of nodes doesn't pay per-node
    /// round-trips.
    public func allTagsByDoc() throws -> [String: [String]] {
        try dbPool.read { db in
            let rows = try Row.fetchAll(db, sql: "SELECT doc_id, tag FROM tags ORDER BY doc_id, tag")
            var map: [String: [String]] = [:]
            for row in rows {
                guard let id = row["doc_id"] as String?,
                      let tag = row["tag"] as String? else { continue }
                map[id, default: []].append(tag)
            }
            return map
        }
    }

    /// Total degree (incoming + outgoing resolved links) per document. Nodes
    /// without any resolved links get 0.
    public func docDegrees() throws -> [String: Int] {
        try dbPool.read { db in
            let rows = try Row.fetchAll(db, sql: """
                SELECT doc_id, SUM(cnt) AS degree FROM (
                    SELECT source_id AS doc_id, COUNT(*) AS cnt
                        FROM links WHERE target_id IS NOT NULL GROUP BY source_id
                    UNION ALL
                    SELECT target_id AS doc_id, COUNT(*) AS cnt
                        FROM links WHERE target_id IS NOT NULL GROUP BY target_id
                ) GROUP BY doc_id
            """)
            var out: [String: Int] = [:]
            for row in rows {
                guard let id = row["doc_id"] as String? else { continue }
                out[id] = row["degree"] as Int? ?? 0
            }
            return out
        }
    }

    /// BFS traversal up to `depth` hops from `rootID`, following resolved links
    /// in both directions (undirected). Returns the set of documents reached
    /// plus every resolved link between those documents.
    ///
    /// Cycles are handled by visited-set tracking (LNK-UW-002). A `depth` of 0
    /// returns only the root. `depth` is clamped to 0...10 to prevent runaway
    /// traversals on pathologically connected vaults.
    public func localGraph(rootID: String, depth: Int) throws -> (nodes: [DocumentRecord], edges: [(source: String, target: String)]) {
        let bounded = max(0, min(depth, 10))
        return try dbPool.read { db in
            // BFS in Swift rather than a recursive CTE — the CTE version gets
            // awkward with undirected traversal (need UNION on both sides) and
            // the vault sizes we care about fit in memory comfortably.
            var visited: Set<String> = [rootID]
            var frontier: Set<String> = [rootID]
            for _ in 0..<bounded {
                guard !frontier.isEmpty else { break }
                let placeholders = frontier.map { _ in "?" }.joined(separator: ", ")
                let args = StatementArguments(Array(frontier))
                let rows = try Row.fetchAll(db, sql: """
                    SELECT source_id, target_id FROM links
                    WHERE target_id IS NOT NULL
                      AND (source_id IN (\(placeholders)) OR target_id IN (\(placeholders)))
                """, arguments: args + args)
                var next: Set<String> = []
                for row in rows {
                    if let s = row["source_id"] as String?, !visited.contains(s) {
                        next.insert(s); visited.insert(s)
                    }
                    if let t = row["target_id"] as String?, !visited.contains(t) {
                        next.insert(t); visited.insert(t)
                    }
                }
                frontier = next
            }

            let idPlaceholders = visited.map { _ in "?" }.joined(separator: ", ")
            let idArgs = StatementArguments(Array(visited))
            let docs = try DocumentRecord.fetchAll(db, sql: """
                SELECT * FROM documents WHERE id IN (\(idPlaceholders))
            """, arguments: idArgs)

            let edgeRows = try Row.fetchAll(db, sql: """
                SELECT source_id, target_id FROM links
                WHERE target_id IS NOT NULL
                  AND source_id IN (\(idPlaceholders))
                  AND target_id IN (\(idPlaceholders))
            """, arguments: idArgs + idArgs)
            let edges: [(source: String, target: String)] = edgeRows.compactMap { row in
                guard let s = row["source_id"] as String?,
                      let t = row["target_id"] as String? else { return nil }
                return (source: s, target: t)
            }
            return (nodes: docs, edges: edges)
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

/// Lightweight note item returned by `DatabaseManager.searchNotes(query:limit:)`
/// and disk fallback for the search-first AI Polish picker.
public struct NotePickerItem: Codable, Identifiable, Equatable, Sendable {
    public let id: String
    public let path: String
    public let title: String
    public let docType: String
    public let status: String
    public let modifiedAt: String
    public let snippet: String?
    public let matchSource: String?

    public var displayTitle: String {
        title.isEmpty ? (path as NSString).lastPathComponent : title
    }

    public init(
        id: String,
        path: String,
        title: String,
        docType: String = "",
        status: String = "",
        modifiedAt: String = "",
        snippet: String? = nil,
        matchSource: String? = nil
    ) {
        self.id = id
        self.path = path
        self.title = title
        self.docType = docType
        self.status = status
        self.modifiedAt = modifiedAt
        self.snippet = snippet
        self.matchSource = matchSource
    }
}
