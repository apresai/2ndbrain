package document

import (
	"testing"
)

func TestParseFrontmatter_Unix(t *testing.T) {
	content := []byte("---\ntitle: Hello\n---\nBody\n")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", meta["title"])
	}
	if body != "Body\n" {
		t.Errorf("body = %q, want %q", body, "Body\n")
	}
}

func TestParseFrontmatter_CRLF(t *testing.T) {
	// Pure CRLF: Windows-authored file. Opening is 5 bytes, closing is 7.
	content := []byte("---\r\ntitle: Hello\r\n---\r\nBody\r\n")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", meta["title"])
	}
	if body != "Body\r\n" {
		t.Errorf("body = %q, want %q", body, "Body\r\n")
	}
}

func TestParseFrontmatter_CRLFOpenLFClose(t *testing.T) {
	// Mixed: CRLF open, LF close. The skip-4-bytes bug used to leave a
	// stray "\n" in the YAML; verify the fix eats the full opening.
	content := []byte("---\r\ntitle: Hello\n---\nBody\n")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", meta["title"])
	}
	if body != "Body\n" {
		t.Errorf("body = %q, want %q", body, "Body\n")
	}
}

func TestParseFrontmatter_LFOpenCRLFClose(t *testing.T) {
	content := []byte("---\ntitle: Hello\r\n---\r\nBody\r\n")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", meta["title"])
	}
	if body != "Body\r\n" {
		t.Errorf("body = %q, want %q", body, "Body\r\n")
	}
}

func TestParseFrontmatter_EOFClose(t *testing.T) {
	// Frontmatter ending at EOF with just "---", no body.
	content := []byte("---\ntitle: Hello\n---")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", meta["title"])
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestParseFrontmatter_CRLFEOFClose(t *testing.T) {
	// CRLF frontmatter ending at EOF with just "---", no body.
	// This tests the bug fix for CRLF EOF handling.
	content := []byte("---\r\ntitle: Hello\r\n---")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", meta["title"])
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	content := []byte("Just text\n")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil meta, got %v", meta)
	}
	if body != "Just text\n" {
		t.Errorf("body = %q", body)
	}
}

func TestSerializeFrontmatter_EmptyMeta(t *testing.T) {
	data, err := SerializeFrontmatter(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "---\n---\n" {
		t.Errorf("got %q, want %q", string(data), "---\n---\n")
	}
}

func TestIsSensitiveKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"secret", true},
		{"Secret", true},
		{"password", true},
		{"token", true},
		{"key", true},
		{"title", false},
		{"tags", false},
	}
	for _, tc := range tests {
		got := IsSensitiveKey(tc.key)
		if got != tc.want {
			t.Errorf("IsSensitiveKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestFilterSensitive(t *testing.T) {
	meta := map[string]any{
		"title":    "Doc",
		"secret":   "hidden",
		"password": "p4ss",
		"token":    "tok",
		"key":      "k",
		"tags":     []string{"a"},
	}
	filtered := FilterSensitive(meta)
	if _, ok := filtered["secret"]; ok {
		t.Error("secret should be filtered")
	}
	if _, ok := filtered["password"]; ok {
		t.Error("password should be filtered")
	}
	if _, ok := filtered["title"]; !ok {
		t.Error("title should survive filtering")
	}
	if _, ok := filtered["tags"]; !ok {
		t.Error("tags should survive filtering")
	}
}

// The four tests below cover a note that opens with an EMPTY frontmatter block
// ("---" immediately followed by "---"). Obsidian writes that shape when a note
// loses its last property, and SerializeFrontmatter writes it for an empty map,
// so 2nb produces it too. The parser used to search past the empty block for a
// closing delimiter and match the next "---" in the body instead, so the note
// failed to parse on every index.
func TestParseFrontmatter_EmptyBlockLF(t *testing.T) {
	content := []byte("---\n---\n# Heading\n\nBody\n")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta == nil {
		t.Error("meta = nil, want an empty map (the block exists, it is just empty)")
	}
	if len(meta) != 0 {
		t.Errorf("meta = %v, want empty", meta)
	}
	if body != "# Heading\n\nBody\n" {
		t.Errorf("body = %q, want %q", body, "# Heading\n\nBody\n")
	}
}

func TestParseFrontmatter_EmptyBlockCRLF(t *testing.T) {
	content := []byte("---\r\n---\r\nBody\r\n")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta) != 0 {
		t.Errorf("meta = %v, want empty", meta)
	}
	if body != "Body\r\n" {
		t.Errorf("body = %q, want %q", body, "Body\r\n")
	}
}

func TestParseFrontmatter_EmptyBlockAtEOF(t *testing.T) {
	// Nothing at all after the block, and no trailing newline.
	content := []byte("---\n---")
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta) != 0 {
		t.Errorf("meta = %v, want empty", meta)
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

// TestParseFrontmatter_EmptyBlockKeepsBodyWithHorizontalRule is the shape that
// broke a real vault: an empty block followed by prose that contains its own
// "---" horizontal rule. The body must come back byte for byte.
func TestParseFrontmatter_EmptyBlockKeepsBodyWithHorizontalRule(t *testing.T) {
	const wantBody = "# AGENTS\n\nsome text\n\n---\n\nmore text\n"
	meta, body, err := ParseFrontmatter([]byte("---\n---\n" + wantBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta) != 0 {
		t.Errorf("meta = %v, want empty", meta)
	}
	if body != wantBody {
		t.Errorf("body = %q, want %q (the horizontal rule is body, not a closing delimiter)", body, wantBody)
	}
}

// TestUpdateDocumentFrontmatterAST_EmptyBlock covers the write side: adding a
// property to a note with an empty block must not treat the body's horizontal
// rule as the frontmatter region.
func TestUpdateDocumentFrontmatterAST_EmptyBlock(t *testing.T) {
	original := []byte("---\n---\n# Heading\n\ntext\n\n---\n\nmore\n")
	meta, body, err := ParseFrontmatter(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_ = meta
	out, err := UpdateDocumentFrontmatterAST(original, map[string]any{"title": "Hello"}, body)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	gotMeta, gotBody, err := ParseFrontmatter(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if gotMeta["title"] != "Hello" {
		t.Errorf("title = %v, want Hello", gotMeta["title"])
	}
	if gotBody != body {
		t.Errorf("body = %q, want %q (unchanged)", gotBody, body)
	}
}
