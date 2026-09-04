package document

import (
	"slices"
	"testing"
)

// A frontmatter value has two readings, and these tests pin which field gets
// which. A DATE field normalizes to RFC3339, because that is the format the
// index column stores and `stale` parses back, and every way of writing the
// same instant must land there identically. A TEXT field keeps the note's own
// text, because resolution is LOSSY: `2026-09-04` and `2026-09-04T00:00:00Z`
// decode to the same time.Time, so no formatting choice can reproduce both.

// DATE fields: every legitimate spelling of an instant normalizes to the same
// RFC3339 string. The unquoted forms are what Obsidian's own Date property
// writes; before this they left the column EMPTY and `stale` (which filters
// `modified_at != ”`) omitted the note however old it was.
func TestParse_DateFieldsNormalizeToRFC3339(t *testing.T) {
	cases := []struct {
		name     string
		fm       string
		created  string
		modified string
	}{
		{
			name:     "unquoted timestamp (what Obsidian's Date property writes)",
			fm:       "created: 2020-01-01T00:00:00Z\nmodified: 2020-02-02T03:04:05Z\n",
			created:  "2020-01-01T00:00:00Z",
			modified: "2020-02-02T03:04:05Z",
		},
		{
			name:     "quoted timestamp (what 2nb create and 2nb meta write)",
			fm:       "created: \"2020-01-01T00:00:00Z\"\nmodified: \"2020-02-02T03:04:05Z\"\n",
			created:  "2020-01-01T00:00:00Z",
			modified: "2020-02-02T03:04:05Z",
		},
		{
			name:     "unquoted date with no time normalizes to RFC3339 midnight",
			fm:       "created: 2020-01-01\nmodified: 2020-02-02\n",
			created:  "2020-01-01T00:00:00Z",
			modified: "2020-02-02T00:00:00Z",
		},
		{
			name:     "unquoted timestamp with an offset keeps its offset",
			fm:       "created: 2020-01-01T00:00:00+02:00\nmodified: 2020-02-02T03:04:05+02:00\n",
			created:  "2020-01-01T00:00:00+02:00",
			modified: "2020-02-02T03:04:05+02:00",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse("n.md", []byte("---\ntitle: N\n"+tc.fm+"---\nbody\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if doc.CreatedAt != tc.created {
				t.Errorf("CreatedAt = %q, want %q", doc.CreatedAt, tc.created)
			}
			if doc.ModifiedAt != tc.modified {
				t.Errorf("ModifiedAt = %q, want %q", doc.ModifiedAt, tc.modified)
			}
		})
	}
}

