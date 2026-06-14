package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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

	t.Run("unknown type falls back to %v without erroring", func(t *testing.T) {
		var buf bytes.Buffer
		if err := Write(&buf, FormatRaw, testItem{Name: "x", Value: 1}); err != nil {
			t.Fatalf("Write(raw, struct) should not error: %v", err)
		}
		if buf.Len() == 0 {
			t.Errorf("raw fallback produced no output")
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
