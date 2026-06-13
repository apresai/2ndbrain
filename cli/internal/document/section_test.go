package document

import (
	"strings"
	"testing"
)

func TestSectionBounds_TopLevel(t *testing.T) {
	body := "# Alpha\nalpha line 1\nalpha line 2\n# Beta\nbeta line\n"
	lines := strings.Split(body, "\n")

	start, end, ok := SectionBounds(body, "Alpha")
	if !ok {
		t.Fatalf("expected Alpha to be found")
	}
	// Heading at line 0; content lines 1..2; next heading "# Beta" at line 3.
	if start != 1 || end != 3 {
		t.Fatalf("Alpha bounds = (%d,%d), want (1,3)", start, end)
	}
	got := strings.Join(lines[start:end], "\n")
	if got != "alpha line 1\nalpha line 2" {
		t.Fatalf("Alpha content = %q", got)
	}
}

func TestSectionBounds_Nested(t *testing.T) {
	// A nested subsection's bounds must stop at the next sibling/parent heading.
	body := strings.Join([]string{
		"# Top",          // 0
		"top content",    // 1
		"## Child A",     // 2
		"child a line",   // 3
		"### Grandchild", // 4
		"gc line",        // 5
		"## Child B",     // 6
		"child b line",   // 7
	}, "\n")

	// Child A (level 2) ends at Child B (level 2), so it INCLUDES the
	// grandchild lines (deeper headings do not terminate the section).
	start, end, ok := SectionBounds(body, "## Child A")
	if !ok {
		t.Fatalf("expected Child A found")
	}
	if start != 3 || end != 6 {
		t.Fatalf("Child A bounds = (%d,%d), want (3,6)", start, end)
	}

	// Grandchild (level 3) ends at Child B (level 2, shallower).
	start, end, ok = SectionBounds(body, "Grandchild")
	if !ok {
		t.Fatalf("expected Grandchild found")
	}
	if start != 5 || end != 6 {
		t.Fatalf("Grandchild bounds = (%d,%d), want (5,6)", start, end)
	}

	// Top (level 1) runs to EOF since nothing is shallower-or-equal after it.
	lines := strings.Split(body, "\n")
	start, end, ok = SectionBounds(body, "Top")
	if !ok {
		t.Fatalf("expected Top found")
	}
	if start != 1 || end != len(lines) {
		t.Fatalf("Top bounds = (%d,%d), want (1,%d)", start, end, len(lines))
	}
}

func TestSectionBounds_DuplicateHeadingFirstWins(t *testing.T) {
	body := strings.Join([]string{
		"## Notes", // 0
		"first",    // 1
		"## Other", // 2
		"x",        // 3
		"## Notes", // 4 (duplicate)
		"second",   // 5
	}, "\n")

	start, end, ok := SectionBounds(body, "Notes")
	if !ok {
		t.Fatalf("expected Notes found")
	}
	// First "## Notes" at line 0; content line 1; ends at "## Other" line 2.
	if start != 1 || end != 2 {
		t.Fatalf("Notes bounds = (%d,%d), want (1,2)", start, end)
	}
	lines := strings.Split(body, "\n")
	if got := strings.Join(lines[start:end], "\n"); got != "first" {
		t.Fatalf("first-match content = %q, want %q", got, "first")
	}
}

func TestSectionBounds_PreambleNotMatched(t *testing.T) {
	// Text before the first heading is the preamble; SectionBounds matches
	// real headings only, so a preamble query returns not-found.
	body := "intro text\nmore intro\n# Real\ncontent\n"
	if _, _, ok := SectionBounds(body, "intro text"); ok {
		t.Fatalf("preamble text should not match as a heading")
	}
	if _, _, ok := SectionBounds(body, "(preamble)"); ok {
		t.Fatalf("(preamble) sentinel is not a heading and must not match")
	}
}

func TestSectionBounds_LastSectionToEOF(t *testing.T) {
	body := "# One\na\n# Two\nb\nc\n"
	lines := strings.Split(body, "\n")
	start, end, ok := SectionBounds(body, "Two")
	if !ok {
		t.Fatalf("expected Two found")
	}
	if start != 3 || end != len(lines) {
		t.Fatalf("Two bounds = (%d,%d), want (3,%d)", start, end, len(lines))
	}
}

func TestSectionBounds_EmptySection(t *testing.T) {
	// A heading immediately followed by a sibling heading has empty content
	// (start == end).
	body := "## A\n## B\nb content\n"
	start, end, ok := SectionBounds(body, "A")
	if !ok {
		t.Fatalf("expected A found")
	}
	if start != 1 || end != 1 {
		t.Fatalf("empty section A bounds = (%d,%d), want (1,1)", start, end)
	}
}

func TestSectionBounds_NotFound(t *testing.T) {
	body := "# Alpha\nx\n"
	if _, _, ok := SectionBounds(body, "Missing"); ok {
		t.Fatalf("expected not-found for missing heading")
	}
}

func TestReplaceSection_SwapsContentKeepsSiblings(t *testing.T) {
	body := strings.Join([]string{
		"# Title",
		"",
		"## Decision",
		"old decision text",
		"",
		"## Consequences",
		"consequence one",
		"",
	}, "\n")

	out, ok := ReplaceSection(body, "Decision", "We chose plan B.")
	if !ok {
		t.Fatalf("expected ReplaceSection to find Decision")
	}
	if !strings.Contains(out, "We chose plan B.") {
		t.Fatalf("new content missing:\n%s", out)
	}
	if strings.Contains(out, "old decision text") {
		t.Fatalf("old content not removed:\n%s", out)
	}
	// Sibling section is untouched.
	if !strings.Contains(out, "## Consequences") || !strings.Contains(out, "consequence one") {
		t.Fatalf("sibling section damaged:\n%s", out)
	}
	// Heading line preserved.
	if !strings.Contains(out, "## Decision") {
		t.Fatalf("heading line removed:\n%s", out)
	}
}

func TestReplaceSection_NotFoundReturnsOriginal(t *testing.T) {
	body := "# A\nbody\n"
	out, ok := ReplaceSection(body, "Nope", "x")
	if ok {
		t.Fatalf("expected not-found")
	}
	if out != body {
		t.Fatalf("body should be unchanged on miss")
	}
}

func TestReplaceSection_PreservesComment(t *testing.T) {
	// %% comments %% inside a sibling section must survive a replace of another
	// section, because SectionBounds operates on the raw body.
	body := strings.Join([]string{
		"## Keep",
		"%% private note %%",
		"visible",
		"## Edit",
		"old",
	}, "\n")
	out, ok := ReplaceSection(body, "Edit", "new")
	if !ok {
		t.Fatalf("expected Edit found")
	}
	if !strings.Contains(out, "%% private note %%") {
		t.Fatalf("comment in sibling section lost:\n%s", out)
	}
	if !strings.Contains(out, "new") || strings.Contains(out, "old") {
		t.Fatalf("edit section not swapped:\n%s", out)
	}
}
