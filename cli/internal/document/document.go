package document

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Document struct {
	Path        string         `json:"path"`
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Type        string         `json:"type"`
	Status      string         `json:"status"`
	Tags        []string       `json:"tags"`
	CreatedAt   string         `json:"created_at"`
	ModifiedAt  string         `json:"modified_at"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body,omitempty"`
	ContentHash string         `json:"content_hash,omitempty"`
}

// ComputeContentHash sets ContentHash to the SHA-256 of the normalized body.
// Excludes frontmatter so metadata-only changes (tags, status) don't trigger re-embedding.
// Normalizes whitespace/line endings to prevent editor artifacts from causing false changes.
func (d *Document) ComputeContentHash() {
	d.ContentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(normalizeBody(d.Body))))
}

// normalizeBody produces a canonical form for hashing:
// - CRLF → LF
// - strip trailing whitespace per line
// - strip leading/trailing blank lines
// - collapse 3+ consecutive blank lines to 2
func normalizeBody(body string) string {
	// CRLF → LF
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	// Strip trailing whitespace per line and collapse excessive blank lines
	lines := strings.Split(body, "\n")
	var out []string
	blankRun := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			blankRun++
			if blankRun <= 2 {
				out = append(out, "")
			}
		} else {
			blankRun = 0
			out = append(out, trimmed)
		}
	}

	result := strings.Join(out, "\n")
	result = strings.TrimSpace(result)
	return result
}

func Parse(path string, content []byte) (*Document, error) {
	meta, body, err := ParseFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	doc := &Document{
		Path:        path,
		Frontmatter: meta,
		Body:        body,
	}

	if meta != nil {
		if id, ok := meta["id"].(string); ok {
			doc.ID = id
		}
		if title, ok := meta["title"].(string); ok {
			doc.Title = title
		}
		if typ, ok := meta["type"].(string); ok {
			doc.Type = typ
		}
		if status, ok := meta["status"].(string); ok {
			doc.Status = status
		}
		if created, ok := meta["created"].(string); ok {
			doc.CreatedAt = created
		}
		if modified, ok := meta["modified"].(string); ok {
			doc.ModifiedAt = modified
		}
		doc.Tags = extractTags(meta)
	}

	return doc, nil
}

func ParseFile(path string) (*Document, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(path, content)
}

func NewDocument(title, docType, templateBody string) *Document {
	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()

	meta := map[string]any{
		"id":       id,
		"title":    title,
		"type":     docType,
		"status":   "draft",
		"tags":     []any{},
		"created":  now,
		"modified": now,
	}

	return &Document{
		ID:          id,
		Title:       title,
		Type:        docType,
		Status:      "draft",
		Tags:        []string{},
		CreatedAt:   now,
		ModifiedAt:  now,
		Frontmatter: meta,
		Body:        templateBody,
	}
}

func (d *Document) Serialize() ([]byte, error) {
	d.Frontmatter["modified"] = time.Now().UTC().Format(time.RFC3339)
	return SerializeDocument(d.Frontmatter, d.Body)
}

func (d *Document) SetMeta(key string, value any) {
	if d.Frontmatter == nil {
		d.Frontmatter = make(map[string]any)
	}
	d.Frontmatter[key] = value

	// Keep struct fields in sync
	switch key {
	case "title":
		if s, ok := value.(string); ok {
			d.Title = s
		}
	case "type":
		if s, ok := value.(string); ok {
			d.Type = s
		}
	case "status":
		if s, ok := value.(string); ok {
			d.Status = s
		}
	case "tags":
		d.Tags = extractTags(d.Frontmatter)
	}
}

func (d *Document) WriteFile(dir string) (string, error) {
	content, err := d.Serialize()
	if err != nil {
		return "", err
	}

	path := d.Path
	if path == "" {
		path = filepath.Join(dir, slugify(d.Title)+".md")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename temp file: %w", err)
	}

	d.Path = path
	return path, nil
}

func extractTags(meta map[string]any) []string {
	raw, ok := meta["tags"]
	if !ok {
		return nil
	}

	switch v := raw.(type) {
	case []any:
		tags := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				tags = append(tags, s)
			}
		}
		return tags
	case []string:
		return v
	default:
		return nil
	}
}

func slugify(s string) string {
	result := make([]byte, 0, len(s))
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			result = append(result, c)
		case c >= 'A' && c <= 'Z':
			result = append(result, c+32) // lowercase
		case c == ' ' || c == '-' || c == '_':
			if len(result) > 0 && result[len(result)-1] != '-' {
				result = append(result, '-')
			}
		}
	}
	return string(result)
}
