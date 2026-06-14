package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// nullIfEmpty returns nil for an empty string so an optional TEXT column stores
// SQL NULL rather than "", keeping "block_id IS NOT NULL" filters meaningful.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (db *DB) UpsertChunks(chunks []document.Chunk) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO chunks (id, doc_id, heading_path, level, content, content_hash, start_line, end_line, sort_order, block_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			content_hash = excluded.content_hash,
			start_line = excluded.start_line,
			end_line = excluded.end_line,
			sort_order = excluded.sort_order,
			block_id = excluded.block_id
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(c.ID, c.DocID, c.HeadingPath, c.Level, c.Content, c.ContentHash, c.StartLine, c.EndLine, c.SortOrder, nullIfEmpty(c.BlockID)); err != nil {
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

	stmt, err := tx.Prepare("INSERT INTO links (source_id, target_raw, heading, alias, block_id) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, link := range links {
		if _, err := stmt.Exec(docID, link.Target, link.Heading, link.Alias, nullIfEmpty(link.Block)); err != nil {
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

// ResolveLinks matches unresolved wikilinks to existing documents by path,
// title, or alias, using a shortest-unique-path disambiguation algorithm.
func (db *DB) ResolveLinks() error {
	// 1-3. Fetch documents + aliases and build the shared lookup index
	// (exact paths, shortest-unique-suffix nameIndex, titles, aliases). This is
	// the same index ResolveTarget uses, so wikilink resolution and CLI target
	// resolution can never drift apart. O(links + paths) overall.
	docs, err := db.fetchDocInfos()
	if err != nil {
		return fmt.Errorf("resolve links: %w", err)
	}
	aliases, err := db.fetchAliases()
	if err != nil {
		return fmt.Errorf("resolve links: %w", err)
	}
	idx := buildLookupIndex(docs, aliases)

	// 4. Fetch all links
	linkRows, err := db.conn.Query("SELECT id, target_raw FROM links")
	if err != nil {
		return fmt.Errorf("resolve links query links: %w", err)
	}
	defer linkRows.Close()

	type linkItem struct {
		id        int
		targetRaw string
	}
	var links []linkItem
	for linkRows.Next() {
		var l linkItem
		if err := linkRows.Scan(&l.id, &l.targetRaw); err != nil {
			return err
		}
		links = append(links, l)
	}
	if err := linkRows.Err(); err != nil {
		return fmt.Errorf("resolve links iterate links: %w", err)
	}

	// 5. Resolve each link
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	setStmt, err := tx.Prepare("UPDATE links SET target_id = ?, resolved = 1 WHERE id = ?")
	if err != nil {
		return err
	}
	defer setStmt.Close()

	// Unresolved links are still marked resolved=1 with target_id=NULL so a
	// later pass doesn't keep re-scanning them; graph/retrieval/lint consumers
	// all gate on target_id IS NOT NULL, so this is safe.
	clearStmt, err := tx.Prepare("UPDATE links SET target_id = NULL, resolved = 1 WHERE id = ?")
	if err != nil {
		return err
	}
	defer clearStmt.Close()

	for _, l := range links {
		// Normalize path slashes and strip any leading slash. The heading/block
		// anchor was already split off by document.ExtractWikiLinks.
		target := strings.ReplaceAll(l.targetRaw, "\\", "/")
		target = strings.TrimPrefix(target, "/")

		var resolvedID string

		// A. Exact full-path match (path is unique).
		if id, ok := idx.exactPaths[target]; ok {
			resolvedID = id
		} else if id, ok := idx.exactPaths[target+".md"]; ok {
			resolvedID = id
		}

		// B. Shortest-unique-name match (path suffix or basename).
		if resolvedID == "" {
			if id, ok := idx.uniqueDocID(target); ok {
				resolvedID = id
			} else if id, ok := idx.uniqueDocID(target + ".md"); ok {
				resolvedID = id
			}
		}

		// C. Title match.
		if resolvedID == "" {
			if ids, ok := idx.titles[target]; ok && len(ids) == 1 {
				resolvedID = ids[0]
			}
		}

		// D. Alias match.
		if resolvedID == "" {
			if ids, ok := idx.aliases[target]; ok && len(ids) == 1 {
				resolvedID = ids[0]
			}
		}

		if resolvedID != "" {
			if _, err := setStmt.Exec(resolvedID, l.id); err != nil {
				return fmt.Errorf("update link target: %w", err)
			}
		} else if _, err := clearStmt.Exec(l.id); err != nil {
			return fmt.Errorf("clear link target: %w", err)
		}
	}

	return tx.Commit()
}

type docInfo struct {
	id    string
	path  string
	title string
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

// UpsertDocumentTx is the transaction-aware variant of UpsertDocument.
func (db *DB) UpsertDocumentTx(tx *sql.Tx, doc *document.Document) error {
	fm, err := json.Marshal(doc.Frontmatter)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	_, err = tx.Exec(`
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

// UpsertChunksTx inserts chunks within an existing transaction.
func (db *DB) UpsertChunksTx(tx *sql.Tx, chunks []document.Chunk) error {
	stmt, err := tx.Prepare(`
		INSERT INTO chunks (id, doc_id, heading_path, level, content, content_hash, start_line, end_line, sort_order, block_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			content_hash = excluded.content_hash,
			start_line = excluded.start_line,
			end_line = excluded.end_line,
			sort_order = excluded.sort_order,
			block_id = excluded.block_id
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(c.ID, c.DocID, c.HeadingPath, c.Level, c.Content, c.ContentHash, c.StartLine, c.EndLine, c.SortOrder, nullIfEmpty(c.BlockID)); err != nil {
			return fmt.Errorf("upsert chunk %s: %w", c.ID, err)
		}
	}
	return nil
}

// UpsertTagsTx replaces tags for a doc within an existing transaction.
func (db *DB) UpsertTagsTx(tx *sql.Tx, docID string, tags []string) error {
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
	return nil
}

// UpsertAliasesTx replaces aliases for a doc within an existing transaction.
func (db *DB) UpsertAliasesTx(tx *sql.Tx, docID string, aliases []string) error {
	if _, err := tx.Exec("DELETE FROM aliases WHERE doc_id = ?", docID); err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO aliases (doc_id, alias) VALUES (?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, alias := range aliases {
		if _, err := stmt.Exec(docID, alias); err != nil {
			return fmt.Errorf("insert alias %s: %w", alias, err)
		}
	}
	return nil
}

// UpsertLinksTx replaces links for a doc within an existing transaction.
func (db *DB) UpsertLinksTx(tx *sql.Tx, docID string, links []document.WikiLink) error {
	if _, err := tx.Exec("DELETE FROM links WHERE source_id = ?", docID); err != nil {
		return err
	}

	stmt, err := tx.Prepare("INSERT INTO links (source_id, target_raw, heading, alias, block_id) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, link := range links {
		if _, err := stmt.Exec(docID, link.Target, link.Heading, link.Alias, nullIfEmpty(link.Block)); err != nil {
			return fmt.Errorf("insert link to %s: %w", link.Target, err)
		}
	}
	return nil
}
