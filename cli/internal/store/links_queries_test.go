package store_test

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

// The fixture each test builds is a small real vault:
//
//	A -> B (resolved) and A -> NoSuchDoc (unresolved/broken)
//	B (plain, no outbound links)
//	C (plain, no outbound links)
//
// After CreateAndIndex the links table has source_id/target_raw rows but no
// target_id; ResolveLinks fills target_id for the [[Doc B]] link and leaves the
// broken [[NoSuchDoc]] link with target_id NULL. The queries under test gate on
// target_id IS NOT NULL, so this fixture exercises both the resolved and the
// broken paths.

func TestBacklinks_ReturnsResolvedInboundOnly(t *testing.T) {
	v := testutil.NewTestVault(t)

	docB := testutil.CreateAndIndex(t, v, "Doc B", "note", "Plain target document.")
	testutil.CreateAndIndex(t, v, "Doc C", "note", "Another plain document.")
	docA := testutil.CreateAndIndex(t, v, "Doc A", "note", "See [[Doc B]] and also [[NoSuchDoc]].")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	refs, err := v.DB.Backlinks(docB.ID)
	if err != nil {
		t.Fatalf("backlinks: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("backlinks(B): got %d refs, want 1: %+v", len(refs), refs)
	}
	if refs[0].Path != docA.Path {
		t.Errorf("backlink source path: got %q, want %q", refs[0].Path, docA.Path)
	}
	if refs[0].Title != "Doc A" {
		t.Errorf("backlink source title: got %q, want %q", refs[0].Title, "Doc A")
	}
	if !refs[0].Resolved {
		t.Errorf("backlink should be marked resolved")
	}
	if refs[0].TargetRaw != "Doc B" {
		t.Errorf("backlink target_raw: got %q, want %q", refs[0].TargetRaw, "Doc B")
	}

	// The broken [[NoSuchDoc]] link must NOT surface as a backlink to anything,
	// and a document with no inbound links must report zero backlinks.
	refsC, err := v.DB.Backlinks(docA.ID)
	if err != nil {
		t.Fatalf("backlinks(A): %v", err)
	}
	if len(refsC) != 0 {
		t.Errorf("backlinks(A): got %d, want 0 (nothing links to A): %+v", len(refsC), refsC)
	}
}

func TestOutboundLinks_IncludesBrokenWithResolvedFlag(t *testing.T) {
	v := testutil.NewTestVault(t)

	testutil.CreateAndIndex(t, v, "Doc B", "note", "Plain target document.")
	docA := testutil.CreateAndIndex(t, v, "Doc A", "note", "See [[Doc B]] and also [[NoSuchDoc]].")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	refs, err := v.DB.OutboundLinks(docA.ID)
	if err != nil {
		t.Fatalf("outbound links: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("outbound(A): got %d, want 2 (one resolved, one broken): %+v", len(refs), refs)
	}

	byRaw := map[string]bool{} // target_raw -> resolved
	for _, r := range refs {
		byRaw[r.TargetRaw] = r.Resolved
	}
	resolved, ok := byRaw["Doc B"]
	if !ok || !resolved {
		t.Errorf("outbound link to Doc B should be present and resolved, got %+v", refs)
	}
	broken, ok := byRaw["NoSuchDoc"]
	if !ok || broken {
		t.Errorf("outbound link to NoSuchDoc should be present and unresolved, got %+v", refs)
	}
}

func TestOrphans_ExcludesLinkedIncludesUnlinked(t *testing.T) {
	v := testutil.NewTestVault(t)

	docB := testutil.CreateAndIndex(t, v, "Doc B", "note", "Plain target document.")
	docC := testutil.CreateAndIndex(t, v, "Doc C", "note", "Another plain document.")
	docA := testutil.CreateAndIndex(t, v, "Doc A", "note", "See [[Doc B]] and also [[NoSuchDoc]].")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	refs, err := v.DB.Orphans()
	if err != nil {
		t.Fatalf("orphans: %v", err)
	}

	paths := map[string]bool{}
	for _, r := range refs {
		paths[r.Path] = true
	}
	// B has an inbound link from A → not an orphan.
	if paths[docB.Path] {
		t.Errorf("Doc B has an inbound link and must NOT be an orphan: %+v", refs)
	}
	// A and C have no inbound link → both orphans.
	if !paths[docA.Path] {
		t.Errorf("Doc A has no inbound link and must be an orphan: %+v", refs)
	}
	if !paths[docC.Path] {
		t.Errorf("Doc C has no inbound link and must be an orphan: %+v", refs)
	}
}

func TestDeadends_ExcludesLinkerIncludesNonLinkers(t *testing.T) {
	v := testutil.NewTestVault(t)

	docB := testutil.CreateAndIndex(t, v, "Doc B", "note", "Plain target document.")
	docC := testutil.CreateAndIndex(t, v, "Doc C", "note", "Another plain document.")
	docA := testutil.CreateAndIndex(t, v, "Doc A", "note", "See [[Doc B]] and also [[NoSuchDoc]].")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	refs, err := v.DB.Deadends()
	if err != nil {
		t.Fatalf("deadends: %v", err)
	}

	paths := map[string]bool{}
	for _, r := range refs {
		paths[r.Path] = true
	}
	// A has a resolved outbound link to B → not a deadend (the broken
	// [[NoSuchDoc]] link doesn't save it, but [[Doc B]] does).
	if paths[docA.Path] {
		t.Errorf("Doc A has a resolved outbound link and must NOT be a deadend: %+v", refs)
	}
	// B and C have no outbound links → both deadends.
	if !paths[docB.Path] {
		t.Errorf("Doc B has no outbound link and must be a deadend: %+v", refs)
	}
	if !paths[docC.Path] {
		t.Errorf("Doc C has no outbound link and must be a deadend: %+v", refs)
	}
}

// TestDeadends_BrokenOnlyLinksStillDeadend confirms a document whose only
// outbound link is broken is still a deadend: a broken link leads nowhere
// indexed, so it doesn't count as a real outbound edge.
func TestDeadends_BrokenOnlyLinksStillDeadend(t *testing.T) {
	v := testutil.NewTestVault(t)

	docX := testutil.CreateAndIndex(t, v, "Doc X", "note", "Only a [[GhostDoc]] link here.")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	refs, err := v.DB.Deadends()
	if err != nil {
		t.Fatalf("deadends: %v", err)
	}
	found := false
	for _, r := range refs {
		if r.Path == docX.Path {
			found = true
		}
	}
	if !found {
		t.Errorf("Doc X has only a broken outbound link and must be a deadend: %+v", refs)
	}
}
