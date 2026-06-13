package store

import (
	"fmt"
	"path/filepath"
	"sort"
)

// TagCount is one tag with the number of documents that carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// AliasRef maps a frontmatter alias to the document that declares it.
type AliasRef struct {
	Alias string `json:"alias"`
	Path  string `json:"path"`
	Title string `json:"title"`
}

// FolderCount is a directory prefix of documents.path with its document count.
// Root-level documents are bucketed under the literal label "(root)".
type FolderCount struct {
	Folder string `json:"folder"`
	Count  int    `json:"count"`
}

// rootFolderLabel is the bucket name for documents that live directly at the
// vault root (no directory prefix).
const rootFolderLabel = "(root)"

// TagCounts returns every tag in the vault with its document count, ordered by
// descending count then tag name. It groups over the tags table (tags(doc_id,
// tag)).
func (db *DB) TagCounts() ([]TagCount, error) {
	rows, err := db.conn.Query(`
		SELECT tag, COUNT(DISTINCT doc_id) AS n
		FROM tags
		GROUP BY tag
		ORDER BY n DESC, tag ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query tag counts: %w", err)
	}
	defer rows.Close()

	var out []TagCount
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, fmt.Errorf("scan tag count: %w", err)
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// AllAliases returns every frontmatter alias in the vault joined to the
// document that declares it (alias -> path/title), ordered by alias. It reads
// the aliases table (aliases(doc_id, alias)) joined to documents.
func (db *DB) AllAliases() ([]AliasRef, error) {
	rows, err := db.conn.Query(`
		SELECT a.alias, d.path, d.title
		FROM aliases a
		JOIN documents d ON d.id = a.doc_id
		ORDER BY a.alias ASC, d.path ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query aliases: %w", err)
	}
	defer rows.Close()

	var out []AliasRef
	for rows.Next() {
		var ar AliasRef
		if err := rows.Scan(&ar.Alias, &ar.Path, &ar.Title); err != nil {
			return nil, fmt.Errorf("scan alias: %w", err)
		}
		out = append(out, ar)
	}
	return out, rows.Err()
}

// FolderCounts returns the directory prefix of every document's path with the
// number of documents directly in it, ordered by folder name. Documents at the
// vault root (no directory component) are bucketed under "(root)". Folding is
// done in Go via filepath.Dir so a document is counted under its immediate
// parent directory only, matching how a file browser would show counts.
func (db *DB) FolderCounts() ([]FolderCount, error) {
	rows, err := db.conn.Query(`SELECT path FROM documents`)
	if err != nil {
		return nil, fmt.Errorf("query document paths: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan document path: %w", err)
		}
		dir := filepath.Dir(p)
		if dir == "." || dir == "" {
			dir = rootFolderLabel
		} else {
			// Normalize to forward slashes so output is stable across OSes.
			dir = filepath.ToSlash(dir)
		}
		counts[dir]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]FolderCount, 0, len(counts))
	for folder, n := range counts {
		out = append(out, FolderCount{Folder: folder, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Folder < out[j].Folder
	})
	return out, nil
}
