package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_WithFullFrontmatter(t *testing.T) {
	content := []byte("---\nid: abc-123\ntitle: Test Doc\ntype: adr\nstatus: proposed\ntags:\n  - auth\n  - jwt\n---\n# Test Doc\n\nBody here.\n")
	doc, err := Parse("test.md", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.ID != "abc-123" {
		t.Errorf("ID = %q, want %q", doc.ID, "abc-123")
	}
	if doc.Title != "Test Doc" {
		t.Errorf("Title = %q, want %q", doc.Title, "Test Doc")
	}
	if doc.Type != "adr" {
		t.Errorf("Type = %q, want %q", doc.Type, "adr")
	}
	if doc.Status != "proposed" {
		t.Errorf("Status = %q, want %q", doc.Status, "proposed")
	}
	if len(doc.Tags) != 2 {
		t.Errorf("Tags count = %d, want 2", len(doc.Tags))
	}
	if !strings.Contains(doc.Body, "Body here.") {
		t.Errorf("Body should contain 'Body here.', got %q", doc.Body)
	}
}

func TestParse_SingleStringTag(t *testing.T) {
	// `tags: foo` (no list) is valid YAML and parses as a plain string;
	// it must be treated as a single-item tag list, not silently dropped.
	content := []byte("---\nid: single\ntitle: Single Tag\ntags: mytag\n---\nbody")
	doc, err := Parse("single.md", content)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Tags) != 1 || doc.Tags[0] != "mytag" {
		t.Errorf("Tags = %v, want [mytag]", doc.Tags)
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	content := []byte("# Just a heading\n\nSome text.\n")
	doc, err := Parse("test.md", content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Frontmatter != nil {
		t.Errorf("expected nil frontmatter, got %v", doc.Frontmatter)
	}
	if !strings.Contains(doc.Body, "Just a heading") {
		t.Errorf("Body should contain full content")
	}
}

func TestParse_MalformedYAML(t *testing.T) {
	content := []byte("---\n[broken yaml\n---\nBody\n")
	_, err := Parse("test.md", content)
	if err == nil {
		t.Error("expected error for malformed YAML")
	}
}

func TestNewDocument(t *testing.T) {
	doc := NewDocument("My Doc", "adr", "# My Doc\n")
	if doc.ID == "" {
		t.Error("ID should not be empty")
	}
	if len(doc.ID) != 36 { // UUID format
		t.Errorf("ID should be UUID, got %q", doc.ID)
	}
	if doc.Title != "My Doc" {
		t.Errorf("Title = %q, want %q", doc.Title, "My Doc")
	}
	if doc.Type != "adr" {
		t.Errorf("Type = %q, want %q", doc.Type, "adr")
	}
	if doc.Status != "draft" {
		t.Errorf("Status = %q, want %q", doc.Status, "draft")
	}
}

func TestSerialize_RoundTrip(t *testing.T) {
	doc := NewDocument("Round Trip", "note", "# Round Trip\n\nSome body.\n")
	data, err := doc.Serialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}

	doc2, err := Parse("test.md", data)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if doc2.ID != doc.ID {
		t.Errorf("ID mismatch: %q vs %q", doc2.ID, doc.ID)
	}
	if doc2.Title != doc.Title {
		t.Errorf("Title mismatch: %q vs %q", doc2.Title, doc.Title)
	}
}

func TestSetMeta_UpdatesStructFields(t *testing.T) {
	doc := NewDocument("Old", "note", "")
	doc.SetMeta("title", "New Title")
	if doc.Title != "New Title" {
		t.Errorf("Title = %q, want %q", doc.Title, "New Title")
	}
	doc.SetMeta("status", "complete")
	if doc.Status != "complete" {
		t.Errorf("Status = %q, want %q", doc.Status, "complete")
	}
	doc.SetMeta("type", "adr")
	if doc.Type != "adr" {
		t.Errorf("Type = %q, want %q", doc.Type, "adr")
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Hello World", "hello-world"},
		{"Use JWT for Auth!", "use-jwt-for-auth"},
		{"", ""},
		{"  spaces  ", "spaces-"},
		// Accented Latin decomposes to ASCII after NFD + combining-mark strip.
		{"Café", "cafe"},
		{"Naïve", "naive"},
		{"Résumé — Draft", "resume-draft"},
		// CJK and emoji don't decompose to ASCII; empty slug triggers the
		// UUID fallback in uniqueFilename.
		{"会議の議事録", ""},
		{"🚀 Launch Plan", "launch-plan"},
	}
	for _, tc := range tests {
		got := slugify(tc.input)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestUniqueFilename(t *testing.T) {
	dir := t.TempDir()

	// First create: the bare slug.
	p1 := uniqueFilename(dir, "my-note", "id-1")
	if filepath.Base(p1) != "my-note.md" {
		t.Fatalf("first = %q, want my-note.md", filepath.Base(p1))
	}
	if err := os.WriteFile(p1, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second create with the same slug: appends -1.
	p2 := uniqueFilename(dir, "my-note", "id-2")
	if filepath.Base(p2) != "my-note-1.md" {
		t.Fatalf("second = %q, want my-note-1.md", filepath.Base(p2))
	}
	if err := os.WriteFile(p2, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Third: -2.
	p3 := uniqueFilename(dir, "my-note", "id-3")
	if filepath.Base(p3) != "my-note-2.md" {
		t.Fatalf("third = %q, want my-note-2.md", filepath.Base(p3))
	}

	// Empty slug (e.g. an all-CJK title) falls back to the document id.
	pEmpty := uniqueFilename(dir, "", "id-cjk")
	if filepath.Base(pEmpty) != "id-cjk.md" {
		t.Fatalf("empty slug = %q, want id-cjk.md (UUID fallback)", filepath.Base(pEmpty))
	}
}

// TestWriteFile_NoSilentOverwriteOnDuplicateTitle guards the fix where two
// fresh creates with the same title in one directory silently clobbered each
// other (the second's WriteFile overwrote the first). They must now land at
// distinct paths.
func TestWriteFile_NoSilentOverwriteOnDuplicateTitle(t *testing.T) {
	dir := t.TempDir()

	a := NewDocument("API Keys", "note", "first body")
	pa, err := a.WriteFile(dir)
	if err != nil {
		t.Fatalf("write a: %v", err)
	}

	b := NewDocument("API Keys", "note", "second body")
	pb, err := b.WriteFile(dir)
	if err != nil {
		t.Fatalf("write b: %v", err)
	}

	if pa == pb {
		t.Fatalf("both docs wrote to the same path %q (silent overwrite)", pa)
	}
	if filepath.Base(pa) != "api-keys.md" || filepath.Base(pb) != "api-keys-1.md" {
		t.Errorf("paths = %q, %q; want api-keys.md, api-keys-1.md", filepath.Base(pa), filepath.Base(pb))
	}
	// The first file's content must survive.
	first, err := os.ReadFile(pa)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "first body") {
		t.Errorf("first doc was overwritten; content = %q", string(first))
	}
}

func TestAppendToBody(t *testing.T) {
	tests := []struct {
		name, body, content, want string
	}{
		{"non-empty body gets a newline separator", "existing", "new", "existing\nnew"},
		{"empty body has no leading blank line", "", "new", "new"},
		{"empty content leaves the body unchanged (no trailing blank line)", "existing", "", "existing"},
		{"both empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AppendToBody(tc.body, tc.content); got != tc.want {
				t.Errorf("AppendToBody(%q, %q) = %q, want %q", tc.body, tc.content, got, tc.want)
			}
		})
	}
}

func TestPrependToBody(t *testing.T) {
	tests := []struct {
		name, body, content, want string
	}{
		{"non-empty body gets a newline separator", "existing", "new", "new\nexisting"},
		{"empty body has no trailing blank line", "", "new", "new"},
		{"empty content leaves the body unchanged (no leading blank line)", "existing", "", "existing"},
		{"both empty", "", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PrependToBody(tc.body, tc.content); got != tc.want {
				t.Errorf("PrependToBody(%q, %q) = %q, want %q", tc.body, tc.content, got, tc.want)
			}
		})
	}
}

func TestSerialize_SurgicalASTPreservesCommentsAndOrder(t *testing.T) {
	// 1. Create a temporary file representing the original note on disk
	tmpFile := filepath.Join(t.TempDir(), "note.md")
	originalContent := "---\n# Important note configuration\nid: test-id\ntitle: Original Title\n# The status determines lifecycle\nstatus: draft\ntags:\n  - testing\n---\n# Heading\nBody content here."

	if err := os.WriteFile(tmpFile, []byte(originalContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Parse the document
	doc, err := ParseFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}

	// 3. Modify a metadata field
	doc.SetMeta("title", "Updated Title")
	doc.SetMeta("status", "complete")

	// 4. Serialize
	serialized, err := doc.Serialize()
	if err != nil {
		t.Fatal(err)
	}

	// 5. Verify the comments and key ordering are preserved
	serializedStr := string(serialized)

	// Verify title was updated
	if !strings.Contains(serializedStr, "title: Updated Title") {
		t.Errorf("expected updated title, got:\n%s", serializedStr)
	}
	// Verify status was updated
	if !strings.Contains(serializedStr, "status: complete") {
		t.Errorf("expected updated status, got:\n%s", serializedStr)
	}

	// Verify comments are still present
	if !strings.Contains(serializedStr, "# Important note configuration") {
		t.Errorf("missing config comment, got:\n%s", serializedStr)
	}
	if !strings.Contains(serializedStr, "# The status determines lifecycle") {
		t.Errorf("missing status comment, got:\n%s", serializedStr)
	}

	// Verify order is preserved (id should still be before title, and status after title)
	idIdx := strings.Index(serializedStr, "id:")
	titleIdx := strings.Index(serializedStr, "title:")
	statusIdx := strings.Index(serializedStr, "status:")
	if idIdx > titleIdx || titleIdx > statusIdx {
		t.Errorf("ordering was not preserved, got:\n%s", serializedStr)
	}
}
