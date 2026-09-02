package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/apresai/2ndbrain/internal/store"
)

// A vault that was just created is at the current logic generation by
// construction: it holds no chunks and no vectors. Stamping it at birth is what
// keeps the documented quick start (vault create, then create, then index) from
// reporting "UPGRADE REEMBED RECOMMENDED" on a vault seconds old. That nag sent
// a brand-new user to pay for a full re-embed to fix nothing.
func TestInit_StampsCurrentGenerations(t *testing.T) {
	v, err := Init(filepath.Join(t.TempDir(), "fresh"))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer v.Close()

	if got := v.DB.GetMetaInt(store.MetaEmbedGeneration, 0); got != EmbedGeneration {
		t.Errorf("embed_generation after Init = %d, want %d", got, EmbedGeneration)
	}
	if got := v.DB.GetMetaInt(store.MetaIndexGeneration, 0); got != IndexGeneration {
		t.Errorf("index_generation after Init = %d, want %d", got, IndexGeneration)
	}
	// Nothing has been indexed, so the version stamp must NOT be claimed.
	if got, _, _ := v.DB.GetMeta(store.MetaIndexedByVersion); got != "" {
		t.Errorf("indexed_by_version after Init = %q, want empty (nothing was indexed)", got)
	}
	if f := CheckIndexFreshness(v.DB); f.Stale() {
		t.Errorf("a freshly created vault reads as stale: %+v", f)
	}
}

// The other direction must survive: a vault with embeddings and NO stamp is a
// pre-stamp vault and must still be told to re-embed. The birth stamp closes
// the false positive without opening a false negative.
func TestCheckIndexFreshness_UnstampedVaultWithEmbeddingsStillStale(t *testing.T) {
	v, err := Init(filepath.Join(t.TempDir(), "old"))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer v.Close()

	// Simulate a vault that predates the stamp mechanism: real vectors, no meta.
	if _, err := v.DB.Conn().Exec(`DELETE FROM meta WHERE key IN (?, ?)`, store.MetaEmbedGeneration, store.MetaIndexGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := v.DB.Conn().Exec(`INSERT INTO documents (id, path, title, doc_type, status, created_at, modified_at, content_hash, frontmatter, embedding, embedding_model)
		VALUES ('d1','old.md','Old','note','draft','2025-01-01','2025-01-01','h','{}', X'00000000', 'legacy')`); err != nil {
		t.Fatalf("seed legacy embedding: %v", err)
	}

	f := CheckIndexFreshness(v.DB)
	if !f.ReembedRecommended {
		t.Errorf("an unstamped vault holding embeddings must still recommend a re-embed: %+v", f)
	}
}

// Open's self-heal creates an empty index.db for a vault 2nb has never seen
// (an existing Obsidian vault adopted via --vault). That database is empty, so
// it is at the current generation for the same reason a freshly Init'd one
// is; without the stamp, a write that embeds inline before the first full
// index (create, append, daily) left the vault nagging for a re-embed.
func TestOpen_SelfHealedIndexIsStampedCurrent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "adopted")
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing.md"), []byte("---\ntitle: Existing\ntype: note\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer v.Close()
	if got := v.DB.GetMetaInt(store.MetaEmbedGeneration, 0); got != EmbedGeneration {
		t.Errorf("embed_generation after self-heal = %d, want %d", got, EmbedGeneration)
	}
	if f := CheckIndexFreshness(v.DB); f.Stale() {
		t.Errorf("a self-healed empty index reads as stale: %+v", f)
	}
}
