package document

import (
	"testing"
	"time"
)

// Obsidian's own datetime property editor writes `2026-09-04T12:34:56`: no
// zone, no "Z". That spelling is on NO yaml.v3 layout list (resolve.go's
// allowedTimestampFormats carries RFC3339Nano, its lower-case "t" twin, the
// space-separated zone-less form and the date-only form, and nothing that is
// T-separated without a zone), so yaml.v3 resolves it to a plain !!str and the
// verbatim text landed in documents.modified_at.
//
// The consequence is the one asserted last here: time.Parse(time.RFC3339, ...)
// at cli/stale.go fails on that text, so DaysStale stayed 0 and the note was
// reported fresh forever.
func TestParse_ObsidianZonelessDatetimeIsADate(t *testing.T) {
	for _, tc := range []struct{ fm, want string }{
		// The two shapes Obsidian's datetime editor writes.
		{"modified: 2026-09-04T12:34:56\n", "2026-09-04T12:34:56Z"},
		{"modified: 2026-09-04T12:34\n", "2026-09-04T12:34:00Z"},
		// Quoted, which is the shape 2nb itself has been writing. Every quoted
		// value is a !!str whatever its shape, so these reach the string case
		// too, including the two layouts yaml.v3 resolves when unquoted.
		{"modified: \"2026-09-04\"\n", "2026-09-04T00:00:00Z"},
		{"modified: \"2026-09-04 12:34:56\"\n", "2026-09-04T12:34:56Z"},
		{"modified: \"2026-09-04T12:34:56Z\"\n", "2026-09-04T12:34:56Z"},
		// A zone offset is preserved rather than shifted to UTC: the column
		// stores RFC3339, and an offset is RFC3339.
		{"modified: \"2026-09-04T12:34:56+02:00\"\n", "2026-09-04T12:34:56+02:00"},
		// Unquoted forms yaml.v3 already resolves keep working unchanged.
		{"modified: 2026-09-04T12:34:56Z\n", "2026-09-04T12:34:56Z"},
		{"modified: 2026-09-04\n", "2026-09-04T00:00:00Z"},
	} {
		doc, err := Parse("n.md", []byte("---\ntitle: N\n"+tc.fm+"---\nbody\n"))
		if err != nil {
			t.Fatalf("parse %q: %v", tc.fm, err)
		}
		if doc.ModifiedAt != tc.want {
			t.Errorf("%q -> ModifiedAt = %q, want %q", tc.fm, doc.ModifiedAt, tc.want)
		}
		if _, perr := time.Parse(time.RFC3339, doc.ModifiedAt); perr != nil {
			t.Errorf("%q -> ModifiedAt %q does not parse as RFC3339, so `stale` reports DaysStale 0: %v", tc.fm, doc.ModifiedAt, perr)
		}
	}
}

// Text that is not a date passes through verbatim. Normalizing is an
// improvement on top of the old behavior, never a filter: a value the layouts
// do not recognize must land in the column exactly as it did before, so a vault
// carrying some other convention is not silently blanked.
func TestParse_NonDateTextInADateFieldIsUnchanged(t *testing.T) {
	for _, tc := range []struct{ fm, want string }{
		{"modified: not a date\n", "not a date"},
		{"modified: \"\"\n", ""},
		{"modified: 2026-13-45\n", "2026-13-45"},
		{"modified: \"09/04/2026\"\n", "09/04/2026"},
	} {
		doc, err := Parse("n.md", []byte("---\ntitle: N\n"+tc.fm+"---\nbody\n"))
		if err != nil {
			t.Fatalf("parse %q: %v", tc.fm, err)
		}
		if doc.ModifiedAt != tc.want {
			t.Errorf("%q -> ModifiedAt = %q, want %q", tc.fm, doc.ModifiedAt, tc.want)
		}
	}
}

// A TEXT field is not a date field, and normalizing one is the bug 0.22.4 was
// released to fix: a daily note titled `2026-09-04` indexed as
// `2026-09-04T00:00:00Z` and could not be found by its own name. Only
// frontmatterTime normalizes; frontmatterText reads the node's verbatim value
// and must stay that way.
func TestParse_ADateShapedTitleIsStillItsOwnText(t *testing.T) {
	for _, fm := range []string{"title: 2026-09-04\n", "title: \"2026-09-04\"\n", "title: 2026-09-04T12:34:56\n"} {
		doc, err := Parse("n.md", []byte("---\n"+fm+"---\nbody\n"))
		if err != nil {
			t.Fatalf("parse %q: %v", fm, err)
		}
		want := fm[len("title: ") : len(fm)-1]
		if len(want) > 1 && want[0] == '"' {
			want = want[1 : len(want)-1]
		}
		if doc.Title != want {
			t.Errorf("%q -> Title = %q, want %q", fm, doc.Title, want)
		}
	}
}

