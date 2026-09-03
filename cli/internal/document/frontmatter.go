package document

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

var sensitiveKeys = map[string]bool{
	"secret":   true,
	"password": true,
	"token":    true,
	"key":      true,
}

func IsSensitiveKey(key string) bool {
	return sensitiveKeys[strings.ToLower(key)]
}

// emptyFrontmatterBlock reports whether the region after the opening delimiter
// begins with the CLOSING delimiter, i.e. the note opens with an empty
// frontmatter block ("---\n---\n"), and returns the body that follows it.
//
// Obsidian writes that shape when every property is removed from a note, and
// SerializeFrontmatter writes it for an empty map, so 2nb produces it itself.
// Without this check the closing-delimiter search below runs straight past the
// empty block and matches the next "---" in the BODY (a horizontal rule),
// handing real prose to the YAML parser. The note then fails to parse on every
// index, and one such note was enough to fail a whole force-reembed and roll
// 313 good embeddings back.
func emptyFrontmatterBlock(rest string) (body string, ok bool) {
	switch {
	case strings.HasPrefix(rest, "---\r\n"):
		return rest[len("---\r\n"):], true
	case strings.HasPrefix(rest, "---\n"):
		return rest[len("---\n"):], true
	case rest == "---":
		// Closing delimiter at end of file, no trailing newline.
		return "", true
	}
	return "", false
}

// legacyDoubledDelimiterFrontmatter re-reads a doubled opening delimiter the way
// the parser did before the empty-block rule: everything up to the NEXT closing
// delimiter is the YAML region, and its leading "---" is a document start marker
// that YAML accepts. It wins only when that region yields a NON-EMPTY mapping;
// a parse error or an empty result means the block really is empty.
//
// A BLANK line right after the second "---" ends the reading before it starts.
// Frontmatter is contiguous from its opening fence, so a note that opens with an
// empty block and then leaves a blank line has body from there on, whatever the
// body happens to look like. Without that rule the reading reached past the
// blank line and swallowed prose: "---\n---\n\nStatus: draft\n\n---\n\nRest\n"
// read "Status: draft" as a property, and because UpdateDocumentFrontmatterAST
// consults this same function, `2nb meta --set` and `2nb tag add` then REWROTE
// the file, lifting those lines out of the visible body and into a frontmatter
// block on disk. A frontmatter-only command must never touch the body.
//
// What survives the rule is the genuine doubled fence, where properties follow
// the second delimiter immediately, closed by a newline "---" or by end of file
// (the same two closers the main parser accepts, in LF and in CRLF).
//
// The inherited cost, unchanged: a body that starts on the very next line and
// happens to parse as a mapping is read as frontmatter. A markdown heading is a
// YAML comment, so "---\n---\n# H\n\nkey: value\n---\nmore" reads {key: value}
// as metadata. That is the ambiguity of a doubled delimiter with no blank line
// after it, and losing real metadata is the worse of the two failures.
func legacyDoubledDelimiterFrontmatter(rest string) (map[string]any, string, bool) {
	var afterOpen int
	switch {
	case strings.HasPrefix(rest, "---\r\n"):
		afterOpen = len("---\r\n")
	case strings.HasPrefix(rest, "---\n"):
		afterOpen = len("---\n")
	default:
		// A second delimiter at end of file: an empty block, nothing behind it.
		return nil, "", false
	}
	if next := rest[afterOpen:]; next == "" ||
		strings.HasPrefix(next, "\n") || strings.HasPrefix(next, "\r\n") {
		return nil, "", false
	}

	idx := strings.Index(rest, "\n---\n")
	closeLen := len("\n---\n")
	if idx == -1 {
		idx = strings.Index(rest, "\r\n---\r\n")
		closeLen = len("\r\n---\r\n")
	}
	// End-of-file closers, CRLF first: "\r\n---" also ends with "\n---", and
	// taking the LF reading there would leave a stray "\r" on the YAML region.
	if idx == -1 && strings.HasSuffix(rest, "\r\n---") {
		idx, closeLen = len(rest)-len("\r\n---"), len("\r\n---")
	}
	if idx == -1 && strings.HasSuffix(rest, "\n---") {
		idx, closeLen = len(rest)-len("\n---"), len("\n---")
	}
	if idx == -1 {
		return nil, "", false
	}
	meta := make(map[string]any)
	if err := yaml.Unmarshal([]byte(rest[:idx]), &meta); err != nil || len(meta) == 0 {
		return nil, "", false
	}
	return meta, rest[idx+closeLen:], true
}

