package document

import (
	"strings"
	"testing"
)

// reparse is the assertion a write bug hides from: a write that produces
// invalid YAML looks fine until the next READ, and by then the note has
// dropped out of the index and every `meta` and `tag` on it errors out.
func reparse(t *testing.T, out []byte) map[string]any {
	t.Helper()
	meta, _, err := ParseFrontmatter(out)
	if err != nil {
		t.Fatalf("the file this write produced does not parse: %v\n%s", err, out)
	}
	return meta
}

// writeThrough applies edits (and removals) to a note the way `meta --set` and
// `meta --remove` do, and returns the bytes that would be written to disk.
func writeThrough(t *testing.T, src string, edits map[string]any, remove ...string) []byte {
	t.Helper()
	doc, err := Parse("n.md", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fm := make(map[string]any, len(doc.Frontmatter))
	for k, v := range doc.Frontmatter {
		fm[k] = v
	}
	for k, v := range edits {
		fm[k] = v
	}
	for _, k := range remove {
		delete(fm, k)
	}
	out, err := UpdateDocumentFrontmatterAST([]byte(src), fm, doc.Body)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	return out
}

// EVERY fence decision goes through one predicate.
//
// Teaching the CLOSING fence to tolerate a trailing space while
// emptyFrontmatterBlock still demanded an exact "---" made an empty block with
// one read as UNTERMINATED: the whole note became body, and the write then
// prepended a fresh block in front of the original, leaving the note with TWO
// frontmatter blocks and the old fence stranded in its text. An OPENING fence
// carrying the same invisible character was not recognized at all, so every
// property fell into the body. Two definitions of one boundary is the shape
// this file has been fixed for three times.
func TestFence_OneDefinitionEverywhere(t *testing.T) {
	t.Run("an empty block with a trailing space is still an empty block", func(t *testing.T) {
		out := writeThrough(t, "---\n--- \nbody\n", map[string]any{"status": "published"})
		got := string(out)
		if strings.Count(got, "---") != 2 {
			t.Errorf("the note came out with more than one frontmatter block:\n%q", got)
		}
		if !strings.Contains(got, "status: published") || !strings.Contains(got, "body") {
			t.Errorf("the write lost content:\n%q", got)
		}
		reparse(t, out)
	})

	t.Run("an opening fence with a trailing space still opens the block", func(t *testing.T) {
		for _, src := range []string{
			"--- \ntitle: X\n---\nbody\n",
			"---\t\ntitle: X\n---\nbody\n",
			"--- \r\ntitle: X\r\n---\r\nbody\r\n",
		} {
			meta, body, err := ParseFrontmatter([]byte(src))
			if err != nil {
				t.Fatalf("parse %q: %v", src, err)
			}
			if got, _ := meta["title"].(string); got != "X" {
				t.Errorf("%q -> title = %q, want X (meta = %v)", src, got, meta)
			}
			if !strings.HasPrefix(body, "body") {
				t.Errorf("%q -> body = %q, want it to start with the body", src, body)
			}
		}
	})

	// The fence and the blank-line rule must agree about what is invisible.
	// isBlankLine was widened in cycle 3 to unicode.IsSpace plus the zero-width
	// format characters precisely because a tab is invisible; a fence that
	// accepted only ' ' and '\t' disagreed with it, so a non-breaking space or
	// a zero-width character on the fence dropped every property into the body.
	t.Run("the fence accepts every character a blank line calls invisible", func(t *testing.T) {
		for _, filler := range []string{" ", "\t", "\u00a0", "\u200b", "\ufeff", "\u200c", "\u200d", " \t\u00a0"} {
			src := "---\ntitle: X\n---" + filler + "\nbody\n"
			meta, body, err := ParseFrontmatter([]byte(src))
			if err != nil {
				t.Fatalf("parse with filler %q: %v", filler, err)
			}
			if got, _ := meta["title"].(string); got != "X" {
				t.Errorf("filler %q -> title = %q, want X", filler, got)
			}
			if body != "body\n" {
				t.Errorf("filler %q -> body = %q, want \"body\\n\"", filler, body)
			}
		}
	})

	// Still not a fence: a rule, and a line that merely starts with one.
	t.Run("visible characters after the dashes are still body", func(t *testing.T) {
		for _, src := range []string{
			"---\ntitle: X\n----\nbody\n",
			"---\ntitle: X\n---more\nbody\n",
			"---\ntitle: X\n--- x\nbody\n",
		} {
			meta, body, err := ParseFrontmatter([]byte(src))
			if err != nil {
				t.Fatalf("parse %q: %v", src, err)
			}
			if len(meta) != 0 {
				t.Errorf("%q read as frontmatter %v, want none", src, meta)
			}
			if body != src {
				t.Errorf("%q -> body = %q, want the whole file", src, body)
			}
		}
	})
}

// A write must never leave a DANGLING ALIAS. Replacing the node that carries an
// anchor while an alias still points at it produces a file that is not YAML, so
// the note fails every future parse, drops out of the index, and `meta` and
// `tag` on it error out. That is worse than the wholesale re-marshal it
// replaced, which resolved the alias and stayed valid.
//
// The alias is RESOLVED rather than the anchor carried onto the replacement.
// Carrying it would make the aliasing key follow the edited one, silently
// changing the value of a key the user did not touch, which is exactly what the
// surgical write exists to prevent. Resolving keeps that key's VALUE and loses
// only the anchor structure.
func TestUpdateDocumentFrontmatterAST_NeverLeavesADanglingAlias(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		edits   map[string]any
		remove  []string
		wantKey string
		want    any
	}{
		{
			name:    "editing a scalar anchor resolves the alias to its value",
			src:     "---\nstatus: &s draft\nother: *s\n---\nbody\n",
			edits:   map[string]any{"status": "published"},
			wantKey: "other",
			want:    "draft",
		},
		{
			name:    "REMOVING a scalar anchor resolves the alias too",
			src:     "---\nstatus: &s draft\nother: *s\n---\nbody\n",
			remove:  []string{"status"},
			wantKey: "other",
			want:    "draft",
		},
		{
			name:    "editing a SEQUENCE anchor",
			src:     "---\ntags: &a [x, y]\nalso: *a\n---\nbody\n",
			edits:   map[string]any{"tags": []any{"z"}},
			wantKey: "also",
			want:    []any{"x", "y"},
		},
		{
			name:    "editing a MAPPING anchor",
			src:     "---\nm: &a {k: v}\nalso: *a\n---\nbody\n",
			edits:   map[string]any{"m": map[string]any{"k": "w"}},
			wantKey: "also",
			want:    map[string]any{"k": "v"},
		},
		{
			name:    "an anchor NESTED inside the edited key",
			src:     "---\na: {x: &s 1}\nb: *s\n---\nbody\n",
			edits:   map[string]any{"a": map[string]any{"x": 2}},
			wantKey: "b",
			want:    1,
		},
		{
			// Editing the ALIAS needs nothing special: an anchor with no
			// remaining reference is valid YAML. Guard.
			name:    "editing the alias leaves the anchor alone",
			src:     "---\nstatus: &s draft\nother: *s\n---\nbody\n",
			edits:   map[string]any{"other": "X"},
			wantKey: "status",
			want:    "draft",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := writeThrough(t, tc.src, tc.edits, tc.remove...)
			meta := reparse(t, out)
			got := meta[tc.wantKey]
			if !deepEqualish(got, tc.want) {
				t.Errorf("%s = %#v after the write, want %#v\n%s", tc.wantKey, got, tc.want, out)
			}
		})
	}
}