// normalizeDateText refuses what it cannot parse rather than guessing, so the
// caller can pass the text through instead of inventing an instant.
func TestNormalizeDateText_RefusesNonDates(t *testing.T) {
	for _, s := range []string{"", "not a date", "2026", "2026-13-45", "09/04/2026", "2026-09-04T12"} {
		if got, ok := normalizeDateText(s); ok {
			t.Errorf("normalizeDateText(%q) = (%q, true), want not ok", s, got)
		}
	}
}

// Every layout in the table has a named writer, and these are the two groups
// added after the first release of this reader shipped without them.
//
// The lower-case "t" separator is yaml.v3's own (allowedTimestampFormats
// carries `2006-1-2t15:4:5.999999999Z07:00`), so an UNQUOTED value spelled that
// way already resolved to a time.Time while the same value QUOTED, or supplied
// to `meta --set`, failed to parse and was stored as text: the reader and the
// writer disagreed about one instant.
//
// Minute precision carrying a zone is on no yaml.v3 layout at all, and it is
// what a datetime editor writes when the seconds are zero, so `stale` reported
// 0 days for those notes exactly as it did for the zone-less form.
func TestParse_LowercaseTAndMinutePrecisionAreDates(t *testing.T) {
	for _, tc := range []struct{ fm, want string }{
		{"modified: 2026-09-04t12:34:56Z\n", "2026-09-04T12:34:56Z"},
		{"modified: \"2026-09-04t12:34:56Z\"\n", "2026-09-04T12:34:56Z"},
		{"modified: \"2026-09-04t12:34:56\"\n", "2026-09-04T12:34:56Z"},
		{"modified: 2026-09-04t12:34\n", "2026-09-04T12:34:00Z"},
		{"modified: 2026-09-04T12:34Z\n", "2026-09-04T12:34:00Z"},
		{"modified: 2026-09-04T12:34+02:00\n", "2026-09-04T12:34:00+02:00"},
		{"modified: \"2026-09-04T12:34-07:00\"\n", "2026-09-04T12:34:00-07:00"},
		// Unpadded fields and a fractional second are yaml.v3's spelling too,
		// and the fraction is dropped because the column is second precision.
		{"modified: \"2026-9-4\"\n", "2026-09-04T00:00:00Z"},
		{"modified: \"2026-09-04T12:34:56.75Z\"\n", "2026-09-04T12:34:56Z"},
	} {
		doc, err := Parse("n.md", []byte("---\ntitle: N\n"+tc.fm+"---\nbody\n"))
		if err != nil {
			t.Fatalf("parse %q: %v", tc.fm, err)
		}
		if doc.ModifiedAt != tc.want {
			t.Errorf("%q -> ModifiedAt = %q, want %q", tc.fm, doc.ModifiedAt, tc.want)
		}
		if _, perr := time.Parse(time.RFC3339, doc.ModifiedAt); perr != nil {
			t.Errorf("%q -> ModifiedAt %q does not parse as RFC3339: %v", tc.fm, doc.ModifiedAt, perr)
		}
	}
}

// A shorter layout must never CLAIM a longer value and silently drop what it
// could not consume. time.Parse is anchored, so `2006-1-2T15:4` fails on a
// value carrying seconds rather than truncating them, and `2006-1-2` fails on a
// value carrying a time. This is the assertion that makes the table's ordering
// defensive rather than load-bearing: if it ever regressed, every second and
// every time-of-day Obsidian wrote would be quietly discarded.
func TestParseFrontmatterDate_AShorterLayoutNeverTruncates(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"2026-09-04T12:34:56", "2026-09-04T12:34:56Z"},
		{"2026-09-04T12:34:56Z", "2026-09-04T12:34:56Z"},
		{"2026-09-04 12:34:56", "2026-09-04T12:34:56Z"},
		{"2026-09-04t12:34:56", "2026-09-04T12:34:56Z"},
	} {
		got, ok := ParseFrontmatterDate(tc.in)
		if !ok {
			t.Fatalf("ParseFrontmatterDate(%q) refused", tc.in)
		}
		if s := got.Format(time.RFC3339); s != tc.want {
			t.Errorf("ParseFrontmatterDate(%q) = %s, want %s (a shorter layout truncated it)", tc.in, s, tc.want)
		}
	}
}
