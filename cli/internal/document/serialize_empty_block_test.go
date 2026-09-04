package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 2nb wrote a note it then misread. An empty frontmatter block is closed by a
// delimiter identical to its opener, so SerializeDocument emitted a doubled
// fence with the body starting on the very next line, and the parser's
// doubled-delimiter reading then took the body's first line as a property.
// Nobody had to hand-write the shape: 2nb manufactured it.

// TestSerializeDocument_EmptyBlockRoundTripsAYAMLishBody is the loss case,
// asserted as a round trip because that is how it bites: write, then read back.
//
// The separator the writer inserts is itself part of the body it reads back, so
// the round trip is a FIXED POINT rather than byte-identical on the first write:
// nothing is lost, and a second write produces the same bytes, so the blank line
// can never grow. Byte-identical on the first write would need the parser to
// consume the separator, which would move the frontmatter/body boundary a second
// time in one release; `normalizeBody` trims leading whitespace, so the
// separator changes no content hash, no chunk and no embedding.
func TestSerializeDocument_EmptyBlockRoundTripsAYAMLishBody(t *testing.T) {
	const body = "Status: draft\n\n---\n\nAction items\n"

	out, err := SerializeDocument(map[string]any{}, body)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	meta, got, err := ParseFrontmatter(out)
	if err != nil {
		t.Fatalf("re-parse %q: %v", out, err)
	}
	if len(meta) != 0 {
		t.Errorf("meta = %v, want empty: 2nb wrote an empty block and must read one back", meta)
	}
	if got != "\n"+body {
		t.Errorf("body = %q, want %q (the note plus the separator the writer added); 2nb lost part of the note it just wrote\n  bytes written: %q",
			got, "\n"+body, out)
	}

	// Fixed point: writing what we just read produces the same bytes, so the
	// separator is added once and never accumulates.
	again, err := SerializeDocument(map[string]any{}, got)
	if err != nil {
		t.Fatalf("re-serialize: %v", err)
	}
	if string(again) != string(out) {
		t.Errorf("second write = %q, want the same bytes as the first (%q): the separator must not grow", again, out)
	}

	// The separator is invisible to indexing: the content hash is computed from
	// the normalized body, which trims leading whitespace.
	before := &Document{Body: body}
	after := &Document{Body: got}
	before.ComputeContentHash()
	after.ComputeContentHash()
	if before.ContentHash != after.ContentHash {
		t.Errorf("content hash changed (%s to %s): the separator would re-chunk and re-embed the note",
			before.ContentHash, after.ContentHash)
	}
}

// TestSerializeDocument_EmptyBlockDoesNotDoubleABlankLine is the guard on the
// other side: a body that already begins with a newline is emitted unchanged, so
// repeated writes cannot grow blank lines at the top of a note.
func TestSerializeDocument_EmptyBlockDoesNotDoubleABlankLine(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"LF", "\nStatus: draft\n\n---\n\nAction items\n"},
		{"CRLF", "\r\nStatus: draft\r\n\r\n---\r\n\r\nAction items\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := SerializeDocument(map[string]any{}, tc.body)
			if err != nil {
				t.Fatalf("serialize: %v", err)
			}
			if want := "---\n---\n" + tc.body; string(out) != want {
				t.Errorf("wrote %q, want %q: a body that is already spaced must be emitted unchanged", out, want)
			}
			meta, got, err := ParseFrontmatter(out)
			if err != nil {
				t.Fatalf("re-parse: %v", err)
			}
			if len(meta) != 0 || got != tc.body {
				t.Errorf("round trip = %v / %q, want empty meta and %q", meta, got, tc.body)
			}
		})
	}

	// A non-empty block closes unambiguously and gets no blank line at all.
	out, err := SerializeDocument(map[string]any{"title": "T"}, "Status: draft\n")
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if want := "---\ntitle: T\n---\nStatus: draft\n"; string(out) != want {
		t.Errorf("wrote %q, want %q: a real frontmatter block needs no separator", out, want)
	}
}

// TestSerializeDocument_SurgicalPathStaysByteStable is the guard on the path the
// fix must not disturb. Removing a note's LAST property goes through
// UpdateDocumentFrontmatterAST, which rewrites the block in place as
// "---\n{}\n---\n" rather than through SerializeDocument, and that already
// round-tripped. It stays byte-stable, so one path cannot be fixed by breaking
// the other.
func TestSerializeDocument_SurgicalPathStaysByteStable(t *testing.T) {
	const body = "Status: draft\n\n---\n\nAction items\n"
	original := []byte("---\ntitle: T\n---\n" + body)

	meta, parsedBody, err := ParseFrontmatter(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsedBody != body {
		t.Fatalf("setup: body = %q, want %q", parsedBody, body)
	}
	delete(meta, "title") // what `2nb meta --remove title` does to the last property

	out, err := UpdateDocumentFrontmatterAST(original, meta, parsedBody)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	gotMeta, gotBody, err := ParseFrontmatter(out)
	if err != nil {
		t.Fatalf("re-parse %q: %v", out, err)
	}
	if len(gotMeta) != 0 {
		t.Errorf("meta = %v, want empty after removing the last property", gotMeta)
	}
	if gotBody != body {
		t.Errorf("body = %q, want %q byte for byte\n  bytes written: %q", gotBody, body, out)
	}
}

// TestDocumentWriteFile_EmptyFrontmatterKeepsItsBody walks the real file path a
// user's note takes, so the fix is proved where it lands rather than only in the
// serializer.
func TestDocumentWriteFile_EmptyFrontmatterKeepsItsBody(t *testing.T) {
	const body = "Status: draft\n\n---\n\nAction items\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")

	out, err := SerializeDocument(map[string]any{}, body)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	doc, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse file: %v", err)
	}
	if len(doc.Frontmatter) != 0 {
		t.Errorf("frontmatter = %v, want empty", doc.Frontmatter)
	}
	if doc.Body != "\n"+body {
		t.Errorf("body on disk read back as %q, want %q (the note plus the writer's separator)", doc.Body, "\n"+body)
	}
	if !strings.Contains(doc.Body, "Status: draft") {
		t.Errorf("the note on disk lost its first line: %q", doc.Body)
	}
}
