package testutil

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/vault"
)

// NewTestVault creates an isolated vault in t.TempDir().
func NewTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	dir := t.TempDir()
	v, err := vault.Init(dir)
	if err != nil {
		t.Fatalf("init test vault: %v", err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

// CreateAndIndex creates a document with the given title, type, and body,
// writes it to disk, and indexes it (doc, chunks, tags, links).
func CreateAndIndex(t *testing.T, v *vault.Vault, title, docType, body string) *document.Document {
	t.Helper()

	initialStatus := "draft"
	if schema, ok := v.Schemas.Types[docType]; ok && schema.Status != nil {
		initialStatus = schema.Status.Initial
	}

	tmplBody := vault.GetTemplate(docType)
	tmplBody = strings.ReplaceAll(tmplBody, "{{.Title}}", title)
	tmplBody = strings.ReplaceAll(tmplBody, "{{.Status}}", initialStatus)
	if body != "" {
		tmplBody = body
	}

	doc := document.NewDocument(title, docType, tmplBody)
	doc.SetMeta("status", initialStatus)

	path, err := doc.WriteFile(v.Root)
	if err != nil {
		t.Fatalf("write document %q: %v", title, err)
	}
	doc.Path = v.RelPath(path)

	if err := v.DB.UpsertDocument(doc); err != nil {
		t.Fatalf("upsert document %q: %v", title, err)
	}

	chunks := document.ChunkDocument(doc)
	if err := v.DB.UpsertChunks(chunks); err != nil {
		t.Fatalf("upsert chunks %q: %v", title, err)
	}

	if err := v.DB.UpsertTags(doc.ID, doc.Tags); err != nil {
		t.Fatalf("upsert tags %q: %v", title, err)
	}

	links := document.ExtractWikiLinks(doc.Body)
	if err := v.DB.UpsertLinks(doc.ID, links); err != nil {
		t.Fatalf("upsert links %q: %v", title, err)
	}

	return doc
}
