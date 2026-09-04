package output

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"
)

// A payload that is ONE scalar is one PLAIN cell.
//
// writeDelimited's non-slice fallback JSON-marshals whatever it is given, and
// the csv writer then escapes the quotes that produced, so a bare string came
// out as """bedrock""": a consumer had to strip CSV quoting and then
// JSON-unquote to read one word. `config get ai.provider --format csv` and
// `meta --get title --format csv` are the two commands whose whole output is
// one scalar.
//
// The composite fallback is deliberately unchanged: a map or a struct really is
// compact JSON in a cell, which is what this format promises.
func TestWriteDelimited_ScalarPayloadIsOnePlainCell(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
		want string
	}{
		{"string", "bedrock", "bedrock"},
		{"int", 1024, "1024"},
		{"float", 0.25, "0.25"},
		{"bool", true, "true"},
		{"string that needs csv quoting", "a,b", "a,b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []Format{FormatCSV, FormatTSV} {
				var buf bytes.Buffer
				if err := Write(&buf, format, tc.data); err != nil {
					t.Fatalf("%s: %v", format, err)
				}
				r := csv.NewReader(strings.NewReader(buf.String()))
				if format == FormatTSV {
					r.Comma = '\t'
				}
				recs, err := r.ReadAll()
				if err != nil || len(recs) != 1 || len(recs[0]) != 1 {
					t.Fatalf("%s is not one cell (%v): %q", format, err, buf.String())
				}
				if recs[0][0] != tc.want {
					t.Errorf("%s cell = %q, want %q (raw: %q)", format, recs[0][0], tc.want, buf.String())
				}
			}
		})
	}
}

// A composite payload keeps the compact-JSON cell it has always had.
func TestWriteDelimited_CompositePayloadStaysJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, FormatCSV, map[string]any{"b": 2, "a": 1}); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil || len(recs) != 1 {
		t.Fatalf("not one record (%v): %q", err, buf.String())
	}
	if recs[0][0] != `{"a":1,"b":2}` {
		t.Errorf("map cell = %q, want sorted-key compact JSON", recs[0][0])
	}
}

// A value that renders ITSELF as text is a scalar too, and it is a STRUCT, so
// the kind switch never saw it: `meta --get created --format csv` came out as
// """2020-01-01T00:00:00Z""".
func TestWriteDelimited_ATextMarshalerPayloadIsOnePlainCell(t *testing.T) {
	when := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, format := range []Format{FormatCSV, FormatTSV} {
		var buf bytes.Buffer
		if err := Write(&buf, format, when); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		got := strings.TrimSpace(buf.String())
		if got != "2020-01-01T00:00:00Z" {
			t.Errorf("%s cell = %q, want the plain instant", format, got)
		}
	}
}
