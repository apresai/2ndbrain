package document

import (
	"strconv"
	"time"
)

// ScalarText renders one YAML frontmatter scalar as the text a string-typed
// field wants, reporting false for anything that is not a scalar (a nil value,
// a list, a nested mapping).
//
// It exists because a frontmatter value is only a Go `string` when the YAML
// scalar could not be resolved to something narrower. gopkg.in/yaml.v3 decodes
// into `any` with the full core schema, so an UNQUOTED value takes its own Go
// type: `2020-01-01T00:00:00Z` and `2020-01-01` become time.Time, `true`
// becomes bool, `12345` becomes int, `3.5` becomes float64. A bare
// `meta[key].(string)` assertion silently fails for every one of those, and the
// field it fed simply stayed empty.
//
// That is not a hypothetical shape. Obsidian's own native Date property writes
// the unquoted form, while `2nb create` and `2nb meta` quote theirs, so the
// assertion held for 2nb-authored notes and failed for hand-edited and imported
// ones, with no warning anywhere.
//
// The switch is exhaustive over what yaml.v3 produces for a scalar decoded into
// `any` (string, bool, int, int64, uint64, float64, time.Time, and []byte for
// !!binary), which is the only way frontmatter is ever built here. A time is
// RFC3339, matching what encoding/json emits for a time.Time and what
// output.delimitedCell emits via MarshalText, so every view of the same note
// agrees.
func ScalarText(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case time.Time:
		return t.Format(time.RFC3339), true
	case bool:
		return strconv.FormatBool(t), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case uint64:
		return strconv.FormatUint(t, 10), true
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64), true
	case []byte:
		return string(t), true
	}
	return "", false
}

// frontmatterTime reads a DATE field (created, modified, and any date field a
// future schema adds) and normalizes it to RFC3339, the format the index column
// stores and the format `stale` and `list --sort modified` parse back.
//
// Only the two shapes a date can legitimately arrive in are accepted: a string
// (the quoted form 2nb writes itself) and a time.Time (the unquoted form
// Obsidian's Date property writes). A number or a boolean under `modified:` is
// not a date, and coercing one to text would put an unparseable value in a
// timestamp column instead of leaving it empty.
func frontmatterTime(meta map[string]any, key string) (string, bool) {
	switch t := meta[key].(type) {
	case string:
		return t, true
	case time.Time:
		return t.Format(time.RFC3339), true
	}
	return "", false
}

// frontmatterText reads a string-typed field (id, title, type, status),
// accepting any scalar YAML resolved to a narrower Go type. A note titled
// `title: 12345` or `title: 2026-09-04` indexed with an EMPTY title before
// this, so it could not be found by name and rendered nameless in every listing.
func frontmatterText(meta map[string]any, key string) (string, bool) {
	return ScalarText(meta[key])
}
