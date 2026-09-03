package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type testItem struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

func TestWrite_JSON(t *testing.T) {
	var buf bytes.Buffer
	item := testItem{Name: "hello", Value: 42}

	if err := Write(&buf, FormatJSON, item); err != nil {
		t.Fatalf("Write(JSON) returned error: %v", err)
	}

	var got testItem
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Name != item.Name {
		t.Errorf("name: got %q, want %q", got.Name, item.Name)
	}
	if got.Value != item.Value {
		t.Errorf("value: got %d, want %d", got.Value, item.Value)
	}
}

func TestWrite_YAML(t *testing.T) {
	var buf bytes.Buffer
	item := testItem{Name: "world", Value: 99}

	if err := Write(&buf, FormatYAML, item); err != nil {
		t.Fatalf("Write(YAML) returned error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "{") || strings.Contains(out, "[") {
		t.Errorf("YAML output contains JSON brackets, got: %s", out)
	}
	if !strings.Contains(out, "name:") {
		t.Errorf("YAML output missing 'name:' key, got: %s", out)
	}
	if !strings.Contains(out, "value:") {
		t.Errorf("YAML output missing 'value:' key, got: %s", out)
	}
	if !strings.Contains(out, "world") {
		t.Errorf("YAML output missing name value 'world', got: %s", out)
	}
	if !strings.Contains(out, "99") {
		t.Errorf("YAML output missing value '99', got: %s", out)
	}
}

func TestWrite_CSV(t *testing.T) {
	var buf bytes.Buffer
	items := []testItem{
		{Name: "alpha", Value: 1},
		{Name: "beta", Value: 2},
	}

	if err := Write(&buf, FormatCSV, items); err != nil {
		t.Fatalf("Write(CSV) returned error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (header + 2 rows), got %d: %q", len(lines), buf.String())
	}

	// Header row must use json tags
	header := lines[0]
	if !strings.Contains(header, "name") {
		t.Errorf("header row missing 'name' column, got: %s", header)
	}
	if !strings.Contains(header, "value") {
		t.Errorf("header row missing 'value' column, got: %s", header)
	}

	// First data row
	if !strings.Contains(lines[1], "alpha") {
		t.Errorf("first data row missing 'alpha', got: %s", lines[1])
	}
	if !strings.Contains(lines[1], "1") {
		t.Errorf("first data row missing '1', got: %s", lines[1])
	}

	// Second data row
	if !strings.Contains(lines[2], "beta") {
		t.Errorf("second data row missing 'beta', got: %s", lines[2])
	}
	if !strings.Contains(lines[2], "2") {
		t.Errorf("second data row missing '2', got: %s", lines[2])
	}
}

// A zero-row listing is a csv document with a header and no data rows. It used
// to fall through to the JSON-record fallback, which wrote the literal `[]`
// (empty slice) or `null` (nil slice) as the one and only cell: a JSON value
// inside a csv stream, which is the corruption the delimited formats exist to
// avoid. Both spellings of "no rows" now render the same way.
func TestWrite_CSV_EmptySlice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []testItem
	}{
		{"empty slice", []testItem{}},
		{"nil slice", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []Format{FormatCSV, FormatTSV} {
				var buf bytes.Buffer
				if err := Write(&buf, format, tc.items); err != nil {
					t.Fatalf("Write(%s): %v", format, err)
				}
				out := buf.String()
				for _, bad := range []string{"null", "[]"} {
					if strings.Contains(out, bad) {
						t.Errorf("%s emitted the JSON record %q into a delimited stream: %q", format, bad, out)
					}
				}
				lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
				if len(lines) != 1 {
					t.Fatalf("%s: want the header row alone, got %d lines: %q", format, len(lines), out)
				}
				if !strings.Contains(lines[0], "name") || !strings.Contains(lines[0], "value") {
					t.Errorf("%s header does not carry the columns: %q", format, lines[0])
				}
			}
		})
	}
}

func TestWrite_TSV(t *testing.T) {
	var buf bytes.Buffer
	items := []testItem{{Name: "alpha", Value: 1}, {Name: "beta", Value: 2}}
	if err := Write(&buf, FormatTSV, items); err != nil {
		t.Fatalf("Write(TSV) error: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header + 2 rows, got %d: %q", len(lines), buf.String())
	}
	// Columns are tab-separated, not comma-separated.
	if !strings.Contains(lines[0], "name\tvalue") {
		t.Errorf("TSV header not tab-separated, got: %q", lines[0])
	}
	if !strings.Contains(lines[1], "alpha\t1") {
		t.Errorf("TSV row not tab-separated, got: %q", lines[1])
	}
}

func TestWrite_Text(t *testing.T) {
	t.Run("string verbatim", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Write(&buf, FormatText, "hello"); err != nil {
			t.Fatalf("Write(text) error: %v", err)
		}
		if buf.String() != "hello" {
			t.Errorf("text string = %q, want %q", buf.String(), "hello")
		}
	})
	t.Run("slice one item per line", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Write(&buf, FormatText, []string{"a", "b"}); err != nil {
			t.Fatalf("Write(text slice) error: %v", err)
		}
		if buf.String() != "a\nb\n" {
			t.Errorf("text slice = %q, want %q", buf.String(), "a\nb\n")
		}
	})
}

