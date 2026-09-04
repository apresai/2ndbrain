package document

import "testing"

// The blank-line classifier decides BOTH directions of the doubled-fence rule,
// so a narrow definition of "blank" costs data twice over: prose is absorbed as
// properties when the classifier misses a blank line, and real properties are
// discarded into the body when it recognizes one but the YAML parser then chokes
// on it.
//
// The second was the worse failure and it was invisible to a suite that only
// covered the prose direction: a tab-only blank line landed on "empty" there for
// the RIGHT outcome by the WRONG mechanism (YAML forbids tab indentation, so the
// region failed to parse and the code fell back), while the same line in front
// of real properties silently threw them away.
//
// Every variant is written as an escape sequence on purpose: a raw U+FEFF in Go
// source is rejected by the compiler as an illegal byte order mark.
var blankLineVariants = []struct {
	name string
	fill string
}{
	{"empty", ""},
	{"space", " "},
	{"tab", "\t"},
	{"tab then CR", "\t\r"},
	{"formfeed", "\f"},
	{"vertical tab", "\v"},
	{"non-breaking space", "\u00a0"},
	{"zero-width space", "\u200b"},
	{"byte order mark", "\ufeff"},
	{"zero-width non-joiner", "\u200c"},
	{"zero-width joiner", "\u200d"},
	{"space, tab and a zero-width space", " \t \u200b"},
}

// TestParseFrontmatter_BlankFillerKeepsRealProperties is the direction the suite
// never covered, and the one that lost user data: whatever invisible filler the
// editor left on the line between the fence and the properties, the properties
// are still properties.
func TestParseFrontmatter_BlankFillerKeepsRealProperties(t *testing.T) {
	for _, v := range blankLineVariants {
		t.Run(v.name, func(t *testing.T) {
			input := "---\n---\n" + v.fill + "\ntitle: Real Note\ntags: [a, b]\n---\nBody\n"
			meta, body, err := ParseFrontmatter([]byte(input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if meta["title"] != "Real Note" {
				t.Errorf("meta = %v, want title kept: a blank line the rule accepts must not reach the YAML parser", meta)
			}
			if tags, ok := meta["tags"].([]any); !ok || len(tags) != 2 {
				t.Errorf("meta[tags] = %v, want both tags kept", meta["tags"])
			}
			if body != "Body\n" {
				t.Errorf("body = %q, want %q", body, "Body\n")
			}
		})
	}
}

// TestParseFrontmatter_BlankFillerLeavesProseAlone is the mirror direction, kept
// green by the same predicate. The tab case here now passes BY RULE: the region
// is rejected because it is not a contiguous key block, not because YAML happened
// to choke on the tab.
func TestParseFrontmatter_BlankFillerLeavesProseAlone(t *testing.T) {
	for _, v := range blankLineVariants {
		t.Run(v.name, func(t *testing.T) {
			input := "---\n---\n" + v.fill + "\nStatus: draft\n\n---\n\nRest\n"
			meta, body, err := ParseFrontmatter([]byte(input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(meta) != 0 {
				t.Errorf("meta = %v, want empty: this region is not a contiguous key block", meta)
			}
			if want := input[len("---\n---\n"):]; body != want {
				t.Errorf("body = %q, want %q byte for byte", body, want)
			}
		})
	}
}

// TestContiguousKeyBlockStripsWhatItSkips is the root cause, asserted directly.
// The old code RECOGNIZED the leading blank lines and then handed the region to
// YAML with them still in it. Validating one region and parsing another is the
// shape of the bug; the block that comes back is the block that gets parsed.
func TestContiguousKeyBlockStripsWhatItSkips(t *testing.T) {
	block, ok := contiguousKeyBlock("\t\r\n \u200b\ntitle: Real\ntags: [a]")
	if !ok {
		t.Fatal("a region of blank filler above two key lines is a contiguous key block")
	}
	if block != "title: Real\ntags: [a]" {
		t.Errorf("block = %q, want the blank lines stripped, not merely skipped", block)
	}

	if _, ok := contiguousKeyBlock("title: Real\n\ntags: [a]"); ok {
		t.Error("a blank line INSIDE the region means it is not a contiguous key block")
	}
	if _, ok := contiguousKeyBlock("title: Real\n"); ok {
		t.Error("a trailing blank line means it is not a contiguous key block")
	}
	if _, ok := contiguousKeyBlock("\t\n \n"); ok {
		t.Error("nothing but blank filler is not a key block")
	}
}

// TestClosingFenceIsOneDefinition: the reader and the surgical writer must agree
// about where frontmatter ends. They used to search separately, and the writer's
// version matched a bare "\n---" ANYWHERE rather than only at end of file.
func TestClosingFenceIsOneDefinition(t *testing.T) {
	cases := []struct {
		name    string
		rest    string
		wantIdx int
		wantLen int
	}{
		{"LF fence", "title: X\n---\nbody\n", 8, 5},
		{"CRLF fence", "title: X\r\n---\r\nbody\r\n", 8, 7},
		{"LF at end of file", "title: X\n---", 8, 4},
		{"CRLF at end of file", "title: X\r\n---", 8, 5},
		{"a bare --- mid-document is NOT a fence", "title: X\n---abc\nmore\n", -1, 0},
		{"no fence at all", "title: X\nbody\n", -1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, length := closingFence(tc.rest)
			if idx != tc.wantIdx || length != tc.wantLen {
				t.Errorf("closingFence(%q) = (%d, %d), want (%d, %d)", tc.rest, idx, length, tc.wantIdx, tc.wantLen)
			}
		})
	}
}

// TestUpdateDocumentFrontmatterAST_BlankFillerKeepsRealProperties is the write
// half at the unit level: the surgical editor must preserve the properties the
// reader just recognized, whatever filler preceded them.
func TestUpdateDocumentFrontmatterAST_BlankFillerKeepsRealProperties(t *testing.T) {
	for _, v := range blankLineVariants {
		t.Run(v.name, func(t *testing.T) {
			original := []byte("---\n---\n" + v.fill + "\ntitle: Real Note\ntags: [a, b]\n---\nBody\n")
			meta, body, err := ParseFrontmatter(original)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			meta["added"] = "yes"
			out, err := UpdateDocumentFrontmatterAST(original, meta, body)
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			gotMeta, gotBody, err := ParseFrontmatter(out)
			if err != nil {
				t.Fatalf("re-parse %q: %v", out, err)
			}
			if gotMeta["title"] != "Real Note" || gotMeta["added"] != "yes" {
				t.Errorf("meta = %v, want the existing title and the added key", gotMeta)
			}
			if gotBody != "Body\n" {
				t.Errorf("body = %q, want %q", gotBody, "Body\n")
			}
		})
	}
}
