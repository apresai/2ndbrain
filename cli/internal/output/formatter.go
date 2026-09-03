package output

import (
	"encoding"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

type Format string

const (
	FormatJSON  Format = "json"
	FormatCSV   Format = "csv"
	FormatTSV   Format = "tsv"
	FormatYAML  Format = "yaml"
	FormatTable Format = "table"
	FormatRaw   Format = "raw"
	FormatMD    Format = "md"
	FormatText  Format = "text"
)

// RendersJSON reports whether Write will emit JSON for this format. The empty
// format falls to the JSON branch below, so it is JSON too; callers normalizing
// a nil slice to [] for machine consumers must treat both the same.
func RendersJSON(format Format) bool {
	return format == FormatJSON || format == ""
}

func Write(w io.Writer, format Format, data any) error {
	switch format {
	case FormatJSON:
		return writeJSON(w, data)
	case FormatCSV:
		return writeDelimited(w, data, ',')
	case FormatTSV:
		return writeDelimited(w, data, '\t')
	case FormatYAML:
		return writeYAML(w, data)
	case FormatRaw, FormatMD:
		// md is the markdown body of a document, which Serialize() already
		// produces (identical to raw for the document/string/[]byte shapes).
		return writeRaw(w, data)
	case FormatText:
		return writeText(w, data)
	default:
		return writeJSON(w, data)
	}
}

// writeText renders a best-effort plain-text view: strings/[]byte/Serialize()
// verbatim (like raw), a slice as one %v-rendered element per line, and any
// other value via %v. Useful for human-readable piping where JSON is overkill.
func writeText(w io.Writer, data any) error {
	switch v := data.(type) {
	case string:
		_, err := io.WriteString(w, v)
		return err
	case []byte:
		_, err := w.Write(v)
		return err
	}
	if s, ok := data.(interface{ Serialize() ([]byte, error) }); ok {
		b, err := s.Serialize()
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
	rv := reflect.ValueOf(data)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Slice {
		for i := 0; i < rv.Len(); i++ {
			if _, err := fmt.Fprintf(w, "%v\n", rv.Index(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := fmt.Fprintf(w, "%v\n", data)
	return err
}

func writeRaw(w io.Writer, data any) error {
	switch v := data.(type) {
	case string:
		_, err := io.WriteString(w, v)
		return err
	case []byte:
		_, err := w.Write(v)
		return err
	default:
		// Try duck-typing for Serialize() method (e.g. *document.Document)
		if s, ok := data.(interface {
			Serialize() ([]byte, error)
		}); ok {
			b, err := s.Serialize()
			if err != nil {
				return err
			}
			_, err = w.Write(b)
			return err
		}
		// A scalar still has an obvious raw form.
		switch data.(type) {
		case int, int64, float64, bool:
			_, err := fmt.Fprintf(w, "%v\n", data)
			return err
		}
		// Anything else has no body to emit. The old fallback printed Go's %v
		// rendering of the value, so `search --format md` produced a struct dump
		// like `[{uuid path title ...}]` and exit 0. raw and md exist for a
		// document body; a value without one is a caller error worth saying.
		return fmt.Errorf("--format raw/md emits a document body; this value has none (use --json)")
	}
}

func writeJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// writeYAML renders the value with the SAME field names, tags and omitempty
// rules the JSON view uses, by routing it through encoding/json first.
//
// yaml.v3 lowercases a bare Go field name (ModifiedAt becomes modifiedat) and
// honors no `omitempty` it was not itself given. Every output struct in this
// repo carries json tags only, so `--yaml` emitted a THIRD set of key names,
// matching neither the documented json view nor anything else: modifiedat,
// daysstale, sourcepath, driftjtarget. Marshalling to JSON and re-reading it
// makes --yaml exactly the json view in YAML syntax, which is what a user
// picking a serialization format expects, and it makes the derived fields the
// json view computes (vendor, family, active, reachable, compatible, working)
// appear there too.
//
// Node.Style is cleared recursively because JSON text parses as FLOW-style
// nodes, which would re-emit the braces and brackets. Node.Tag is deliberately
// KEPT: it is what preserves a JSON string "8192" as a quoted YAML string
// rather than an int.
//
// This is the OUTPUT formatter only. The persisted YAML writers (the user model
// catalog, vault config.yaml, schemas.yaml) are separate and untouched: their
// key names are an on-disk format, not a rendering.
func writeYAML(w io.Writer, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("render yaml: %w", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return fmt.Errorf("render yaml: %w", err)
	}
	// A JSON `null` parses to a node with no Kind, which the encoder refuses.
	// Nil payloads reach here (an unset pointer, an empty interface), so emit
	// the document YAML would have emitted for them.
	if node.Kind == 0 {
		_, err := io.WriteString(w, "null\n")
		return err
	}
	clearYAMLNodeStyle(&node)
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(&node)
}

// clearYAMLNodeStyle drops the flow style JSON parsing leaves behind, so the
// output is block YAML. It never touches Tag, which carries the scalar's type.
func clearYAMLNodeStyle(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		clearYAMLNodeStyle(c)
	}
}

// writeDelimited renders a slice of structs as delimiter-separated values
// (comma for CSV, tab for TSV). Non-struct-slice data falls back to a single
// JSON-encoded record, matching the prior CSV behavior.
func writeDelimited(w io.Writer, data any, comma rune) error {
	cw := csv.NewWriter(w)
	cw.Comma = comma
	defer cw.Flush()

	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	// Handle slices of structs
	if v.Kind() == reflect.Slice && v.Len() > 0 {
		elem := v.Index(0)
		if elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct {
			return writeStructSliceCSV(cw, v)
		}
	}

	// Fallback: marshal as JSON lines
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return cw.Write([]string{string(b)})
}

// delimitedCell renders one struct field as a csv/tsv cell.
//
// Composite fields (a map, a slice, a struct, or a pointer/interface to one)
// are emitted as compact JSON; scalars keep the %v rendering they have always
// had. `%v` on a composite produced Go syntax that no consumer can parse and
// that is not even stable: `search --format csv` printed every frontmatter cell
// as `map[]`, and a populated one would have come out in Go map syntax with a
// nondeterministic key order. The JSON encoder sorts map keys, so the same row
// renders the same way every time. A value JSON cannot encode (a channel, a
// func, a cyclic graph) falls back to %v rather than failing the whole render.
func delimitedCell(field reflect.Value) string {
	// A value that knows how to render itself as TEXT does so, before the
	// composite branch can see it. time.Time is the case that bit: it is a
	// struct, so it went through json.Marshal, which produces a QUOTED string,
	// and the CSV writer then doubled those quotes, so `git activity --format
	// csv` printed """2026-09-03T13:22:25+02:00""" for every date. MarshalText
	// gives the same RFC3339 instant as plain text, which is one clean cell.
	if tm, ok := field.Interface().(encoding.TextMarshaler); ok {
		// A nil pointer implementing the interface would panic in MarshalText;
		// %v renders it as <nil>, which is what every other nil cell does.
		if field.Kind() != reflect.Ptr || !field.IsNil() {
			if b, err := tm.MarshalText(); err == nil {
				return string(b)
			}
		}
	}
	v := field
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return fmt.Sprintf("%v", field.Interface())
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		// A byte string is TEXT, not a composite, so it renders as its text.
		// It is carved out of the JSON branch because the encoder would base64
		// it; falling through to %v instead was no better, since that prints
		// Go's byte-number syntax ([112 108 97 …]), the very rendering the
		// composite change removed everywhere else.
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return string(v.Bytes())
		}
		b, err := json.Marshal(field.Interface())
		if err != nil {
			return fmt.Sprintf("%v", field.Interface())
		}
		return string(b)
	}
	return fmt.Sprintf("%v", field.Interface())
}

func writeStructSliceCSV(cw *csv.Writer, v reflect.Value) error {
	if v.Len() == 0 {
		return nil
	}

	// Write header from struct field names
	first := v.Index(0)
	if first.Kind() == reflect.Ptr {
		first = first.Elem()
	}
	t := first.Type()
	headers := make([]string, t.NumField())
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			headers[i] = t.Field(i).Name
		} else {
			headers[i] = strings.Split(tag, ",")[0]
		}
	}
	if err := cw.Write(headers); err != nil {
		return err
	}

	// Write rows
	for i := range v.Len() {
		row := v.Index(i)
		if row.Kind() == reflect.Ptr {
			row = row.Elem()
		}
		record := make([]string, row.NumField())
		for j := range row.NumField() {
			record[j] = delimitedCell(row.Field(j))
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	return nil
}
