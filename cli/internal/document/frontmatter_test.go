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
