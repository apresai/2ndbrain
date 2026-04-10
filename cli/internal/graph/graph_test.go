package graph

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestTraverse_SingleNode verifies that traversing a document with no links
// returns exactly one node and no edges.
func TestTraverse_SingleNode(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "Lone Note", "note", "No links here.")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	g, err := Traverse(v.DB.Conn(), doc.ID, 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(g.Nodes) != 1 {
		t.Errorf("nodes: got %d, want 1", len(g.Nodes))
	}
	if g.Nodes[0].ID != doc.ID {
		t.Errorf("node ID: got %q, want %q", g.Nodes[0].ID, doc.ID)
	}
	if len(g.Edges) != 0 {
		t.Errorf("edges: got %d, want 0", len(g.Edges))
	}
}

// TestTraverse_OneHop verifies that a document linking to one other document
// produces 2 nodes and 1 edge when traversed at depth 1.
func TestTraverse_OneHop(t *testing.T) {
	v := testutil.NewTestVault(t)

	docB := testutil.CreateAndIndex(t, v, "Doc B", "note", "Target document.")
	docA := testutil.CreateAndIndex(t, v, "Doc A", "note", "See [[Doc B]] for details.")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	g, err := Traverse(v.DB.Conn(), docA.ID, 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(g.Nodes) != 2 {
		t.Errorf("nodes: got %d, want 2", len(g.Nodes))
	}
	if len(g.Edges) != 1 {
		t.Errorf("edges: got %d, want 1", len(g.Edges))
	}

	// Verify the edge connects docA to docB.
	edge := g.Edges[0]
	if edge.Source != docA.ID || edge.Target != docB.ID {
		t.Errorf("edge: got source=%q target=%q, want source=%q target=%q",
			edge.Source, edge.Target, docA.ID, docB.ID)
	}
}

// TestTraverse_DepthZero verifies that depth=0 returns only the root node
// with no edges, even when outgoing links exist.
func TestTraverse_DepthZero(t *testing.T) {
	v := testutil.NewTestVault(t)

	testutil.CreateAndIndex(t, v, "Doc B", "note", "Target document.")
	docA := testutil.CreateAndIndex(t, v, "Doc A", "note", "See [[Doc B]] for details.")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	g, err := Traverse(v.DB.Conn(), docA.ID, 0)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(g.Nodes) != 1 {
		t.Errorf("nodes: got %d, want 1", len(g.Nodes))
	}
	if g.Nodes[0].ID != docA.ID {
		t.Errorf("node ID: got %q, want %q", g.Nodes[0].ID, docA.ID)
	}
	if len(g.Edges) != 0 {
		t.Errorf("edges: got %d, want 0", len(g.Edges))
	}
}

// TestTraverse_Cycle verifies that a mutual link between two documents does not
// cause infinite traversal — the graph should contain exactly 2 nodes.
func TestTraverse_Cycle(t *testing.T) {
	v := testutil.NewTestVault(t)

	// Create both docs. docA links to docB; docB links back to docA.
	docA := testutil.CreateAndIndex(t, v, "Cycle A", "note", "See [[Cycle B]].")
	docB := testutil.CreateAndIndex(t, v, "Cycle B", "note", "See [[Cycle A]].")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	g, err := Traverse(v.DB.Conn(), docA.ID, 5)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if len(g.Nodes) != 2 {
		t.Errorf("nodes: got %d, want 2 (cycle must not duplicate nodes)", len(g.Nodes))
	}

	seen := make(map[string]bool)
	for _, n := range g.Nodes {
		if seen[n.ID] {
			t.Errorf("duplicate node in graph: %q", n.ID)
		}
		seen[n.ID] = true
	}

	if !seen[docA.ID] || !seen[docB.ID] {
		t.Errorf("expected both docA (%q) and docB (%q) in graph", docA.ID, docB.ID)
	}
}

// TestAdjacencyList_Empty verifies that an isolated document returns an empty
// (but non-nil) adjacency map.
func TestAdjacencyList_Empty(t *testing.T) {
	v := testutil.NewTestVault(t)
	doc := testutil.CreateAndIndex(t, v, "Isolated Doc", "note", "No links here.")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	adj, err := AdjacencyList(v.DB.Conn(), doc.ID)
	if err != nil {
		t.Fatalf("AdjacencyList: %v", err)
	}

	if adj == nil {
		t.Error("AdjacencyList returned nil map, want non-nil empty map")
	}
	if len(adj) != 0 {
		t.Errorf("adjacency map length: got %d, want 0", len(adj))
	}
}

// TestAdjacencyList_WithLinks verifies that a document linking to two others
// returns the correct adjacency entries after link resolution.
func TestAdjacencyList_WithLinks(t *testing.T) {
	v := testutil.NewTestVault(t)

	docB := testutil.CreateAndIndex(t, v, "Target B", "note", "Doc B body.")
	docC := testutil.CreateAndIndex(t, v, "Target C", "note", "Doc C body.")
	docA := testutil.CreateAndIndex(t, v, "Source A", "note", "Links to [[Target B]] and [[Target C]].")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	adj, err := AdjacencyList(v.DB.Conn(), docA.ID)
	if err != nil {
		t.Fatalf("AdjacencyList: %v", err)
	}

	neighbors, ok := adj[docA.ID]
	if !ok {
		t.Fatalf("adjacency map missing entry for docA (%q); map = %v", docA.ID, adj)
	}

	neighborSet := make(map[string]bool)
	for _, n := range neighbors {
		neighborSet[n] = true
	}

	if !neighborSet[docB.ID] {
		t.Errorf("expected docB (%q) in neighbors of docA, got %v", docB.ID, neighbors)
	}
	if !neighborSet[docC.ID] {
		t.Errorf("expected docC (%q) in neighbors of docA, got %v", docC.ID, neighbors)
	}
}
