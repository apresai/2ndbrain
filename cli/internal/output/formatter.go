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
	FormatYAML  Format = "yaml"
	FormatTable Format = "table"
)

func Write(w io.Writer, format Format, data any) error {
	switch format {
	case FormatJSON:
		return writeJSON(w, data)
	case FormatCSV:
		return writeCSV(w, data)
	case FormatYAML:
		return writeYAML(w, data)
	default:
		return writeJSON(w, data)
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

func writeCSV(w io.Writer, data any) error {
	cw := csv.NewWriter(w)
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
