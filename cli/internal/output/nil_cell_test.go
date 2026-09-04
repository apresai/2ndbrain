package output

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
)

// A nil pointer is an EMPTY cell, not the literal `<nil>`.
//
// `<nil>` is Go syntax, and this repo's contract for a delimited stream is that
// a cell is compact JSON and never Go syntax. json renders these same fields as
// `null`, and an empty cell is what a csv consumer reads a null as.
// `models list --csv` carried the 4-character token in 16 rows of a 91-row
// catalog (the reachable, credentials and benchmark columns).
//
// The dereference behavior for a NON-nil pointer is unchanged and is asserted
// in the same rows, because the fix must not turn a real value into a blank.
type nilCellRow struct {
	Name      string  `json:"name"`
	Reachable *bool   `json:"reachable"`
	Score     *int    `json:"score"`
	Label     *string `json:"label"`
}

func nilCellRows() []nilCellRow {
	yes, score, label := true, 7, "here"
	return []nilCellRow{
		{Name: "all-nil"},
		{Name: "all-set", Reachable: &yes, Score: &score, Label: &label},
	}
}

func TestDelimited_NilPointerIsAnEmptyCell(t *testing.T) {
	for _, format := range []Format{FormatCSV, FormatTSV} {
		t.Run(string(format), func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, format, nilCellRows()); err != nil {
				t.Fatalf("write: %v", err)
			}
			out := buf.String()
			if strings.Contains(out, "<nil>") {
				t.Errorf("%s still renders a nil pointer as Go syntax:\n%s", format, out)
			}

			r := csv.NewReader(strings.NewReader(out))
			if format == FormatTSV {
				r.Comma = '\t'
			}
			recs, err := r.ReadAll()
			if err != nil {
				t.Fatalf("%s is not a delimited document (%v):\n%s", format, err, out)
			}
			if len(recs) != 3 {
				t.Fatalf("want a header plus two rows, got %d records:\n%s", len(recs), out)
			}
			if got := recs[1]; got[1] != "" || got[2] != "" || got[3] != "" {
				t.Errorf("the all-nil row should carry empty cells, got %q", got)
			}
			if got := recs[2]; got[1] != "true" || got[2] != "7" || got[3] != "here" {
				t.Errorf("the all-set row should carry the pointed-to values, got %q", got)
			}
		})
	}
}

// text renders a MAP through the same delimitedCell without the struct view's
// nil-field skip, so it is the shape that could still print `<nil>` after the
// struct path stopped.
func TestText_NilPointerInAMapIsAnEmptyValue(t *testing.T) {
	yes := true
	var missing *bool
	var buf bytes.Buffer
	if err := Write(&buf, FormatText, map[string]any{"set": &yes, "unset": missing}); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<nil>") {
		t.Errorf("--format text still renders a nil pointer as Go syntax:\n%s", out)
	}
	if !strings.Contains(out, "set: true") {
		t.Errorf("--format text lost the non-nil value:\n%s", out)
	}
	if !strings.Contains(out, "unset: \n") && !strings.HasSuffix(out, "unset: ") {
		t.Errorf("--format text should render the nil as an empty value:\n%q", out)
	}
}
