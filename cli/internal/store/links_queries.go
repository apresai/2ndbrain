package store

import "fmt"

// LinkRef describes a single wikilink relationship between two documents, used
// by the backlinks and links commands. For an inbound link (backlinks) Path and
// Title name the source document; for an outbound link they name the target
// document when resolved (empty when the link is broken). Heading, Alias, and
// TargetRaw carry the original link form for display. Resolved is true only when
// the link points at a real indexed document (target_id IS NOT NULL).
type LinkRef struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	Heading   string `json:"heading,omitempty"`
	Alias     string `json:"alias,omitempty"`
	TargetRaw string `json:"target_raw"`
	Resolved  bool   `json:"resolved"`
}

// DocRef is a minimal document reference (path + title), used by the orphans and
// deadends health queries.
type DocRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// Backlinks returns the resolved inbound links to the document with the given
// id: every document that links to it via a wikilink that resolved to a real
// doc. Results are sorted by source path. The Resolved field is always true
// here (the query only returns resolved links); it is included so the JSON
// shape matches OutboundLinks.
func (db *DB) Backlinks(docID string) ([]LinkRef, error) {
	rows, err := db.conn.Query(`
		SELECT d.path, d.title, l.heading, l.alias, l.target_raw
		FROM links l
		JOIN documents d ON d.id = l.source_id
		WHERE l.target_id = ? AND l.resolved = 1
		ORDER BY d.path
	`, docID)
	if err != nil {
		return nil, fmt.Errorf("query backlinks: %w", err)
	}
	defer rows.Close()

	var refs []LinkRef
	for rows.Next() {
		var r LinkRef
		if err := rows.Scan(&r.Path, &r.Title, &r.Heading, &r.Alias, &r.TargetRaw); err != nil {
			return nil, fmt.Errorf("scan backlink: %w", err)
		}
		r.Resolved = true
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// OutboundLinks returns every wikilink emitted by the document with the given
// id, including unresolved (broken) links so this doubles as a per-file
// broken-link view. For resolved links Path and Title name the target document;
// for unresolved links they are empty and Resolved is false. Results are sorted
// by the raw link target.
func (db *DB) OutboundLinks(docID string) ([]LinkRef, error) {
	rows, err := db.conn.Query(`
		SELECT
			COALESCE(d.path, '')  AS target_path,
			COALESCE(d.title, '') AS target_title,
			l.heading,
			l.alias,
			l.target_raw,
			(l.target_id IS NOT NULL) AS resolved
		FROM links l
		LEFT JOIN documents d ON d.id = l.target_id
		WHERE l.source_id = ?
		ORDER BY l.target_raw
	`, docID)
	if err != nil {
		return nil, fmt.Errorf("query outbound links: %w", err)
	}
	defer rows.Close()

	var refs []LinkRef
	for rows.Next() {
		var r LinkRef
		if err := rows.Scan(&r.Path, &r.Title, &r.Heading, &r.Alias, &r.TargetRaw, &r.Resolved); err != nil {
			return nil, fmt.Errorf("scan outbound link: %w", err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// Orphans returns every document that has no resolved inbound link: nothing in
// the vault links to it. A link counts only when it resolved to a real document
// (target_id IS NOT NULL AND resolved = 1), so broken links to a name that
// happens to match are not counted. Results are sorted by path.
func (db *DB) Orphans() ([]DocRef, error) {
	rows, err := db.conn.Query(`
		SELECT path, title
		FROM documents
		WHERE id NOT IN (
			SELECT target_id FROM links
			WHERE target_id IS NOT NULL AND resolved = 1
		)
		ORDER BY path
	`)
	if err != nil {
		return nil, fmt.Errorf("query orphans: %w", err)
	}
	defer rows.Close()

	return scanDocRefs(rows)
}

// Deadends returns every document that has no resolved outbound link: it links
// to nothing real in the vault (it may still emit broken links). A document
// with only broken outbound links is a deadend, since none of those links lead
// anywhere indexed. Results are sorted by path.
func (db *DB) Deadends() ([]DocRef, error) {
	rows, err := db.conn.Query(`
		SELECT path, title
		FROM documents
		WHERE id NOT IN (
			SELECT DISTINCT source_id FROM links
			WHERE target_id IS NOT NULL
		)
		ORDER BY path
	`)
	if err != nil {
		return nil, fmt.Errorf("query deadends: %w", err)
	}
	defer rows.Close()

	return scanDocRefs(rows)
}

// scanDocRefs reads (path, title) rows into a DocRef slice.
func scanDocRefs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]DocRef, error) {
	var refs []DocRef
	for rows.Next() {
		var r DocRef
		if err := rows.Scan(&r.Path, &r.Title); err != nil {
			return nil, fmt.Errorf("scan doc ref: %w", err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
