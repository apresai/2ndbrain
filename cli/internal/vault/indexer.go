package vault

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/google/uuid"
)

// ErrUnparseable marks an index failure caused by the NOTE (frontmatter that
// will not parse), rather than by the database or the filesystem. The two are
// handled differently: a bad note is reported and skipped so the rest of the
// vault still builds, while a bad database is a failure the run must surface.
var ErrUnparseable = errors.New("unparseable note")

// UnparseableDoc is one note a pass could not use, with the reason. Err is the
// parser's (or the filesystem's) own message, kept as a string so the value
// survives JSON.
type UnparseableDoc struct {
	Path string `json:"path"`
	Err  string `json:"error"`
}

// MergeUnparseable concatenates per-phase lists, keeping the FIRST entry for
// each path, and never returns nil so a JSON key is always present. A note can
// be reported by the walk and again by the embed pass (the walk keeps an
// unreadable note's row, the embed pass then tries to re-read it), and listing
// it twice would overstate how many notes need fixing. It lives here rather
// than in the CLI so the MCP kb_index result cannot drift from index --json.
func MergeUnparseable(lists ...[]UnparseableDoc) []UnparseableDoc {
	out := []UnparseableDoc{}
	seen := map[string]bool{}
	for _, list := range lists {
		for _, d := range list {
			if seen[d.Path] {
				continue
			}
			seen[d.Path] = true
			out = append(out, d)
		}
	}
	return out
}

type IndexStats struct {
	FilesScanned  int `json:"files_scanned"`
	DocsIndexed   int `json:"docs_indexed"`
	ChunksCreated int `json:"chunks_created"`
	LinksFound    int `json:"links_found"`
	Errors        int `json:"errors"`
	// Unparseable names the notes whose CONTENT would not parse. They are also
	// counted in Errors: the run genuinely failed to index them, and redefining
	// Errors to exclude them would silently change what a consumer of that
	// number is being told.
	Unparseable []UnparseableDoc `json:"unparseable"`
	// Unreadable names the notes that could not be READ this run. They are
	// counted in Errors for the same reason, but they are NOT unparseable:
	// nothing is known about their contents, so each keeps whatever index row
	// it already had.
	Unreadable []UnparseableDoc `json:"unreadable"`
	// ExcludedPurged counts index rows deleted because their note sits under a
	// folder that is now excluded from indexing.
	ExcludedPurged int `json:"excluded_purged"`
}

