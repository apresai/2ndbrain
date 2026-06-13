package store

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// seedCatalogVault inserts a few documents (with tags and aliases) spread
// across folders so the catalog queries have something to group over.
func seedCatalogVault(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)

	docs := []*document.Document{
		{ID: "d1", Path: "engineering/architecture.md", Title: "System Architecture", Type: "note", Status: "draft", Tags: []string{"design", "infra"}},
		{ID: "d2", Path: "engineering/runbook.md", Title: "Deploy Runbook", Type: "runbook", Status: "active", Tags: []string{"infra"}},
		{ID: "d3", Path: "marketing/launch.md", Title: "Launch Plan", Type: "note", Status: "draft", Tags: []string{"design"}},
		{ID: "d4", Path: "readme.md", Title: "Readme", Type: "note", Status: "draft", Tags: nil},
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

	// Aliases: only d1 and d4 declare them.
	tx, err := db.conn.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := db.UpsertAliasesTx(tx, "d1", []string{"arch", "design-doc"}); err != nil {
		t.Fatalf("upsert aliases d1: %v", err)
	}
	if err := db.UpsertAliasesTx(tx, "d4", []string{"home"}); err != nil {
		t.Fatalf("upsert aliases d4: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	return db
}

func TestTagCounts_GroupsByTag(t *testing.T) {
	db := seedCatalogVault(t)

	got, err := db.TagCounts()
	if err != nil {
		t.Fatalf("TagCounts: %v", err)
	}

	// design appears on d1 + d3 (2), infra on d1 + d2 (2). Expect both with
	// count 2; ordering is count desc then tag asc, so "design" precedes "infra".
	want := map[string]int{"design": 2, "infra": 2}
	if len(got) != len(want) {
		t.Fatalf("TagCounts returned %d tags, want %d: %+v", len(got), len(want), got)
	}
	for _, tc := range got {
		if w, ok := want[tc.Tag]; !ok {
			t.Errorf("unexpected tag %q", tc.Tag)
		} else if tc.Count != w {
			t.Errorf("tag %q count = %d, want %d", tc.Tag, tc.Count, w)
		}
	}
	// Ordering: tag asc tie-break with equal counts.
	if got[0].Tag != "design" || got[1].Tag != "infra" {
		t.Errorf("tag ordering = [%s, %s], want [design, infra]", got[0].Tag, got[1].Tag)
	}
}

func TestAllAliases_ReturnsAliasToPath(t *testing.T) {
	db := seedCatalogVault(t)

	got, err := db.AllAliases()
	if err != nil {
		t.Fatalf("AllAliases: %v", err)
	}

	// d1 -> arch, design-doc ; d4 -> home. Three rows total.
	if len(got) != 3 {
		t.Fatalf("AllAliases returned %d, want 3: %+v", len(got), got)
	}

	byAlias := map[string]AliasRef{}
	for _, a := range got {
		byAlias[a.Alias] = a
	}
	for alias, wantPath := range map[string]string{
		"arch":       "engineering/architecture.md",
		"design-doc": "engineering/architecture.md",
		"home":       "readme.md",
	} {
		ar, ok := byAlias[alias]
		if !ok {
			t.Errorf("missing alias %q", alias)
			continue
		}
		if ar.Path != wantPath {
			t.Errorf("alias %q -> path %q, want %q", alias, ar.Path, wantPath)
		}
	}

	// Ordering is alias asc: arch, design-doc, home.
	if got[0].Alias != "arch" || got[1].Alias != "design-doc" || got[2].Alias != "home" {
		t.Errorf("alias ordering = %v, want [arch, design-doc, home]", []string{got[0].Alias, got[1].Alias, got[2].Alias})
	}
}

func TestFolderCounts_BucketsByDir(t *testing.T) {
	db := seedCatalogVault(t)

	got, err := db.FolderCounts()
	if err != nil {
		t.Fatalf("FolderCounts: %v", err)
	}

	// engineering: d1 + d2 (2); marketing: d3 (1); root: d4 (1, under "(root)").
	want := map[string]int{"engineering": 2, "marketing": 1, rootFolderLabel: 1}
	if len(got) != len(want) {
		t.Fatalf("FolderCounts returned %d folders, want %d: %+v", len(got), len(want), got)
	}
	for _, fc := range got {
		w, ok := want[fc.Folder]
		if !ok {
			t.Errorf("unexpected folder %q", fc.Folder)
			continue
		}
		if fc.Count != w {
			t.Errorf("folder %q count = %d, want %d", fc.Folder, fc.Count, w)
		}
	}

	// Ordering is folder name asc: "(root)" sorts before letters.
	if got[0].Folder != rootFolderLabel {
		t.Errorf("first folder = %q, want %q (sorts first)", got[0].Folder, rootFolderLabel)
	}
}
