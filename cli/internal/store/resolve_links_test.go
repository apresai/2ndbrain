package store

import (
	"database/sql"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// TestResolveLinks_TitleAndSuffixBranches exercises the shortest-unique multi-
// segment suffix branch and the title-match branch, which the broader
// Obsidian-native test resolves via exact-path/alias instead.
func TestResolveLinks_TitleAndSuffixBranches(t *testing.T) {
	db := openTestDB(t)

	d1 := &document.Document{
		ID: "d1", Path: "a/b/deep.md", Title: "Deep Title",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	d2 := &document.Document{
		ID: "d2", Path: "root.md", Title: "Root",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	if err := db.UpsertDocument(d1); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDocument(d2); err != nil {
		t.Fatal(err)
	}

	if err := db.UpsertLinks("d2", []document.WikiLink{
		{Target: "b/deep"},     // multi-segment suffix → d1 (suffix branch)
		{Target: "Deep Title"}, // title → d1 (title branch)
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveLinks(); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	for _, raw := range []string{"b/deep", "Deep Title"} {
		var target sql.NullString
		if err := db.conn.QueryRow(
			"SELECT target_id FROM links WHERE source_id='d2' AND target_raw=?", raw,
		).Scan(&target); err != nil {
			t.Fatalf("query %q: %v", raw, err)
		}
		if target.String != "d1" {
			t.Errorf("link %q resolved to %q, want d1", raw, target.String)
		}
	}
}

// TestResolveLinks_PercentEncodedMarkdownTarget pins the decode retry: an
// Obsidian-generated markdown link target like "My%20Note.md" resolves to the
// spaced note, a malformed escape stays unresolved, and a literal-% filename
// keeps raw-first precedence over the decoded form.
func TestResolveLinks_PercentEncodedMarkdownTarget(t *testing.T) {
	db := openTestDB(t)

	spaced := &document.Document{
		ID: "sp", Path: "My Note.md", Title: "My Note",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	src := &document.Document{
		ID: "src", Path: "src.md", Title: "Source",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	for _, d := range []*document.Document{spaced, src} {
		if err := db.UpsertDocument(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertLinks("src", []document.WikiLink{
		{Target: "My%20Note.md"}, // decodes to the spaced note
		{Target: "My%ZGNote.md"}, // malformed escape: falls through, unresolved
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveLinks(); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	assertTarget := func(raw, wantID string) {
		t.Helper()
		var target sql.NullString
		if err := db.conn.QueryRow(
			"SELECT target_id FROM links WHERE source_id='src' AND target_raw=?", raw,
		).Scan(&target); err != nil {
			t.Fatalf("query %q: %v", raw, err)
		}
		if target.String != wantID {
			t.Errorf("link %q resolved to %q, want %q", raw, target.String, wantID)
		}
	}
	assertTarget("My%20Note.md", "sp")
	assertTarget("My%ZGNote.md", "")

	// Raw-first precedence: with a literal "My%20Note.md" note present, the
	// encoded link resolves to the literal note, and the spaced form still
	// resolves to the spaced note.
	literal := &document.Document{
		ID: "lit", Path: "My%20Note.md", Title: "Literal Percent",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	if err := db.UpsertDocument(literal); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertLinks("src", []document.WikiLink{
		{Target: "My%20Note.md"},
		{Target: "My Note.md"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveLinks(); err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	assertTarget("My%20Note.md", "lit")
	assertTarget("My Note.md", "sp")
}

// TestLinksByRawName_PercentEncodedTarget pins move discovery: a note whose
// only reference to the moved file is a percent-encoded markdown link is still
// surfaced by LinksByRawName (the decode retry), so move opens it for rewrite.
func TestLinksByRawName_PercentEncodedTarget(t *testing.T) {
	db := openTestDB(t)

	src := &document.Document{
		ID: "src", Path: "src.md", Title: "Source",
		Type: "note", Status: "draft", Frontmatter: map[string]any{},
	}
	if err := db.UpsertDocument(src); err != nil {
		t.Fatal(err)
	}
	// The target note does not exist, so the link stays unresolved.
	if err := db.UpsertLinks("src", []document.WikiLink{{Target: "notes/My%20Note.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveLinks(); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	refs, err := db.LinksByRawName("notes/My Note.md")
	if err != nil {
		t.Fatalf("LinksByRawName: %v", err)
	}
	if len(refs) != 1 || refs[0].Path != "src.md" {
		t.Fatalf("encoded referrer not discovered: %+v", refs)
	}
	if refs[0].TargetRaw != "notes/My%20Note.md" {
		t.Errorf("target_raw must stay verbatim: %q", refs[0].TargetRaw)
	}
}
