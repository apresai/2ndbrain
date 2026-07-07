package polish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

// writeUnindexedNote writes a markdown note directly to disk WITHOUT touching
// the index DB, simulating a note created in Obsidian before any reindex.
func writeUnindexedNote(t *testing.T, v *vault.Vault, relPath, title, body string) {
	t.Helper()
	abs := filepath.Join(v.Root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	content := "---\ntitle: " + title + "\ntype: note\nstatus: draft\n---\n\n# " + title + "\n\n" + body + "\n"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

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

// Reproduces the real-world dead-end: a note whose only resolvable form is its
// hyphenated basename (no frontmatter title), linked with the spaced display
// form. 2nb's resolver is case- AND separator-sensitive, so the link is broken;
// before the normalizeName hyphen/underscore fold the spaced target never
// matched the kebab basename, so repair reported no_match and the GUI
// dead-ended. After the fold it repairs to the basename.
func TestRepairBrokenLinks_RepairsHyphenSpaceDriftToKebabBasename(t *testing.T) {
	v := testutil.NewTestVault(t)
	// Write the target note directly to disk WITHOUT a frontmatter title so
	// ONLY the hyphenated basename remains as a resolvable form. Repair reads
	// the live filesystem, so the title-less state must exist on disk;
	// testutil.CreateAndIndex always writes a title, which would bridge the
	// spaced target and mask the hyphen-vs-space drift.
	abs := filepath.Join(v.Root, "claude-code-skills-reference-and-index.md")
	if err := os.WriteFile(abs, []byte("---\ntype: note\nstatus: draft\n---\n\nReference and index.\n"), 0o644); err != nil {
		t.Fatalf("write title-less target: %v", err)
	}

	src := testutil.CreateAndIndex(t, v, "Src", "note",
		note("Src", "Run the MCP server inside [[Claude Code Skills Reference and Index]] or Cursor.\n"))

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}
	if len(res.Repaired) != 1 || res.Repaired[0].Raw != "Claude Code Skills Reference and Index" ||
		res.Repaired[0].NewTarget != "claude-code-skills-reference-and-index" {
		t.Fatalf("expected spaced->kebab repair to the basename, got repaired=%+v skipped=%+v", res.Repaired, res.Skipped)
	}
	if !strings.Contains(res.Body, "[[claude-code-skills-reference-and-index]]") {
		t.Fatalf("body not rewritten to the kebab basename: %q", res.Body)
	}
}

// Stale-DB direction 1: a note added on disk after the last index (created in
// Obsidian, not yet reindexed) must be visible to repair. The DB-backed repair
// index used to skip a fixable case-drift link to it as no_match while lint
// (which walks the live filesystem) reported it repairable.
func TestRepairBrokenLinks_SeesNoteAddedAfterIndex(t *testing.T) {
	v := testutil.NewTestVault(t)
	src := testutil.CreateAndIndex(t, v, "Source Doc", "note",
		note("Source Doc", "See [[auth flow]] and [[Auth Flow]].\n"))

	// The target exists ONLY on disk; the DB has no row for it.
	writeUnindexedNote(t, v, "auth-flow.md", "Auth Flow", "How auth works.")

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}
	if len(res.Repaired) != 1 || res.Repaired[0].Raw != "auth flow" || res.Repaired[0].NewTarget != "Auth Flow" {
		t.Fatalf("expected the unindexed note to repair auth flow -> Auth Flow, got repaired=%+v skipped=%+v",
			res.Repaired, res.Skipped)
	}
	if !strings.Contains(res.Body, "[[Auth Flow]]") {
		t.Fatalf("repaired body missing [[Auth Flow]]: %q", res.Body)
	}
	// [[Auth Flow]] resolves against the live filesystem (title match), so it
	// is neither repaired nor reported broken, matching what lint says. The
	// DB-backed already-resolves check used to report it no_match noise.
	if len(res.Skipped) != 0 {
		t.Fatalf("a link resolving on disk must not be reported, got %+v", res.Skipped)
	}
}

// Stale-DB direction 2: a note deleted from disk but still present in the DB
// (no reindex since) must NOT be offered as a repair target. The DB-backed
// repair index used to "fix" a link to that ghost, contradicting lint, which
// reports the link broken because the file is gone.
func TestRepairBrokenLinks_IgnoresNoteDeletedFromDisk(t *testing.T) {
	v := testutil.NewTestVault(t)
	ghost := testutil.CreateAndIndex(t, v, "Auth Flow", "note", note("Auth Flow", "How auth works."))
	src := testutil.CreateAndIndex(t, v, "Src", "note", note("Src", "See [[auth flow]].\n"))

	// Delete the target from disk; its row remains in the DB.
	if err := os.Remove(filepath.Join(v.Root, ghost.Path)); err != nil {
		t.Fatalf("remove ghost note: %v", err)
	}

	res, err := RepairBrokenLinks(v, src.Body)
	if err != nil {
		t.Fatalf("RepairBrokenLinks: %v", err)
	}
	if len(res.Repaired) != 0 {
		t.Fatalf("a note deleted on disk must never be a repair target, got %+v", res.Repaired)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Raw != "auth flow" || res.Skipped[0].Reason != "no_match" {
		t.Fatalf("expected one no_match skip for the ghost target, got %+v", res.Skipped)
	}
	if !strings.Contains(res.Body, "[[auth flow]]") {
		t.Fatalf("body must be left untouched: %q", res.Body)
	}
}

// normalizeName is the symmetric chokepoint that makes case, hyphen/underscore,
// and whitespace drift collide on one key. Distinct names must stay distinct so
// the fold never widens a match into a wrong rewrite.
func TestNormalizeName_FoldsSeparatorsCaseAndWhitespace(t *testing.T) {
	// Each group's members must normalize to the SAME key.
	same := [][]string{
		{"claude-code-skills-reference-and-index", "Claude Code Skills Reference and Index", "claude_code_skills_reference_and_index"},
		{"go-modules", "go modules", "Go_Modules", "  go   modules  "},
		{"auth-flow", "Auth Flow", "AUTH FLOW"},
	}
	for _, group := range same {
		want := normalizeName(group[0])
		for _, s := range group[1:] {
			if got := normalizeName(s); got != want {
				t.Errorf("normalizeName(%q)=%q, want %q (same group as %q)", s, got, want, group[0])
			}
		}
	}
	// Genuinely different names must NOT collapse together.
	diff := [][2]string{
		{"go-modules", "go-mod-why"},
		{"claude-code", "claude code review"},
		{"auth-flow", "auth flows"},
	}
	for _, pair := range diff {
		if normalizeName(pair[0]) == normalizeName(pair[1]) {
			t.Errorf("normalizeName collapsed distinct names %q and %q to %q", pair[0], pair[1], normalizeName(pair[0]))
		}
	}
}
