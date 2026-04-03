package store

const schemaV1 = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS documents (
    id          TEXT PRIMARY KEY,
    path        TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL DEFAULT '',
    doc_type    TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT '',
    modified_at TEXT NOT NULL DEFAULT '',
    indexed_at  TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL DEFAULT '',
    frontmatter TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_documents_path ON documents(path);
CREATE INDEX IF NOT EXISTS idx_documents_type ON documents(doc_type);
CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(status);
CREATE INDEX IF NOT EXISTS idx_documents_modified ON documents(modified_at);

CREATE TABLE IF NOT EXISTS chunks (
    id           TEXT PRIMARY KEY,
    doc_id       TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    heading_path TEXT NOT NULL,
    level        INTEGER NOT NULL DEFAULT 0,
    content      TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    start_line   INTEGER NOT NULL DEFAULT 0,
    end_line     INTEGER NOT NULL DEFAULT 0,
    sort_order   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_chunks_doc ON chunks(doc_id);

CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    content,
    heading_path,
    content='chunks',
    content_rowid='rowid',
    tokenize='porter unicode61'
);

CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, content, heading_path)
    VALUES (new.rowid, new.content, new.heading_path);
END;

CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content, heading_path)
    VALUES ('delete', old.rowid, old.content, old.heading_path);
END;

CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, content, heading_path)
    VALUES ('delete', old.rowid, old.content, old.heading_path);
    INSERT INTO chunks_fts(rowid, content, heading_path)
    VALUES (new.rowid, new.content, new.heading_path);
END;

CREATE TABLE IF NOT EXISTS links (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id   TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    target_id   TEXT,
    target_raw  TEXT NOT NULL,
    heading     TEXT NOT NULL DEFAULT '',
    alias       TEXT NOT NULL DEFAULT '',
    resolved    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_links_source ON links(source_id);
CREATE INDEX IF NOT EXISTS idx_links_target ON links(target_id);

CREATE TABLE IF NOT EXISTS tags (
    doc_id TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    tag    TEXT NOT NULL,
    PRIMARY KEY (doc_id, tag)
);

CREATE INDEX IF NOT EXISTS idx_tags_tag ON tags(tag);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

INSERT OR IGNORE INTO schema_version (version) VALUES (1);
`
