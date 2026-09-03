package output

import (
	"encoding"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
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

// writeText renders a plain-text view for READING. A document body is emitted
// verbatim (a string, a []byte, or a value with Serialize(), so `read --format
// text` and `git diff --format text` still print the body); every other value
// renders as named lines.
//
// It used to render a slice element, and any non-slice value, with %v. %v on a
// struct prints Go's own debug syntax, so the format that promises plain text
// emitted a struct dump: `list --format text` printed
// `{cb9316a1-... resources/x.md Title note draft 2026-...}` (positional, no
// field names, unparseable), `folders --format text` printed `{resources 5}`,
// `models list --format text` printed one such line per model, and `config show
// --format text` printed a HEAP ADDRESS (`0x353ff8c07860`) for its nested
// config pointer. A memory address is not output.
//
// The shapes, in order:
//   - a slice of structs: one line per row, `name=value` pairs
//   - a single struct: one `name: value` per line
//   - a map: one `key: value` per line, keys sorted
//   - a slice of scalars: one value per line (unchanged)
//   - anything else: %v (unchanged)
//
// Names come from the json tag, so text, json and csv agree about what a field
// is called. Values render through delimitedCell, so a pointer or interface is
// dereferenced (`true`, not an address), a composite is compact JSON with
// sorted keys, and a time is RFC3339. A nil pointer/interface/map/slice, and an
// `,omitempty` field at its zero value, are OMITTED: that is what drops the
// `<nil>` columns and the empty positional gaps a 30-field row was mostly made
// of.
//
// text is for reading, not for cutting: a value containing a space is not
// quoted and nothing is escaped. Use tsv (or json) to split fields.
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
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			break
		}
		rv = rv.Elem()
	}
	switch {
	case !rv.IsValid():
		// A nil interface: %v is "<nil>", which is what it has always printed.
	case rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array:
		return writeTextRows(w, rv)
	case isRowStruct(rv.Type()):
		return writeTextPairs(w, structTextPairs(rv))
	case rv.Kind() == reflect.Map:
		return writeTextPairs(w, mapTextPairs(rv))
	}
	_, err := fmt.Fprintf(w, "%v\n", data)
	return err
}

// textPair is one rendered field: its name and its already-formatted value.
type textPair struct {
	name  string
	value string
}

// writeTextPairs writes one `name: value` per line (the single-struct and map
// shapes).
func writeTextPairs(w io.Writer, pairs []textPair) error {
	for _, p := range pairs {
		if _, err := fmt.Fprintf(w, "%s: %s\n", p.name, p.value); err != nil {
			return err
		}
	}
	return nil
}

// writeTextRows writes one line per slice element: `name=value` pairs for a
// struct row, the value alone for a scalar.
func writeTextRows(w io.Writer, rv reflect.Value) error {
	elem := rv.Type().Elem()
	for elem.Kind() == reflect.Ptr {
		elem = elem.Elem()
	}
	rows := isRowStruct(elem)
	for i := range rv.Len() {
		item := rv.Index(i)
		if rows {
			for item.Kind() == reflect.Ptr && !item.IsNil() {
				item = item.Elem()
			}
			if item.Kind() != reflect.Struct {
				// A nil element in a slice of pointers keeps its %v form.
				if _, err := fmt.Fprintf(w, "%v\n", item.Interface()); err != nil {
					return err
				}
				continue
			}
			pairs := structTextPairs(item)
			parts := make([]string, len(pairs))
			for j, p := range pairs {
				parts[j] = p.name + "=" + p.value
			}
			if _, err := fmt.Fprintln(w, strings.Join(parts, " ")); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintln(w, delimitedCell(item)); err != nil {
			return err
		}
	}
	return nil
}

// textMarshalerType is encoding.TextMarshaler, used to tell a struct that
// renders ITSELF as text (time.Time) from one that is a row of fields.
var textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()

// isRowStruct reports whether t is a struct the text renderer should expand
// into named fields. A struct with its own text form is a scalar, not a row:
// expanding time.Time into wall/ext/loc would be exactly the debug dump this
// renderer exists to stop.
func isRowStruct(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return !t.Implements(textMarshalerType) && !reflect.PointerTo(t).Implements(textMarshalerType)
}

// structTextPairs renders a struct's exported fields, dropping the ones that
// carry nothing: a nil pointer/interface/map/slice, and an `,omitempty` field
// at its zero value (the same fields the json view omits).
func structTextPairs(rv reflect.Value) []textPair {
	specs := fieldSpecs(rv.Type())
	pairs := make([]textPair, 0, len(specs))
	for _, sp := range specs {
		f := rv.Field(sp.index)
		if isNilValue(f) {
			continue
		}
		if sp.omitEmpty && isEmptyValue(f) {
			continue
		}
		pairs = append(pairs, textPair{name: sp.name, value: delimitedCell(f)})
	}
	return pairs
}

// mapTextPairs renders a map's entries sorted by key, so the same map always
// prints in the same order.
func mapTextPairs(rv reflect.Value) []textPair {
	keys := rv.MapKeys()
	pairs := make([]textPair, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, textPair{
			name:  fmt.Sprintf("%v", k.Interface()),
			value: delimitedCell(rv.MapIndex(k)),
		})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].name < pairs[j].name })
	return pairs
}

