package vault

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// dateSchemas declares one field of every FieldDef.Type the coercion cares
// about, so the routing decision is asserted rather than assumed. Until now
// FieldDef.Type had exactly two consumers, "list" and "tags", so a schema
// declaring `date` was parsed and then ignored.
func dateSchemas() *SchemaSet {
	return &SchemaSet{Types: map[string]DocTypeSchema{
		"meeting": {
			Name: "Meeting",
			Fields: map[string]FieldDef{
				"held":      {Type: "date"},
				"starts_at": {Type: "datetime"},
				"summary":   {Type: "text"},
				"attendees": {Type: "list"},
			},
		},
	}}
}

func TestIsDateField_UniversalAndSchemaDeclared(t *testing.T) {
	s := dateSchemas()
	for _, tc := range []struct {
		docType, field string
		want           bool
	}{
		// created and modified are universal: every note carries them whether
		// or not its type declares them, and NewDocument writes both.
		{"meeting", "created", true},
		{"meeting", "modified", true},
		{"note", "created", true},
		{"note", "modified", true},
		// Schema-declared.
		{"meeting", "held", true},
		{"meeting", "starts_at", true},
		// Declared as something else, or not declared at all.
		{"meeting", "summary", false},
		{"meeting", "attendees", false},
		{"meeting", "whatever", false},
		{"nosuchtype", "held", false},
	} {
		if got := s.IsDateField(tc.docType, tc.field); got != tc.want {
			t.Errorf("IsDateField(%q, %q) = %v, want %v", tc.docType, tc.field, got, tc.want)
		}
	}
}

// CoerceDate is the one place the CLI `meta --set` path and the MCP
// kb_update_meta path agree about what a date is. It coerces only a date-shaped
// string in a date field, and reports false for everything else so the caller
// stores the value exactly as it arrived.
func TestCoerceDate_OnlyDateShapedTextInADateField(t *testing.T) {
	s := dateSchemas()
	for _, tc := range []struct {
		name           string
		docType, field string
		value          any
		want           string // "" means the coercion must refuse
	}{
		// A CALENDAR DATE keeps the spelling the caller typed; an INSTANT is
		// normalized. Reformatting a calendar date to the instant it parses to
		// is what wrote `2026-07-14T00:00:00Z` over `2026-07-14` in 0.23.0,
		// inventing a time of day nobody typed and flipping Obsidian's type for
		// that property from Date to Date and time. `created` and `modified`
		// are instants whatever a schema says, since 2nb writes them itself and
		// `stale` compares them.
		{"created from the CLI string form", "note", "created", "2026-09-04T12:34:56Z", "2026-09-04T12:34:56Z"},
		{"created from the zone-less form normalizes to an instant", "note", "created", "2026-09-04T12:34:56", "2026-09-04T12:34:56Z"},
		{"modified from a bare date is an instant, not a calendar date", "note", "modified", "2026-09-04", "2026-09-04T00:00:00Z"},
		{"a schema date field stays date-only", "meeting", "held", "2026-09-04", "2026-09-04"},
		{"a schema datetime field is an instant too", "meeting", "starts_at", "2026-09-04T09:30:00", "2026-09-04T09:30:00Z"},
		// A fraction is the one precision the reader cannot keep (it formats
		// RFC3339), so it is normalized to the second rather than left to make
		// the file and the index column disagree forever.
		{"a sub-second value normalizes to the second", "note", "created", "2026-09-04T12:34:56.75Z", "2026-09-04T12:34:56Z"},
		{"a text field is never coerced", "meeting", "summary", "2026-09-04", ""},
		{"an undeclared field is never coerced", "meeting", "notes", "2026-09-04", ""},
		{"unparseable text in a date field is left alone", "note", "created", "tomorrow", ""},
		{"an empty value is left alone", "note", "created", "", ""},
		{"a non-string value is left alone", "note", "created", 42, ""},
		{"a list value is left alone", "note", "created", []any{"2026-09-04"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := s.CoerceDate(tc.docType, tc.field, tc.value)
			if tc.want == "" {
				if ok {
					t.Errorf("CoerceDate(%q, %q, %v) = (%v, true), want refused", tc.docType, tc.field, tc.value, got)
				}
				return
			}
			if !ok {
				t.Fatalf("CoerceDate(%q, %q, %v) refused, want %s", tc.docType, tc.field, tc.value, tc.want)
			}
			if string(got) != tc.want {
				t.Errorf("CoerceDate(%q, %q, %v) = %s, want %s", tc.docType, tc.field, tc.value, got, tc.want)
			}
			// Whatever precision it keeps, the value must still BE a date: the
			// index column parses it back, and text that is not a date there
			// leaves `stale` with nothing to compare.
			if _, ok := document.ParseFrontmatterDate(string(got)); !ok {
				t.Errorf("CoerceDate returned %q, which does not parse back as a date", got)
			}
		})
	}
}
