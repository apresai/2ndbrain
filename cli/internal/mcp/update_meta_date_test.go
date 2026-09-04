package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// kb_update_meta passes the raw JSON value straight to SetMeta, so an agent
// writing `modified` would requote a date node kb_create had just written
// plain, and the note would revert to Obsidian's Text property type. Both write
// surfaces route through vault.SchemaSet.CoerceDate for exactly that reason:
// the CLI and the MCP server cannot be allowed to disagree about what a date
// is.
//
// Asserted on the bytes on disk, because the quoting IS the property type.
func TestUsageMCP_UpdateMetaKeepsADateUnquoted(t *testing.T) {
	h, v := makeHandlers(t)
	ctx := context.Background()

	path, _ := createNoteViaMCP(t, h, ctx, "Agent Dated Note")
	abs := filepath.Join(v.Root, path)

	before, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"created", "modified"} {
		if strings.Contains(string(before), key+`: "`) {
			t.Fatalf("kb_create wrote a quoted %s, which Obsidian types as Text:\n%s", key, before)
		}
	}

	for _, value := range []string{"2026-09-04T12:34:56Z", "2026-09-04T12:34:56", "2026-09-04"} {
		res, err := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{
			"path":   path,
			"fields": map[string]any{"modified": value},
		}))
		callOK(t, res, err)

		after, err := os.ReadFile(abs)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(after), `modified: "`) {
			t.Errorf("kb_update_meta modified=%s requoted the date, reverting it to Text:\n%s", value, after)
		}
		// Reparse: whatever spelling went in, the note must read back as the
		// instant it names.
		doc, perr := document.ParseFile(abs)
		if perr != nil {
			t.Fatalf("reparse after modified=%s: %v", value, perr)
		}
		if doc.ModifiedAt != "2026-09-04T12:34:56Z" && doc.ModifiedAt != "2026-09-04T00:00:00Z" {
			t.Errorf("kb_update_meta modified=%s reparsed as %q, want the same instant", value, doc.ModifiedAt)
		}
	}

	// A value that is not a date is still written verbatim: the coercion is
	// additive and must not start refusing an agent's writes.
	res, err := h.handleKBUpdateMeta(ctx, makeRequest(map[string]any{
		"path":   path,
		"fields": map[string]any{"modified": "sometime soon"},
	}))
	callOK(t, res, err)
	after, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "modified: sometime soon\n") {
		t.Errorf("a non-date value was not stored verbatim:\n%s", after)
	}
}
