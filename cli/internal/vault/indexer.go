package vault

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
)

type IndexStats struct {
	FilesScanned  int `json:"files_scanned"`
	DocsIndexed   int `json:"docs_indexed"`
	ChunksCreated int `json:"chunks_created"`
	LinksFound    int `json:"links_found"`
	Errors        int `json:"errors"`
}

// IndexVault walks all markdown files and indexes them into the database.
func IndexVault(v *Vault, onProgress func(path string)) (*IndexStats, error) {
	stats := &IndexStats{}

	// Purge documents whose files no longer exist on disk. A failure here
	// shouldn't abort the index (partial purge is better than no index),
	// but the caller should see the error if the initial SELECT failed.
	if err := purgeStale(v); err != nil {
		return nil, fmt.Errorf("purge stale: %w", err)
	}

	err := filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}

		// Skip directories
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only markdown files
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}

		relPath := v.RelPath(path)
		if IsIgnored(relPath) {
			return nil
		}

		stats.FilesScanned++
		if onProgress != nil {
			onProgress(relPath)
		}

		if err := indexFile(v.DB, path, relPath); err != nil {
			stats.Errors++
			slog.Warn("index file failed", "path", relPath, "err", err)
			fmt.Fprintf(os.Stderr, "warning: index %s: %v\n", relPath, err)
			return nil
		}

		stats.DocsIndexed++
		return nil
	})

	if err != nil {
		return stats, fmt.Errorf("walk vault: %w", err)
	}

	// Resolve wikilinks now that all documents are indexed
	if err := v.DB.ResolveLinks(); err != nil {
		slog.Warn("resolve links failed", "err", err)
		fmt.Fprintf(os.Stderr, "warning: resolve links: %v\n", err)
	}

	// Count chunks and links
	if stats.ChunksCreated, err = countRows(v.DB, "chunks"); err != nil {
		return stats, err
	}
	if stats.LinksFound, err = countRows(v.DB, "links"); err != nil {
		return stats, err
	}

	return stats, nil
}

// IndexSingleFile indexes one markdown file (upsert document, chunks, tags,
// links) and re-resolves the global link table. Use this for incremental
// editor saves instead of rebuilding the whole vault.
func IndexSingleFile(v *Vault, absPath string) error {
	relPath := v.RelPath(absPath)
	if IsIgnored(relPath) {
		return fmt.Errorf("path is ignored: %s", relPath)
	}
	if err := indexFile(v.DB, absPath, relPath); err != nil {
		return fmt.Errorf("index file: %w", err)
	}
	// Re-resolve wikilinks so any new [[targets]] in this doc get linked
	// against the rest of the vault. This is a cheap SQL-only operation.
	if err := v.DB.ResolveLinks(); err != nil {
		return fmt.Errorf("resolve links: %w", err)
	}
	return nil
}

func indexFile(db *store.DB, absPath, relPath string) error {
	doc, err := document.ParseFile(absPath)
	if err != nil {
		return err
	}
	doc.Path = relPath
	doc.ComputeContentHash()

	// Ensure document has an ID
	if doc.ID == "" {
		return fmt.Errorf("document %s has no id in frontmatter", relPath)
	}

	// Wrap all DB operations in a single transaction so a partial
	// failure (e.g., kill mid-index) doesn't leave orphaned chunks,
	// stale FTS5 entries, or inconsistent tags/links.
	tx, err := db.Conn().Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Upsert document
	if err := db.UpsertDocumentTx(tx, doc); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	// Delete old chunks for this doc then insert new ones
	if _, err := tx.Exec("DELETE FROM chunks WHERE doc_id = ?", doc.ID); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	chunks := document.ChunkDocument(doc)
	if err := db.UpsertChunksTx(tx, chunks); err != nil {
		return fmt.Errorf("upsert chunks: %w", err)
	}

	// Tags
	if err := db.UpsertTagsTx(tx, doc.ID, doc.Tags); err != nil {
		return fmt.Errorf("upsert tags: %w", err)
	}

	// Links
	links := document.ExtractWikiLinks(doc.Body)
	if err := db.UpsertLinksTx(tx, doc.ID, links); err != nil {
		return fmt.Errorf("upsert links: %w", err)
	}

	return tx.Commit()
}

// purgeStale removes index entries for files that no longer exist on disk.
// Returns an error only when the initial SELECT fails (the index state is
// unknown at that point); per-document scan or delete errors are logged and
// skipped so a single bad row doesn't block the pass.
func purgeStale(v *Vault) error {
	rows, err := v.DB.Conn().Query("SELECT id, path FROM documents")
	if err != nil {
		return fmt.Errorf("query documents: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			slog.Warn("purge stale scan failed", "err", err)
			fmt.Fprintf(os.Stderr, "warning: purgeStale scan: %v\n", err)
			continue
		}
		absPath := filepath.Join(v.Root, path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		// Cursor aborted mid-scan; the stale list is partial. Warn but
		// proceed with what we have so we don't leave the user stuck.
		slog.Warn("purge stale iteration incomplete", "err", err)
		fmt.Fprintf(os.Stderr, "warning: purgeStale iteration incomplete: %v\n", err)
	}

	for _, id := range stale {
		if err := v.DB.DeleteDocument(id); err != nil {
			slog.Warn("purge stale delete failed", "id", id, "err", err)
			fmt.Fprintf(os.Stderr, "warning: purgeStale delete %s: %v\n", id, err)
			continue
		}
		slog.Info("purged stale document", "id", id)
		fmt.Fprintf(os.Stderr, "  purged stale: %s\n", id)
	}
	return nil
}

// countRows returns the row count for a known table. The table parameter
// must be one of the whitelisted names to prevent SQL injection.
func countRows(db *store.DB, table string) (int, error) {
	allowed := map[string]bool{"chunks": true, "links": true, "documents": true, "tags": true}
	if !allowed[table] {
		return 0, fmt.Errorf("count rows: table %q is not allowed", table)
	}
	var count int
	row := db.Conn().QueryRow("SELECT COUNT(*) FROM " + table)
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count rows in %s: %w", table, err)
	}
	return count, nil
}
