package document

import (
	"strings"
	"testing"
)

func TestUpdateDocumentFrontmatterAST_NewAndRemovedKeys(t *testing.T) {
	orig := []byte("---\nid: x\ntitle: Old\nstatus: draft\n---\nbody\n")
	// status omitted (should be removed), tags added (should be inserted).
	meta := map[string]any{"id": "x", "title": "New", "tags": []string{"a"}}
	out, err := UpdateDocumentFrontmatterAST(orig, meta, "body\n")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "title: New") {
		t.Errorf("title not updated:\n%s", s)
	}
	if strings.Contains(s, "status:") {
		t.Errorf("status key not removed:\n%s", s)
	}
	if !strings.Contains(s, "tags:") {
		t.Errorf("new tags key not inserted:\n%s", s)
	}
}

func TestUpdateDocumentFrontmatterAST_CRLF(t *testing.T) {
	orig := []byte("---\r\nid: x\r\ntitle: Old\r\n---\r\nbody line\r\n")
	meta := map[string]any{"id": "x", "title": "New"}
	out, err := UpdateDocumentFrontmatterAST(orig, meta, "body line\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "title: New") {
		t.Errorf("CRLF frontmatter update failed:\n%q", out)
	}
}

func TestUpdateDocumentFrontmatterAST_NoFrontmatterFallback(t *testing.T) {
	body := "just a plain body, no frontmatter\n"
	out, err := UpdateDocumentFrontmatterAST([]byte(body), map[string]any{"title": "T"}, body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "---\n") {
		t.Errorf("expected fallback to serialize frontmatter, got:\n%q", out)
	}
	if !strings.Contains(string(out), "title: T") {
		t.Errorf("fallback missing title:\n%q", out)
	}
}
