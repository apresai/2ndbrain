package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A closing fence is a LINE THAT IS EXACTLY "---", and anything else on that
// line is BODY.
//
// The reader used to search for "\n---" with no check that the match was at end
// of file, so the FIRST such sequence anywhere in the note ended the
// frontmatter and everything after it was discarded. A markdown horizontal rule
// ("----"), a longer one, a fence carrying a trailing space, and a line
// beginning "---more" each cost a note its entire body. It is not a read-only
// bug: Serialize rewrites the file from that truncated body, so one unrelated
// `meta --set` destroyed the note on disk. Verified against the released
// 0.22.3, so it shipped.
func TestParseFrontmatter_OnlyAnExactFenceEndsTheBlock(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		title string // "" means the note has no frontmatter at all
		body  string
		// wantErr marks a region that is not YAML at all, where the contract is
		// to REPORT rather than to guess at a shorter region.
		wantErr bool
	}{
		{
			name:  "an exact fence closes the block",
			src:   "---\ntitle: X\n---\nreal body\n",
			title: "X",
			body:  "real body\n",
		},
		{
			name:  "a CRLF fence closes the block",
			src:   "---\r\ntitle: X\r\n---\r\nreal body\r\n",
			title: "X",
			body:  "real body\r\n",
		},
		{
			name:  "a fence at end of file closes the block",
			src:   "---\ntitle: X\n---",
			title: "X",
			body:  "",
		},
		{
			name:  "a CRLF fence at end of file closes the block",
			src:   "---\r\ntitle: X\r\n---",
			title: "X",
			body:  "",
		},
		{
			// Invisible in every editor, so it is still a fence, on the same
			// reasoning that makes isBlankLine judge blankness by invisibility.
			name:  "a trailing space on the fence still closes the block",
			src:   "---\ntitle: X\n--- \nreal body\n",
			title: "X",
			body:  "real body\n",
		},
		{
			name:  "a trailing tab on the fence still closes the block",
			src:   "---\ntitle: X\n---\t\nreal body\n",
			title: "X",
			body:  "real body\n",
		},
		{
			name:  "a trailing space on a CRLF fence still closes the block",
			src:   "---\r\ntitle: X\r\n--- \r\nreal body\r\n",
			title: "X",
			body:  "real body\r\n",
		},
		{
			// A markdown horizontal rule is not a fence, so the block is
			// UNTERMINATED and the whole file is body. That is the reading that
			// loses nothing: the note simply has no properties.
			name:  "a four-dash horizontal rule is body",
			src:   "---\ntitle: X\n----\nreal body\n",
			title: "",
			body:  "---\ntitle: X\n----\nreal body\n",
		},
		{
			name:  "a longer horizontal rule is body",
			src:   "---\ntitle: X\n--------\nreal body\n",
			title: "",
			body:  "---\ntitle: X\n--------\nreal body\n",
		},
		{
			name:  "a line beginning --- with text after it is body",
			src:   "---\ntitle: X\n---more\nreal body\n",
			title: "",
			body:  "---\ntitle: X\n---more\nreal body\n",
		},
		{
			// The rule is INSIDE the region the real fence closes, so the whole
			// region is handed to YAML and "----" is not a key: the note is
			// reported as unparseable, which is this repo's documented handling
			// (named, skipped, its row dropped) and never rewrites the file.
			// The old search stopped at the rule and silently kept half of it.
			name:    "an unparseable region is reported, not silently halved",
			src:     "---\ntitle: X\n----\nstill: props\n---\nreal body\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, body, err := ParseFrontmatter([]byte(tc.src))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want a parse error, got meta=%v body=%q", meta, body)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if body != tc.body {
				t.Errorf("body = %q, want %q", body, tc.body)
			}
			got, _ := meta["title"].(string)
			if got != tc.title {
				t.Errorf("title = %q, want %q (meta = %v)", got, tc.title, meta)
			}
		})
	}
}

// The same rule, end to end and on disk: a note whose body sits under a
// horizontal rule must survive an unrelated frontmatter edit byte for byte.
// Serialize rewrites the whole file from the parsed body, so a truncated read
// wrote a truncated file.
func TestSerialize_ABodyUnderAHorizontalRuleSurvivesAnEdit(t *testing.T) {
	for _, src := range []string{
		"---\ntitle: X\n---\nreal body that must survive\n\n---\n\nand more after a rule\n",
		"---\ntitle: X\n---\nreal body\n\n----\n\nunder a four-dash rule\n",
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "n.md")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		doc, err := ParseFile(path)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		doc.SetMeta("status", "published")
		out, err := doc.Serialize()
		if err != nil {
			t.Fatalf("serialize: %v", err)
		}
		if !strings.Contains(string(out), doc.Body) || doc.Body == "" {
			t.Fatalf("the body did not survive:\nsrc:\n%s\ngot:\n%s", src, out)
		}
		for _, want := range []string{"real body", "status: published", "title: X"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("%q is missing from:\n%s", want, out)
			}
		}
	}
}