// The two intents in ONE note, which is the case that makes the distinction
// concrete: a daily note titled by its date, dated by the same date. The title
// must read back exactly as written, and the date must normalize.
func TestParse_ADailyNoteKeepsItsTitleAndNormalizesItsDate(t *testing.T) {
	doc, err := Parse("2026-09-04.md", []byte(
		"---\ntitle: 2026-09-04\nmodified: 2026-09-04\n---\nstandup\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Title != "2026-09-04" {
		t.Errorf("Title = %q, want the text the note carries, 2026-09-04", doc.Title)
	}
	if doc.ModifiedAt != "2026-09-04T00:00:00Z" {
		t.Errorf("ModifiedAt = %q, want the normalized 2026-09-04T00:00:00Z", doc.ModifiedAt)
	}
}

// TEXT fields: id, title, type and status read back as the note wrote them,
// quoted or not. yaml.v3 resolves an unquoted scalar to its own Go type, and a
// bare .(string) assertion left every one of these EMPTY, so a note titled with
// a bare date or a bare number could not be found by name and rendered nameless
// in every listing. Formatting the RESOLVED value instead was the other half of
// the bug: it turned `title: 2026-09-04` into `2026-09-04T00:00:00Z`, which is
// not what the file says.
func TestParse_TextFieldsKeepTheNotesOwnText(t *testing.T) {
	cases := []struct {
		name                       string
		fm                         string
		id, title, docType, status string
	}{
		{
			name:    "unquoted scalars keep their text",
			fm:      "id: 007\ntitle: 2026-09-04\ntype: 3\nstatus: true\n",
			id:      "007",
			title:   "2026-09-04",
			docType: "3",
			status:  "true",
		},
		{
			name:    "quoted scalars are unchanged",
			fm:      "id: \"007\"\ntitle: \"2026-09-04\"\ntype: \"3\"\nstatus: \"true\"\n",
			id:      "007",
			title:   "2026-09-04",
			docType: "3",
			status:  "true",
		},
		{
			name:    "an unquoted full timestamp title keeps its own spelling",
			fm:      "id: a\ntitle: 2026-09-04T00:00:00Z\ntype: note\nstatus: draft\n",
			id:      "a",
			title:   "2026-09-04T00:00:00Z",
			docType: "note",
			status:  "draft",
		},
		{
			name:    "a trailing zero survives",
			fm:      "id: b\ntitle: 3.50\ntype: note\nstatus: draft\n",
			id:      "b",
			title:   "3.50",
			docType: "note",
			status:  "draft",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse("n.md", []byte("---\n"+tc.fm+"---\nbody\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if doc.ID != tc.id {
				t.Errorf("ID = %q, want %q", doc.ID, tc.id)
			}
			if doc.Title != tc.title {
				t.Errorf("Title = %q, want %q", doc.Title, tc.title)
			}
			if doc.Type != tc.docType {
				t.Errorf("Type = %q, want %q", doc.Type, tc.docType)
			}
			if doc.Status != tc.status {
				t.Errorf("Status = %q, want %q", doc.Status, tc.status)
			}
		})
	}
}

// A tag or an alias is TEXT: a bare date stays the date the note shows, a bare
// number stays its digits, and the quoted form of each is identical. Every one
// of these was dropped from the note entirely before, and then rendered as a
// normalized timestamp by the first attempt at fixing that.
func TestExtractTagsAndAliases_KeepTheNotesOwnText(t *testing.T) {
	for _, tc := range []struct {
		name string
		fm   string
	}{
		{"unquoted", "tags:\n  - 2026-09-04\n  - 42\n  - true\n  - real\naliases:\n  - 2026-09-04\n  - plain\n"},
		{"quoted", "tags:\n  - \"2026-09-04\"\n  - \"42\"\n  - \"true\"\n  - \"real\"\naliases:\n  - \"2026-09-04\"\n  - \"plain\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse("n.md", []byte("---\ntitle: N\n"+tc.fm+"---\nbody\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			wantTags := []string{"2026-09-04", "42", "true", "real"}
			if !slices.Equal(doc.Tags, wantTags) {
				t.Errorf("Tags = %v, want %v", doc.Tags, wantTags)
			}
			wantAliases := []string{"2026-09-04", "plain"}
			if got := AliasesOf(doc); !slices.Equal(got, wantAliases) {
				t.Errorf("aliases = %v, want %v", got, wantAliases)
			}
		})
	}
}

