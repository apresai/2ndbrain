package store

import (
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// TestChunkContentByID exercises the IN-clause query directly (the rerank
// backfill path): known ids return their content, unknown ids are absent, and
// an empty id list is a no-op.
func TestChunkContentByID(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	if err := db.UpsertDocument(&document.Document{ID: "d1", Path: "a.md", Title: "A"}); err != nil {
		t.Fatalf("upsert doc: %v", err)
	}
	chunks := []document.Chunk{
		{ID: "c1", DocID: "d1", HeadingPath: "H1", Content: "alpha content", ContentHash: "h1"},
		{ID: "c2", DocID: "d1", HeadingPath: "H2", Content: "beta content", ContentHash: "h2"},
	}
	if err := db.UpsertChunks(chunks); err != nil {
		t.Fatalf("upsert chunks: %v", err)
	}

	got, err := db.ChunkContentByID([]string{"c1", "c2", "missing"})
	if err != nil {
		t.Fatalf("ChunkContentByID: %v", err)
	}
	if got["c1"] != "alpha content" {
		t.Errorf("c1 content = %q, want %q", got["c1"], "alpha content")
	}
	if got["c2"] != "beta content" {
		t.Errorf("c2 content = %q, want %q", got["c2"], "beta content")
	}
	if _, ok := got["missing"]; ok {
		t.Error("unknown id must be absent from the map")
	}
	if len(got) != 2 {
		t.Errorf("map size = %d, want 2", len(got))
	}

	empty, err := db.ChunkContentByID(nil)
	if err != nil {
		t.Fatalf("ChunkContentByID(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty ids should return empty map, got %d entries", len(empty))
	}
}
