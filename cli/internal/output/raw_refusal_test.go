package output

import (
	"bytes"
	"testing"
)

// What raw/md are FOR must keep working exactly as before.
func TestWrite_RawStillEmitsBodies(t *testing.T) {
	cases := []struct {
		name string
		data any
		want string
	}{
		{"string", "hello\n", "hello\n"},
		{"bytes", []byte("bytes\n"), "bytes\n"},
		{"serializable", serializable{body: "body\n"}, "body\n"},
		{"int scalar", 42, "42\n"},
		{"bool scalar", true, "true\n"},
	}
	for _, format := range []Format{FormatRaw, FormatMD} {
		for _, c := range cases {
			var buf bytes.Buffer
			if err := Write(&buf, format, c.data); err != nil {
				t.Errorf("%s/%s: unexpected error: %v", format, c.name, err)
				continue
			}
			if buf.String() != c.want {
				t.Errorf("%s/%s: got %q, want %q", format, c.name, buf.String(), c.want)
			}
		}
	}
}

// The empty format is JSON: output.Write's default branch. Callers normalizing
// nil slices for machine consumers must treat it as such, which is what let a
// bare `2nb tags` keep printing `null` after the explicit --json form was fixed.
func TestRendersJSON(t *testing.T) {
	for f, want := range map[Format]bool{FormatJSON: true, "": true, FormatCSV: false, FormatYAML: false, FormatRaw: false, FormatText: false} {
		if got := RendersJSON(f); got != want {
			t.Errorf("RendersJSON(%q) = %v, want %v", f, got, want)
		}
	}
}
