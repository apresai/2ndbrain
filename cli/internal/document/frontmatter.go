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

	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return nil, s, nil
	}

	// Find closing ---
	rest := s[4:] // skip opening "---\n"
	idx := strings.Index(rest, "\n---\n")
	if idx == -1 {
		// Try \r\n
		idx = strings.Index(rest, "\r\n---\r\n")
		if idx == -1 {
			// Frontmatter might end at EOF with just "---"
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
