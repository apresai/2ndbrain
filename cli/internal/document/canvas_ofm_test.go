package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseCanvas_GroupAndLinkNodes(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "board.canvas")
	canvasJSON := `{"nodes":[
		{"id":"g1","type":"group","label":"My Group"},
		{"id":"l1","type":"link","url":"https://example.com"},
		{"id":"t1","type":"text","text":"hello"}
	],"edges":[{"id":"e1","fromNode":"g1","toNode":"l1"}]}`
	if err := os.WriteFile(p, []byte(canvasJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if !strings.Contains(doc.Body, "My Group") {
		t.Errorf("group label missing: %s", doc.Body)
	}
	if !strings.Contains(doc.Body, "https://example.com") {
		t.Errorf("link url missing: %s", doc.Body)
	}
	// Edge descriptions reference the group and link nodes.
	if !strings.Contains(doc.Body, `Group "My Group" (g1)`) {
		t.Errorf("group edge description missing: %s", doc.Body)
	}
	if !strings.Contains(doc.Body, `Link "https://example.com" (l1)`) {
		t.Errorf("link edge description missing: %s", doc.Body)
	}
}

func TestParseCanvas_Malformed(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "bad.canvas")
	if err := os.WriteFile(p, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(p); err == nil {
		t.Error("expected error parsing malformed canvas")
	}
}

func TestDescribeNodeForEdge_RuneSafeTruncation(t *testing.T) {
	// 40 multi-byte runes, longer than the 30-rune limit: truncation must not
	// split a rune (the old byte-slice [:27] could).
	n := CanvasNode{ID: "n1", Type: "text", Text: strings.Repeat("é", 40)}
	got := describeNodeForEdge(n)
	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "...") {
		t.Errorf("expected ellipsis on truncated text, got %q", got)
	}
}
