package document

import (
	"reflect"
	"testing"
)

func TestExtractAliases(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]any
		want []string
	}{
		{"nil meta", nil, nil},
		{"absent key", map[string]any{"x": 1}, nil},
		{"[]any of strings", map[string]any{"aliases": []any{"a", "b"}}, []string{"a", "b"}},
		// A non-string SCALAR is coerced, not dropped: YAML resolves an
		// unquoted alias to its own Go type, and dropping it lost a real alias
		// the note declared. Only a non-scalar (a nested list or mapping) is
		// still dropped, because it is not an alias at all.
		{"[]any coerces scalars", map[string]any{"aliases": []any{"a", 2, "b"}}, []string{"a", "2", "b"}},
		{"[]any drops a nested composite", map[string]any{"aliases": []any{"a", []any{"nested"}}}, []string{"a"}},
		{"[]string", map[string]any{"aliases": []string{"c"}}, []string{"c"}},
		{"single string", map[string]any{"aliases": "solo"}, []string{"solo"}},
		{"empty string", map[string]any{"aliases": ""}, nil},
		{"bare scalar", map[string]any{"aliases": 42}, []string{"42"}},
		{"unsupported type", map[string]any{"aliases": map[string]any{"k": "v"}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractAliases(tc.meta); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v want %#v", got, tc.want)
			}
		})
	}
}
