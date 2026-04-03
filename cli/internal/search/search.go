package search

import (
	"database/sql"
	"fmt"
	"strings"
)

type Result struct {
	DocID       string         `json:"doc_id"`
	Path        string         `json:"path"`
	Title       string         `json:"title"`
	ChunkID     string         `json:"chunk_id"`
	HeadingPath string         `json:"heading_path"`
	Content     string         `json:"content"`
	Score       float64        `json:"score"`
	DocType     string         `json:"type"`
	Status      string         `json:"status"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
}

type Options struct {
	Query  string
	Type   string
	Status string
	Tag    string
	Limit  int
}

type Engine struct {
	db *sql.DB
}

func NewEngine(db *sql.DB) *Engine {
	return &Engine{db: db}
}

// Search performs a hybrid search. Currently BM25 via FTS5 with structured filters.
// Vector search will be added when embedding pipeline is ready.
func (e *Engine) Search(opts Options) ([]Result, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	if opts.Query == "" {
		return e.listByFilters(opts)
	}

	return e.bm25Search(opts)
}

func (e *Engine) bm25Search(opts Options) ([]Result, error) {
	// Build the query with FTS5 BM25 ranking + joins for metadata filters
	query := `
		SELECT
			d.id, d.path, d.title, c.id, c.heading_path,
			snippet(chunks_fts, 0, '>>>','<<<', '...', 40),
			bm25(chunks_fts),
			d.doc_type, d.status, d.frontmatter
		FROM chunks_fts fts
		JOIN chunks c ON c.rowid = fts.rowid
		JOIN documents d ON d.id = c.doc_id
	`

	var conditions []string
	var args []any

	conditions = append(conditions, "chunks_fts MATCH ?")
	args = append(args, ftsQuery(opts.Query))

	if opts.Type != "" {
		conditions = append(conditions, "d.doc_type = ?")
		args = append(args, opts.Type)
	}
	if opts.Status != "" {
		conditions = append(conditions, "d.status = ?")
		args = append(args, opts.Status)
	}
	if opts.Tag != "" {
		conditions = append(conditions, "d.id IN (SELECT doc_id FROM tags WHERE tag = ?)")
		args = append(args, opts.Tag)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY bm25(chunks_fts) LIMIT ?"
	args = append(args, opts.Limit)

	return e.executeSearch(query, args)
}

func (e *Engine) listByFilters(opts Options) ([]Result, error) {
	query := `
		SELECT
			d.id, d.path, d.title, '' as chunk_id, '' as heading_path,
			substr(d.frontmatter, 1, 200) as content,
			0.0 as score,
			d.doc_type, d.status, d.frontmatter
		FROM documents d
	`

	var conditions []string
	var args []any

	if opts.Type != "" {
		conditions = append(conditions, "d.doc_type = ?")
		args = append(args, opts.Type)
	}
	if opts.Status != "" {
		conditions = append(conditions, "d.status = ?")
		args = append(args, opts.Status)
	}
	if opts.Tag != "" {
		conditions = append(conditions, "d.id IN (SELECT doc_id FROM tags WHERE tag = ?)")
		args = append(args, opts.Tag)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	query += " ORDER BY d.modified_at DESC LIMIT ?"
	args = append(args, opts.Limit)

	return e.executeSearch(query, args)
}

func (e *Engine) executeSearch(query string, args []any) ([]Result, error) {
	rows, err := e.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		var fmJSON string
		if err := rows.Scan(&r.DocID, &r.Path, &r.Title, &r.ChunkID, &r.HeadingPath,
			&r.Content, &r.Score, &r.DocType, &r.Status, &fmJSON); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		// Negate BM25 score (FTS5 returns negative, lower = better)
		if r.Score < 0 {
			r.Score = -r.Score
		}
		results = append(results, r)
	}

	return results, rows.Err()
}

// ftsQuery converts a natural language query to FTS5 syntax.
// Wraps each word in quotes for phrase-like matching with implicit AND.
func ftsQuery(q string) string {
	words := strings.Fields(q)
	if len(words) == 0 {
		return q
	}
	// Use implicit AND between terms
	var parts []string
	for _, w := range words {
		// Escape any FTS5 special characters
		w = strings.ReplaceAll(w, "\"", "")
		w = strings.ReplaceAll(w, "*", "")
		if w != "" {
			parts = append(parts, w)
		}
	}
	return strings.Join(parts, " ")
}
