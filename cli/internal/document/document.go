package document

import (
	"crypto/sha256"
	"errors"
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

	// raw is the VERBATIM text of each top-level frontmatter key, kept because
	// resolution is lossy for a text field: `2026-09-04` and
	// `2026-09-04T00:00:00Z` decode to the same time.Time, so a title or a tag
	// can only be reproduced from the node. Unexported, so it never reaches
	// JSON; nil for a document not built by Parse, where every reader falls
	// back to ScalarText. See frontmatter_scalar.go.
	raw rawFrontmatter
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
	meta, raw, body, err := parseFrontmatterFull(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	doc := &Document{
		Path:        path,
		Frontmatter: meta,
		Body:        body,
		raw:         raw,
	}

	// Every field below goes through a helper rather than a bare
	// meta[key].(string), because an UNQUOTED YAML scalar is not a Go string:
	// yaml.v3 resolves it to time.Time, bool, int or float64, and the assertion
	// silently failed for all of them. See frontmatter_scalar.go.
	//
	// The parsed map is deliberately NOT normalized in place. Serialize
	// re-marshals every key of Frontmatter through
	// UpdateDocumentFrontmatterAST, so writing a string back over a time.Time
	// would make an unrelated `meta --set`, `tag add` or `polish --write`
	// requote a date line the user never touched. The struct fields the index
	// reads are normalized; the note's bytes are left alone.
	if meta != nil {
		if id, ok := frontmatterText(meta, raw, "id"); ok {
			doc.ID = id
		}
		if title, ok := frontmatterText(meta, raw, "title"); ok {
			doc.Title = title
		}
		if typ, ok := frontmatterText(meta, raw, "type"); ok {
			doc.Type = typ
		}
		if status, ok := frontmatterText(meta, raw, "status"); ok {
			doc.Status = status
		}
		if created, ok := frontmatterTime(meta, "created"); ok {
			doc.CreatedAt = created
		}
		if modified, ok := frontmatterTime(meta, "modified"); ok {
			doc.ModifiedAt = modified
		}
		doc.Tags = extractTags(meta, raw)
	}

	return doc, nil
}

// ErrRead marks a failure to READ a file, as distinct from a failure to parse
// what was read. The two must never be treated alike: a note that will not parse
// is the note's own problem, reported and skipped, while a note that cannot be
// read right now (a permission bit, a file locked mid-save, any transient I/O
// error) says NOTHING about its contents and must never cost it its index row.
// Callers classify with errors.Is(err, document.ErrRead).
var ErrRead = errors.New("read")

func ParseFile(path string) (*Document, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", ErrRead, path, err)
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
	// The verbatim text of this key is now stale: it describes what the FILE
	// said, and the caller just replaced it. Drop it so every reader falls back
	// to the value actually set.
	delete(d.raw, key)

	// Keep struct fields in sync. EVERY field this struct mirrors from
	// frontmatter is here: the index reads these, not the map, so one that is
	// missing leaves a stale value flowing into the database and the chunk
	// tables. `id` was missing outright, and title/type/status used a strict
	// value.(string), so setting any of them to a non-string scalar left the
	// field unpopulated. The struct's frontmatter-mirroring fields are ID,
	// Title, Type, Status, Tags, CreatedAt and ModifiedAt; adding a new one
	// means adding a case here.
	switch key {
	case "id":
		if s, ok := setMetaText(value); ok {
			d.ID = s
		}
	case "title":
		if s, ok := setMetaText(value); ok {
			d.Title = s
		}
	case "type":
		if s, ok := setMetaText(value); ok {
			d.Type = s
		}
	case "status":
		if s, ok := setMetaText(value); ok {
			d.Status = s
		}
	case "tags":
		d.Tags = extractTags(d.Frontmatter, d.raw)
	case "created":
		// The DATE fields normalize, because the index compares them as
		// instants: a value set here lands in the column in the same form a
		// parse would have produced.
		if s, ok := frontmatterTime(d.Frontmatter, "created"); ok {
			d.CreatedAt = s
		}
	case "modified":
		if s, ok := frontmatterTime(d.Frontmatter, "modified"); ok {
			d.ModifiedAt = s
		}
	}
}

// setMetaText renders a value handed to SetMeta as the text a string-typed
// struct field wants. There is no node behind a programmatic write, so
// ScalarText is the right reading here, unlike on the parse path.
func setMetaText(value any) (string, bool) {
	return ScalarText(value)
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

func extractTags(meta map[string]any, raw rawFrontmatter) []string {
	return scalarList(meta, raw, "tags")
}

// scalarList reads a list-valued frontmatter field (tags, aliases) as the note
// WROTE it. Each element is its verbatim node text, so an unquoted entry YAML
// resolves to a date, a number or a boolean stays the text the file carries:
// `- 2026-09-04` is the tag `2026-09-04`, not `2026-09-04T00:00:00Z` and not,
// as before this, dropped from the note entirely. An element that is not a
// scalar (a nested list or mapping) is not a tag and is still dropped.
func scalarList(meta map[string]any, raw rawFrontmatter, key string) []string {
	value, ok := meta[key]
	if !ok {
		return nil
	}

	switch v := value.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			// The node's text wins; ScalarText on the resolved element is the
			// fallback where there is no node (a map built in code).
			if s, ok := raw.item(key, i); ok {
				out = append(out, s)
				continue
			}
			if s, ok := ScalarText(item); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		// `tags: foo` in YAML parses as a bare scalar; treat it as one entry.
		s, ok := raw.scalar(key)
		if !ok {
			s, ok = ScalarText(v)
		}
		if !ok || s == "" {
			return nil
		}
		return []string{s}
	}
}

// MetaText returns the VERBATIM text of a top-level frontmatter SCALAR, as the
// note wrote it, reporting false when the key is absent, is not a scalar, or
// has no node behind it (a document built in code). Use it wherever a field is
// SHOWN rather than compared: the resolved value cannot reproduce the note's
// text for a date (see frontmatter_scalar.go).
func (d *Document) MetaText(key string) (string, bool) {
	if d == nil {
		return "", false
	}
	return d.raw.scalar(key)
}

// ForgetMetaText drops the note's verbatim text for one key, for a caller that
// REMOVES a key from Frontmatter directly rather than through SetMeta (which
// invalidates it itself). Leaving it behind would let a stale reading shadow a
// later write of the same key on this document.
func (d *Document) ForgetMetaText(key string) {
	if d != nil {
		delete(d.raw, key)
	}
}

// MetaTextItem is MetaText for one element of a top-level frontmatter LIST,
// index-aligned with the decoded []any.
func (d *Document) MetaTextItem(key string, i int) (string, bool) {
	if d == nil {
		return "", false
	}
	return d.raw.item(key, i)
}

// TagsOf reads a document's frontmatter tags as the note wrote them. Prefer it
// over reading the map wherever a *Document is in hand: a caller that asserted
// item.(string) DROPPED every unquoted date, integer and boolean tag, and the
// tag commands then wrote that shortened list back to the file.
func TagsOf(d *Document) []string {
	if d == nil {
		return nil
	}
	return scalarList(d.Frontmatter, d.raw, "tags")
}

// AliasesOf reads a document's frontmatter aliases as the note wrote them.
// Prefer it over ExtractAliases wherever a *Document is in hand: it carries the
// verbatim node text, so an unquoted alias YAML resolves to a date keeps the
// text the file shows rather than a normalized timestamp.
func AliasesOf(d *Document) []string {
	if d == nil {
		return nil
	}
	return scalarList(d.Frontmatter, d.raw, "aliases")
}

// ExtractAliases is the map-only reading, for a caller with no Document (and so
// no node text). A resolved scalar renders through ScalarText.
func ExtractAliases(meta map[string]any) []string {
	if meta == nil {
		return nil
	}
	return scalarList(meta, nil, "aliases")
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
