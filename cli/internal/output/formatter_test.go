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
