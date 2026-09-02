package search

import "testing"

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
