package store

import (
	"fmt"
	"strings"
)

// LinksByRawName returns one LinkRef per UNRESOLVED link whose raw target
// (target_raw, with any #heading/#^block/|alias already split off at index time)
// names the document at oldPath, by any of the resolvable forms: the full
// vault-relative path, the bare basename, or a "/"-delimited path suffix, each
// matched with and without the ".md" extension and slash-normalized.
//
// This is the companion to Backlinks for a link-aware move. Backlinks returns
// only RESOLVED inbound links (target_id IS NOT NULL); a link whose target_raw
// matches the moved doc's name but never resolved (target_id IS NULL) is invisible
// to it. Those broken-by-name links still need rewriting on a move so they keep
// pointing at the note after it changes path, so this query surfaces exactly the
// unresolved ones. Path and Title name the SOURCE document holding the link;
// Resolved is always false here.
//
// Matching is done in Go over the link rows rather than in SQL so the same
// normalization/suffix logic used elsewhere (normalize slashes, strip .md, every
// path suffix) stays in one place and SQL stays injection-free.
func (db *DB) LinksByRawName(oldPath string) ([]LinkRef, error) {
	forms := rawNameForms(oldPath)
	if len(forms) == 0 {
		return nil, nil
	}

	rows, err := db.conn.Query(`
		SELECT d.path, d.title, l.heading, l.alias, l.target_raw
		FROM links l
		JOIN documents d ON d.id = l.source_id
		WHERE l.target_id IS NULL
		ORDER BY d.path, l.target_raw
	`)
	if err != nil {
		return nil, fmt.Errorf("query unresolved links: %w", err)
	}
	defer rows.Close()

	var refs []LinkRef
	for rows.Next() {
		var r LinkRef
		if err := rows.Scan(&r.Path, &r.Title, &r.Heading, &r.Alias, &r.TargetRaw); err != nil {
			return nil, fmt.Errorf("scan unresolved link: %w", err)
		}
		if _, ok := forms[normalizeRawName(r.TargetRaw)]; ok {
			r.Resolved = false
			refs = append(refs, r)
		}
	}
	return refs, rows.Err()
}

// DeleteDocumentByPath removes the index row (and FK-cascaded chunks/tags/links)
// for the document at the given vault-relative path, if any. It is a no-op when
// no row matches. The move command uses this to purge the stale old-path row
// after indexing the moved file at its new path: if the moved file kept its
// frontmatter id, the upsert at the new path already updated the same row (so
// this is a no-op); if a fresh id was generated, the orphaned old-path row is
// purged here. Deleting by id would be wrong in the id-reuse case (it would drop
// the freshly indexed row).
func (db *DB) DeleteDocumentByPath(path string) error {
	_, err := db.conn.Exec("DELETE FROM documents WHERE path = ?", path)
	return err
}

// rawNameForms returns the set of resolvable names for a vault-relative path:
// the full path, every "/"-delimited suffix (the basename being the shortest),
// each normalized (slashes, leading slash, .md extension stripped). A link whose
// normalized target_raw is in this set names the document at path.
func rawNameForms(path string) map[string]struct{} {
	full := normalizeRawName(path)
	if full == "" {
		return nil
	}
	forms := map[string]struct{}{full: {}}
	segs := strings.Split(full, "/")
	for i := 1; i < len(segs); i++ {
		suffix := strings.Join(segs[i:], "/")
		if suffix != "" {
			forms[suffix] = struct{}{}
		}
	}
	return forms
}

// normalizeRawName canonicalizes a link target or path for name matching:
// backslashes to forward slashes, leading slash stripped, ".md" extension
// stripped. Mirrors the normalization in ResolveLinks.
func normalizeRawName(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, ".md")
	return s
}
