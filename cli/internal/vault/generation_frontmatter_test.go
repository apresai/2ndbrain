package vault

import (
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
)

// TestIndexGenerationCoversTheFrontmatterBoundaryChange: 0.22.3 moved the
// frontmatter/body boundary for notes that open with an empty properties block,
// so the text those notes were indexed from differs from what 0.22.2 stored. A
// vault stamped at the previous generation must be told to reindex, and must NOT
// be told to pay for a whole-vault re-embed: the content hash is computed from
// the parsed body, so a plain reindex re-embeds exactly the affected notes.
func TestIndexGenerationCoversTheFrontmatterBoundaryChange(t *testing.T) {
	if IndexGeneration != 3 {
		t.Fatalf("IndexGeneration = %d, want 3: the frontmatter boundary change needs its own generation", IndexGeneration)
	}

	db, err := store.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// A vault as 0.22.2 left it: embedded, and stamped at the generations that
	// release shipped.
	doc := &document.Document{ID: "d1", Path: "a.md", Title: "A", Body: "hello world"}
	if err := db.UpsertDocument(doc); err != nil {
		t.Fatalf("upsert doc: %v", err)
	}
	if err := db.SetEmbedding("d1", []float32{0.1, 0.2, 0.3}, "test-model", "h1"); err != nil {
		t.Fatalf("set embedding: %v", err)
	}
	if err := db.SetMetaInt(store.MetaIndexGeneration, 2); err != nil {
		t.Fatalf("stamp index generation: %v", err)
	}
	if err := db.SetMetaInt(store.MetaEmbedGeneration, EmbedGeneration); err != nil {
		t.Fatalf("stamp embed generation: %v", err)
	}

	f := CheckIndexFreshness(db)
	if !f.ReindexRecommended {
		t.Errorf("freshness = %+v, want ReindexRecommended for a vault stamped at index generation 2", f)
	}
	if f.ReembedRecommended {
		t.Errorf("freshness = %+v, want ReembedRecommended false: a paid whole-vault re-embed is the wrong remedy here", f)
	}
	if f.Fix != "2nb index" {
		t.Errorf("fix = %q, want '2nb index'", f.Fix)
	}
}