// deepEqualish compares decoded YAML values without pulling in reflect for the
// test: everything here is a string, an int, a []any or a map[string]any.
func deepEqualish(a, b any) bool {
	switch bv := b.(type) {
	case []any:
		av, ok := a.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range bv {
			if !deepEqualish(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		av, ok := a.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k := range bv {
			if !deepEqualish(av[k], bv[k]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// A CRLF note keeps its line endings. The yaml encoder emits LF, so a CRLF note
// came back with LF fences and LF between its keys while its body kept CRLF:
// one file, two conventions, from an edit that touched one property.
func TestUpdateDocumentFrontmatterAST_PreservesLineEndings(t *testing.T) {
	t.Run("CRLF stays CRLF", func(t *testing.T) {
		out := writeThrough(t, "---\r\ntitle: X\r\n---\r\nbody\r\n", map[string]any{"status": "published"})
		got := string(out)
		if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
			t.Errorf("a bare LF survived in a CRLF note:\n%q", got)
		}
		if !strings.Contains(got, "status: published\r\n") {
			t.Errorf("the written key does not carry the note's line ending:\n%q", got)
		}
		reparse(t, out)
	})

	t.Run("LF stays LF", func(t *testing.T) {
		out := writeThrough(t, "---\ntitle: X\n---\nbody\n", map[string]any{"status": "published"})
		if strings.Contains(string(out), "\r") {
			t.Errorf("a CR appeared in an LF note:\n%q", out)
		}
		reparse(t, out)
	})
}

// SetMeta syncs EVERY struct field the Document mirrors from frontmatter. The
// index reads these fields, not the map, so one that is missing leaves a stale
// value flowing into the database and the chunk tables. `id` was missing
// outright, and title/type/status used a strict value.(string), so setting one
// to a non-string scalar left the field unpopulated.
func TestSetMeta_SyncsEveryMirroredStructField(t *testing.T) {
	doc, err := Parse("n.md", []byte(
		"---\nid: a\ntitle: T\ntype: note\nstatus: draft\ntags: [one]\ncreated: 2020-01-01T00:00:00Z\nmodified: 2020-01-01T00:00:00Z\n---\nb\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	doc.SetMeta("id", "new-id")
	if doc.ID != "new-id" {
		t.Errorf("ID = %q, want new-id", doc.ID)
	}
	doc.SetMeta("type", "adr")
	if doc.Type != "adr" {
		t.Errorf("Type = %q, want adr", doc.Type)
	}
	doc.SetMeta("tags", []any{"two"})
	if len(doc.Tags) != 1 || doc.Tags[0] != "two" {
		t.Errorf("Tags = %v, want [two]", doc.Tags)
	}

	// A non-string scalar populates the field rather than being dropped.
	doc.SetMeta("title", 12345)
	if doc.Title != "12345" {
		t.Errorf("Title = %q after a numeric set, want 12345", doc.Title)
	}
	doc.SetMeta("status", true)
	if doc.Status != "true" {
		t.Errorf("Status = %q after a boolean set, want true", doc.Status)
	}
	doc.SetMeta("id", 7)
	if doc.ID != "7" {
		t.Errorf("ID = %q after a numeric set, want 7", doc.ID)
	}
}

// The line-ending decision is taken ONCE, before any early return. An empty
// frontmatter block takes a different route through the writer (it delegates to
// the serializer), and that route hardcoded LF, so a CRLF note with an emptied
// properties block came back with LF fences over a CRLF body. Two paths that
// should agree is the fault this file keeps producing.
func TestUpdateDocumentFrontmatterAST_LineEndingIsOneDecision(t *testing.T) {
	for _, tc := range []struct {
		name    string
		src     string
		wantCR  bool
		wantKey string
	}{
		{"CRLF with an EMPTY block", "---\r\n---\r\nbody\r\n", true, "status: published\r\n"},
		{"CRLF with properties", "---\r\ntitle: X\r\n---\r\nbody\r\n", true, "status: published\r\n"},
		{"LF with an EMPTY block", "---\n---\nbody\n", false, "status: published\n"},
		{"LF with properties", "---\ntitle: X\n---\nbody\n", false, "status: published\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := writeThrough(t, tc.src, map[string]any{"status": "published"})
			got := string(out)
			if !strings.Contains(got, tc.wantKey) {
				t.Errorf("the written key does not carry the note's line ending:\n%q", got)
			}
			if tc.wantCR {
				if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
					t.Errorf("a bare LF survived in a CRLF note:\n%q", got)
				}
			} else if strings.Contains(got, "\r") {
				t.Errorf("a CR appeared in an LF note:\n%q", got)
			}
			reparse(t, out)
		})
	}
}

// BOTH fence lines come back byte for byte. The reader deliberately admits a
// fence carrying invisible trailing characters, so a writer that normalized
// them to a bare "---" rewrote a line the user never touched: the same
// reader-accepts/writer-normalizes asymmetry that produced the duplicated block
// and the unrecognized opening fence.
func TestUpdateDocumentFrontmatterAST_PreservesBothFencesVerbatim(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src        string
		wantFences []string
	}{
		{
			name:       "a zero-width character on the opening fence",
			src:        "---​\ntitle: X\n---\nbody\n",
			wantFences: []string{"---​\n", "\n---\n"},
		},
		{
			name:       "a non-breaking space on the closing fence",
			src:        "---\ntitle: X\n--- \nbody\n",
			wantFences: []string{"---\n", "--- \n"},
		},
		{
			name:       "trailing spaces on both",
			src:        "--- \ntitle: X\n--- \nbody\n",
			wantFences: []string{"--- \n", "--- \n"},
		},
		{
			name:       "a plain note keeps plain fences",
			src:        "---\ntitle: X\n---\nbody\n",
			wantFences: []string{"---\n", "---\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := writeThrough(t, tc.src, map[string]any{"status": "published"})
			got := string(out)
			for _, fence := range tc.wantFences {
				if !strings.Contains(got, fence) {
					t.Errorf("the fence %q was rewritten:\n%q", fence, got)
				}
			}
			if !strings.Contains(got, "status: published") || !strings.Contains(got, "body") {
				t.Errorf("the write lost content:\n%q", got)
			}
			reparse(t, out)
		})
	}
}

// A NIL clears the mirrored struct field. nil is how a property is emptied
// (kb_update_meta passes a JSON null straight through), and leaving the field
// holding the old value while the map says the property is gone is the same
// stale-field bug as the missing `id` case: the index reads the field, not the
// map. A list or a mapping is not a value these fields can hold, so those leave
// the field as it was rather than blanking it on a write that says nothing.
func TestSetMeta_NilClearsTheMirroredField(t *testing.T) {
	doc, err := Parse("n.md", []byte(
		"---\nid: a\ntitle: T\ntype: note\nstatus: draft\ncreated: 2020-01-01T00:00:00Z\nmodified: 2020-01-01T00:00:00Z\n---\nb\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, key := range []string{"id", "title", "type", "status", "created", "modified"} {
		doc.SetMeta(key, nil)
	}
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"ID", doc.ID}, {"Title", doc.Title}, {"Type", doc.Type},
		{"Status", doc.Status}, {"CreatedAt", doc.CreatedAt}, {"ModifiedAt", doc.ModifiedAt},
	} {
		if tc.got != "" {
			t.Errorf("%s = %q after being set to nil, want empty", tc.name, tc.got)
		}
	}

	// A composite says nothing about a string field, so it leaves it alone.
	doc2, err := Parse("n.md", []byte("---\ntitle: T\n---\nb\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc2.SetMeta("title", []any{"a", "b"})
	if doc2.Title != "T" {
		t.Errorf("Title = %q after a list set, want the previous value untouched", doc2.Title)
	}
}
