package document

import (
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// A frontmatter value has TWO readings, and which one a field wants depends on
// what the field means.
//
// gopkg.in/yaml.v3 decodes into `any` with the full core schema, so an UNQUOTED
// value takes its own Go type: `2020-01-01T00:00:00Z` and `2020-01-01` both
// become time.Time, `true` becomes bool, `12345` becomes int, `007` becomes the
// int 7. A bare `meta[key].(string)` assertion silently fails for every one of
// those and leaves the field it fed EMPTY, which is the bug this file exists to
// fix: Obsidian's own Date property writes the unquoted form while `2nb create`
// and `2nb meta` quote theirs, so the assertion held for 2nb-authored notes and
// failed for hand-edited and imported ones.
//
// A DATE field (created, modified) wants the RESOLVED value, normalized to
// RFC3339: that is the format the index column stores and the format `stale`
// and `list --sort modified` parse back, and `2026-09-04` and
// `2026-09-04T00:00:00Z` genuinely are the same instant.
//
// A TEXT field (title, type, status, id, and every tag and alias) wants the
// ORIGINAL TEXT. Resolution is lossy in exactly the way that matters here:
// `2026-09-04` and `2026-09-04T00:00:00Z` are different inputs that resolve to
// the same time.Time, so NO formatting choice can recover both. Formatting the
// resolved time gave a daily note titled `2026-09-04` the title
// `2026-09-04T00:00:00Z`, which is not what the file says and is what `list`,
// `search` and `meta --get` then showed.
//
// The original text is not in the map, but it IS in the yaml.Node: a scalar
// node's `Value` is the verbatim text with quoting and escapes removed, so
// `title: 2026-09-04` and `title: "2026-09-04"` both read back as `2026-09-04`,
// `id: 007` keeps its leading zeros, and `num: 3.50` keeps its trailing one.
// rawFrontmatter carries those, alongside the decoded map, from the one place
// the frontmatter region is isolated. The repo already works with yaml.Node in
// UpdateDocumentFrontmatterAST; this follows the same shape.

// rawItem is one sequence element's verbatim text. ok is false for an element
// that is not a scalar (a nested list or mapping), which is not a tag or an
// alias and is dropped rather than rendered.
type rawItem struct {
	text string
	ok   bool
}

// rawValue is one top-level frontmatter key's verbatim text.
type rawValue struct {
	text string
	ok   bool
	// items is index-aligned with the decoded []any for a sequence value, so a
	// caller iterating the decoded slice can read each element's own text.
	items []rawItem
}

// rawFrontmatter maps a top-level frontmatter key to its verbatim text. It is
// nil when there is no node to read (a hand-built map, a synthetic .canvas or
// .base document, a region that would not parse), and every reader falls back
// to ScalarText on the resolved value in that case.
type rawFrontmatter map[string]rawValue

func (r rawFrontmatter) scalar(key string) (string, bool) {
	v, ok := r[key]
	if !ok || !v.ok {
		return "", false
	}
	return v.text, true
}

func (r rawFrontmatter) item(key string, i int) (string, bool) {
	v, ok := r[key]
	if !ok || i < 0 || i >= len(v.items) || !v.items[i].ok {
		return "", false
	}
	return v.items[i].text, true
}

// rawFrontmatterOf reads the verbatim text of every top-level key in a
// frontmatter region. It returns nil rather than an error for anything it
// cannot read: the resolved map is the authority on whether the region parses,
// and this is only ever an improvement on top of it.
func rawFrontmatterOf(yamlStr string) rawFrontmatter {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(yamlStr), &node); err != nil {
		return nil
	}
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return nil
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	raw := make(rawFrontmatter, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i].Value, root.Content[i+1]
		switch val.Kind {
		case yaml.ScalarNode:
			raw[key] = rawValue{text: val.Value, ok: true}
		case yaml.SequenceNode:
			items := make([]rawItem, len(val.Content))
			for j, item := range val.Content {
				if item.Kind == yaml.ScalarNode {
					items[j] = rawItem{text: item.Value, ok: true}
				}
			}
			raw[key] = rawValue{items: items}
		}
	}
	return raw
}

// ScalarText renders a RESOLVED frontmatter scalar as text, reporting false for
// anything that is not a scalar (a nil value, a list, a nested mapping).
//
// It is the FALLBACK for a value with no node behind it: a map built in code, a
// synthetic .canvas/.base document, or a `SetMeta` write. Where a node exists,
// its verbatim text is used instead, because this function cannot recover the
// original text of a resolved date (see the file comment). The switch is
// exhaustive over what yaml.v3 produces for a scalar decoded into `any`.
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
// It reads the RESOLVED value, deliberately: normalizing is the point here, so
// that a quoted timestamp, an unquoted one, a date with no time and one
// carrying a zone offset all land in the column as the same comparable
// instant.
//
// Only the two shapes a date can legitimately arrive in are accepted: a string
// (the quoted form 2nb writes itself) and a time.Time (the unquoted form
// Obsidian's Date property writes). A number, a boolean, a list or a mapping
// under `modified:` is not a date, and coercing one to text would put an
// unparseable value in a timestamp column instead of leaving it empty.
func frontmatterTime(meta map[string]any, key string) (string, bool) {
	switch t := meta[key].(type) {
	case string:
		return t, true
	case time.Time:
		return t.Format(time.RFC3339), true
	}
	return "", false
}

// frontmatterText reads a TEXT field (id, title, type, status) as the note
// wrote it. The verbatim node text wins; ScalarText on the resolved value is
// the fallback where there is no node.
//
// A note titled `title: 12345` or `title: 2026-09-04` (the shape a daily note
// takes) indexed with an EMPTY title before any of this, so it could not be
// found by name and rendered nameless in every listing.
func frontmatterText(meta map[string]any, raw rawFrontmatter, key string) (string, bool) {
	if _, present := meta[key]; !present {
		return "", false
	}
	if s, ok := raw.scalar(key); ok {
		return s, true
	}
	return ScalarText(meta[key])
}
