package output

import (
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
		// Fallback: print string representation
		_, err := fmt.Fprintf(w, "%v\n", data)
		return err
	}
}

func writeJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func writeYAML(w io.Writer, data any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(data)
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
			record[j] = fmt.Sprintf("%v", row.Field(j).Interface())
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	return nil
}