// textRow carries the four shapes that made the old %v renderer unreadable: a
// nil pointer, a time, a nested map, and an omitempty string left empty.
type textRow struct {
	Path      string         `json:"path"`
	Reachable *bool          `json:"reachable"`
	Modified  time.Time      `json:"modified"`
	Meta      map[string]any `json:"meta"`
	Note      string         `json:"note,omitempty"`
	Count     int            `json:"count"`
}

// The reported defect: `--format text` printed Go's %v rendering of a struct.
// `2nb list --format text` emitted `{cb9316a1-... resources/x.md Title note
// draft 2026-...}`: no field names, positional, unparseable, and for a struct
// holding a pointer (`config show`) a HEAP ADDRESS. A row renders as named
// pairs now.
func TestWrite_TextStructSliceIsNamedPairs(t *testing.T) {
	yes := true
	rows := []textRow{
		{
			Path:     "a.md",
			Modified: time.Date(2026, 9, 3, 13, 22, 25, 0, time.UTC),
			Meta:     map[string]any{"zeta": 1, "alpha": "one"},
		},
		{Path: "b.md", Reachable: &yes, Count: 2},
	}
	var buf bytes.Buffer
	if err := Write(&buf, FormatText, rows); err != nil {
		t.Fatalf("Write(text): %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per row, got %d:\n%s", len(lines), out)
	}
	// A line that STARTS with "{" is the Go struct dump; a "{" inside a value
	// is the compact JSON of a composite cell, which is the intended shape.
	for i, line := range lines {
		if strings.HasPrefix(line, "{") {
			t.Errorf("line %d is still a Go struct dump: %q", i, line)
		}
	}
	if strings.Contains(out, "0x") {
		t.Errorf("text output carries a heap address:\n%s", out)
	}
	if strings.Contains(out, "<nil>") {
		t.Errorf("a nil field was rendered rather than omitted:\n%s", out)
	}

	// Row 1: names present, time as RFC3339, map as sorted-key JSON.
	for _, want := range []string{"path=a.md", "modified=2026-09-03T13:22:25Z", `meta={"alpha":"one","zeta":1}`, "count=0"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("row 1 missing %q: %q", want, lines[0])
		}
	}
	// A nil pointer and an omitempty zero are omitted; a plain zero is not
	// (count=0 above), because the json view keeps it too.
	if strings.Contains(lines[0], "reachable=") {
		t.Errorf("nil pointer field was not omitted: %q", lines[0])
	}
	if strings.Contains(lines[0], "note=") {
		t.Errorf("omitempty empty string was not omitted: %q", lines[0])
	}
	// Row 2: a non-nil pointer is its VALUE, which is the case that printed an
	// address.
	if !strings.Contains(lines[1], "reachable=true") {
		t.Errorf("row 2 should render the pointed-to value: %q", lines[1])
	}
}

// A single struct (the `config show` shape) renders one field per line, and a
// nested pointer renders as its content rather than as its address.
func TestWrite_TextSingleStructIsNamedLines(t *testing.T) {
	type inner struct {
		A string `json:"a"`
		B int    `json:"b"`
	}
	type outer struct {
		Name string `json:"name"`
		Cfg  *inner `json:"cfg"`
		Gone *inner `json:"gone"`
	}
	var buf bytes.Buffer
	if err := Write(&buf, FormatText, outer{Name: "vault", Cfg: &inner{A: "one", B: 2}}); err != nil {
		t.Fatalf("Write(text): %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "0x") {
		t.Fatalf("nested pointer rendered as a heap address:\n%s", out)
	}
	if !strings.Contains(out, "name: vault") {
		t.Errorf("missing `name: vault`:\n%s", out)
	}
	if !strings.Contains(out, `cfg: {"a":"one","b":2}`) {
		t.Errorf("nested struct should render as compact JSON:\n%s", out)
	}
	if strings.Contains(out, "gone:") {
		t.Errorf("nil pointer field was not omitted:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(line, "{") {
			t.Errorf("line is a Go struct dump: %q", line)
		}
	}
}

// A map renders sorted, so the same value always prints the same way (Go's %v
// on a map has no ordering guarantee at all).
func TestWrite_TextMapIsSorted(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, FormatText, map[string]any{"zeta": 1, "alpha": "one", "mid": true}); err != nil {
		t.Fatalf("Write(text): %v", err)
	}
	if got, want := buf.String(), "alpha: one\nmid: true\nzeta: 1\n"; got != want {
		t.Errorf("map text = %q, want %q", got, want)
	}
}

func TestWrite_MD_IsRaw(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, FormatMD, "# Heading\n\nbody"); err != nil {
		t.Fatalf("Write(md) error: %v", err)
	}
	if buf.String() != "# Heading\n\nbody" {
		t.Errorf("md = %q, want raw verbatim", buf.String())
	}
}

// serializable is a stand-in for *document.Document: a type whose raw form
// comes from a Serialize() method, which writeRaw should emit verbatim.
type serializable struct{ body string }

func (s serializable) Serialize() ([]byte, error) { return []byte(s.body), nil }

