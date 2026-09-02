package search

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/store"
)

// The structured filters (--type/--status/--tag) are applied by the BM25 leg in
// SQL. The vector legs score on embeddings alone and know nothing about
// document metadata, so before this was fixed RRF happily merged an unfiltered
// vector hit into a filtered query: `search --type adr` returned notes and
// `--status accepted` returned drafts. The bug only showed when the semantic
// channel was healthy, which is the default, and BM25-only was always correct.
//
// Credential-free: HybridSearch takes the query vector as an argument, so the
// embedder is never involved.
func TestHybridSearch_FiltersApplyToTheVectorLeg(t *testing.T) {
	db := setupSearchDB(t)
	// Both docs mention "rocket" so BM25 has candidates; the vector channel is
	// what would otherwise smuggle the wrong-typed one through.
	insertTestDoc(t, db, "d-adr", "a.md", "Aligned ADR", "adr", "accepted",
		"# Rocket\n\nRocket science in an ADR.\n")
	insertTestDoc(t, db, "d-note", "n.md", "Aligned Note", "note", "draft",
		"# Rocket\n\nRocket science in a note.\n")

	// Identical vectors: neither doc can win on similarity, so any difference in
	// the result set is the filter and nothing else.
	db.SetEmbedding("d-adr", []float32{1, 0}, "t", "h1")
	db.SetEmbedding("d-note", []float32{1, 0}, "t", "h2")
	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	engine := NewEngine(db.Conn())

	paths := func(t *testing.T, opts Options) []string {
		t.Helper()
		res, mode, err := engine.HybridSearch(opts, []float32{1, 0}, ids, vecs)
		if err != nil {
			t.Fatalf("HybridSearch: %v", err)
		}
		if mode != ModeHybrid {
			t.Fatalf("mode = %q, want hybrid (the vector leg must have run for this test to mean anything)", mode)
		}
		out := make([]string, 0, len(res))
		for _, r := range res {
			out = append(out, r.Path)
		}
		return out
	}

	t.Run("no filter returns both", func(t *testing.T) {
		if got := paths(t, Options{Query: "rocket", Limit: 10}); len(got) != 2 {
			t.Errorf("want both docs, got %v", got)
		}
	})

	t.Run("type filter excludes the other type", func(t *testing.T) {
		got := paths(t, Options{Query: "rocket", Limit: 10, Type: "adr"})
		if len(got) != 1 || got[0] != "a.md" {
			t.Errorf("--type adr returned %v, want only a.md; a note leaked through the vector leg", got)
		}
	})

	t.Run("status filter excludes the other status", func(t *testing.T) {
		got := paths(t, Options{Query: "rocket", Limit: 10, Status: "draft"})
		if len(got) != 1 || got[0] != "n.md" {
			t.Errorf("--status draft returned %v, want only n.md", got)
		}
	})

	t.Run("tag filter excludes an untagged doc", func(t *testing.T) {
		// --tag is a different SQL shape from type/status (a subquery against the
		// tags table), so it needs its own coverage.
		if err := db.UpsertTags("d-adr", []string{"space"}); err != nil {
			t.Fatalf("UpsertTags: %v", err)
		}
		got := paths(t, Options{Query: "rocket", Limit: 10, Tag: "space"})
		if len(got) != 1 || got[0] != "a.md" {
			t.Errorf("--tag space returned %v, want only a.md", got)
		}
		if got := paths(t, Options{Query: "rocket", Limit: 10, Tag: "nosuchtag"}); len(got) != 0 {
			t.Errorf("--tag nosuchtag returned %v, want none", got)
		}
	})

	t.Run("a filter matching nothing returns nothing", func(t *testing.T) {
		if got := paths(t, Options{Query: "rocket", Limit: 10, Type: "postmortem"}); len(got) != 0 {
			t.Errorf("--type postmortem returned %v on a vault with no postmortem, want none", got)
		}
	})

	t.Run("filters do not over-filter the matching doc", func(t *testing.T) {
		got := paths(t, Options{Query: "rocket", Limit: 10, Type: "note", Status: "draft"})
		if len(got) != 1 || got[0] != "n.md" {
			t.Errorf("type+status matching one doc returned %v, want n.md", got)
		}
	})
}

