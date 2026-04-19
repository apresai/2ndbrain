package integration_test

import (
	"os"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/graph"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

func TestFullLifecycle_InitCreateIndexSearch(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "JWT Authentication", "adr", "")

	engine := search.NewEngine(v.DB.Conn())
	results, err := engine.Search(search.Options{Query: "authentication", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results")
	}

	found := false
	for _, r := range results {
		if r.DocID == doc.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("created document not found in search results")
	}
}

func TestADRStatusTransitions(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "Auth Strategy", "adr", "")

	if doc.Status != "proposed" {
		t.Fatalf("initial status = %q, want proposed", doc.Status)
	}

	if err := v.Schemas.ValidateStatusTransition("adr", "proposed", "accepted"); err != nil {
		t.Errorf("proposed -> accepted should be valid: %v", err)
	}
	if err := v.Schemas.ValidateStatusTransition("adr", "proposed", "superseded"); err == nil {
		t.Error("proposed -> superseded should be invalid")
	}
	if err := v.Schemas.ValidateStatusTransition("adr", "accepted", "deprecated"); err != nil {
		t.Errorf("accepted -> deprecated should be valid: %v", err)
	}
	if err := v.Schemas.ValidateStatusTransition("adr", "deprecated", "accepted"); err == nil {
		t.Error("deprecated is terminal")
	}
}

func TestWikilinksAndGraphTraversal(t *testing.T) {
	v := testutil.NewTestVault(t)

	bodyA := "# Doc A\n\nSee [[doc-b]] for details.\n"
	docA := testutil.CreateAndIndex(t, v, "Doc A", "note", bodyA)

	bodyB := "# Doc B\n\nReferences [[doc-a]] here.\n"
	docB := testutil.CreateAndIndex(t, v, "Doc B", "note", bodyB)

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	g, err := graph.Traverse(v.DB.Conn(), docA.ID, 2)
	if err != nil {
		t.Fatalf("traverse: %v", err)
	}

	if len(g.Nodes) < 2 {
		t.Errorf("expected at least 2 nodes, got %d", len(g.Nodes))
	}

	foundB := false
	for _, n := range g.Nodes {
		if n.ID == docB.ID {
			foundB = true
		}
	}
	if !foundB {
		t.Error("docB not found in graph traversal from docA")
	}
}

func TestSensitiveFieldFiltering(t *testing.T) {
	meta := map[string]any{
		"title": "Doc", "secret": "hidden", "password": "p4ss",
		"token": "tok123", "key": "api-key", "status": "draft",
	}

	filtered := document.FilterSensitive(meta)
	for _, sensitive := range []string{"secret", "password", "token", "key"} {
		if _, ok := filtered[sensitive]; ok {
			t.Errorf("%q should be filtered out", sensitive)
		}
	}
	if _, ok := filtered["title"]; !ok {
		t.Error("title should survive filtering")
	}
}

func TestDeleteCascade(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "Delete Me", "note", "# Delete Me\n\nLink to [[other]].\n")

	got, err := v.DB.GetDocumentByPath(doc.Path)
	if err != nil {
		t.Fatalf("document should exist: %v", err)
	}

	if err := v.DB.DeleteDocument(got.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var count int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM chunks WHERE doc_id = ?", got.ID).Scan(&count)
	if count != 0 {
		t.Errorf("chunks should be 0, got %d", count)
	}
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM links WHERE source_id = ?", got.ID).Scan(&count)
	if count != 0 {
		t.Errorf("links should be 0, got %d", count)
	}
}

func TestListWithFilters(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "ADR One", "adr", "")
	testutil.CreateAndIndex(t, v, "ADR Two", "adr", "")
	testutil.CreateAndIndex(t, v, "Note One", "note", "")

	engine := search.NewEngine(v.DB.Conn())

	adrs, err := engine.Search(search.Options{Type: "adr", Limit: 100})
	if err != nil {
		t.Fatalf("list adrs: %v", err)
	}
	if len(adrs) != 2 {
		t.Errorf("expected 2 ADRs, got %d", len(adrs))
	}
}

func TestIndexVault_FullRun(t *testing.T) {
	v := testutil.NewTestVault(t)

	writeDoc(t, v, "adr-auth.md", "adr-1", "Auth Strategy", "adr", "# Auth Strategy\n\nUse JWT.\n")
	writeDoc(t, v, "runbook-debug.md", "rb-1", "Debug Guide", "runbook", "# Debug Guide\n\nSteps to debug.\n")

	stats, err := vault.IndexVault(v, nil)
	if err != nil {
		t.Fatalf("index: %v", err)
	}

	if stats.DocsIndexed != 2 {
		t.Errorf("docs indexed = %d, want 2", stats.DocsIndexed)
	}
	if stats.ChunksCreated == 0 {
		t.Error("expected chunks to be created")
	}
}

// TestE2E_IndexSearchDeleteReindex covers the full happy-path lifecycle
// across store, vault, and search: index several docs, find one via BM25,
// delete it, reindex, confirm it's gone from the results. Catches
// regressions where any layer (FTS5 cleanup, purgeStale, link resolution)
// fails to remove a deleted doc from the index.
func TestE2E_IndexSearchDeleteReindex(t *testing.T) {
	v := testutil.NewTestVault(t)

	writeDoc(t, v, "alpha.md", "id-alpha", "Alpha", "note",
		"Body mentioning quokkas for a unique search term.\n")
	writeDoc(t, v, "beta.md", "id-beta", "Beta", "note",
		"Body about capybaras.\n")
	writeDoc(t, v, "gamma.md", "id-gamma", "Gamma", "note",
		"Body about pangolins.\n")

	if _, err := vault.IndexVault(v, nil); err != nil {
		t.Fatalf("first index: %v", err)
	}

	engine := search.NewEngine(v.DB.Conn())
	hits, err := engine.Search(search.Options{Query: "quokkas", Limit: 10})
	if err != nil {
		t.Fatalf("initial search: %v", err)
	}
	var found bool
	for _, r := range hits {
		if r.DocID == "id-alpha" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected id-alpha in 'quokkas' results before delete")
	}

	// Delete the file AND its index entry. purgeStale would also catch this
	// on reindex, but DeleteDocument is the explicit path exercised by the
	// CLI/MCP delete flows.
	if err := os.Remove(v.AbsPath("alpha.md")); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := v.DB.DeleteDocument("id-alpha"); err != nil {
		t.Fatalf("delete doc: %v", err)
	}

	if _, err := vault.IndexVault(v, nil); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	hits, err = engine.Search(search.Options{Query: "quokkas", Limit: 10})
	if err != nil {
		t.Fatalf("post-delete search: %v", err)
	}
	for _, r := range hits {
		if r.DocID == "id-alpha" {
			t.Errorf("id-alpha still appears in search after delete+reindex")
		}
	}

	// Sanity: the other docs should still be searchable.
	hits, _ = engine.Search(search.Options{Query: "capybaras", Limit: 10})
	if len(hits) == 0 {
		t.Error("capybaras search returned no results — reindex wiped too much")
	}
}

func writeDoc(t *testing.T, v *vault.Vault, filename, id, title, docType, body string) {
	t.Helper()
	content := strings.Join([]string{
		"---",
		"id: " + id,
		"title: " + title,
		"type: " + docType,
		"status: draft",
		"tags: []",
		"created: 2025-01-01T00:00:00Z",
		"modified: 2025-01-01T00:00:00Z",
		"---",
		body,
	}, "\n")
	path := v.AbsPath(filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}
