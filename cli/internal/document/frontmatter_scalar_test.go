package document

import (
	"slices"
	"testing"
)

// An unquoted ISO-8601 date in frontmatter is a DATE, not a missing field.
// gopkg.in/yaml.v3 resolves such a scalar to time.Time, and Parse's old
// meta["modified"].(string) assertion failed silently for it, so CreatedAt and
// ModifiedAt stayed empty and the note vanished from `stale`. Obsidian's own
// Date property writes exactly this shape.
func TestParse_UnquotedFrontmatterDateIsStillADate(t *testing.T) {
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

// The neighbours of the date fields: id, title, type and status carried the
// same bare .(string) assertion, and a YAML scalar resolves to a narrower Go
// type for a bare number, a bare boolean and a bare date alike. A note titled
// `title: 2026-09-04` (the shape a daily note takes) indexed with an EMPTY
// title, so it could not be found by name and rendered nameless in every
// listing.
func TestParse_UnquotedScalarStringFields(t *testing.T) {
	doc, err := Parse("n.md", []byte(
		"---\nid: 007\ntitle: 2026-09-04\ntype: 3\nstatus: true\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.ID != "7" {
		t.Errorf("ID = %q, want %q", doc.ID, "7")
	}
	if doc.Title != "2026-09-04T00:00:00Z" {
		t.Errorf("Title = %q, want the normalized date", doc.Title)
	}
	if doc.Type != "3" {
		t.Errorf("Type = %q, want %q", doc.Type, "3")
	}
	if doc.Status != "true" {
		t.Errorf("Status = %q, want %q", doc.Status, "true")
	}
}

// A list entry YAML reads as a date, a number or a boolean is still a tag (and
// still an alias). extractTags and ExtractAliases dropped every such entry.
func TestExtractTagsAndAliases_CoerceScalarEntries(t *testing.T) {
	doc, err := Parse("n.md", []byte(
		"---\ntitle: N\ntags:\n  - 2026-09-04\n  - 42\n  - true\n  - real\naliases:\n  - 2026-09-04\n  - plain\n---\nbody\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantTags := []string{"2026-09-04T00:00:00Z", "42", "true", "real"}
	if !slices.Equal(doc.Tags, wantTags) {
		t.Errorf("Tags = %v, want %v", doc.Tags, wantTags)
	}
	wantAliases := []string{"2026-09-04T00:00:00Z", "plain"}
	if got := ExtractAliases(doc.Frontmatter); !slices.Equal(got, wantAliases) {
		t.Errorf("aliases = %v, want %v", got, wantAliases)
	}
}

// A bare scalar in place of a list is one entry, whatever type YAML gave it.
// `tags: foo` already worked; `tags: 2026-09-04` did not.
func TestExtractTags_BareScalar(t *testing.T) {
	for _, tc := range []struct {
		fm   string
		want []string
	}{
		{"tags: foo\n", []string{"foo"}},
		{"tags: 2026-09-04\n", []string{"2026-09-04T00:00:00Z"}},
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
// A number under `modified:` is not a date, and coercing it would put an
// unparseable value in a timestamp column instead of leaving it empty.
func TestFrontmatterTime_RefusesNonDates(t *testing.T) {
	for _, v := range []any{42, true, 3.5, []any{"a"}, nil} {
		if got, ok := frontmatterTime(map[string]any{"modified": v}, "modified"); ok {
			t.Errorf("frontmatterTime(%v) = (%q, true), want not ok", v, got)
		}
	}
}

// A missing key stays missing: the helpers must not invent a value.
func TestFrontmatterHelpers_MissingKey(t *testing.T) {
	empty := map[string]any{}
	if _, ok := frontmatterTime(empty, "modified"); ok {
		t.Error("frontmatterTime found a missing key")
	}
	if _, ok := frontmatterText(empty, "title"); ok {
		t.Error("frontmatterText found a missing key")
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
