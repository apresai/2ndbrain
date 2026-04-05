package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/apresai/2ndbrain/internal/document"
)

func (db *DB) UpsertDocument(doc *document.Document) error {
	fm, err := json.Marshal(doc.Frontmatter)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	_, err = db.conn.Exec(`
		INSERT INTO documents (id, path, title, doc_type, status, created_at, modified_at, indexed_at, content_hash, frontmatter)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			path = excluded.path,
			title = excluded.title,
			doc_type = excluded.doc_type,
			status = excluded.status,
			modified_at = excluded.modified_at,
			indexed_at = excluded.indexed_at,
			content_hash = excluded.content_hash,
			frontmatter = excluded.frontmatter
	`, doc.ID, doc.Path, doc.Title, doc.Type, doc.Status,
		doc.CreatedAt, doc.ModifiedAt, now, doc.ContentHash, string(fm))

	return err
}

func (db *DB) UpsertChunks(chunks []document.Chunk) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO chunks (id, doc_id, heading_path, level, content, content_hash, start_line, end_line, sort_order)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			content_hash = excluded.content_hash,
			start_line = excluded.start_line,
			end_line = excluded.end_line,
			sort_order = excluded.sort_order
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(c.ID, c.DocID, c.HeadingPath, c.Level, c.Content, c.ContentHash, c.StartLine, c.EndLine, c.SortOrder); err != nil {
			return fmt.Errorf("upsert chunk %s: %w", c.ID, err)
		}
	}

	return tx.Commit()
}

func (db *DB) UpsertTags(docID string, tags []string) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM tags WHERE doc_id = ?", docID); err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO tags (doc_id, tag) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, tag := range tags {
		if _, err := stmt.Exec(docID, tag); err != nil {
			return fmt.Errorf("insert tag %s: %w", tag, err)
		}
	}

	return tx.Commit()
}

func (db *DB) UpsertLinks(docID string, links []document.WikiLink) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM links WHERE source_id = ?", docID); err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO links (source_id, target_raw, heading, alias) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, link := range links {
		if _, err := stmt.Exec(docID, link.Target, link.Heading, link.Alias); err != nil {
			return fmt.Errorf("insert link to %s: %w", link.Target, err)
		}
	}

	return tx.Commit()
}

func (db *DB) GetDocumentByPath(path string) (*document.Document, error) {
	row := db.conn.QueryRow(`
		SELECT id, path, title, doc_type, status, created_at, modified_at, frontmatter
		FROM documents WHERE path = ?
	`, path)

	var doc document.Document
	var fmJSON string
	err := row.Scan(&doc.ID, &doc.Path, &doc.Title, &doc.Type, &doc.Status,
		&doc.CreatedAt, &doc.ModifiedAt, &fmJSON)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(fmJSON), &doc.Frontmatter); err != nil {
		doc.Frontmatter = make(map[string]any)
	}

	return &doc, nil
}

// ResolveLinks matches unresolved wikilinks to existing documents by path or title.
func (db *DB) ResolveLinks() error {
	_, err := db.conn.Exec(`
		UPDATE links SET target_id = d.id, resolved = 1
		FROM documents d
		WHERE (links.target_raw = replace(d.path, '.md', '')
		   OR links.target_raw = d.title
		   OR links.target_raw = d.path)
		AND links.resolved = 0
	`)
	return err
}

// FindByContentHash returns the path of an existing document with the same content hash,
// excluding the document with excludeID. Returns empty string if no duplicate found.
func (db *DB) FindByContentHash(hash string, excludeID string) (string, error) {
	if hash == "" {
		return "", nil
	}
	var path string
	err := db.conn.QueryRow(
		`SELECT path FROM documents WHERE content_hash = ? AND id != ? LIMIT 1`,
		hash, excludeID,
	).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("check content hash: %w", err)
	}
	return path, nil
}

func (db *DB) DeleteDocument(docID string) error {
	_, err := db.conn.Exec("DELETE FROM documents WHERE id = ?", docID)
	return err
}

func (db *DB) DeleteChunksByDoc(docID string) error {
	_, err := db.conn.Exec("DELETE FROM chunks WHERE doc_id = ?", docID)
	return err
}
