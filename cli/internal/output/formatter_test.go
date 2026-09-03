package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
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

func TestWrite_CSV_EmptySlice(t *testing.T) {
	var buf bytes.Buffer
	items := []testItem{}

	if err := Write(&buf, FormatCSV, items); err != nil {
		t.Fatalf("Write(CSV, empty slice) returned error: %v", err)
	}

	// Empty slice hits the fallback JSON-line path; result must not be multi-line
	// structural CSV (no header row written). The output should be non-panicking
	// and reasonably short.
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Expect at most 1 line (the fallback JSON marshal of [])
	if len(lines) > 1 {
		t.Errorf("empty slice produced unexpected multi-line output: %q", out)
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
		// The []byte carve-out (G7), pinned as it behaves: it keeps the JSON
		// encoder from base64-ing the value, and the fallback is %v. No shipped
		// struct rendered through csv/tsv has a []byte field, so this pins the
		// carve-out rather than a user-visible rendering.
		if cell["raw"] != fmt.Sprintf("%v", []byte("plain bytes")) {
			t.Errorf("%s []byte cell = %q, want the unchanged %%v rendering", tc.format, cell["raw"])
		}
		if strings.Contains(cell["raw"], "cGxhaW4") {
			t.Errorf("%s base64-ed the []byte cell: %q", tc.format, cell["raw"])
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
