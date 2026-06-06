package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBase_NilAndEmptyValues(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "cfg.base")
	baseYAML := "root:\n  empty_list: []\n  nothing: null\n  count: 0\n"
	if err := os.WriteFile(p, []byte(baseYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	for _, want := range []string{
		"# root.empty_list\n[]",
		"# root.nothing\nnull",
		"# root.count\n0",
	} {
		if !strings.Contains(doc.Body, want) {
			t.Errorf("expected body to contain %q, got:\n%s", want, doc.Body)
		}
	}
}

func TestParseBase_Malformed(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.base")
	// Unterminated flow sequence is invalid YAML.
	if err := os.WriteFile(p, []byte("foo: [1, 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(p); err == nil {
		t.Error("expected error parsing malformed base")
	}
}