// A key the user did not touch comes out BYTE-IDENTICAL.
//
// The writer used to replace the value node of every key with a freshly
// marshaled one, so an unrelated `meta --set status=published` rewrote
// properties nobody touched: a date lost its own spelling, an id lost its
// leading zeros, a float lost its trailing one, a flow list became block style,
// and a value-attached comment disappeared. Once the READ side learned to
// preserve a note's own text, that left the two disagreeing, which is worse
// than both being wrong: `list` showed one thing and the file became another
// the moment any other property was edited.
func TestUpdateDocumentFrontmatterAST_LeavesUntouchedKeysByteIdentical(t *testing.T) {
	for _, line := range []string{
		"modified: 2020-01-01",
		"modified: 2020-01-01T00:00:00Z",
		"modified: 2020-01-01T00:00:00+02:00",
		"title: 2026-09-04",
		"id: 007",
		"num: 3.50",
		"tags: [2026-09-04, 42, real]",
		"note: v # keep me",
		"quoted: \"2026-09-04\"",
		"single: 'it''s here'",
		"flow: {a: 1, b: 2}",
		"empty:",
	} {
		t.Run(line, func(t *testing.T) {
			src := "---\n" + line + "\nkeep: me\n---\nbody\n"
			doc, err := Parse("n.md", []byte(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			fm := make(map[string]any, len(doc.Frontmatter)+1)
			for k, v := range doc.Frontmatter {
				fm[k] = v
			}
			fm["status"] = "published"

			out, err := UpdateDocumentFrontmatterAST([]byte(src), fm, doc.Body)
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if !strings.Contains(string(out), "\n"+line+"\n") {
				t.Errorf("the untouched line %q was rewritten:\n%s", line, out)
			}
			if !strings.Contains(string(out), "status: published") {
				t.Errorf("the changed key is missing:\n%s", out)
			}
			if !strings.Contains(string(out), "body") {
				t.Errorf("the body is missing:\n%s", out)
			}
		})
	}
}

// The key the caller DID change is rewritten, which is the other half of the
// contract: surgical must not mean inert.
func TestUpdateDocumentFrontmatterAST_RewritesAChangedKey(t *testing.T) {
	src := "---\ntitle: 2026-09-04\nstatus: draft\n---\nbody\n"
	doc, err := Parse("n.md", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc.SetMeta("title", "Renamed")
	fm := make(map[string]any, len(doc.Frontmatter))
	for k, v := range doc.Frontmatter {
		fm[k] = v
	}
	out, err := UpdateDocumentFrontmatterAST([]byte(src), fm, doc.Body)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(string(out), "title: Renamed") {
		t.Errorf("the changed key was not written:\n%s", out)
	}
	if strings.Contains(string(out), "2026-09-04") {
		t.Errorf("the old value survived a deliberate change:\n%s", out)
	}
	if !strings.Contains(string(out), "status: draft") {
		t.Errorf("the untouched key was rewritten:\n%s", out)
	}
}

// A NULL property has no text. `title: null` and `type: ~` mean the property is
// empty, and the node's Value is the literal spelling of the null, so reading
// that spelling gave a note the literal title "null" and turned a null in a tag
// list into the tag "null".
func TestParse_ANullPropertyIsAbsentNotTheWordNull(t *testing.T) {
	doc, err := Parse("n.md", []byte(
		"---\ntitle: null\ntype: ~\nstatus:\ntags: [null, real, ~]\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Title != "" {
		t.Errorf("Title = %q, want empty for a null property", doc.Title)
	}
	if doc.Type != "" {
		t.Errorf("Type = %q, want empty for a null property", doc.Type)
	}
	if doc.Status != "" {
		t.Errorf("Status = %q, want empty for a null property", doc.Status)
	}
	if len(doc.Tags) != 1 || doc.Tags[0] != "real" {
		t.Errorf("Tags = %v, want [real]: a null is not a tag", doc.Tags)
	}
}

// An ALIAS reads the value it points at, not the anchor's NAME. An alias node's
// own Value is the anchor name ("a"), so taking it verbatim would be worse than
// the resolved fallback.
func TestParse_AnAliasReadsTheAnchoredValue(t *testing.T) {
	doc, err := Parse("n.md", []byte(
		"---\nanchor: &a hello\ntitle: *a\ntags: [*a, real]\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Title != "hello" {
		t.Errorf("Title = %q, want the anchored value hello", doc.Title)
	}
	if len(doc.Tags) != 2 || doc.Tags[0] != "hello" {
		t.Errorf("Tags = %v, want [hello real]", doc.Tags)
	}
}

// SetMeta syncs the DATE struct fields, which the index reads instead of the
// map: `meta --set modified=...` followed by a reindex wrote the OLD timestamp.
func TestSetMeta_SyncsTheDateStructFields(t *testing.T) {
	doc, err := Parse("n.md", []byte("---\ncreated: 2020-01-01T00:00:00Z\nmodified: 2020-01-01T00:00:00Z\n---\nb\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc.SetMeta("modified", "2030-02-03T04:05:06Z")
	if doc.ModifiedAt != "2030-02-03T04:05:06Z" {
		t.Errorf("ModifiedAt = %q after SetMeta, want the value just set", doc.ModifiedAt)
	}
	doc.SetMeta("created", "2029-01-01T00:00:00Z")
	if doc.CreatedAt != "2029-01-01T00:00:00Z" {
		t.Errorf("CreatedAt = %q after SetMeta, want the value just set", doc.CreatedAt)
	}
}

// TagsOf reads a tag list as the note wrote it. The tag COMMANDS used to assert
// item.(string) on the parsed map, which dropped every unquoted date, integer
// and boolean tag, and then wrote the shortened list back to the file: one
// `tag add` deleted the others from disk.
func TestTagsOf_KeepsEveryScalarTag(t *testing.T) {
	doc, err := Parse("n.md", []byte("---\ntags: [2026-09-04, 42, true, real]\n---\nb\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"2026-09-04", "42", "true", "real"}
	got := TagsOf(doc)
	if len(got) != len(want) {
		t.Fatalf("TagsOf = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TagsOf = %v, want %v", got, want)
		}
	}
}
