package store

import (
	"database/sql"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// TestResolveLinks_TitleAndSuffixBranches exercises the shortest-unique multi-
// segment suffix branch and the title-match branch, which the broader
// Obsidian-native test resolves via exact-path/alias instead.
func TestResolveLinks_TitleAndSuffixBranches(t *testing.T) {
	db := openTestDB(t)

	d1 := &document.Document{
		ID: "d1", Path: "a/b/deep.md", Title: "Deep Title",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	d2 := &document.Document{
		ID: "d2", Path: "root.md", Title: "Root",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	if err := db.UpsertDocument(d1); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDocument(d2); err != nil {
		t.Fatal(err)
	}

	if err := db.UpsertLinks("d2", []document.WikiLink{
		{Target: "b/deep"},     // multi-segment suffix → d1 (suffix branch)
		{Target: "Deep Title"}, // title → d1 (title branch)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveLinks(); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	for _, raw := range []string{"b/deep", "Deep Title"} {
		var target sql.NullString
		if err := db.conn.QueryRow(
			"SELECT target_id FROM links WHERE source_id='d2' AND target_raw=?", raw,
		).Scan(&target); err != nil {
			t.Fatalf("query %q: %v", raw, err)
		}
		if target.String != "d1" {
			t.Errorf("link %q resolved to %q, want d1", raw, target.String)
		}
	}
}
