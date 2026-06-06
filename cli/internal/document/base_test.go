package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBase(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "service.base")

	baseYAML := `
base:
  name: Service Config
  version: 2.1.0
  settings:
    timeout_ms: 1200
    retry_attempts: 5
    endpoints:
      - https://api.prod.local
      - https://api.backup.local
`

	if err := os.WriteFile(basePath, []byte(baseYAML), 0o644); err != nil {
		t.Fatalf("write temp base file: %v", err)
	}

	doc, err := ParseFile(basePath)
	if err != nil {
		t.Fatalf("unexpected error parsing base: %v", err)
	}

	if doc.Type != "base" {
		t.Errorf("expected doc type 'base', got %q", doc.Type)
	}
	if doc.Title != "service" {
		t.Errorf("expected title 'service', got %q", doc.Title)
	}
	if doc.Status != "complete" {
		t.Errorf("expected status 'complete', got %q", doc.Status)
	}

	// Verify flattened structure is present in the markdown body
	expectedHeadings := []string{
		"# base.name\nService Config",
		"# base.version\n2.1.0",
		"# base.settings.timeout_ms\n1200",
		"# base.settings.retry_attempts\n5",
		"# base.settings.endpoints.0\nhttps://api.prod.local",
		"# base.settings.endpoints.1\nhttps://api.backup.local",
	}

	for _, expected := range expectedHeadings {
		if !strings.Contains(doc.Body, expected) {
			t.Errorf("expected body to contain %q, got body:\n%s", expected, doc.Body)
		}
	}
}
