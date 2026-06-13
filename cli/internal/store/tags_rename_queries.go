package store

import "fmt"

// DocsWithTag returns every document that carries the given tag, as a minimal
// (path, title) reference, ordered by path. It joins the tags table (tags(doc_id,
// tag)) to documents so the caller can load each affected file and rewrite its
// frontmatter. The tags table holds both frontmatter tags and inline body #tags,
// so a doc surfaces here if either source carries the tag; `tags rename` is
// frontmatter-only and skips any doc whose tag lives only inline.
func (db *DB) DocsWithTag(tag string) ([]DocRef, error) {
	rows, err := db.conn.Query(`
		SELECT d.path, d.title
		FROM tags t
		JOIN documents d ON d.id = t.doc_id
		WHERE t.tag = ?
		ORDER BY d.path
	`, tag)
	if err != nil {
		return nil, fmt.Errorf("query docs with tag: %w", err)
	}
	defer rows.Close()

	return scanDocRefs(rows)
}
