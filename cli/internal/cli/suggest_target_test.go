package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// suggest-target's drift and keyword tiers are AI-free, so these tests run
// offline (the semantic tier skips silently when no embedder is configured).

// seedSuggestVault creates a source note whose body is heavily about "ghostty"
// and contains the broken [[ghostty]] link, plus a separate note the link
// plausibly meant. Returns the vault root and the source's vault-relative path.
func seedSuggestVault(t *testing.T) (root, source string) {
	t.Helper()
	_, root = newContractVault(t)
	writeNote(t, root, "Ghostty Config.md", "Ghostty Config",
		"# Ghostty Config\n\nGhostty terminal configuration reference.")
	source = "ghostty-matrix-theme.md"
	writeNote(t, root, source, "Ghostty Matrix Theme",
		"# Ghostty Matrix Theme\n\nGhostty theming guide: ghostty colors, ghostty fonts, ghostty keybinds. See [[ghostty]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	return root, source
}

func suggestPaths(t *testing.T, root string, argv ...string) []string {
	t.Helper()
	out, err := runCLIArgs(t, root, append([]string{"suggest-target"}, argv...)...)
	if err != nil {
		t.Fatalf("suggest-target: %v\n%s", err, out)
	}
	var results []SuggestLinkResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	paths := make([]string, 0, len(results))
	for _, r := range results {
		paths = append(paths, r.Path)
	}
	return paths
}

// Regression guard for the bug's existence: without --source, the note
// containing the broken link surfaces as a candidate for its own fix (its
// body is the strongest BM25 match for the target words).
func TestSuggestTarget_WithoutSourceIncludesSourceNote(t *testing.T) {
	root, source := seedSuggestVault(t)
	paths := suggestPaths(t, root, "ghostty", "--json", "--porcelain")
	if !containsString(paths, source) {
		t.Errorf("without --source the source note should appear (the bug this flag fixes): %v", paths)
	}
}

func TestSuggestTarget_SourceExcludedFromAllTiers(t *testing.T) {
	root, source := seedSuggestVault(t)
	paths := suggestPaths(t, root, "ghostty", "--source", source, "--json", "--porcelain")
	if containsString(paths, source) {
		t.Errorf("--source note must never be offered as its own fix: %v", paths)
	}
	if !containsString(paths, "Ghostty Config.md") {
		t.Errorf("other candidates should remain: %v", paths)
	}
}

// A --source that resolves to nothing (e.g. the note was just deleted) must
// not error the command; the exclusion falls back to the cleaned raw path.
func TestSuggestTarget_UnresolvableSourceDoesNotError(t *testing.T) {
	root, _ := seedSuggestVault(t)
	paths := suggestPaths(t, root, "ghostty", "--source", "./no-such-note.md", "--json", "--porcelain")
	if len(paths) == 0 {
		t.Errorf("candidates should still be returned for an unresolvable --source")
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// The drift tier (polish.SuggestRepairTargets) is where a case-drifted
// self-link surfaces: a note titled "Ghostty" is the unique normalized match
// for the broken [[ghostty]] inside that same note (the resolver is
// case-sensitive, so the link IS broken). The exclusion must drop it there
// too, not only in the BM25 tier.
func TestSuggestTarget_SourceExcludedFromDriftTier(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "ghostty.md", "Ghostty",
		"# Ghostty\n\nSelf-referential case drift: see [[ghostty]].")
	writeNote(t, root, "Ghostty Config.md", "Ghostty Config",
		"# Ghostty Config\n\nGhostty terminal configuration reference.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	paths := suggestPaths(t, root, "ghostty", "--json", "--porcelain")
	if !containsString(paths, "ghostty.md") {
		t.Fatalf("precondition: without --source the drift tier should offer the note itself: %v", paths)
	}
	paths = suggestPaths(t, root, "ghostty", "--source", "ghostty.md", "--json", "--porcelain")
	if containsString(paths, "ghostty.md") {
		t.Errorf("drift tier must not offer the --source note as its own fix: %v", paths)
	}
}

// The drift tier resolves against the LIVE FILESYSTEM, not the index DB, so a
// note created on disk after the last index (e.g. in Obsidian, before any
// reindex) still surfaces as a "did you mean?" candidate. The DB-backed tier
// used to miss it entirely.
func TestSuggestTarget_DriftTierSeesUnindexedNote(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "some-note.md", "Some Note",
		"# Some Note\n\nSee [[ghostty config]].")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	// Created AFTER the index run; it exists only on disk.
	writeNote(t, root, "Ghostty Config.md", "Ghostty Config",
		"# Ghostty Config\n\nGhostty terminal configuration reference.")

	paths := suggestPaths(t, root, "ghostty config", "--json", "--porcelain")
	if !containsString(paths, "Ghostty Config.md") {
		t.Errorf("drift tier must surface the unindexed on-disk note: %v", paths)
	}
}

// An AMBIGUOUS --source (two notes share the basename, no exact file match)
// is the case that actually reaches the cleaned-raw-path fallback: auto-mode
// resolution returns an AmbiguousTargetError, the error is logged at debug,
// and the command still runs.
func TestSuggestTarget_AmbiguousSourceFallsBackWithoutError(t *testing.T) {
	root, _ := seedSuggestVault(t)
	writeNote(t, root, "one/dup.md", "Dup One", "# Dup One\n\nFirst duplicate.")
	writeNote(t, root, "two/dup.md", "Dup Two", "# Dup Two\n\nSecond duplicate.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	paths := suggestPaths(t, root, "ghostty", "--source", "dup", "--json", "--porcelain")
	if len(paths) == 0 {
		t.Errorf("candidates should still be returned when --source is ambiguous")
	}
}

// Confidence tests. All offline: candidates come from the drift and BM25 tiers
// (the semantic tier skips silently without an embedder), and the grading rule
// is deterministic (see SuggestLinkResult.Confidence).

func suggestResults(t *testing.T, root string, argv ...string) []SuggestLinkResult {
	t.Helper()
	out, err := runCLIArgs(t, root, append([]string{"suggest-target"}, argv...)...)
	if err != nil {
		t.Fatalf("suggest-target: %v\n%s", err, out)
	}
	var results []SuggestLinkResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	return results
}

func confidenceOf(t *testing.T, results []SuggestLinkResult, path string) string {
	t.Helper()
	for _, r := range results {
		if r.Path == path {
			return r.Confidence
		}
	}
	t.Fatalf("expected %s among candidates: %+v", path, results)
	return ""
}

// Whole-word subset + dominance (sole candidate): target "ghostty" against the
// single note "Ghostty Config" must grade high.
func TestSuggestTarget_ConfidenceHigh_SubsetAndDominant(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "Ghostty Config.md", "Ghostty Config",
		"# Ghostty Config\n\nGhostty terminal configuration reference.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	results := suggestResults(t, root, "ghostty", "--json", "--porcelain")
	if got := confidenceOf(t, results, "Ghostty Config.md"); got != "high" {
		t.Errorf("subset + dominant must be high, got %q: %+v", got, results)
	}
}

// Two near-equal-score candidates both containing the target word: word match
// without dominance grades medium on both.
func TestSuggestTarget_ConfidenceMedium_WordMatchWithoutDominance(t *testing.T) {
	_, root := newContractVault(t)
	// Identically shaped bodies so the BM25 scores stay well inside the 1.4x
	// dominance ratio.
	writeNote(t, root, "Ghostty Config.md", "Ghostty Config",
		"# Ghostty Config\n\nGhostty terminal configuration reference.")
	writeNote(t, root, "Ghostty Themes.md", "Ghostty Themes",
		"# Ghostty Themes\n\nGhostty terminal theming reference.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	results := suggestResults(t, root, "ghostty", "--json", "--porcelain")
	for _, path := range []string{"Ghostty Config.md", "Ghostty Themes.md"} {
		if got := confidenceOf(t, results, path); got != "medium" {
			t.Errorf("%s: word match without dominance must be medium, got %q: %+v", path, got, results)
		}
	}
}

// A candidate that matches only via BM25 body text (no word overlap with its
// title or basename) and is not dominant grades low.
func TestSuggestTarget_ConfidenceLow_NoWordOverlapNotDominant(t *testing.T) {
	_, root := newContractVault(t)
	writeNote(t, root, "Ghostty Config.md", "Ghostty Config",
		"# Ghostty Config\n\nGhostty configuration: ghostty fonts, ghostty colors, ghostty keybinds.")
	writeNote(t, root, "Terminal Emulators.md", "Terminal Emulators",
		"# Terminal Emulators\n\nComparison of terminal emulators. One option is ghostty.")
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	results := suggestResults(t, root, "ghostty", "--json", "--porcelain")
	if got := confidenceOf(t, results, "Terminal Emulators.md"); got != "low" {
		t.Errorf("body-only match without dominance must be low, got %q: %+v", got, results)
	}
}

// A unique tier-1 drift match is inherently high even when the general rule
// would grade it lower: here the target matches only via a frontmatter ALIAS,
// so the title/basename word check fails, and without the drift pin the sole
// candidate would grade medium (dominant only).
func TestSuggestTarget_ConfidenceHigh_UniqueDriftViaAlias(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "Ghostty Config.md"),
		[]byte("---\nid: gc1\ntitle: Ghostty Config\naliases:\n  - gt\ntype: note\nstatus: draft\n---\n\n# Ghostty Config\n\nTerminal configuration reference.\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	results := suggestResults(t, root, "gt", "--json", "--porcelain")
	if got := confidenceOf(t, results, "Ghostty Config.md"); got != "high" {
		t.Errorf("the unique drift (alias) match must be high, got %q: %+v", got, results)
	}
}