// An element that is not a scalar is not a tag, and is still dropped.
func TestExtractTags_DropsANestedComposite(t *testing.T) {
	doc, err := Parse("n.md", []byte(
		"---\ntitle: N\ntags:\n  - real\n  - [nested]\n  - {k: v}\n  - alsoreal\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"real", "alsoreal"}
	if !slices.Equal(doc.Tags, want) {
		t.Errorf("Tags = %v, want %v", doc.Tags, want)
	}
}

// A bare scalar in place of a list is one entry, in the note's own text.
func TestExtractTags_BareScalar(t *testing.T) {
	for _, tc := range []struct {
		fm   string
		want []string
	}{
		{"tags: foo\n", []string{"foo"}},
		{"tags: 2026-09-04\n", []string{"2026-09-04"}},
		{"tags: 42\n", []string{"42"}},
		{"tags:\n", nil},
		{"tags: \"\"\n", nil},
	} {
		doc, err := Parse("n.md", []byte("---\ntitle: N\n"+tc.fm+"---\nbody\n"))
		if err != nil {
			t.Fatalf("parse %q: %v", tc.fm, err)
		}
		if !slices.Equal(doc.Tags, tc.want) {
			t.Errorf("%q -> Tags = %v, want %v", tc.fm, doc.Tags, tc.want)
		}
	}
}

// frontmatterTime accepts only the two shapes a date legitimately arrives in.
// A number, a boolean, a list, a mapping or a null under `modified:` is not a
// date, and coercing one would put an unparseable value in a timestamp column
// instead of leaving it empty.
func TestFrontmatterTime_RefusesNonDates(t *testing.T) {
	for _, v := range []any{42, true, 3.5, []any{"a"}, map[string]any{"k": "v"}, nil} {
		if got, ok := frontmatterTime(map[string]any{"modified": v}, "modified"); ok {
			t.Errorf("frontmatterTime(%v) = (%q, true), want not ok", v, got)
		}
	}
}

// The same shapes on a real note leave the date columns empty rather than
// inventing a value.
func TestParse_NonDateInADateFieldLeavesTheColumnEmpty(t *testing.T) {
	for _, fm := range []string{"modified:\n", "modified: [a, b]\n", "modified:\n  k: v\n"} {
		doc, err := Parse("n.md", []byte("---\ntitle: N\n"+fm+"---\nbody\n"))
		if err != nil {
			t.Fatalf("parse %q: %v", fm, err)
		}
		if doc.ModifiedAt != "" {
			t.Errorf("%q -> ModifiedAt = %q, want empty", fm, doc.ModifiedAt)
		}
	}
}

// A missing key stays missing: the helpers must not invent a value.
func TestFrontmatterHelpers_MissingKey(t *testing.T) {
	empty := map[string]any{}
	if _, ok := frontmatterTime(empty, "modified"); ok {
		t.Error("frontmatterTime found a missing key")
	}
	if _, ok := frontmatterText(empty, nil, "title"); ok {
		t.Error("frontmatterText found a missing key")
	}
}

// With no node behind the map (a document built in code, a synthetic
// .canvas/.base view, a SetMeta write), the readers fall back to ScalarText on
// the resolved value rather than losing the field.
func TestFrontmatterText_FallsBackWithoutANode(t *testing.T) {
	meta := map[string]any{"title": 12345, "status": true}
	if got, ok := frontmatterText(meta, nil, "title"); !ok || got != "12345" {
		t.Errorf("frontmatterText(title) = (%q, %v), want (\"12345\", true)", got, ok)
	}
	if got, ok := frontmatterText(meta, nil, "status"); !ok || got != "true" {
		t.Errorf("frontmatterText(status) = (%q, %v), want (\"true\", true)", got, ok)
	}
}

// SetMeta replaces a value, so the note's old text for that key must stop being
// used. Without the invalidation the stale node text would win over the value
// just written.
func TestSetMeta_InvalidatesTheNotesOldText(t *testing.T) {
	doc, err := Parse("n.md", []byte("---\ntitle: 2026-09-04\ntags:\n  - 2026-09-04\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	doc.SetMeta("title", "Renamed")
	if doc.Title != "Renamed" {
		t.Errorf("Title = %q after SetMeta, want Renamed", doc.Title)
	}
	doc.SetMeta("tags", []any{"fresh"})
	if !slices.Equal(doc.Tags, []string{"fresh"}) {
		t.Errorf("Tags = %v after SetMeta, want [fresh]", doc.Tags)
	}
}

// Parse must not NORMALIZE the frontmatter map. Serialize re-marshals every key
// of Frontmatter through UpdateDocumentFrontmatterAST, so replacing a time.Time
// with a string there would make an unrelated `meta --set` requote a date line
// the user never touched. GUARD: this holds at base and must keep holding.
func TestParse_LeavesFrontmatterValuesUntouched(t *testing.T) {
	doc, err := Parse("n.md", []byte("---\ntitle: N\nmodified: 2020-01-01T00:00:00Z\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s, isString := doc.Frontmatter["modified"].(string); isString {
		t.Errorf("Parse rewrote frontmatter[modified] as the string %q; the note's own bytes must be left alone", s)
	}
}
