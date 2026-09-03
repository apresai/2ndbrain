package document

import "testing"

// The doubled-opening-delimiter reading is bounded by one rule: frontmatter is
// contiguous from its opening fence, so it applies only when a NON-BLANK line
// follows the second "---". These tests pin both sides of that boundary.

// TestParseFrontmatter_BlankLineAfterEmptyBlockIsBody is the data-loss case. A
// note with an empty properties block, a blank line, a colon-bearing line and a
// horizontal rule had those body lines read as properties. The blank line is the
// disambiguator: nothing after it is frontmatter.
func TestParseFrontmatter_BlankLineAfterEmptyBlockIsBody(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			// The reported shape: "Status: draft" is prose that parses as YAML.
			name:  "colon bearing line",
			input: "---\n---\n\nStatus: draft\n\n---\n\nRest of the note\n",
		},
		{
			// A guard rather than a new failure: this shape already read as an
			// empty block, because the region does not parse as a mapping. It is
			// here so the rule cannot be narrowed to the colon case alone.
			name:  "plain prose",
			input: "---\n---\n\nJust prose\n\n---\n\nMore\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, body, err := ParseFrontmatter([]byte(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(meta) != 0 {
				t.Errorf("meta = %v, want empty: a blank line after the opening block ends the frontmatter", meta)
			}
			want := tc.input[len("---\n---\n"):]
			if body != want {
				t.Errorf("body = %q, want %q: nothing may be lifted out of the body", body, want)
			}
		})
	}
}

// TestParseFrontmatter_DoubledDelimiterClosesAtEndOfFile: the main parser accepts
// a closing "---" at end of file, and so must the doubled-delimiter reading, or a
// note whose real properties sit behind a doubled fence and end at EOF loses them
// into the body.
func TestParseFrontmatter_DoubledDelimiterClosesAtEndOfFile(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"LF", "---\n---\ntitle: Real\n---"},
		{"CRLF", "---\r\n---\r\ntitle: Real\r\n---"},
		{
			// A guard: a trailing newline after the closer already matched the
			// "\n---\n" form, so this one passed before the EOF closers existed.
			name:  "LF with trailing newline",
			input: "---\n---\ntitle: Real\n---\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, body, err := ParseFrontmatter([]byte(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if meta["title"] != "Real" {
				t.Errorf("meta = %v, want title: Real kept as metadata, not moved into the body", meta)
			}
			if body != "" {
				t.Errorf("body = %q, want empty: the file ends at the closing delimiter", body)
			}
		})
	}
}

// TestParseFrontmatter_DoubledDelimiterWithNoCloserIsBody is a guard on the other
// edge: content on the very next line but no closing fence anywhere is body, and
// the empty-block reading stands.
func TestParseFrontmatter_DoubledDelimiterWithNoCloserIsBody(t *testing.T) {
	meta, body, err := ParseFrontmatter([]byte("---\n---\nplain body text\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(meta) != 0 {
		t.Errorf("meta = %v, want empty", meta)
	}
	if body != "plain body text\n" {
		t.Errorf("body = %q, want %q", body, "plain body text\n")
	}
}

// TestUpdateDocumentFrontmatterAST_BlankLineAfterEmptyBlockLeavesBodyAlone is why
// the read-side rule matters most. The write path consults the same reading, so
// a frontmatter-only edit used to rewrite the file with the absorbed body lines
// hoisted into a properties block, against the guarantee that only `meta` and the
// body-write commands may change a note's body.
func TestUpdateDocumentFrontmatterAST_BlankLineAfterEmptyBlockLeavesBodyAlone(t *testing.T) {
	original := []byte("---\n---\n\nStatus: draft\n\n---\n\nRest of the note\n")
	meta, body, err := ParseFrontmatter(original)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	meta["tags"] = []any{"added"}

	out, err := UpdateDocumentFrontmatterAST(original, meta, body)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	gotMeta, gotBody, err := ParseFrontmatter(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if gotBody != body {
		t.Errorf("body = %q, want %q unchanged: a frontmatter edit may not move body text", gotBody, body)
	}
	if _, absorbed := gotMeta["Status"]; absorbed {
		t.Errorf("meta = %v, want no Status key: that line is body text, not a property", gotMeta)
	}
}
