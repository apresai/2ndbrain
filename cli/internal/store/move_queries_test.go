package store_test

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

// LinksByRawName surfaces UNRESOLVED links whose raw target names a (future)
// document path, which is exactly the set Backlinks misses (it returns only
// resolved links). On a move, those broken-by-name links still need rewriting so
// they keep pointing at the note after it changes path.

func TestLinksByRawName_FindsUnresolvedLinksToName(t *testing.T) {
	v := testutil.NewTestVault(t)

	// "Source" links to a bare name "target" that is NOT yet a document, so the
	// link resolves to target_id NULL.
	src := testutil.CreateAndIndex(t, v, "Source", "note", "Points at [[target]] which does not exist yet.")
	// An unrelated note that links to something else entirely.
	testutil.CreateAndIndex(t, v, "Other", "note", "Links to [[somewhere-else]].")

	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	// The eventual path of the target note would be "target.md"; LinksByRawName
	// matches on the basename form.
	refs, err := v.DB.LinksByRawName("target.md")
	if err != nil {
		t.Fatalf("LinksByRawName: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want 1: %+v", len(refs), refs)
	}
	if refs[0].Path != src.Path {
		t.Errorf("source path: got %q, want %q", refs[0].Path, src.Path)
	}
	if refs[0].Resolved {
		t.Errorf("LinksByRawName must return only unresolved links")
	}
	if refs[0].TargetRaw != "target" {
		t.Errorf("target_raw: got %q, want %q", refs[0].TargetRaw, "target")
	}
}

func TestLinksByRawName_MatchesPathSuffixForm(t *testing.T) {
	v := testutil.NewTestVault(t)

	// A link written as a path suffix [[b/target]] to a not-yet-existing note
	// whose eventual full path is a/b/target.md. The suffix b/target is one of
	// that path's resolvable forms, so it must match.
	testutil.CreateAndIndex(t, v, "Ref", "note", "Suffix link [[b/target]] here.")
	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	refs, err := v.DB.LinksByRawName("a/b/target.md")
	if err != nil {
		t.Fatalf("LinksByRawName: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("path-suffix match: got %d refs, want 1: %+v", len(refs), refs)
	}
	if refs[0].TargetRaw != "b/target" {
		t.Errorf("target_raw: got %q, want %q", refs[0].TargetRaw, "b/target")
	}

	// A link whose path form is NOT a suffix of the moved doc's path must not
	// match (a different note that merely shares the basename "target").
	noMatch, err := v.DB.LinksByRawName("c/d/other.md")
	if err != nil {
		t.Fatalf("LinksByRawName no-match: %v", err)
	}
	if len(noMatch) != 0 {
		t.Errorf("unrelated path should not match the suffix link: %+v", noMatch)
	}
}

func TestLinksByRawName_IgnoresResolvedLinks(t *testing.T) {
	v := testutil.NewTestVault(t)

	// "Target" exists, so a link to it resolves and must NOT appear in the
	// unresolved-by-name set.
	testutil.CreateAndIndex(t, v, "Target", "note", "I exist.")
	testutil.CreateAndIndex(t, v, "Linker", "note", "Links to [[Target]].")
	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	refs, err := v.DB.LinksByRawName("Target.md")
	if err != nil {
		t.Fatalf("LinksByRawName: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("resolved links must be excluded; got %+v", refs)
	}
}