func TestWrite_Raw(t *testing.T) {
	t.Run("string emitted verbatim, no JSON wrapping", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Write(&buf, FormatRaw, "hello world"); err != nil {
			t.Fatalf("Write(raw, string): %v", err)
		}
		if buf.String() != "hello world" {
			t.Errorf("raw string: got %q, want %q (no quotes/newline added)", buf.String(), "hello world")
		}
	})

	t.Run("bytes emitted verbatim", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Write(&buf, FormatRaw, []byte("raw bytes")); err != nil {
			t.Fatalf("Write(raw, []byte): %v", err)
		}
		if buf.String() != "raw bytes" {
			t.Errorf("raw bytes: got %q", buf.String())
		}
	})

	t.Run("Serialize()-able type emits its serialized form", func(t *testing.T) {
		var buf bytes.Buffer
		doc := serializable{body: "---\ntitle: X\n---\nbody"}
		if err := Write(&buf, FormatRaw, doc); err != nil {
			t.Fatalf("Write(raw, serializable): %v", err)
		}
		if buf.String() != "---\ntitle: X\n---\nbody" {
			t.Errorf("raw Serialize: got %q", buf.String())
		}
	})

	t.Run("a value with no body is refused, not dumped", func(t *testing.T) {
		// This subtest used to pin the opposite: "unknown type falls back to %v
		// without erroring". That fallback is what made `search --format md`
		// print a Go struct dump like `[{uuid path title ...}]` with exit 0.
		// raw and md emit a document body; a value without one is a caller
		// error, and saying so beats emitting something that is neither
		// markdown nor a body.
		for _, format := range []Format{FormatRaw, FormatMD} {
			var buf bytes.Buffer
			err := Write(&buf, format, []testItem{{Name: "x", Value: 1}})
			if err == nil {
				t.Errorf("%s on a struct slice wrote %q with no error; want a refusal", format, buf.String())
				continue
			}
			if !strings.Contains(err.Error(), "document body") {
				t.Errorf("%s refusal %q does not explain that raw/md emit a document body", format, err)
			}
		}
	})
}

func TestWrite_DefaultFormat(t *testing.T) {
	var buf bytes.Buffer
	item := testItem{Name: "default", Value: 7}

	// Empty string is not a named format constant — falls through to default JSON
	if err := Write(&buf, Format(""), item); err != nil {
		t.Fatalf("Write(default) returned error: %v", err)
	}

	var got testItem
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("default output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if got.Name != item.Name {
		t.Errorf("name: got %q, want %q", got.Name, item.Name)
	}
	if got.Value != item.Value {
		t.Errorf("value: got %d, want %d", got.Value, item.Value)
	}
}

// csv/tsv used to render every composite field with %v, so a map came out as Go
// syntax: `search --format csv` printed `map[]` in the frontmatter column for
// every row, and a populated map would have printed with a nondeterministic key
// order. Composite cells are compact JSON now, which parses and is stable.
func TestWriteDelimited_CompositeCellsAreJSON(t *testing.T) {
	type row struct {
		Path        string         `json:"path"`
		Count       int            `json:"count"`
		Tags        []string       `json:"tags"`
		Frontmatter map[string]any `json:"frontmatter"`
	}
	rows := []row{{
		Path:        "a.md",
		Count:       3,
		Tags:        []string{"x", "y"},
		Frontmatter: map[string]any{"zeta": 1, "alpha": "one"},
	}}

	for _, tc := range []struct {
		format Format
		comma  rune
	}{{FormatCSV, ','}, {FormatTSV, '\t'}} {
		var buf bytes.Buffer
		if err := Write(&buf, tc.format, rows); err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		raw := buf.String()
		if strings.Contains(raw, "map[") || strings.Contains(raw, "[x y]") {
			t.Errorf("%s still renders composites as Go syntax:\n%s", tc.format, raw)
		}

		// Decode the delimited stream so the assertions are about CELL VALUES,
		// not about the writer's quoting.
		r := csv.NewReader(strings.NewReader(raw))
		r.Comma = tc.comma
		recs, err := r.ReadAll()
		if err != nil {
			t.Fatalf("%s is not parseable: %v\n%s", tc.format, err, raw)
		}
		if len(recs) != 2 {
			t.Fatalf("%s: want a header and one row, got %d records", tc.format, len(recs))
		}
		cell := map[string]string{}
		for i, h := range recs[0] {
			cell[h] = recs[1][i]
		}
		// The JSON encoder sorts map keys, so this is byte-stable per row.
		if cell["frontmatter"] != `{"alpha":"one","zeta":1}` {
			t.Errorf("%s frontmatter cell = %q, want sorted-key JSON", tc.format, cell["frontmatter"])
		}
		if cell["tags"] != `["x","y"]` {
			t.Errorf("%s tags cell = %q, want JSON", tc.format, cell["tags"])
		}
		// Scalars keep exactly the rendering they had.
		if cell["path"] != "a.md" || cell["count"] != "3" {
			t.Errorf("%s changed a scalar cell: path=%q count=%q", tc.format, cell["path"], cell["count"])
		}
	}
}

