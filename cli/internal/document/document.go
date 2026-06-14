package document

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
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
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".canvas") {
		return ParseCanvas(path, content)
	}
	if strings.HasSuffix(lower, ".base") {
		return ParseBase(path, content)
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

// IsReadOnlyType reports whether a document type is a synthetic, read-only view
// produced by parsing a non-Markdown Obsidian file (.canvas JSON, .base YAML).
// These must never be written back: Serialize would emit the synthesized
// markdown body over the original JSON/YAML and corrupt the file.
func IsReadOnlyType(docType string) bool {
	return docType == "canvas" || docType == "base"
}

func (d *Document) Serialize() ([]byte, error) {
	// Defense-in-depth: refuse to serialize a synthetic .canvas/.base view.
	// Callers (meta, kb_update_meta) guard earlier with a clearer message;
	// this catches any future write path before it can corrupt the file.
	if IsReadOnlyType(d.Type) {
		return nil, fmt.Errorf("refusing to write read-only %s document %q (.canvas/.base files are indexed read-only)", d.Type, d.Path)
	}

	// Clone frontmatter to avoid mutating the receiver as a side effect.
	fm := make(map[string]any, len(d.Frontmatter))
	for k, v := range d.Frontmatter {
		fm[k] = v
	}

	// Try to update existing file frontmatter surgically to preserve comments
	// and layout. The body comes from d.Body (in-memory), not the freshly
	// re-read disk body, so a caller that edited the body has its changes
	// persisted rather than silently discarded. For meta-only edits d.Body
	// equals the on-disk body (both came from the same ParseFile), so this is
	// a no-op for the common path.
	if d.Path != "" {
		if content, err := os.ReadFile(d.Path); err == nil {
			if _, _, perr := ParseFrontmatter(content); perr == nil {
				updated, err := UpdateDocumentFrontmatterAST(content, fm, d.Body)
				if err == nil {
					return updated, nil
				}
			}
		}
	}

	return SerializeDocument(fm, d.Body)
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

	// An explicit d.Path (set by the caller, e.g. daily notes or an in-place
	// rewrite) is honored verbatim and may overwrite. A fresh create (empty
	// d.Path) derives a collision-free filename from the title slug so two
	// same-titled creates in one directory don't silently clobber each other.
	path := d.Path
	if path == "" {
		path = uniqueFilename(dir, slugify(d.Title), d.ID)
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

// SlugFilename returns the canonical "<slug>.md" filename a fresh create would
// use for a title, or "" when the title produces no ASCII slug (e.g. all CJK or
// emoji, which fall back to a UUID name that can't be located by title). Used
// by `create --append`/`--overwrite` to find an existing note for a title.
func SlugFilename(title string) string {
	slug := slugify(title)
	if slug == "" {
		return ""
	}
	return slug + ".md"
}

// uniqueFilename returns an absolute, collision-free ".md" path in dir for a new
// document. It prefers the title slug; if a file with that slug already exists
// it appends "-1", "-2", ... until the name is free. An empty slug (a title that
// produces no ASCII slug, e.g. all CJK or emoji) falls back to the document's
// UUID so the file always has a stable, non-empty name. Used only for fresh
// creates (an empty d.Path); explicit paths are never deduplicated.
func uniqueFilename(dir, slug, id string) string {
	if slug == "" {
		slug = id
	}
	candidate := filepath.Join(dir, slug+".md")
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	for n := 1; ; n++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%d.md", slug, n))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// AppendToBody appends content to the end of a document body, separating it
// from existing content with a single newline. If either side is empty the
// other is returned alone, so neither a fresh/empty note (`"" + "\n" + content`)
// nor an empty append (`body + "\n" + ""`) picks up a spurious blank line.
// Shared by the `append` CLI command and the `kb_append` MCP tool so both
// behave identically.
func AppendToBody(body, content string) string {
	if body == "" {
		return content
	}
	if content == "" {
		return body
	}
	return body + "\n" + content
}

// PrependToBody inserts content at the start of a document body, separating it
// from existing content with a single newline. If either side is empty the
// other is returned alone (no stray blank line). Counterpart to AppendToBody,
// shared by the `prepend` CLI command.
func PrependToBody(body, content string) string {
	if body == "" {
		return content
	}
	if content == "" {
		return body
	}
	return content + "\n" + body
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
	case string:
		// `tags: foo` in YAML parses as a bare string; treat it as one tag.
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return nil
	}
}

func ExtractAliases(meta map[string]any) []string {
	if meta == nil {
		return nil
	}
	raw, ok := meta["aliases"]
	if !ok {
		return nil
	}

	switch v := raw.(type) {
	case []any:
		aliases := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				aliases = append(aliases, s)
			}
		}
		return aliases
	case []string:
		return v
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return nil
	}
}

func slugify(s string) string {
	// Unicode decomposition + strip combining marks: "Café" → "Cafe",
	// "naïve" → "naive", "résumé" → "resume". CJK/emoji don't decompose
	// into ASCII, so they fall through to the rune loop and are dropped —
	// callers get an empty slug and the UUID fallback takes over (see
	// uniqueFilename).
	if decomposed, _, err := transform.String(
		transform.Chain(
			norm.NFD,
			runes.Remove(runes.In(unicode.Mn)),
			norm.NFC,
		),
		s,
	); err == nil {
		s = decomposed
	}

	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			result = append(result, c)
		case c >= 'A' && c <= 'Z':
			result = append(result, c+32)
		case c == ' ' || c == '-' || c == '_':
			if len(result) > 0 && result[len(result)-1] != '-' {
				result = append(result, '-')
			}
		}
	}
	return string(result)
}