// IndexVault walks all markdown files and indexes them into the database.
func IndexVault(v *Vault, onProgress func(path string)) (*IndexStats, error) {
	// Never nil: these lists are part of the `index --json` contract, where a
	// key that disappears when it is empty forces every consumer to special-case
	// its absence.
	stats := &IndexStats{Unparseable: []UnparseableDoc{}, Unreadable: []UnparseableDoc{}}
	excluded := ObsidianTemplateFolders(v.Root)
	if len(excluded) > 0 {
		slog.Debug("excluding obsidian template folders from the index", "folders", excluded)
	}

	// Purge rows the vault no longer indexes: notes whose file is gone, and
	// notes now sitting under an excluded folder. Both run BEFORE the walk, so a
	// row the walk will never visit again cannot survive by simply not being
	// looked at. A failure here shouldn't abort the index (partial purge is
	// better than no index), but the caller should see the error if the initial
	// SELECT failed.
	purgedExcluded, err := purgeStale(v, excluded)
	if err != nil {
		return nil, fmt.Errorf("purge stale: %w", err)
	}
	stats.ExcludedPurged = purgedExcluded

	err = filepath.Walk(v.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}

		// Skip directories
		if info.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" {
				return filepath.SkipDir
			}
			// Obsidian's template folders are skipped whole, the same way
			// .obsidian and .2ndbrain are: their contents are placeholder
			// syntax, not notes.
			if IsExcludedFolderPath(v.RelPath(path), excluded) {
				return filepath.SkipDir
			}
			return nil
		}

		// Only markdown, canvas, or base files
		lower := strings.ToLower(path)
		if !strings.HasSuffix(lower, ".md") && !strings.HasSuffix(lower, ".canvas") && !strings.HasSuffix(lower, ".base") {
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

		if err := store.RetryBusy(func() error { return indexFile(v.DB, path, relPath) }); err != nil {
			stats.Errors++
			switch {
			case errors.Is(err, document.ErrRead):
				// The file could not be READ. Nothing is known about its
				// contents, so whatever row it already has is still the best
				// answer available and stays untouched. A permission bit or a
				// file locked mid-save used to be classified as unparseable and
				// cost the note its row, chunks and embeddings.
				slog.Warn("index could not read a note", "path", relPath, "err", err)
				stats.Unreadable = append(stats.Unreadable, UnparseableDoc{Path: relPath, Err: err.Error()})
			case errors.Is(err, ErrUnparseable):
				// Reported once per RUN below, not once per file: a vault with
				// a handful of unreadable notes produced the same three
				// warnings on every single index, which trains the reader to
				// ignore the channel real failures also use.
				slog.Info("index skipped an unparseable note", "path", relPath, "err", err)
				stats.Unparseable = append(stats.Unparseable, UnparseableDoc{Path: relPath, Err: err.Error()})
				dropUnparseableRow(v, relPath)
			default:
				slog.Warn("index file failed", "path", relPath, "err", err)
				fmt.Fprintf(os.Stderr, "warning: index %s: %v\n", relPath, err)
			}
			return nil
		}

		stats.DocsIndexed++
		return nil
	})

	if err != nil {
		return stats, fmt.Errorf("walk vault: %w", err)
	}

	// One summary per run. The paths are already at INFO above, so -v still
	// names every file.
	// Logged once per run, never printed here: the CLI owns the human summary
	// (it is the only caller that knows the EMBED phase's list too, and two
	// writers produced one line that told the reader to run -v beside a
	// complete list of the same notes).
	if n := len(stats.Unparseable); n > 0 {
		slog.Warn("notes could not be parsed", "count", n)
	}
	if n := len(stats.Unreadable); n > 0 {
		slog.Warn("notes could not be read", "count", n)
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
	// Same exclusion as the walk, so the macOS app's per-save reindex cannot
	// put back what the full index deliberately leaves out. Name the folder:
	// "not indexed" is not actionable without knowing which setting did it.
	if folder, ok := ExcludedFolderFor(relPath, ObsidianTemplateFolders(v.Root)); ok {
		return fmt.Errorf("%s is inside the Obsidian template folder %q, which is not indexed", relPath, folder)
	}
	if err := store.RetryBusy(func() error { return indexFile(v.DB, absPath, relPath) }); err != nil {
		return fmt.Errorf("index file: %w", err)
	}
	// Re-resolve wikilinks so any new [[targets]] in this doc get linked
	// against the rest of the vault. This is a cheap SQL-only operation.
	if err := v.DB.ResolveLinks(); err != nil {
		return fmt.Errorf("resolve links: %w", err)
	}
	return nil
}

// dropUnparseableRow removes the index row of a note that used to parse and no
// longer does. Without this the last good version keeps answering searches from
// the index (its chunks, links, tags and chunk vectors all survive) while the
// file on disk says something else, and it keeps entering the embed work set on
// every run only to fail there too. Best-effort: a note that was never indexed
// has no row, and a delete failure is logged, never fatal.
func dropUnparseableRow(v *Vault, relPath string) {
	var id string
	if err := v.DB.Conn().QueryRow("SELECT id FROM documents WHERE path = ?", relPath).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Never indexed, so there is nothing to drop. Not a failure.
			return
		}
		// Anything else means the drop did not happen and nobody was told: the
		// adjacent delete failure has always logged, this one used to be
		// swallowed, so a stale row could outlive its note in silence.
		slog.Warn("look up index row for unparseable note failed", "path", relPath, "err", err)
		return
	}
	if err := v.DB.DeleteDocument(id); err != nil {
		slog.Warn("drop index row for unparseable note failed", "path", relPath, "err", err)
		return
	}
	slog.Info("dropped index row for unparseable note", "path", relPath)
}