// An empty map is a real value, not a missing one: it must render as {} rather
// than the `map[]` Go syntax that made the column unparseable. A nil map is JSON
// null, so the column stays parseable for every row. A nil POINTER keeps its %v
// rendering, since a pointer cell is not a composite value.
func TestWriteDelimited_EmptyAndNilComposites(t *testing.T) {
	type row struct {
		Empty map[string]any `json:"empty"`
		Nil   map[string]any `json:"nil_map"`
		Ptr   *string        `json:"ptr"`
	}
	var buf bytes.Buffer
	if err := Write(&buf, FormatCSV, []row{{Empty: map[string]any{}}}); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()
	if strings.Contains(raw, "map[") {
		t.Errorf("csv still renders a map as Go syntax:\n%s", raw)
	}
	recs, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil {
		t.Fatalf("csv is not parseable: %v\n%s", err, raw)
	}
	cell := map[string]string{}
	for i, h := range recs[0] {
		cell[h] = recs[1][i]
	}
	if cell["empty"] != "{}" {
		t.Errorf("empty map cell = %q, want {}", cell["empty"])
	}
	if cell["nil_map"] != "null" {
		t.Errorf("nil map cell = %q, want null so the column parses on every row", cell["nil_map"])
	}
	if cell["ptr"] != "<nil>" {
		t.Errorf("nil pointer cell = %q, want the unchanged %%v rendering <nil>", cell["ptr"])
	}
}

// A time.Time is a struct, so it used to go through json.Marshal, which produces
// a QUOTED string; the CSV writer then doubled those quotes and every date cell
// came out as """2026-09-03T13:22:25Z""". Reproduced live on `2nb git activity
// --format csv` and `2nb mcp status --format csv`. Anything implementing
// encoding.TextMarshaler renders through MarshalText instead: one clean cell.
func TestWriteDelimited_TextMarshalerCellsAreUnquotedText(t *testing.T) {
	type row struct {
		Hash string         `json:"hash"`
		Date time.Time      `json:"date"`
		Raw  []byte         `json:"raw"`
		Meta map[string]any `json:"meta"`
	}
	when := time.Date(2026, 9, 3, 13, 22, 25, 0, time.UTC)
	rows := []row{{Hash: "abc123", Date: when, Raw: []byte("plain bytes"), Meta: map[string]any{"b": 2, "a": 1}}}

	for _, tc := range []struct {
		format Format
		comma  rune
	}{{FormatCSV, ','}, {FormatTSV, '\t'}} {
		var buf bytes.Buffer
		if err := Write(&buf, tc.format, rows); err != nil {
			t.Fatalf("%s: %v", tc.format, err)
		}
		raw := buf.String()
		r := csv.NewReader(strings.NewReader(raw))
		r.Comma = tc.comma
		recs, err := r.ReadAll()
		if err != nil || len(recs) != 2 {
			t.Fatalf("%s is not parseable (%v):\n%s", tc.format, err, raw)
		}
		cell := map[string]string{}
		for i, h := range recs[0] {
			cell[h] = recs[1][i]
		}
		// The decoded cell is the RFC3339 instant with no quote characters of its
		// own. Before the fix it decoded to `"2026-09-03T13:22:25Z"`, quotes and
		// all, because json.Marshal quoted it and the writer then escaped those
		// quotes into the stream.
		if cell["date"] != "2026-09-03T13:22:25Z" {
			t.Errorf("%s date cell = %q, want unquoted RFC3339", tc.format, cell["date"])
		}
		if strings.Contains(cell["date"], `"`) {
			t.Errorf("%s date cell still carries quote characters: %q", tc.format, cell["date"])
		}
		// The []byte carve-out: a byte string is text and renders as its text.
		// It is carved out of the JSON branch because the encoder would base64
		// it, and it must not fall through to %v either, which prints Go's
		// byte-number syntax.
		if cell["raw"] != "plain bytes" {
			t.Errorf("%s []byte cell = %q, want the bytes as text", tc.format, cell["raw"])
		}
		if strings.Contains(cell["raw"], "cGxhaW4") {
			t.Errorf("%s base64-ed the []byte cell: %q", tc.format, cell["raw"])
		}
		if strings.HasPrefix(cell["raw"], "[") {
			t.Errorf("%s rendered the []byte cell as Go slice syntax: %q", tc.format, cell["raw"])
		}
		// A map is still compact JSON with sorted keys.
		if cell["meta"] != `{"a":1,"b":2}` {
			t.Errorf("%s map cell = %q, want sorted-key JSON", tc.format, cell["meta"])
		}
		if cell["hash"] != "abc123" {
			t.Errorf("%s changed a scalar cell: %q", tc.format, cell["hash"])
		}
	}
}

// A nil pointer whose type implements TextMarshaler must not reach MarshalText
// (that panics); it keeps the %v rendering every other nil cell has.
func TestWriteDelimited_NilTextMarshalerPointer(t *testing.T) {
	type row struct {
		When *time.Time `json:"when"`
	}
	var buf bytes.Buffer
	if err := Write(&buf, FormatCSV, []row{{}}); err != nil {
		t.Fatalf("csv: %v", err)
	}
	recs, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil || len(recs) != 2 {
		t.Fatalf("csv is not parseable (%v):\n%s", err, buf.String())
	}
	if recs[1][0] != "<nil>" {
		t.Errorf("nil *time.Time cell = %q, want <nil>", recs[1][0])
	}
}

