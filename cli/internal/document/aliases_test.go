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
		{"[]any drops non-strings", map[string]any{"aliases": []any{"a", 2, "b"}}, []string{"a", "b"}},
		{"[]string", map[string]any{"aliases": []string{"c"}}, []string{"c"}},
		{"single string", map[string]any{"aliases": "solo"}, []string{"solo"}},
		{"empty string", map[string]any{"aliases": ""}, nil},
		{"unsupported type", map[string]any{"aliases": 42}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractAliases(tc.meta); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v want %#v", got, tc.want)
			}
		})
	}
}