func ParseFrontmatter(content []byte) (meta map[string]any, body string, err error) {
	s := string(content)

	// Length of the opening delimiter depends on which line ending the file
	// uses. Skipping a fixed 4 bytes for a "---\r\n" opening leaves a stray
	// "\n" at the start of the YAML region — harmless today (YAML tolerates
	// leading whitespace) but throws off every offset downstream.
	var openLen int
	switch {
	case strings.HasPrefix(s, "---\r\n"):
		openLen = 5
	case strings.HasPrefix(s, "---\n"):
		openLen = 4
	default:
		return nil, s, nil
	}

	rest := s[openLen:]
	if emptyBody, ok := emptyFrontmatterBlock(rest); ok {
		// A doubled delimiter is genuinely ambiguous, so prefer the reading that
		// cannot LOSE data. "---\n---\nreal: value\n---\nbody" is either an
		// empty block followed by prose that happens to look like YAML, or a
		// mapping whose region opens with a stray document marker. Before the
		// empty-block rule existed the second reading always won (the search
		// below found the THIRD delimiter and YAML accepted the leading "---" as
		// a document start), so taking the first reading unconditionally moved a
		// note's real metadata into its body.
		if meta, legacyBody, ok := legacyDoubledDelimiterFrontmatter(rest); ok {
			return meta, legacyBody, nil
		}
		return map[string]any{}, emptyBody, nil
	}
	idx := strings.Index(rest, "\n---\n")
	if idx == -1 {
		// Try CRLF with trailing newline
		idx = strings.Index(rest, "\r\n---\r\n")
		if idx == -1 {
			// Try CRLF at EOF
			idx = strings.Index(rest, "\r\n---")
			if idx != -1 && idx+len("\r\n---") == len(rest) {
				yamlStr := rest[:idx]
				meta = make(map[string]any)
				if err := yaml.Unmarshal([]byte(yamlStr), &meta); err != nil {
					return nil, s, fmt.Errorf("malformed YAML frontmatter: %w", err)
				}
				return meta, "", nil
			}
			// Try LF at EOF
			idx = strings.Index(rest, "\n---")
			if idx == -1 {
				return nil, s, nil
			}
			yamlStr := rest[:idx]
			meta = make(map[string]any)
			if err := yaml.Unmarshal([]byte(yamlStr), &meta); err != nil {
				return nil, s, fmt.Errorf("malformed YAML frontmatter: %w", err)
			}
			return meta, "", nil
		}
		yamlStr := rest[:idx]
		meta = make(map[string]any)
		if err := yaml.Unmarshal([]byte(yamlStr), &meta); err != nil {
			return nil, s, fmt.Errorf("malformed YAML frontmatter: %w", err)
		}
		body = rest[idx+len("\r\n---\r\n"):]
		return meta, body, nil
	}

	yamlStr := rest[:idx]
	meta = make(map[string]any)
	if err := yaml.Unmarshal([]byte(yamlStr), &meta); err != nil {
		return nil, s, fmt.Errorf("malformed YAML frontmatter: %w", err)
	}
	body = rest[idx+len("\n---\n"):]
	return meta, body, nil
}

