package polish

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestRepairBrokenLinks_RepairsCaseDriftLeavesRestAlone(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Flow", "note", note("Auth Flow", "How auth works."))
	testutil.CreateAndIndex(t, v, "JWT Tokens", "note", note("JWT Tokens", "Token details."))

	// [[auth flow]] is broken in 2nb (case-sensitive resolver: neither the title
	// "Auth Flow" nor the basename "auth-flow" matches "auth flow"), though it
	// works in Obsidian. [[JWT Tokens]] resolves. [[Nonexistent Topic]] has no
	// match. ![[diagram.png]] is an asset embed, not a note link.
	src := testutil.CreateAndIndex(t, v, "Source Doc", "note",
		note("Source Doc", "See [[auth flow]] and [[JWT Tokens]].\n\nAlso [[Nonexistent Topic]] and ![[diagram.png]].\n"))

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}

	if len(res.Repaired) != 1 || res.Repaired[0].Raw != "auth flow" || res.Repaired[0].NewTarget != "Auth Flow" {
		t.Fatalf("expected one repair auth flow -> Auth Flow, got %+v", res.Repaired)
	}
	if !strings.Contains(res.Body, "[[Auth Flow]]") {
		t.Fatalf("repaired body missing [[Auth Flow]]: %q", res.Body)
	}
	if strings.Contains(res.Body, "[[auth flow]]") {
		t.Fatalf("repaired body still has broken [[auth flow]]: %q", res.Body)
	}
	// A resolving link and an asset embed are left untouched.
	if !strings.Contains(res.Body, "[[JWT Tokens]]") {
		t.Fatalf("resolving link [[JWT Tokens]] was altered: %q", res.Body)
	}
	if !strings.Contains(res.Body, "![[diagram.png]]") {
		t.Fatalf("asset embed ![[diagram.png]] was altered: %q", res.Body)
	}
	// An unmatched target is reported, never guessed.
	if len(res.Skipped) != 1 || res.Skipped[0].Raw != "Nonexistent Topic" || res.Skipped[0].Reason != "no_match" {
		t.Fatalf("expected one no_match skip for Nonexistent Topic, got %+v", res.Skipped)
	}
}

func TestRepairBrokenLinks_PreservesHeadingAndAliasSuffix(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Flow", "note", note("Auth Flow", "How auth works."))

	src := testutil.CreateAndIndex(t, v, "Src", "note",
		note("Src", "Jump to [[auth flow#Setup|the setup]] please.\n"))

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}
	// The target is repaired but the #heading and |alias suffix are preserved.
	if !strings.Contains(res.Body, "[[Auth Flow#Setup|the setup]]") {
		t.Fatalf("repaired link lost its #heading/|alias suffix: %q", res.Body)
	}
}

func TestRepairBrokenLinks_NoBrokenLinksIsNoop(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Flow", "note", note("Auth Flow", "x"))
	src := testutil.CreateAndIndex(t, v, "Src", "note", note("Src", "See [[Auth Flow]].\n"))

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}
	if len(res.Repaired) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("expected no repairs/skips for a clean doc, got repaired=%+v skipped=%+v", res.Repaired, res.Skipped)
	}
	if res.Body != src.Body {
		t.Fatalf("body changed on a no-op repair")
	}
}

// A path-qualified broken target must NOT be retargeted to a note that merely
// shares the basename, even when that basename is unique. This locks the
// never-wrong-retarget rule for path-form links (Obsidian doesn't resolve them
// by leaf either), so it is reported, not silently repaired.
func TestRepairBrokenLinks_PathQualifiedTargetIsNotRetargetedByBasename(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Auth Flow", "note", note("Auth Flow", "x"))

	src := testutil.CreateAndIndex(t, v, "Src", "note",
		note("Src", "See [[old/folder/auth flow]].\n"))

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}
	if len(res.Repaired) != 0 {
		t.Fatalf("path-qualified target must not be repaired, got %+v", res.Repaired)
	}
	if !strings.Contains(res.Body, "[[old/folder/auth flow]]") {
		t.Fatalf("path-qualified link should be left untouched: %q", res.Body)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "no_match" {
		t.Fatalf("expected the path-qualified target reported as no_match, got %+v", res.Skipped)
	}
}

// When a broken bare name normalizes to more than one distinct note, repair must
// refuse (ambiguous), never pick one.
func TestRepairBrokenLinks_AmbiguousNameIsSkipped(t *testing.T) {
	v := testutil.NewTestVault(t)
	// Two notes whose titles normalize to "my plan" (case differs). Their slugs
	// collide on "my-plan", so the second dedupes to a distinct basename — giving
	// two distinct unambiguous canonical targets under the normalized key.
	testutil.CreateAndIndex(t, v, "My Plan", "note", note("My Plan", "a"))
	testutil.CreateAndIndex(t, v, "MY PLAN", "note", note("MY PLAN", "b"))

	// Double space makes the bare target resolve to neither title exactly, so it
	// is broken, while normalizing to the shared "my plan" key.
	src := testutil.CreateAndIndex(t, v, "Src", "note", note("Src", "See [[My  Plan]].\n"))

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}
	if len(res.Repaired) != 0 {
		t.Fatalf("ambiguous name must not be repaired, got %+v", res.Repaired)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "ambiguous" {
		t.Fatalf("expected one ambiguous skip, got %+v", res.Skipped)
	}
}
