package document

import (
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
	}
	for _, tc := range tests {
		got := slugify(tc.input)
		if got != tc.want {
			t.Errorf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
