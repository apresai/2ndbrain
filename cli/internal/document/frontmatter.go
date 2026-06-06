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
