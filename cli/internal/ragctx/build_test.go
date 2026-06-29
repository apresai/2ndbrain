package ragctx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/search"
)

func writeNote(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return name // vault-relative path
}

func TestBuild_WholeNoteWhenItFits(t *testing.T) {
	dir := t.TempDir()
	body := "# Title\n\n## A\nalpha SENT_A here\n\n## B\nbeta SENT_B here\n\n## C\ngamma SENT_C here\n"
	p := writeNote(t, dir, "note.md", body)

	chunks, warns := Build([]search.Result{{Path: p, Title: "Note"}}, dir, Budget{})
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	for _, s := range []string{"SENT_A", "SENT_B", "SENT_C"} {
		if !strings.Contains(chunks[0].Content, s) {
			t.Errorf("whole-note content missing %q", s)
		}
	}
	if strings.Contains(chunks[0].Content, "...") {
		t.Errorf("a note that fits should not be elided")
	}
}

// longNote builds a multi-section note (Intro ... Deep) and returns the rel path
// plus the heading paths of the Intro and Deep sections (discovered via the same
// chunker Build uses, so the test isn't coupled to the heading-path format).
func longNote(t *testing.T, dir string) (relPath, introHP, deepHP string) {
	t.Helper()
	pad := strings.Repeat("lorem ipsum dolor sit amet ", 30) // ~810 runes/section
	var b strings.Builder
	b.WriteString("# Doc\n\n## Intro\nINTROXYZ " + pad + "\n\n")
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&b, "## Mid%d\nmid%d %s\n\n", i, i, pad)
	}
	b.WriteString("## Deep\nDEEPXYZ " + pad + "\n")
	relPath = writeNote(t, dir, "long.md", b.String())

	doc, err := document.ParseFile(filepath.Join(dir, relPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range document.ChunkDocument(doc) {
		switch {
		case strings.Contains(c.Content, "INTROXYZ"):
			introHP = c.HeadingPath
		case strings.Contains(c.Content, "DEEPXYZ"):
			deepHP = c.HeadingPath
		}
	}
	if introHP == "" || deepHP == "" {
		t.Fatalf("could not locate section headings (intro=%q deep=%q)", introHP, deepHP)
	}
	return relPath, introHP, deepHP
}

func TestBuild_WindowsLongNoteAroundDeepSection(t *testing.T) {
	dir := t.TempDir()
	p, _, deepHP := longNote(t, dir)

	// Budget too small for the whole ~5-section note, large enough for a couple.
	chunks, _ := Build([]search.Result{{Path: p, Title: "Doc", HeadingPath: deepHP}}, dir,
		Budget{NoteRunes: 1800, TotalRunes: 60000})
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	c := chunks[0].Content
	if !strings.Contains(c, "DEEPXYZ") {
		t.Errorf("window must include the deep matched section")
	}
	if strings.Contains(c, "INTROXYZ") {
		t.Errorf("window should have elided the note head (INTROXYZ leaked)")
	}
	if !strings.Contains(c, "...") {
		t.Errorf("a windowed note should carry an elision marker")
	}
}

func TestBuild_HeadFallbackWhenHeadingUnknown(t *testing.T) {
	dir := t.TempDir()
	p, _, _ := longNote(t, dir)

	// Empty HeadingPath (e.g. a brute-force vector-only hit) → window from head.
	chunks, _ := Build([]search.Result{{Path: p, Title: "Doc", HeadingPath: ""}}, dir,
		Budget{NoteRunes: 1500, TotalRunes: 60000})
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	c := chunks[0].Content
	if !strings.Contains(c, "INTROXYZ") {
		t.Errorf("head fallback should include the note head (INTROXYZ)")
	}
	if !strings.Contains(c, "...") {
		t.Errorf("a truncated head window should carry an elision marker")
	}
}

func TestBuild_TotalBudgetStops(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("padding word ", 200) // ~2600 runes
	var results []search.Result
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("n%d.md", i)
		writeNote(t, dir, name, "# N\n\n"+big)
		results = append(results, search.Result{Path: name, Title: name})
	}
	chunks, _ := Build(results, dir, Budget{TotalRunes: 3000, NoteRunes: 20000})
	if len(chunks) >= 3 {
		t.Errorf("total budget should have stopped before all 3 notes, got %d", len(chunks))
	}
	if len(chunks) == 0 {
		t.Errorf("expected at least one note within budget")
	}
}

func TestBuild_MaxNotesCap(t *testing.T) {
	dir := t.TempDir()
	a := writeNote(t, dir, "a.md", "# A\n\nshort a")
	b := writeNote(t, dir, "b.md", "# B\n\nshort b")
	chunks, _ := Build([]search.Result{{Path: a}, {Path: b}}, dir, Budget{MaxNotes: 1})
	if len(chunks) != 1 {
		t.Fatalf("MaxNotes=1 → chunks = %d, want 1", len(chunks))
	}
}

func TestBuild_DedupByPath(t *testing.T) {
	dir := t.TempDir()
	p := writeNote(t, dir, "dup.md", "# D\n\nbody")
	chunks, _ := Build([]search.Result{{Path: p}, {Path: p}}, dir, Budget{})
	if len(chunks) != 1 {
		t.Fatalf("dedup by path → chunks = %d, want 1", len(chunks))
	}
}

func TestBuild_UnreadableSourceWarnsAndSkips(t *testing.T) {
	dir := t.TempDir()
	chunks, warns := Build([]search.Result{{Path: "does-not-exist.md"}}, dir, Budget{})
	if len(chunks) != 0 {
		t.Errorf("unreadable source should yield no chunk, got %d", len(chunks))
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "unreadable") {
		t.Errorf("expected one 'unreadable' warning, got %v", warns)
	}
}

func TestBuild_SkipsEmptyNote(t *testing.T) {
	dir := t.TempDir()
	p := writeNote(t, dir, "empty.md", "   \n\t\n  ")
	chunks, warns := Build([]search.Result{{Path: p}}, dir, Budget{})
	if len(chunks) != 0 {
		t.Errorf("empty note should be skipped, got %d chunks", len(chunks))
	}
	if len(warns) != 0 {
		t.Errorf("empty note is a silent skip, not a warning; got %v", warns)
	}
}

func TestBuild_CanvasUsesSyntheticBody(t *testing.T) {
	dir := t.TempDir()
	canvas := `{"nodes":[{"id":"n1","type":"text","text":"Core auth strategy","x":0,"y":0,"width":200,"height":100}],"edges":[]}`
	p := writeNote(t, dir, "board.canvas", canvas)
	chunks, _ := Build([]search.Result{{Path: p, Title: "Board"}}, dir, Budget{})
	if len(chunks) != 1 {
		t.Fatalf("canvas → chunks = %d, want 1", len(chunks))
	}
	if !strings.Contains(chunks[0].Content, "Core auth strategy") {
		t.Errorf("canvas content should be the synthetic markdown body, got %q", chunks[0].Content)
	}
	if strings.HasPrefix(strings.TrimSpace(chunks[0].Content), "{") {
		t.Errorf("canvas content must NOT be raw JSON")
	}
}