func indexFile(db *store.DB, absPath, relPath string) error {
	doc, err := document.ParseFile(absPath)
	if err != nil {
		// Classify at the source: everything below this point is a database
		// failure, and only the caller can tell the three apart. A READ failure
		// passes through unwrapped, because it is not the note's fault and must
		// not be treated as one; only a parse failure is unparseable.
		if errors.Is(err, document.ErrRead) {
			return err
		}
		return fmt.Errorf("%w: %w", ErrUnparseable, err)
	}
	doc.Path = relPath
	doc.ComputeContentHash()

	// Ensure the document has an ID: reuse the surrogate one this path already
	// carries, and mint a new one only when the path has genuinely never been
	// indexed.
	//
	// Only sql.ErrNoRows means "never indexed". Treating EVERY lookup error as
	// that (the previous `if err == nil { ... } else { new uuid }`) mints a
	// fresh id for a note that already has a row, and the upsert then fails on
	// the documents.path UNIQUE constraint with a message that names a
	// constraint rather than the contention that caused it. A transient
	// SQLITE_BUSY is exactly that case, and it is reachable: an app or plugin
	// holding a read while the CLI reindexes one note is the normal state of
	// this product. Returning the error instead lets RetryBusy (which already
	// wraps this whole function, and matches through wrapping) retry and
	// succeed. dropUnparseableRow, ten lines up, already made this
	// distinction; this site had not.
	if doc.ID == "" {
		var existingID string
		err := db.Conn().QueryRow("SELECT id FROM documents WHERE path = ?", relPath).Scan(&existingID)
		switch {
		case err == nil:
			doc.ID = existingID
		case errors.Is(err, sql.ErrNoRows):
			doc.ID = uuid.New().String()
		default:
			return fmt.Errorf("look up the index row for %s: %w", relPath, err)
		}
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

	// ChunkForStorage sub-splits any oversized heading section so a chunk can't
	// exceed Nova's embed limits; the vec search joins vec_chunks -> chunks by
	// chunk_id, so this MUST match embed.Document's chunk set exactly.
	chunks := document.ChunkForStorage(doc)
	if err := db.UpsertChunksTx(tx, chunks); err != nil {
		return fmt.Errorf("upsert chunks: %w", err)
	}

	// Index against the comment-stripped body so %% comments %% never
	// contribute tags or links.
	indexBody := doc.IndexableBody()

	// Tags: frontmatter tags merged with inline body #tags (deduped,
	// frontmatter first).
	tags := mergeTags(doc.Tags, document.ExtractInlineTags(indexBody))
	if err := db.UpsertTagsTx(tx, doc.ID, tags); err != nil {
		return fmt.Errorf("upsert tags: %w", err)
	}

	// Aliases
	aliases := document.ExtractAliases(doc.Frontmatter)
	if err := db.UpsertAliasesTx(tx, doc.ID, aliases); err != nil {
		return fmt.Errorf("upsert aliases: %w", err)
	}

	// Links
	links := document.ExtractWikiLinks(indexBody)
	if err := db.UpsertLinksTx(tx, doc.ID, links); err != nil {
		return fmt.Errorf("upsert links: %w", err)
	}

	return tx.Commit()
}

// mergeTags concatenates frontmatter and inline tags, preserving order
// (frontmatter first) and dropping duplicates.
func mergeTags(frontmatter, inline []string) []string {
	merged := make([]string, 0, len(frontmatter)+len(inline))
	seen := make(map[string]bool, len(frontmatter)+len(inline))
	for _, t := range frontmatter {
		if !seen[t] {
			seen[t] = true
			merged = append(merged, t)
		}
	}
	for _, t := range inline {
		if !seen[t] {
			seen[t] = true
			merged = append(merged, t)
		}
	}
	return merged
}

// purgeStale removes index entries for files that no longer exist on disk.
// Returns an error only when the initial SELECT fails (the index state is
// unknown at that point); per-document scan or delete errors are logged and
// skipped so a single bad row doesn't block the pass.
func purgeStale(v *Vault, excluded []string) (int, error) {
	rows, err := v.DB.Conn().Query("SELECT id, path FROM documents")
	if err != nil {
		return 0, fmt.Errorf("query documents: %w", err)
	}
	defer rows.Close()

	var stale []string
	purgedExcluded := 0
	for rows.Next() {
		var id, path string
		if err := rows.Scan(&id, &path); err != nil {
			slog.Warn("purge stale scan failed", "err", err)
			fmt.Fprintf(os.Stderr, "warning: purgeStale scan: %v\n", err)
			continue
		}
		// A note that is now under an excluded folder is stale for the same
		// reason a deleted one is: the walk will never visit it again, so its
		// row, chunks and vectors would answer searches forever. Exclusion is
		// about LOCATION, so this applies whether or not the file still reads.
		if IsExcludedFolderPath(path, excluded) {
			stale = append(stale, id)
			purgedExcluded++
			slog.Info("purging index row under an excluded folder", "path", path)
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
	if purgedExcluded > 0 {
		fmt.Fprintf(os.Stderr, "  %d note(s) under an excluded folder removed from the index\n", purgedExcluded)
	}
	return purgedExcluded, nil
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
