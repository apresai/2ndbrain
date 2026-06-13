package store

import "fmt"

// AllDocumentPaths returns every indexed document's vault-relative path, sorted
// by path. It backs `2nb tasks`, which reads each document off disk to extract
// GFM checkboxes (tasks aren't stored in the index, so enumeration starts from
// the document list rather than a dedicated table).
func (db *DB) AllDocumentPaths() ([]string, error) {
	rows, err := db.conn.Query(`SELECT path FROM documents ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("query document paths: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan document path: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}