// The test above exercises the brute-force vector fallback. The PRIMARY path is
// the per-chunk vec0 KNN (vecChunkSearchByDoc), which is a different query and
// was equally unfiltered, so it needs its own coverage: populating vec_chunks
// makes HybridSearch take that branch instead.
func TestHybridSearch_FiltersApplyToTheVec0KNNPath(t *testing.T) {
	db := setupSearchDB(t)
	insertTestDoc(t, db, "d-adr", "a.md", "Aligned ADR", "adr", "accepted",
		"# Rocket\n\nRocket science in an ADR.\n")
	insertTestDoc(t, db, "d-note", "n.md", "Aligned Note", "note", "draft",
		"# Rocket\n\nRocket science in a note.\n")

	const dim = 2
	// Fatal, not Skip. sqlite-vec is compiled into the pure-Go build
	// unconditionally, so an unavailable vec_chunks is a real defect and not a
	// capability gap. Skipping here would let a future break silently reopen the
	// exact hole this test exists to close (the sibling
	// TestVecChunkSearchByDoc_CoverageGate treats the same condition as fatal).
	if err := db.EnsureVecChunks(dim); err != nil {
		t.Fatalf("EnsureVecChunks: %v", err)
	}
	// Identical vectors again, so only the filter can explain a difference.
	for _, d := range []struct{ doc, chunk string }{{"d-adr", "c-adr"}, {"d-note", "c-note"}} {
		if err := db.SetDocChunkVectors(d.doc, []store.ChunkVector{{
			ChunkID: d.chunk, DocID: d.doc, ContentHash: "h", Vector: []float32{1, 0},
		}}, "t"); err != nil {
			t.Fatalf("SetDocChunkVectors(%s): %v", d.doc, err)
		}
		// documents.embedding is what len(embeddings) coverage is measured against.
		if err := db.SetEmbedding(d.doc, []float32{1, 0}, "t", "h"); err != nil {
			t.Fatalf("SetEmbedding(%s): %v", d.doc, err)
		}
	}
	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	engine := NewEngine(db.Conn())

	// Confirm the vec0 branch is the one under test; otherwise this duplicates
	// the brute-force test and proves nothing new.
	if _, ok, verr := engine.vecChunkSearchByDoc([]float32{1, 0}, 40, 0, len(vecs)); verr != nil || !ok {
		t.Fatalf("vec0 path not taken (ok=%v err=%v); this test would otherwise duplicate the brute-force one and prove nothing", ok, verr)
	}

	res, mode, err := engine.HybridSearch(Options{Query: "rocket", Limit: 10, Type: "adr"}, []float32{1, 0}, ids, vecs)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if mode != ModeHybrid {
		t.Fatalf("mode = %q, want hybrid", mode)
	}
	for _, r := range res {
		if r.DocType != "adr" {
			t.Errorf("--type adr returned %s [%s]; the vec0 KNN leg leaked the filter", r.Path, r.DocType)
		}
	}
	if len(res) != 1 {
		t.Errorf("want exactly the one adr, got %d results", len(res))
	}
}

// The over-fetch exists because filtering happens AFTER the vector leg has
// already chosen its k. Without a bigger pool when a filter is active, a
// selective filter starves the result set: every candidate the KNN returned is
// the wrong type, so the caller sees nothing even though a matching document
// sits just outside the pool.
//
// The target here is reachable ONLY through the vector leg (the BM25 query word
// does not appear in it), and it is deliberately ranked below a wall of
// wrong-typed noise, so it clears the filtered pool but not the unfiltered one.
// This is what pins the multiplier rather than merely the filtering.
func TestHybridSearch_FilteredQueryOverFetchesDeepEnough(t *testing.T) {
	db := setupSearchDB(t)

	// Noise: nine notes that DO match the BM25 query and sit nearest the query
	// vector, so they crowd the top of the vector ranking.
	for i := range 9 {
		id := "d-noise-" + string(rune('a'+i))
		insertTestDoc(t, db, id, id+".md", "Noise", "note", "draft",
			"# Rocket\n\nRocket telemetry notes.\n")
		if err := db.SetEmbedding(id, []float32{1, 0}, "t", "h"); err != nil {
			t.Fatalf("SetEmbedding(%s): %v", id, err)
		}
	}
	// The target: an adr with NO occurrence of "rocket", so BM25 cannot find it,
	// and a vector further from the query than all the noise.
	insertTestDoc(t, db, "d-target", "target.md", "Target ADR", "adr", "accepted",
		"# Propulsion\n\nStaging and thrust decisions.\n")
	if err := db.SetEmbedding("d-target", []float32{0.7, 0.7}, "t", "h"); err != nil {
		t.Fatalf("SetEmbedding(target): %v", err)
	}

	ids, vecs, err := db.AllEmbeddings()
	if err != nil {
		t.Fatalf("AllEmbeddings: %v", err)
	}
	engine := NewEngine(db.Conn())

	// Limit 1. The unfiltered over-fetch would take 2 vector candidates, all
	// noise; the filtered over-fetch reaches deep enough to include the target.
	res, mode, err := engine.HybridSearch(
		Options{Query: "rocket", Limit: 1, Type: "adr"}, []float32{1, 0}, ids, vecs)
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if mode != ModeHybrid {
		t.Fatalf("mode = %q, want hybrid", mode)
	}
	if len(res) != 1 || res[0].Path != "target.md" {
		t.Errorf("filtered search returned %v; the matching adr sits below the wrong-typed noise, so a filtered query must over-fetch past it", res)
	}
}
