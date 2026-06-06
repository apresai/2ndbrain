package document

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ParseBase parses a .base file containing structured YAML into a Document representation.
func ParseBase(path string, content []byte) (*Document, error) {
	var rawData map[string]any
	if err := yaml.Unmarshal(content, &rawData); err != nil {
		return nil, fmt.Errorf("unmarshal base: %w", err)
	}

	// Flatten the nested map/slice structure
	flatMap := make(map[string]string)
	flattenYAML("", rawData, flatMap)

	// Sort keys for deterministic output
	var keys []string
	for k := range flatMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var bodyBuilder strings.Builder
	for _, k := range keys {
		bodyBuilder.WriteString(fmt.Sprintf("# %s\n%s\n\n", k, flatMap[k]))
	}

	title := filepath.Base(path)
	title = strings.TrimSuffix(title, filepath.Ext(title))

	modTime := time.Now().UTC().Format(time.RFC3339)
	if info, err := os.Stat(path); err == nil {
		modTime = info.ModTime().UTC().Format(time.RFC3339)
	}

	doc := &Document{
		Path:        path,
		Title:       title,
		Type:        "base",
		Status:      "complete",
		CreatedAt:   modTime,
		ModifiedAt:  modTime,
		Body:        bodyBuilder.String(),
		Frontmatter: rawData,
	}

	return doc, nil
}

func flattenYAML(prefix string, value any, target map[string]string) {
	if value == nil {
		target[prefix] = "null"
		return
	}

	switch val := value.(type) {
	case map[string]any:
		for k, v := range val {
			newPrefix := k
			if prefix != "" {
				newPrefix = prefix + "." + k
			}
			flattenYAML(newPrefix, v, target)
		}
	case map[any]any:
		for k, v := range val {
			newPrefix := fmt.Sprintf("%v", k)
			if prefix != "" {
				newPrefix = prefix + "." + newPrefix
			}
			flattenYAML(newPrefix, v, target)
		}
	case []any:
		if len(val) == 0 {
			target[prefix] = "[]"
			return
		}
		for i, v := range val {
			newPrefix := fmt.Sprintf("%s.%d", prefix, i)
			flattenYAML(newPrefix, v, target)
		}
	default:
		target[prefix] = fmt.Sprintf("%v", val)
	}
}
