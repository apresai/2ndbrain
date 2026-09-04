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
// expanding time.Time would print NOTHING at all, because wall, ext and loc are
// unexported and fieldSpecs skips them, so the instant would vanish instead of
// rendering as the RFC3339 text its MarshalText gives.
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
		f, ok := specValue(rv, sp)
		if !ok {
			// The field sits inside a nil embedded pointer: there is nothing to
			// print, which is what a nil renders as anyway.
			continue
		}
		// An assembled object is always printed: json emits a struct field
		// whatever its contents, `omitempty` included.
		if sp.object == nil {
			if isNilValue(f) {
				continue
			}
			if sp.omitEmpty && isEmptyValue(f) {
				continue
			}
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

	// A SCALAR payload is ONE cell, and that cell is the scalar's text.
	// Falling through to the JSON encoder below quotes a string, and the csv
	// writer then escapes those quotes, so `config get ai.provider --format
	// csv` came out as """bedrock""": a consumer had to strip CSV quoting and
	// then JSON-unquote to read one word. The composite fallback stays, because
	// a map or a struct genuinely is compact JSON in a cell.
	if v.IsValid() {
		// A value that renders ITSELF as text (time.Time) is a scalar too, and
		// it is a STRUCT, so the kind switch below never saw it: `meta --get
		// created --format csv` came out as """2020-01-01T00:00:00Z""".
		if _, ok := data.(encoding.TextMarshaler); ok && !isNilValue(v) {
			return cw.Write([]string{delimitedCell(v)})
		}
		switch v.Kind() {
		case reflect.String, reflect.Bool,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return cw.Write([]string{delimitedCell(v)})
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
		// A nil pointer implementing the interface would panic in MarshalText,
		// so it falls through to the dereference loop below and comes back as
		// an empty cell like every other nil.
		if field.Kind() != reflect.Ptr || !field.IsNil() {
			if b, err := tm.MarshalText(); err == nil {
				return string(b)
			}
		}
	}
	v := field
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			// EMPTY, not `<nil>`. `<nil>` is Go syntax, and this repo's
			// contract for a delimited stream is that a cell is compact JSON
			// and never Go syntax; json renders these same fields as `null`,
			// and an empty cell is what a csv consumer reads a null as.
			// `models list --csv` carried the literal 4-character `<nil>` in 16
			// rows of a 91-row catalog (reachable, credentials, benchmark).
			return ""
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
	// `reachable` and `credentials` are the live ones). An address is never an
	// answer. A nil pointer returned above as an EMPTY cell, which is what json
	// means by `null`; it used to return `<nil>`, which is a different way of
	// putting Go syntax in the stream.
	return fmt.Sprintf("%v", v.Interface())
}

// fieldSpec is one struct field as the output formatters see it: the index PATH
// that reaches it (more than one hop when it was promoted out of an embedded
// struct), the name its json tag gives it (the Go name when it has none),
// whether that name CAME from a tag, and whether that tag carries omitempty.
//
// tagged exists only for the collision rule: json breaks an equal-depth tie in
// favor of the tagged field, and calls it a conflict when neither side wins.
//
// object, when non-nil, marks a field whose cell is ASSEMBLED rather than read:
// an anonymous UNEXPORTED struct carrying a json name, which json emits as a
// nested object. reflect refuses Interface() on the embed itself, so the cell is
// built from these leaf specs (index paths from the same row root) instead. An
// empty, non-nil slice is the embed with no exported fields, which json renders
// as `{}`; nil means the field is read directly.
type fieldSpec struct {
	index     []int
	name      string
	tagged    bool
	omitEmpty bool
	object    []fieldSpec
}

// jsonMarshalerType is json.Marshaler, used to tell an embedded struct that
// renders ITSELF as one value from one whose fields are promoted.
var jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

// maxFieldDepth bounds the embedded-struct recursion. Go's own rules make a
// cycle through anonymous struct fields impossible (a type cannot embed itself),
// so this is a belt-and-braces stop, not a correctness condition.
const maxFieldDepth = 8

// fieldSpecs describes every field of a struct type as encoding/json sees it,
// in declaration order, so text and csv name a field the way json does.
//
// Three rules, all taken from encoding/json:
//
//   - `json:"-"` means the field does not exist for output. It used to fall
//     through the `tag != "-"` guard and get emitted under its GO name, so a
//     field deliberately hidden from the json view still reached text and csv.
//   - An ANONYMOUS field of struct type (or pointer to struct) with no json NAME
//     is flattened: its own fields are promoted to the parent level. json does
//     this, the old single-index loop did not, so `doctor --format text` printed
//     `SuiteStatus: {"latest":...}` as one JSON blob keyed by a Go type name
//     that appears nowhere in the json view, and `skills doctor --format text`
//     did the same with `Verification`. An embedded field WITH a json name keeps
//     that name and stays one value.
//   - On a name collision json's own rule applies, in dedupeFieldSpecs.
//   - An unexported field does not exist for output, with one exception json
//     also makes: an anonymous field of unexported STRUCT type. reflect refuses
//     Interface() on the embedded field itself, but not on the exported fields
//     beneath it, because Value.Field does not propagate the embedded read-only
//     flag, so those leaves carry the value. With NO json name they are promoted
//     to the parent level; WITH one they are assembled into a single object cell
//     under that name, which is the nested object json emits. Either way the
//     embed itself is never passed to Interface().
//
// One deliberate narrowing, and it is the ONE place these views differ from
// json. An embedded type that renders itself is kept as ONE value rather than
// flattened. isRowStruct already excludes every encoding.TextMarshaler, and that
// is the branch time.Time takes, so the jsonMarshalerType check below covers
// only what TextMarshaler misses: a json.Marshaler that is NOT also a
// TextMarshaler. It is not what stops an embedded time from exploding into
// wall/ext/loc, which cannot happen anyway: those three are UNEXPORTED, so
// flattening time.Time yields no fields at all.
//
// The divergence is Go's method promotion, not a choice here: an anonymous
// Marshaler promotes its marshal method to the OUTER type, so json collapses the
// WHOLE record to that one value and discards every sibling field. A row-shaped
// format cannot represent that, so the row is rendered instead, and an
// UNEXPORTED such embed is dropped outright since its value cannot be read at
// all. Nothing in internal/ has either shape.
func fieldSpecs(t reflect.Type) []fieldSpec {
	return dedupeFieldSpecs(appendFieldSpecs(nil, t, nil, 0))
}

func appendFieldSpecs(specs []fieldSpec, t reflect.Type, prefix []int, depth int) []fieldSpec {
	if depth >= maxFieldDepth {
		return specs
	}
	for i := range t.NumField() {
		f := t.Field(i)
		unexported := f.PkgPath != ""
		if unexported && !f.Anonymous {
			// An unexported non-embedded field does not exist for json either.
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		spec := fieldSpec{name: f.Name}
		if tag != "" {
			parts := strings.Split(tag, ",")
			if parts[0] != "" {
				spec.name = parts[0]
				spec.tagged = true
			}
			for _, opt := range parts[1:] {
				if opt == "omitempty" {
					spec.omitEmpty = true
				}
			}
		}
		spec.index = append(append(make([]int, 0, len(prefix)+1), prefix...), i)
		if f.Anonymous && !spec.tagged && isFlattenable(f.Type) {
			ft := f.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			specs = appendFieldSpecs(specs, ft, spec.index, depth+1)
			continue
		}
		if unexported {
			if spec.tagged && isFlattenable(f.Type) {
				// A tagged unexported embed: json emits it as a nested OBJECT
				// under the tag name. reflect refuses Interface() on the embed
				// itself, so the cell is assembled from the exported leaves
				// underneath it, which ARE interfaceable. Same leaves the
				// untagged case promotes; the tag only decides whether they land
				// at the parent level or inside one cell.
				ft := f.Type
				if ft.Kind() == reflect.Ptr {
					ft = ft.Elem()
				}
				spec.object = dedupeFieldSpecs(appendFieldSpecs(nil, ft, spec.index, depth+1))
				if spec.object == nil {
					// Non-nil is what marks an object spec, and json renders an
					// embed with no exported fields as `{}` rather than omitting
					// the key.
					spec.object = []fieldSpec{}
				}
				specs = append(specs, spec)
				continue
			}
			// What is left has no readable value and no leaves to assemble: an
			// unexported embed of NON-struct type (json drops it too), or one
			// that renders ITSELF. A self-rendering embed promotes its
			// MarshalJSON to the outer type, so json collapses the WHOLE record
			// to that one value; a row-shaped format cannot represent that, and
			// Interface() on the embed would panic, so the column is dropped.
			continue
		}
		specs = append(specs, spec)
	}
	return specs
}

// isFlattenable reports whether an anonymous field's fields are promoted: it
// must be a struct (or pointer to one) that does not render itself.
func isFlattenable(t reflect.Type) bool {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	if t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType) {
		return false
	}
	return isRowStruct(t)
}

// dedupeFieldSpecs resolves same-name fields exactly as encoding/json's
// dominantField does, so a promoted name and an outer-level one settle the same
// way in text and csv as in json. Grouping is by the EFFECTIVE name, so a
// `json:"n"` field and a bare field named N never meet: they are two names.
//
// Within a name, DEPTH decides first and a tag breaks a tie at equal depth:
//
//   - shallower wins outright, tagged or not
//   - at equal depth, exactly one tagged: the tagged one wins
//   - at equal depth, both tagged or neither: json calls it a CONFLICT and drops
//     the name from the output entirely, and so does this
//
// Every one of those was read off json.Marshal rather than from the spec, and
// TestFieldNames_MatchJSONThroughCollisionsAndUnexportedEmbeds re-reads them
// from json on every run, so a change in the standard library's rule trips the
// suite instead of silently splitting the two views apart.
func dedupeFieldSpecs(specs []fieldSpec) []fieldSpec {
	best := make(map[string]int, len(specs))
	conflicted := make(map[string]bool)
	for i, sp := range specs {
		j, seen := best[sp.name]
		if !seen {
			best[sp.name] = i
			continue
		}
		cur := specs[j]
		switch {
		case len(cur.index) < len(sp.index):
			// A shallower field already won, so this one never competes and
			// cannot revive a conflict resolved above it.
		case len(cur.index) > len(sp.index):
			// This one is shallower, so it wins outright and clears any tie
			// recorded among the deeper fields it just displaced.
			best[sp.name] = i
			delete(conflicted, sp.name)
		case cur.tagged == sp.tagged:
			// Equal depth and neither out-tags the other.
			conflicted[sp.name] = true
		case sp.tagged:
			// Equal depth, and only this one carries a json name.
			best[sp.name] = i
			delete(conflicted, sp.name)
		default:
			// Equal depth, and only the incumbent carries a json name.
		}
	}
	out := make([]fieldSpec, 0, len(specs))
	for i, sp := range specs {
		if best[sp.name] == i && !conflicted[sp.name] {
			out = append(out, sp)
		}
	}
	return out
}

// fieldByIndex walks a fieldSpec's index path to the value, dereferencing an
// embedded POINTER on the way. A nil one has no field to read, so it reports
// false rather than panicking the way reflect.Value.FieldByIndex would: text
// then omits the field (it omits nils anyway) and csv writes an empty cell,
// which keeps the column count right.
// specValue resolves one fieldSpec against a row. A normal field is read at its
// index path; an OBJECT spec is assembled, since its embed cannot be read. The
// bool is false only when there is nothing to render, which is a field promoted
// out of a nil embedded pointer.
func specValue(root reflect.Value, sp fieldSpec) (reflect.Value, bool) {
	if sp.object != nil {
		return reflect.ValueOf(objectCell(root, sp.object)), true
	}
	return fieldByIndex(root, sp.index)
}

// objectCell builds the map a tagged unexported embed renders as: one entry per
// exported leaf, keyed by the leaf's json name, nested where a leaf is itself
// such an embed. delimitedCell then marshals it as one compact-JSON cell.
//
// Interface() is called ONLY on the leaves, never on the embed, which is the
// whole reason this exists. The CanInterface guard makes that an enforced rule
// rather than an assumption about which reflect flags propagate.
//
// Two cell-level differences from json.Marshal, both deliberate: a map marshals
// with SORTED keys where json uses declaration order, and a leaf behind a nil
// embedded pointer is omitted (json's own marshal omits it too, but a nil
// TAGGED pointer embed renders `{}` here where json writes `null`).
func objectCell(root reflect.Value, leaves []fieldSpec) map[string]any {
	obj := make(map[string]any, len(leaves))
	for _, leaf := range leaves {
		if leaf.object != nil {
			obj[leaf.name] = objectCell(root, leaf.object)
			continue
		}
		v, ok := fieldByIndex(root, leaf.index)
		if !ok || !v.CanInterface() {
			continue
		}
		if leaf.omitEmpty && isEmptyValue(v) {
			continue
		}
		obj[leaf.name] = v.Interface()
	}
	return obj
}

func fieldByIndex(rv reflect.Value, index []int) (reflect.Value, bool) {
	for hop, i := range index {
		if hop > 0 {
			for rv.Kind() == reflect.Ptr {
				if rv.IsNil() {
					return reflect.Value{}, false
				}
				rv = rv.Elem()
			}
		}
		if rv.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		rv = rv.Field(i)
	}
	return rv, true
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
			f, ok := specValue(row, sp)
			if !ok {
				// A field promoted out of a nil embedded pointer: an empty cell,
				// so the row still has one cell per header column.
				continue
			}
			record[j] = delimitedCell(f)
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	return nil
}
