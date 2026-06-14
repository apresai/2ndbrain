package store

import (
	"errors"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
)

// seedResolveVault inserts documents (with aliases) that exercise every
// ResolveTarget tier plus the ambiguity cases.
func seedResolveVault(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)

	docs := []*document.Document{
		{ID: "d1", Path: "engineering/architecture.md", Title: "System Architecture", Type: "note", Status: "draft"},
		{ID: "d2", Path: "engineering/runbook.md", Title: "Deploy Runbook", Type: "runbook", Status: "active"},
		{ID: "d3", Path: "readme.md", Title: "Readme", Type: "note", Status: "draft"},
		// Two docs share the basename "dup.md" -> ambiguous by basename.
		{ID: "d4", Path: "projects/dup.md", Title: "Dup A", Type: "note", Status: "draft"},
		{ID: "d5", Path: "areas/dup.md", Title: "Dup B", Type: "note", Status: "draft"},
		// Two docs share the title "Shared Title" -> ambiguous by title.
		{ID: "d6", Path: "a/one.md", Title: "Shared Title", Type: "note", Status: "draft"},
		{ID: "d7", Path: "b/two.md", Title: "Shared Title", Type: "note", Status: "draft"},
	}
	for _, d := range docs {
		if err := db.UpsertDocument(d); err != nil {
			t.Fatalf("upsert %s: %v", d.ID, err)
		}
	}

	tx, err := db.conn.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := db.UpsertAliasesTx(tx, "d1", []string{"arch", "design-doc"}); err != nil {
		t.Fatalf("alias d1: %v", err)
	}
	if err := db.UpsertAliasesTx(tx, "d3", []string{"home"}); err != nil {
		t.Fatalf("alias d3: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return db
}

func TestResolveTarget_Tiers(t *testing.T) {
	db := seedResolveVault(t)

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"exact path", "engineering/architecture.md", "engineering/architecture.md"},
		{"exact path without .md", "engineering/architecture", "engineering/architecture.md"},
		{"leading slash stripped", "/engineering/runbook.md", "engineering/runbook.md"},
		{"basename", "runbook", "engineering/runbook.md"},
		{"basename with .md", "runbook.md", "engineering/runbook.md"},
		{"path suffix", "engineering/architecture", "engineering/architecture.md"},
		{"title", "System Architecture", "engineering/architecture.md"},
		{"alias", "arch", "engineering/architecture.md"},
		{"alias home", "home", "readme.md"},
		{"anchor stripped (heading)", "runbook#Steps", "engineering/runbook.md"},
		{"anchor stripped (block)", "runbook#^abc123", "engineering/runbook.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.ResolveTarget(tc.input)
			if err != nil {
				t.Fatalf("ResolveTarget(%q) error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ResolveTarget(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestResolveTarget_NotFound(t *testing.T) {
	db := seedResolveVault(t)
	_, err := db.ResolveTarget("does-not-exist")
	if !errors.Is(err, ErrTargetNotFound) {
		t.Fatalf("want ErrTargetNotFound, got %v", err)
	}
}

func TestResolveTarget_AmbiguousBasename(t *testing.T) {
	db := seedResolveVault(t)
	_, err := db.ResolveTarget("dup")
	var amb *AmbiguousTargetError
	if !errors.As(err, &amb) {
		t.Fatalf("want *AmbiguousTargetError, got %v", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("want 2 candidates, got %d (%v)", len(amb.Candidates), amb.Candidates)
	}
	// Candidates are sorted for determinism.
	if amb.Candidates[0] != "areas/dup.md" || amb.Candidates[1] != "projects/dup.md" {
		t.Errorf("unexpected candidates: %v", amb.Candidates)
	}
}

func TestResolveTarget_AmbiguousTitle(t *testing.T) {
	db := seedResolveVault(t)
	_, err := db.ResolveTarget("Shared Title")
	var amb *AmbiguousTargetError
	if !errors.As(err, &amb) {
		t.Fatalf("want *AmbiguousTargetError, got %v", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("want 2 candidates, got %d (%v)", len(amb.Candidates), amb.Candidates)
	}
}

// An exact path always wins, even when a basename for it would be ambiguous.
func TestResolveTarget_ExactPathBeatsAmbiguousBasename(t *testing.T) {
	db := seedResolveVault(t)
	got, err := db.ResolveTarget("projects/dup.md")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got != "projects/dup.md" {
		t.Errorf("got %q, want projects/dup.md", got)
	}
}
