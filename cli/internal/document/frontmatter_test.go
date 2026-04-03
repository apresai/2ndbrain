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