// isNilValue reports whether v is a nil reference of any kind.
func isNilValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// isEmptyValue is encoding/json's own omitempty test, so the text view omits
// exactly the fields the json view omits.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
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
	// An empty listing is an empty COLLECTION, not null. Every `--json` listing
	// returns `[]` on an empty vault, but yaml marshalled the same nil slice to
	// JSON `null` on the way through and printed `null`, so `2nb orphans --yaml`
	// and `2nb unresolved --yaml` disagreed with their own json view and with
	// `2nb tasks --yaml`, whose slice merely happens to be built non-nil at its
	// construction site. Normalized here rather than at the eight jsonSafeList
	// call sites, so it reaches every command that renders a list.
	switch rv := reflect.ValueOf(data); rv.Kind() {
	case reflect.Slice:
		if rv.IsNil() {
			_, err := io.WriteString(w, "[]\n")
			return err
		}
	case reflect.Map:
		if rv.IsNil() {
			_, err := io.WriteString(w, "{}\n")
			return err
		}
	}
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

	// Handle slices of structs. The element TYPE decides, not the first element,
	// so a zero-row slice is still a csv document. The old `v.Len() > 0` guard
	// sent an empty listing to the JSON-record fallback below, which wrote the
	// literal record `null` (a nil slice) or `[]` (an empty one) into a csv
	// stream: `2nb orphans --format csv` on a vault with no orphans printed
	// `null` and `2nb tasks --format csv` printed `[]`, which is the exact
	// corruption csv is documented never to see.
	if v.Kind() == reflect.Slice {
		elem := v.Type().Elem()
		for elem.Kind() == reflect.Ptr {
			elem = elem.Elem()
		}
		if elem.Kind() == reflect.Struct {
			return writeStructSliceCSV(cw, v, elem)
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
	// v, not field: the loop above already dereferenced any pointer or
	// interface, and %v on the POINTER prints a heap address. A `*bool` set to
	// true rendered as `0x14000122a30` in csv, tsv and text alike (ModelInfo's
	// `reachable` and `credentials` are the live ones). A nil pointer returned
	// above with its `<nil>`, which is a real answer; an address never is.
	return fmt.Sprintf("%v", v.Interface())
}

// fieldSpec is one struct field as the output formatters see it: where it sits
// in the struct, the name its json tag gives it (the Go name when it has none),
// and whether that tag carries omitempty.
type fieldSpec struct {
	index     int
	name      string
	omitEmpty bool
}

// fieldSpecs describes every EXPORTED field of a struct type, in declaration
// order. Unexported fields are skipped because reflect refuses Interface() on
// one, so rendering it panics; no output struct in this repo has any, which is
// the only reason the previous NumField() loop never hit it.
func fieldSpecs(t reflect.Type) []fieldSpec {
	specs := make([]fieldSpec, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		spec := fieldSpec{index: i, name: f.Name}
		if tag := f.Tag.Get("json"); tag != "" && tag != "-" {
			parts := strings.Split(tag, ",")
			if parts[0] != "" {
				spec.name = parts[0]
			}
			for _, opt := range parts[1:] {
				if opt == "omitempty" {
					spec.omitEmpty = true
				}
			}
		}
		specs = append(specs, spec)
	}
	return specs
}

// fieldNames is the csv/tsv header row for a struct type: one json field name
// per exported field. Shared with the text renderer so the two views can never
// disagree about what a column is called.
func fieldNames(t reflect.Type) []string {
	specs := fieldSpecs(t)
	names := make([]string, len(specs))
	for i, sp := range specs {
		names[i] = sp.name
	}
	return names
}

// writeStructSliceCSV writes the header row and one record per element.
// rowType is the slice's element type with pointers stripped, so a ZERO-ROW
// slice still gets its header: an empty result then parses as a csv document
// with the right columns and no rows, where writing nothing at all leaves a
// consumer (pandas read_csv, csvkit) with no columns to bind, and where the
// old behavior wrote a JSON record into the stream.
func writeStructSliceCSV(cw *csv.Writer, v reflect.Value, rowType reflect.Type) error {
	specs := fieldSpecs(rowType)
	if err := cw.Write(fieldNames(rowType)); err != nil {
		return err
	}

	// Write rows
	for i := range v.Len() {
		row := v.Index(i)
		for row.Kind() == reflect.Ptr && !row.IsNil() {
			row = row.Elem()
		}
		if row.Kind() != reflect.Struct {
			// A nil pointer element has no fields to read; an empty record
			// keeps the column count right (reading through it would panic).
			if err := cw.Write(make([]string, len(specs))); err != nil {
				return err
			}
			continue
		}
		record := make([]string, len(specs))
		for j, sp := range specs {
			record[j] = delimitedCell(row.Field(sp.index))
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	return nil
}