// A NON-nil pointer to a scalar rendered as a heap address: the fallback ran
// %v on the pointer rather than on what it points at, so ModelInfo's
// `reachable` and `credentials` columns came out as `0x14000122a30`. A nil
// pointer keeps its `<nil>`, which is a real answer; an address never is.
func TestWriteDelimited_PointerCellsRenderTheirValue(t *testing.T) {
	type row struct {
		Reachable *bool   `json:"reachable"`
		Count     *int    `json:"count"`
		Missing   *string `json:"missing"`
	}
	yes, n := true, 7
	var buf bytes.Buffer
	if err := Write(&buf, FormatCSV, []row{{Reachable: &yes, Count: &n}}); err != nil {
		t.Fatalf("csv: %v", err)
	}
	raw := buf.String()
	if strings.Contains(raw, "0x") {
		t.Fatalf("csv rendered a pointer as a heap address:\n%s", raw)
	}
	recs, err := csv.NewReader(strings.NewReader(raw)).ReadAll()
	if err != nil || len(recs) != 2 {
		t.Fatalf("csv is not parseable (%v):\n%s", err, raw)
	}
	cell := map[string]string{}
	for i, h := range recs[0] {
		cell[h] = recs[1][i]
	}
	if cell["reachable"] != "true" || cell["count"] != "7" {
		t.Errorf("pointer cells = reachable %q count %q, want true and 7", cell["reachable"], cell["count"])
	}
	if cell["missing"] != "<nil>" {
		t.Errorf("nil pointer cell = %q, want <nil>", cell["missing"])
	}
}

