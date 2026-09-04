package document

import (
	"strings"
	"testing"
)

// The doubled-opening-delimiter reading is bounded by one rule: the region
// between the fences is frontmatter only when it is a CONTIGUOUS key block, i.e.
// no blank line once leading blanks are skipped. Nothing simpler works, because
// both competing shapes have a blank line right after the doubled fence. These
// tests are the full set of shapes this defect produced.

// TestParseFrontmatter_DoubledFenceShapes runs every shape at once, so a change
// to the rule cannot fix one direction by breaking the other. Judging by the
// LEADING blank line did exactly that: it absorbed the prose cases whenever that
// line carried whitespace, and discarded the real-properties case outright.
func TestParseFrontmatter_DoubledFenceShapes(t *testing.T) {
	const openLF, openCRLF = "---\n---\n", "---\r\n---\r\n"
	cases := []struct {
		name string
		// input is open + rest; body must come back as rest, byte for byte,
		// whenever wantMeta is nil.
		input    string
		open     string
		wantMeta map[string]any
		wantBody string
	}{
		{name: "prose divider", input: openLF + "\nStatus: draft\n\n---\n\nRest\n", open: openLF},
		{name: "prose divider, trailing space on the blank line", input: openLF + "   \nStatus: draft\n\n---\n\nRest\n", open: openLF},
		{name: "prose divider, one space", input: openLF + " \nStatus: draft\n\n---\n\nRest\n", open: openLF},
		{
			// By RULE, not by the YAML accident that used to save it: YAML
			// rejects tab indentation, so the unmarshal failed and the code fell
			// back to "empty" for the wrong reason.
			name: "prose divider, tab on the blank line", input: openLF + "\t\nStatus: draft\n\n---\n\nRest\n", open: openLF,
		},
		{name: "AGENTS shape", input: openLF + "\n# AGENTS\n\nInstructions.\n", open: openLF},
		{name: "no closing fence at all", input: openLF + "plain body text\n", open: openLF},
		{name: "meeting notes", input: openLF + "\nMeeting notes: X\n\nAttendees: A, B\n\n---\nItems\n", open: openLF},
		{name: "tripled fence", input: openLF + "---\nbody\n", open: openLF},
		{name: "CRLF prose divider, trailing space", input: openCRLF + "   \r\nStatus: draft\r\n\r\n---\r\n\r\nRest\r\n", open: openCRLF},

		{
			name:     "real properties one line down",
			input:    openLF + "\ntitle: Real Note\ntags: [a, b]\n---\nBody\n",
			wantMeta: map[string]any{"title": "Real Note"},
			wantBody: "Body\n",
		},
		{
			name:     "real properties immediately after the fence",
			input:    openLF + "title: Real\n---\nbody\n",
			wantMeta: map[string]any{"title": "Real"},
			wantBody: "body\n",
		},
		{
			name:     "real properties closed at end of file",
			input:    openLF + "title: Real\n---",
			wantMeta: map[string]any{"title": "Real"},
			wantBody: "",
		},
		{
			name:     "CRLF real properties one line down",
			input:    openCRLF + "\r\ntitle: Real Note\r\ntags: [a, b]\r\n---\r\nBody\r\n",
			wantMeta: map[string]any{"title": "Real Note"},
			wantBody: "Body\r\n",
		},
		{
			name:     "CRLF real properties closed at end of file",
			input:    openCRLF + "title: Real\r\n---",
			wantMeta: map[string]any{"title": "Real"},
			wantBody: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, body, err := ParseFrontmatter([]byte(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantMeta == nil {
				if len(meta) != 0 {
					t.Errorf("meta = %v, want empty: this region is not a contiguous key block", meta)
				}
				// Byte for byte: every byte after the two fences is body,
				// whitespace and line endings included.
				if want := tc.input[len(tc.open):]; body != want {
					t.Errorf("body = %q, want %q byte for byte: nothing may be lifted out of the body", body, want)
				}
				return
			}
			for k, want := range tc.wantMeta {
				if meta[k] != want {
					t.Errorf("meta[%q] = %v, want %v: real properties must not be discarded into the body", k, meta[k], want)
				}
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

// TestParseFrontmatter_CRLFBodyKeepsItsBytes is the guard against fixing this by
// normalizing line endings to find the fences. The body that comes back must be
// the ORIGINAL bytes: the write path rewrites the whole file from it, so a
// normalized body would silently convert every CRLF note to LF on the first
// `2nb meta --set`.
func TestParseFrontmatter_CRLFBodyKeepsItsBytes(t *testing.T) {
	cases := []struct{ name, input, wantBody string }{
		{"properties kept", "---\r\n---\r\n\r\ntitle: Real\r\n---\r\nLine one\r\nLine two\r\n", "Line one\r\nLine two\r\n"},
		{"region is body", "---\r\n---\r\n\r\nStatus: draft\r\n\r\n---\r\n\r\nRest\r\n", "\r\nStatus: draft\r\n\r\n---\r\n\r\nRest\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, body, err := ParseFrontmatter([]byte(tc.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q: CRLF must survive the parse", body, tc.wantBody)
			}
			if strings.Contains(tc.wantBody, "\r\n") && !strings.Contains(body, "\r\n") {
				t.Error("the body was normalized to LF; the write path would rewrite the whole file's line endings")
			}
		})
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

// TestParseFrontmatter_DoubledDelimiterAmbiguityIsWiderThanAHeading pins the
// residual of the blank-line rule, so the next reader does not conclude the
// colon case is fixed outright. It is fixed only when a blank line precedes it.
// With content on the very NEXT line the doubled reading still wins, for a plain
// colon line as much as for the heading case the sibling test pins, and the write
// path still rewrites such a note.
//
// The shape is reachable without anyone typing it: SerializeFrontmatter emits
// "---\n---\n" for an empty map, with no blank line after it. Losing real
// metadata is the worse of the two failures, so the trade stands; changing it
// belongs to a decision, not to a drive-by.
func TestParseFrontmatter_DoubledDelimiterAmbiguityIsWiderThanAHeading(t *testing.T) {
	const input = "---\n---\nStatus: draft\n---\nbody\n"
	meta, body, err := ParseFrontmatter([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta["Status"] != "draft" {
		t.Errorf("meta = %v, want Status: draft: with no blank line the doubled reading still wins", meta)
	}
	if body != "body\n" {
		t.Errorf("body = %q, want %q", body, "body\n")
	}

	// The write path follows the read path, which is the half that costs.
	meta["tags"] = []any{"x"}
	out, err := UpdateDocumentFrontmatterAST([]byte(input), meta, body)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	gotMeta, gotBody, err := ParseFrontmatter(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if gotMeta["Status"] != "draft" || gotBody != "body\n" {
		t.Errorf("round trip = %v / %q, want the same reading back", gotMeta, gotBody)
	}
}
