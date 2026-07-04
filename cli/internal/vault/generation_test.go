package vault

import (
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
)

// TestCheckIndexFreshness covers the three outcomes: an unindexed vault flags a
// reindex, a vault WITH embeddings but no generation stamp flags a re-embed, and
// a freshly-stamped vault reads clean.
func TestCheckIndexFreshness(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// No stamps, no embeddings: stored gens are 0 < current, but with nothing to
	// re-embed the recommendation is a plain reindex.
	f := CheckIndexFreshness(db)
	if !f.ReindexRecommended || f.ReembedRecommended {
		t.Fatalf("empty vault: got %+v, want reindex-only", f)
	}

	// Add a doc WITH an embedding (stored directly — no API, valid test data):
	// now the embed generation is stale (0 < %d) so a re-embed is recommended.
	doc := &document.Document{ID: "d1", Path: "a.md", Title: "A", Body: "hello world"}
	if err := db.UpsertDocument(doc); err != nil {
		t.Fatalf("upsert doc: %v", err)
	}
	if err := db.SetEmbedding("d1", []float32{0.1, 0.2, 0.3}, "test-model", "h1"); err != nil {
		t.Fatalf("set embedding: %v", err)
	}
	f = CheckIndexFreshness(db)
	if !f.ReembedRecommended {
		t.Fatalf("embedded stale vault: got %+v, want ReembedRecommended", f)
	}
	if f.Fix != "2nb index --force-reembed" {
		t.Fatalf("fix = %q, want '2nb index --force-reembed'", f.Fix)
	}

	// Stamp the current generations (as a full --force-reembed would) → fresh.
	if err := StampEmbedGeneration(db, "0.13.0"); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if f = CheckIndexFreshness(db); f.Stale() {
		t.Fatalf("stamped vault should be fresh, got %+v", f)
	}
	if got := db.GetMetaInt(store.MetaEmbedGeneration, -1); got != EmbedGeneration {
		t.Fatalf("embed_generation stamp = %d, want %d", got, EmbedGeneration)
	}
	if got := db.GetMetaInt(store.MetaIndexGeneration, -1); got != IndexGeneration {
		t.Fatalf("index_generation stamp = %d, want %d (force-reembed advances both)", got, IndexGeneration)
	}
}

// TestStampAfterIndex_EmptyNoteDoesNotBlockStamp guards the empty-note footgun:
// an empty/whitespace note keeps a NULL embedding forever (the embed pass skips
// it), so StampAfterIndex must judge "all embedded" chunk-aware (via
// EmbeddingCounts), not by DocumentsNeedingEmbedding — otherwise a fresh vault
// holding a single blank note would nag a re-embed it can never clear.
func TestStampAfterIndex_EmptyNoteDoesNotBlockStamp(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// d1: an embeddable, embedded doc. d2: an empty stub — NULL embedding, no
	// chunks (never inserted), which EmbeddingCounts excludes but the old
	// DocumentsNeedingEmbedding path would have counted.
	if err := db.UpsertDocument(&document.Document{ID: "d1", Path: "a.md", Title: "A", Body: "real content"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetEmbedding("d1", []float32{0.1, 0.2, 0.3}, "m", "h1"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDocument(&document.Document{ID: "d2", Path: "empty.md", Title: "Empty", Body: ""}); err != nil {
		t.Fatal(err)
	}

	// Fresh-vault path (embeddingCountBefore=0, not a force-reembed): must still
	// stamp the embed generation despite the empty note.
	if err := StampAfterIndex(db, "0.13.0", false, 0, 0, 0); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if got := db.GetMetaInt(store.MetaEmbedGeneration, -1); got != EmbedGeneration {
		t.Fatalf("embed_generation = %d, want %d — an empty note must not block the stamp", got, EmbedGeneration)
	}
	if CheckIndexFreshness(db).Stale() {
		t.Fatal("vault should read fresh after stamping, despite the empty note")
	}
}