func SerializeFrontmatter(meta map[string]any) ([]byte, error) {
	if len(meta) == 0 {
		return []byte("---\n---\n"), nil
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(meta); err != nil {
		return nil, fmt.Errorf("serialize frontmatter: %w", err)
	}
	enc.Close()
	buf.WriteString("---\n")
	return buf.Bytes(), nil
}

func SerializeDocument(meta map[string]any, body string) ([]byte, error) {
	fm, err := SerializeFrontmatter(meta)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.Write(fm)
	if body != "" {
		buf.WriteString(body)
	}
	return buf.Bytes(), nil
}

// FilterSensitive returns a copy of meta with sensitive keys removed.
func FilterSensitive(meta map[string]any) map[string]any {
	filtered := make(map[string]any, len(meta))
	for k, v := range meta {
		if !IsSensitiveKey(k) {
			filtered[k] = v
		}
	}
	return filtered
}

// UpdateDocumentFrontmatterAST updates the frontmatter of a document surgically,
// preserving comments, formatting, and key order for all untouched fields.
func UpdateDocumentFrontmatterAST(original []byte, updatedMeta map[string]any, body string) ([]byte, error) {
	s := string(original)
	var openLen int
	switch {
	case strings.HasPrefix(s, "---\r\n"):
		openLen = 5
	case strings.HasPrefix(s, "---\n"):
		openLen = 4
	default:
		return SerializeDocument(updatedMeta, body)
	}

	rest := s[openLen:]
	// An empty frontmatter block carries no YAML to preserve surgically, and
	// the closing-delimiter search below would otherwise match a "---" in the
	// body and treat body prose as the region to edit. A doubled delimiter that
	// still reads as real frontmatter (see legacyDoubledDelimiterFrontmatter)
	// does have a region to preserve, so it falls through to the surgical path.
	if _, ok := emptyFrontmatterBlock(rest); ok {
		if _, _, legacy := legacyDoubledDelimiterFrontmatter(rest); !legacy {
			return SerializeDocument(updatedMeta, body)
		}
	}
	idx := strings.Index(rest, "\n---\n")
	if idx == -1 {
		idx = strings.Index(rest, "\r\n---\r\n")
		if idx == -1 {
			idx = strings.Index(rest, "\r\n---")
			if idx == -1 {
				idx = strings.Index(rest, "\n---")
			}
		}
	}

	if idx == -1 {
		return SerializeDocument(updatedMeta, body)
	}

	yamlStr := rest[:idx]

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(yamlStr), &node); err != nil {
		return nil, err
	}

	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return nil, fmt.Errorf("invalid YAML document node")
	}
	root := node.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("frontmatter must be a MappingNode")
	}

	// 1. Update existing or insert new keys
	for k, v := range updatedMeta {
		vBytes, err := yaml.Marshal(v)
		if err != nil {
			return nil, err
		}
		var vNode yaml.Node
		if err := yaml.Unmarshal(vBytes, &vNode); err != nil {
			return nil, err
		}
		if vNode.Kind == yaml.DocumentNode && len(vNode.Content) > 0 {
			vNode = *vNode.Content[0]
		}

		found := false
		for i := 0; i < len(root.Content); i += 2 {
			keyNode := root.Content[i]
			if keyNode.Value == k {
				root.Content[i+1] = &vNode
				found = true
				break
			}
		}
		if !found {
			keyNode := &yaml.Node{
				Kind:  yaml.ScalarNode,
				Tag:   "!!str",
				Value: k,
			}
			root.Content = append(root.Content, keyNode, &vNode)
		}
	}

	// 2. Remove keys that are not in updatedMeta
	var newContent []*yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		keyNode := root.Content[i]
		if _, exists := updatedMeta[keyNode.Value]; exists {
			newContent = append(newContent, root.Content[i], root.Content[i+1])
		}
	}
	root.Content = newContent

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return nil, err
	}
	enc.Close()

	var docBuf bytes.Buffer
	docBuf.WriteString("---\n")
	docBuf.Write(buf.Bytes())
	docBuf.WriteString("---\n")
	docBuf.WriteString(body)

	return docBuf.Bytes(), nil
}