// yamlItem exercises the three things yaml.v3 got wrong on its own: a
// multi-word json name, omitempty, and a numeric-looking STRING.
type yamlItem struct {
	ModifiedAt string            `json:"modified_at"`
	DaysStale  int               `json:"days_stale,omitempty"`
	ContextLen string            `json:"context_length,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
}

// The reported defect: --yaml invented its own key names. yaml.v3 lowercases a
// bare Go field name, and every output struct here carries json tags only, so
// `modified_at` came out as `modifiedat` and no consumer could read both views
// with one schema.
func TestWrite_YAMLUsesTheJSONFieldNames(t *testing.T) {
	var buf bytes.Buffer
	item := yamlItem{
		ModifiedAt: "2026-09-03T00:00:00Z",
		DaysStale:  7,
		ContextLen: "8192",
		Tags:       []string{"a", "b"},
		Meta:       map[string]string{"k": "v"},
	}
	if err := Write(&buf, FormatYAML, item); err != nil {
		t.Fatalf("Write(YAML): %v", err)
	}
	out := buf.String()

	for _, want := range []string{"modified_at:", "days_stale:", "context_length:", "tags:", "meta:"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing json field name %q in:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"modifiedat", "daysstale", "contextlen"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("yaml emitted the Go field name %q instead of the json one:\n%s", unwanted, out)
		}
	}
	// Nested values stay BLOCK style: JSON parses as flow, which would put the
	// brackets back.
	if strings.Contains(out, "{") || strings.Contains(out, "[") {
		t.Errorf("yaml output contains JSON brackets:\n%s", out)
	}
	// A json STRING that looks like a number must stay a string, or a consumer
	// round-tripping the yaml gets a different type than the json view gave it.
	if !strings.Contains(out, `context_length: "8192"`) {
		t.Errorf("numeric-looking string was not quoted:\n%s", out)
	}
}

// omitempty is part of the json contract, and yaml.v3 never saw it: --yaml
// printed every zero field, so the two views disagreed about which keys exist.
func TestWrite_YAMLHonorsOmitempty(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, FormatYAML, yamlItem{ModifiedAt: "2026-09-03T00:00:00Z"}); err != nil {
		t.Fatalf("Write(YAML): %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "modified_at:") {
		t.Errorf("required field missing:\n%s", out)
	}
	for _, unwanted := range []string{"days_stale", "context_length", "tags", "meta"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("omitempty field %q was rendered:\n%s", unwanted, out)
		}
	}
}

// The degenerate payloads. An empty listing is a bare `[]` (every --json
// listing returns one), and a nil payload parses to a node with no Kind, which
// the yaml encoder refuses outright.
//
// The nil-slice case flipped from `null` to `[]` in 0.22.3: a listing command
// hands its slice straight to the writer, and a vault with no orphans has a NIL
// slice, so `2nb orphans --yaml` printed `null` while `2nb orphans --json`
// printed `[]` and `2nb tasks --yaml` printed `[]` (its slice is merely built
// non-nil). An empty result is an empty collection in both views now. An
// UNTYPED nil is still `null`: it is not a listing, it is the absence of a
// value.
func TestWrite_YAMLDegeneratePayloads(t *testing.T) {
	cases := []struct {
		name string
		data any
		want string
	}{
		{"empty slice", []yamlItem{}, "[]\n"},
		{"nil slice", []yamlItem(nil), "[]\n"},
		{"nil map", map[string]string(nil), "{}\n"},
		{"nil", nil, "null\n"},
		{"scalar string", "hello", "hello\n"},
		{"scalar int", 42, "42\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := Write(&buf, FormatYAML, tc.data); err != nil {
				t.Fatalf("Write(YAML, %v): %v", tc.data, err)
			}
			if buf.String() != tc.want {
				t.Errorf("got %q, want %q", buf.String(), tc.want)
			}
		})
	}
}

// The embedded-struct shapes. These types are EXPORTED because an embedded
// field takes its type's name: an unexported type would make the field
// unexported, and every renderer here skips unexported fields.
type EmbedLeaf struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type EmbedMiddle struct {
	EmbedLeaf
	Installed bool `json:"installed"`
}

// EmbedOuter is the shape DoctorReport and SkillDoctorReport have: an anonymous
// struct carrying the fields callers have always seen at the top level, plus a
// couple of additive ones. Hidden is `json:"-"`, Tagged is a NAMED struct field
// (one value, never flattened).
type EmbedOuter struct {
	EmbedMiddle
	OK     bool      `json:"ok"`
	Hidden string    `json:"-"`
	Tagged EmbedLeaf `json:"tagged"`
}

// An anonymous field WITH a json name is one key, not a flattening.
type EmbedNamed struct {
	EmbedLeaf `json:"leaf"`
	OK        bool `json:"ok"`
}

// An embedded POINTER: json promotes its fields, and a nil one contributes
// none. Reading through it must not panic.
type EmbedPtr struct {
	*EmbedLeaf
	OK bool `json:"ok"`
}

// jsonKeys is the key set json.Marshal actually produces for a value, which is
// the thing text and csv have to agree with.
func jsonKeys(t *testing.T, v any) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	keys := make(map[string]bool, len(m))
	for k := range m {
		keys[k] = true
	}
	return keys
}

func nameSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// The reported defect: fieldSpecs skipped only unexported fields, so an
// ANONYMOUS embedded struct came out as a single cell keyed by its Go TYPE name
// while json flattened it. `2nb skills doctor --format text` printed
// `Verification: {"slug":...}` as one JSON blob, and `2nb doctor --format text`
// printed a `SuiteStatus:` blob, on exactly the two commands a human is
// likeliest to point --format text at. A `json:"-"` field was not skipped
// either: it fell through the `tag != "-"` guard and was emitted under its Go
// name, so text and csv showed a field json omits entirely.
//
// The guarantee is asserted against json.Marshal's own key set, so the three
// views cannot drift apart again.
func TestFieldNames_MatchJSONKeysThroughEmbedding(t *testing.T) {
	// Every field non-zero: an omitempty field json legitimately omits would
	// otherwise show up as a difference against the static csv header.
	full := EmbedOuter{
		EmbedMiddle: EmbedMiddle{
			EmbedLeaf: EmbedLeaf{Slug: "claude-code", Name: "Claude Code"},
			Installed: true,
		},
		OK:     true,
		Hidden: "never rendered",
		Tagged: EmbedLeaf{Slug: "inner", Name: "Inner"},
	}

	got := nameSet(fieldNames(reflect.TypeOf(full)))
	want := jsonKeys(t, full)
	for k := range want {
		if !got[k] {
			t.Errorf("json emits %q but the csv/text header does not", k)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("the csv/text header carries %q, which json does not emit", k)
		}
	}
	if got["EmbedMiddle"] || got["EmbedLeaf"] {
		t.Errorf("an embedded struct is still a single cell keyed by its Go type name: %v", got)
	}
	if got["Hidden"] || got["-"] {
		t.Errorf(`a json:"-" field reached the output: %v`, got)
	}

	// text: the promoted fields are top-level `name: value` lines.
	var textBuf bytes.Buffer
	if err := Write(&textBuf, FormatText, full); err != nil {
		t.Fatalf("Write(text): %v", err)
	}
	text := textBuf.String()
	for _, want := range []string{"slug: claude-code", "name: Claude Code", "installed: true", "ok: true"} {
		if !strings.Contains(text, want+"\n") {
			t.Errorf("text is missing %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"EmbedMiddle:", "EmbedLeaf:", "Hidden:", "never rendered"} {
		if strings.Contains(text, bad) {
			t.Errorf("text still carries %q:\n%s", bad, text)
		}
	}

	// csv: one column per promoted field, in json's own order.
	var csvBuf bytes.Buffer
	if err := Write(&csvBuf, FormatCSV, []EmbedOuter{full}); err != nil {
		t.Fatalf("Write(csv): %v", err)
	}
	recs, err := csv.NewReader(&csvBuf).ReadAll()
	if err != nil {
		t.Fatalf("csv is not parseable: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want a header and one row, got %d records: %v", len(recs), recs)
	}
	wantHeader := []string{"slug", "name", "installed", "ok", "tagged"}
	if strings.Join(recs[0], ",") != strings.Join(wantHeader, ",") {
		t.Errorf("csv header = %v, want %v", recs[0], wantHeader)
	}
	if recs[1][0] != "claude-code" || recs[1][2] != "true" {
		t.Errorf("csv row lost its promoted values: %v", recs[1])
	}
	if recs[1][4] != `{"slug":"inner","name":"Inner"}` {
		t.Errorf("a NAMED struct field should stay one JSON cell, got %q", recs[1][4])
	}
}

// An anonymous field carrying a json NAME is one key in json, so it stays one
// cell here too.
func TestFieldNames_NamedAnonymousFieldStaysOneKey(t *testing.T) {
	v := EmbedNamed{EmbedLeaf: EmbedLeaf{Slug: "s", Name: "n"}, OK: true}
	got := nameSet(fieldNames(reflect.TypeOf(v)))
	want := jsonKeys(t, v)
	if len(got) != len(want) || !got["leaf"] || !got["ok"] {
		t.Errorf("header = %v, want the json keys %v", got, want)
	}
}

// A nil embedded POINTER has no fields to read. reflect.Value.FieldByIndex
// would panic walking through it; the renderers must not.
func TestFieldSpecs_NilEmbeddedPointerRendersEmpty(t *testing.T) {
	var textBuf bytes.Buffer
	if err := Write(&textBuf, FormatText, EmbedPtr{OK: true}); err != nil {
		t.Fatalf("Write(text): %v", err)
	}
	if got := textBuf.String(); got != "ok: true\n" {
		t.Errorf("text = %q, want only the field that exists", got)
	}

	var csvBuf bytes.Buffer
	if err := Write(&csvBuf, FormatCSV, []EmbedPtr{{OK: true}}); err != nil {
		t.Fatalf("Write(csv): %v", err)
	}
	recs, err := csv.NewReader(&csvBuf).ReadAll()
	if err != nil {
		t.Fatalf("csv is not parseable: %v", err)
	}
	if len(recs) != 2 || len(recs[1]) != len(recs[0]) {
		t.Fatalf("want a header and one row of the same width, got %v", recs)
	}
	if strings.Join(recs[0], ",") != "slug,name,ok" {
		t.Fatalf("csv header = %v, want the promoted fields plus ok", recs[0])
	}
	if recs[1][0] != "" || recs[1][1] != "" {
		t.Errorf("a nil embedded pointer should leave empty cells, got %v", recs[1])
	}
	if recs[1][2] != "true" {
		t.Errorf("the outer field is missing from the row: %v", recs[1])
	}
}

// The shapes where fieldSpecs used to disagree with encoding/json. All of them
// are collisions between promoted names, or promotion out of an embedded
// UNEXPORTED struct, and every one is now resolved json's way.
//
// Names are EFFECTIVE names: a `json:"n"` field and a bare field named N never
// collide, because those are two different names. So every pair below shares an
// effective name deliberately.

// Equal depth, both tagged with the same name: json calls it a conflict.
type ClashTagA struct {
	A string `json:"same"`
}
type ClashTagB struct {
	B string `json:"same"`
}
type ClashBothTagged struct {
	ClashTagA
	ClashTagB
	OK bool `json:"ok"`
}

// Equal depth, neither tagged, same Go name: also a conflict.
type ClashBareA struct{ Same string }
type ClashBareB struct{ Same string }
type ClashNeitherTagged struct {
	ClashBareA
	ClashBareB
	OK bool `json:"ok"`
}

// Equal depth, exactly one tagged with the other's name: the tagged one wins.
type ClashTagNamedN struct {
	Other string `json:"N"`
}
type ClashBareN struct{ N string }
type ClashTagWins struct {
	ClashTagNamedN
	ClashBareN
}

// A tagged field at depth 1 against the same name at depth 2: shallower wins,
// which is the case output structs actually hit.
type ClashDeep struct {
	V string `json:"same"`
}
type ClashDeepMid struct{ ClashDeep }
type ClashShallowWins struct {
	ClashDeepMid
	Top string `json:"same"`
}

// A conflict at depth 2, then a winner at depth 1, then ANOTHER conflict at
// depth 2. The depth-1 field must survive: a later deep pair must not revive a
// conflict the shallow field already settled.
type ClashOrdering struct {
	ClashTagA
	ClashTagB
	Top string `json:"same"`
	ClashTagC
	ClashTagD
	OK bool `json:"ok"`
}
type ClashTagC struct {
	C string `json:"same"`
}
type ClashTagD struct {
	D string `json:"same"`
}

// An embedded UNEXPORTED struct. json promotes its exported fields; fieldSpecs
// used to skip it whole.
type hiddenLeaf struct {
	Slug string `json:"slug"`
}
type HiddenOuter struct {
	hiddenLeaf
	OK bool `json:"ok"`
}

// THREE levels, two of them unexported, so a promoted value is read through two
// embedded read-only hops. reflect.Value.Field does not propagate the embedded
// read-only flag, so the exported leaf is still interfaceable; if that were
// wrong, rendering would panic rather than fail an assertion.
type hiddenL3 struct {
	Deep string `json:"deep"`
}
type hiddenL2 struct {
	hiddenL3
	Mid string `json:"mid"`
}
type HiddenL1 struct {
	hiddenL2
	Top string `json:"top"`
}

// A nil embedded POINTER underneath an unexported embed: the deref happens
// after the unexported hop, and Value.Elem DOES propagate the read-only flag,
// so this is the path most likely to panic if the walk is wrong.
type hiddenPtrLeaf struct {
	Slug string `json:"slug"`
}
type hiddenPtrMid struct {
	*hiddenPtrLeaf
	Note string `json:"note"`
}
type HiddenPtrOuter struct {
	hiddenPtrMid
	OK bool `json:"ok"`
}

// fieldSpecs used to diverge from encoding/json in two ways, both of them in
// the function whose entire job is that the json, text and csv views agree
// about what a field is called. It kept the FIRST field on an equal-depth
// collision where json drops the name, and it skipped an embedded unexported
// struct whole where json promotes its exported fields. Both now follow json.
//
// json's rule, read off json.Marshal rather than off the spec: group by
// EFFECTIVE name; shallower depth wins outright; at equal depth a tagged field
// beats an untagged one; at equal depth with both tagged or neither, the name is
// dropped entirely.
//
// Every case reads json's own key set from json.Marshal, so a change to the
// standard library's conflict rule fails here rather than splitting the views.
func TestFieldNames_MatchJSONThroughCollisionsAndUnexportedEmbeds(t *testing.T) {
	cases := []struct {
		name string
		// value must have every field non-zero where it matters: jsonKeys comes
		// from a real marshal, and an omitempty field json legitimately omits
		// would read as a disagreement with the static csv header.
		value any
		want  string // the expected header, json's key set in index order
	}{
		{"equal depth, both tagged: json drops the name", ClashBothTagged{OK: true}, "ok"},
		{"equal depth, neither tagged: json drops the name", ClashNeitherTagged{OK: true}, "ok"},
		{"equal depth, one tagged: the tagged field wins", ClashTagWins{ClashTagNamedN: ClashTagNamedN{Other: "tagged"}}, "N"},
		{"shallower wins over deeper", ClashShallowWins{Top: "shallow"}, "same"},
		{"a shallow winner survives a later deep conflict", ClashOrdering{Top: "shallow", OK: true}, "same,ok"},
		{"an embedded unexported struct is promoted", HiddenOuter{hiddenLeaf: hiddenLeaf{Slug: "s"}, OK: true}, "slug,ok"},
		{"promotion reaches through two unexported levels", HiddenL1{hiddenL2: hiddenL2{hiddenL3: hiddenL3{Deep: "d"}, Mid: "m"}, Top: "t"}, "deep,mid,top"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fieldNames(reflect.TypeOf(tc.value))
			if strings.Join(got, ",") != tc.want {
				t.Errorf("fieldNames = %v, want %v", got, strings.Split(tc.want, ","))
			}
			// The real assertion: the header IS json's key set.
			want := jsonKeys(t, tc.value)
			gotSet := nameSet(got)
			for k := range want {
				if !gotSet[k] {
					t.Errorf("json emits %q but the csv/text header does not", k)
				}
			}
			for k := range gotSet {
				if !want[k] {
					t.Errorf("the csv/text header carries %q, which json does not emit", k)
				}
			}
		})
	}

	// Values, not just names: a promoted field must be READABLE through the
	// unexported hops, and the winner of a tie must be the field json picked.
	t.Run("promoted values are readable", func(t *testing.T) {
		row := HiddenL1{hiddenL2: hiddenL2{hiddenL3: hiddenL3{Deep: "d"}, Mid: "m"}, Top: "t"}
		var buf bytes.Buffer
		if err := Write(&buf, FormatCSV, []HiddenL1{row}); err != nil {
			t.Fatalf("Write(csv): %v", err)
		}
		recs, err := csv.NewReader(&buf).ReadAll()
		if err != nil || len(recs) != 2 {
			t.Fatalf("csv did not render a header and one row (%v): %v", err, recs)
		}
		if strings.Join(recs[1], ",") != "d,m,t" {
			t.Errorf("row = %v, want the values read through two unexported embeds", recs[1])
		}
	})

	t.Run("the tie winner is the field json picked", func(t *testing.T) {
		row := ClashTagWins{ClashTagNamedN: ClashTagNamedN{Other: "tagged"}, ClashBareN: ClashBareN{N: "bare"}}
		var buf bytes.Buffer
		if err := Write(&buf, FormatText, row); err != nil {
			t.Fatalf("Write(text): %v", err)
		}
		if got := buf.String(); got != "N: tagged\n" {
			t.Errorf("text = %q, want the TAGGED field's value under N", got)
		}
	})

	// A nil embedded pointer is the one shape where key-set equality does NOT
	// apply, and deliberately so: json's marshal omits a key it cannot reach,
	// while the csv header is static and must keep the column so every row has
	// the same width. Assert the header and the empty cell instead.
	t.Run("a nil embedded pointer under an unexported embed keeps its column", func(t *testing.T) {
		if keys := jsonKeys(t, HiddenPtrOuter{OK: true}); keys["slug"] {
			t.Fatalf("json started emitting a key behind a nil embedded pointer: %v", keys)
		}
		var buf bytes.Buffer
		if err := Write(&buf, FormatCSV, []HiddenPtrOuter{{OK: true}}); err != nil {
			t.Fatalf("Write(csv): %v", err)
		}
		recs, err := csv.NewReader(&buf).ReadAll()
		if err != nil || len(recs) != 2 {
			t.Fatalf("csv did not render a header and one row (%v): %v", err, recs)
		}
		if strings.Join(recs[0], ",") != "slug,note,ok" {
			t.Errorf("header = %v, want the promoted columns kept", recs[0])
		}
		if recs[1][0] != "" || recs[1][1] != "" || recs[1][2] != "true" {
			t.Errorf("row = %v, want an empty cell for the unreachable field", recs[1])
		}
		// Non-nil, and now the key sets DO agree.
		full := HiddenPtrOuter{hiddenPtrMid: hiddenPtrMid{hiddenPtrLeaf: &hiddenPtrLeaf{Slug: "s"}, Note: "n"}, OK: true}
		want := jsonKeys(t, full)
		for k := range nameSet(fieldNames(reflect.TypeOf(full))) {
			if !want[k] {
				t.Errorf("the header carries %q, which json does not emit for a populated value", k)
			}
		}
	})
}
