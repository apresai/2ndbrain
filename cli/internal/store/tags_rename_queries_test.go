package store

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// seedTagRenameVault inserts documents carrying overlapping tags so DocsWithTag
// can be checked for the right membership and ordering.
func seedTagRenameVault(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)

	docs := []*document.Document{
		{ID: "d1", Path: "a/first.md", Title: "First", Type: "note", Status: "draft", Tags: []string{"draft", "infra"}},
		{ID: "d2", Path: "a/second.md", Title: "Second", Type: "note", Status: "draft", Tags: []string{"draft"}},
		{ID: "d3", Path: "b/third.md", Title: "Third", Type: "note", Status: "draft", Tags: []string{"infra"}},
		{ID: "d4", Path: "untagged.md", Title: "Untagged", Type: "note", Status: "draft", Tags: nil},
	}
	for _, d := range docs {
		if err := db.UpsertDocument(d); err != nil {
			t.Fatalf("upsert %s: %v", d.ID, err)
		}
		if len(d.Tags) > 0 {
			if err := db.UpsertTags(d.ID, d.Tags); err != nil {
				t.Fatalf("upsert tags %s: %v", d.ID, err)
			}
		}
	}
	return db
}

func TestDocsWithTag_ReturnsMatchingDocsOrderedByPath(t *testing.T) {
	db := seedTagRenameVault(t)

	got, err := db.DocsWithTag("draft")
	if err != nil {
		t.Fatalf("DocsWithTag: %v", err)
	}

	// d1 (a/first.md) and d2 (a/second.md) carry "draft". Ordered by path.
	if len(got) != 2 {
		t.Fatalf("DocsWithTag(draft) returned %d, want 2: %+v", len(got), got)
	}
	if got[0].Path != "a/first.md" || got[1].Path != "a/second.md" {
		t.Errorf("ordering = [%s, %s], want [a/first.md, a/second.md]", got[0].Path, got[1].Path)
	}
	if got[0].Title != "First" {
		t.Errorf("first title = %q, want %q", got[0].Title, "First")
	}
}

func TestDocsWithTag_NoMatchesReturnsEmpty(t *testing.T) {
	db := seedTagRenameVault(t)

	got, err := db.DocsWithTag("nonexistent")
	if err != nil {
		t.Fatalf("DocsWithTag: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("DocsWithTag(nonexistent) returned %d, want 0: %+v", len(got), got)
	}
}

func TestDocsWithTag_SharedTagAcrossFolders(t *testing.T) {
	db := seedTagRenameVault(t)

	got, err := db.DocsWithTag("infra")
	if err != nil {
		t.Fatalf("DocsWithTag: %v", err)
	}

	// d1 (a/first.md) and d3 (b/third.md) carry "infra", ordered by path.
	if len(got) != 2 {
		t.Fatalf("DocsWithTag(infra) returned %d, want 2: %+v", len(got), got)
	}
	if got[0].Path != "a/first.md" || got[1].Path != "b/third.md" {
		t.Errorf("ordering = [%s, %s], want [a/first.md, b/third.md]", got[0].Path, got[1].Path)
	}
}
