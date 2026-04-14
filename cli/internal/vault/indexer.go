package vault

import (
	"fmt"
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

	// Purge documents whose files no longer exist on disk
	purgeStale(v)

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
		fmt.Fprintf(os.Stderr, "warning: resolve links: %v\n", err)
	}

	// Count chunks and links
	stats.ChunksCreated = countRows(v.DB, "chunks")
	stats.LinksFound = countRows(v.DB, "links")

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

	// Upsert document
	if err := db.UpsertDocument(doc); err != nil {
		return fmt.Errorf("upsert document: %w", err)
	}

	// Delete old chunks for this doc then insert new ones
	if err := db.DeleteChunksByDoc(doc.ID); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}

	chunks := document.ChunkDocument(doc)
	if err := db.UpsertChunks(chunks); err != nil {
		return fmt.Errorf("upsert chunks: %w", err)
	}

	// Tags
	if err := db.UpsertTags(doc.ID, doc.Tags); err != nil {
		return fmt.Errorf("upsert tags: %w", err)
	}

	// Links
	links := document.ExtractWikiLinks(doc.Body)
	if err := db.UpsertLinks(doc.ID, links); err != nil {
		return fmt.Errorf("upsert links: %w", err)
	}

	return nil
}

// purgeStale removes index entries for files that no longer exist on disk.
func purgeStale(v *Vault) {
	rows, err := v.DB.Conn().Query("SELECT id, path FROM documents")
	if err != nil {
		return
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			continue
		}
		absPath := filepath.Join(v.Root, path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			stale = append(stale, id)
		}
	}

	for _, id := range stale {
		v.DB.DeleteDocument(id)
		fmt.Fprintf(os.Stderr, "  purged stale: %s\n", id)
	}
}

func countRows(db *store.DB, table string) int {
	var count int
	row := db.Conn().QueryRow("SELECT COUNT(*) FROM " + table)
	row.Scan(&count)
	return count
}
