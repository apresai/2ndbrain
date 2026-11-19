package polish

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

func writeRawVaultNote(t *testing.T, root, relPath, content string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
	return abs
}

func readRawVaultFile(t *testing.T, abs string) string {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	return string(b)
}

func TestFixVault_AllSixCategoriesAndAmbiguousSkip(t *testing.T) {
	v := testutil.NewTestVault(t)

	// Target notes (including unindexed on disk and relocated folder notes).
	writeRawVaultNote(t, v.Root, "AI/Claude Code/Claude Code CLI Setup.md",
		"---\ntitle: Claude Code CLI Setup\ntype: note\nstatus: draft\n---\n\n# Claude Code CLI Setup\n\nCLI setup.\n")
	writeRawVaultNote(t, v.Root, "Guides/target-guide.md",
		"---\ntitle: Target Guide\ntype: note\nstatus: active\n---\n\n# Target Guide\n\nGuide body.\n")
	writeRawVaultNote(t, v.Root, "Notes/Drift Topic.md",
		"---\ntitle: Drift Topic\ntype: note\nstatus: reference\n---\n\n# Drift Topic\n\nDrift body.\n")
	writeRawVaultNote(t, v.Root, "AI/How to Build Multi-Agent Systems Using ADK.md",
		"---\ntitle: How to Build Multi-Agent Systems Using ADK\ntype: note\nstatus: published\n---\n\n# ADK Multi-Agent Systems\n\nDetailed architecture.\n")

	// Ambiguous target pair (same basename in two folders).
	writeRawVaultNote(t, v.Root, "one/dup.md", "---\ntitle: Dup One\ntype: note\nstatus: draft\n---\n\n# Dup One\n\nOne.\n")
	writeRawVaultNote(t, v.Root, "two/dup.md", "---\ntitle: Dup Two\ntype: note\nstatus: draft\n---\n\n# Dup Two\n\nTwo.\n")

	// Note with:
	// - custom frontmatter comment + unknown key + quoted date + missing title
	// - format issues: [[Guides\\target-guide.md]], [[Drift Topic|]], [Topic](Notes/Drift Topic.md)
	// - moved path: [[archive/old/target-guide]]
	// - case drift: [[drift topic]]
	// - obvious prefix match: [[claude-code]]
	// - dead links: [[ghost-note]], [[page|the page]], [missing](no-such-page.md)
	// - ambiguous link: [[dup]]
	// - fenced & inline code links that MUST remain untouched
	sourceOrig := "---\n# Preserve user comment\nowner_note: custom-value\ncreated: \"2026-04-20T10:00:00Z\"\ntype: runbook\n---\n\n# Incident Handover\n\n" +
		"Format: [[Guides\\target-guide.md]], [[Drift Topic|]], and [Topic](Notes/Drift Topic.md).\n" +
		"Moved: [[archive/old/target-guide]] and drift: [[drift topic]].\n" +
		"Obvious: [[claude-code]].\n" +
		"Dead: [[ghost-note]], [[page|the page]], and [missing](no-such-page.md).\n" +
		"Ambiguous: [[dup]].\n" +
		"Code fence and inline: `[[ghost-note]]` and `[x](no-such-page.md)`.\n" +
		"```markdown\n[[archive/old/target-guide]] and [[ghost-note]]\n```\n" +
		"External: [Example](https://example.com/no-such-page) and ![[diagram.png]].\n"
	sourceAbs := writeRawVaultNote(t, v.Root, "Runbooks/incident-handover.md", sourceOrig)

	// Negative test note for adk-agent low overlap vs 9-word note.
	adkNoteOrig := "---\ntitle: Agent Links\ntype: note\nstatus: archived\n---\n\n# Agent Links\n\nSee [[adk-agent]].\n"
	adkAbs := writeRawVaultNote(t, v.Root, "Agent Links.md", adkNoteOrig)

	schemaAbs := filepath.Join(v.Root, ".2ndbrain", "schemas.yaml")
	origSchema := readRawVaultFile(t, schemaAbs)

	// Preview mode (write=false) must NOT modify files or create recovery snapshot.
	preview, err := FixVault(v, false)
	if err != nil {
		t.Fatalf("FixVault(preview): %v", err)
	}
	if preview.Written {
		t.Errorf("preview.Written = true, want false")
	}
	if !preview.SchemaUpdated {
		t.Errorf("preview.SchemaUpdated = false, want true")
	}
	if preview.TotalFixes == 0 || preview.NotesChanged == 0 {
		t.Errorf("expected non-zero preview fixes and notes changed: %+v", preview)
	}
	if len(preview.Ambiguous) != 1 || preview.Ambiguous[0].Target != "dup" {
		t.Errorf("expected ambiguous [dup], got %+v", preview.Ambiguous)
	}
	if got := readRawVaultFile(t, sourceAbs); got != sourceOrig {
		t.Fatalf("preview mutated %s on disk", sourceAbs)
	}
	if got := readRawVaultFile(t, schemaAbs); got != origSchema {
		t.Fatalf("preview mutated schemas.yaml on disk")
	}
	if HasFixUndo(v) {
		t.Fatalf("HasFixUndo(v) should be false before write")
	}

	// Write mode applies deterministically and records snapshot.
	applied, err := FixVault(v, true)
	if err != nil {
		t.Fatalf("FixVault(write): %v", err)
	}
	if !applied.Written || !applied.UndoAvailable || !HasFixUndo(v) {
		t.Fatalf("expected Written=true and UndoAvailable=true: %+v", applied)
	}
	if !applied.SchemaUpdated || applied.SchemaIssuesResolved < 3 {
		t.Errorf("expected SchemaUpdated with resolved issues, got %+v", applied)
	}
	if applied.LinksUpdated < 3 {
		t.Errorf("expected at least 3 relinked actions (moved, drift, obvious), got %+v", applied)
	}
	if applied.LinksUnlinked < 3 {
		t.Errorf("expected at least 3 unlinked dead links (including adk-agent or dead links), got %+v", applied)
	}
	if applied.FormattingFixed < 3 {
		t.Errorf("expected at least 3 formatting fixes, got %+v", applied)
	}

	// Verify schemas.yaml widened note status enum while preserving rest of file.
	reloadedSchemas, err := vault.LoadSchemas(filepath.Join(v.Root, ".2ndbrain"))
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	noteEnum := reloadedSchemas.Types["note"].Fields["status"].Enum
	for _, want := range []string{"draft", "complete", "active", "reference", "archived", "published"} {
		found := false
		for _, got := range noteEnum {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("note.fields.status.enum missing %q: %v", want, noteEnum)
		}
	}

	// Verify source note transformations & preservations.
	updatedSource := readRawVaultFile(t, sourceAbs)
	if !strings.Contains(updatedSource, "# Preserve user comment") || !strings.Contains(updatedSource, "owner_note: custom-value") {
		t.Errorf("frontmatter comment or unknown field was stripped:\n%s", updatedSource)
	}
	if !strings.Contains(updatedSource, "title: Incident Handover") {
		t.Errorf("missing required title was not populated:\n%s", updatedSource)
	}
	if !strings.Contains(updatedSource, "status: draft") {
		t.Errorf("missing required status was not populated:\n%s", updatedSource)
	}
	if !strings.Contains(updatedSource, "created: 2026-04-20T10:00:00Z\n") {
		t.Errorf("quoted date was not unquoted:\n%s", updatedSource)
	}
	for _, want := range []string{
		"[[Guides/target-guide]]",
		"[[Drift Topic]]",
		"[Topic](Notes/Drift%20Topic.md)",
		"Moved: [[Target Guide]] and drift: [[Drift Topic]].",
		"Obvious: [[Claude Code CLI Setup]].",
		"Dead: ghost-note, the page, and missing.",
		"Ambiguous: [[dup]].",
		"Code fence and inline: `[[ghost-note]]` and `[x](no-such-page.md)`.",
		"```markdown\n[[archive/old/target-guide]] and [[ghost-note]]\n```",
		"External: [Example](https://example.com/no-such-page) and ![[diagram.png]].",
	} {
		if !strings.Contains(updatedSource, want) {
			t.Errorf("updated source missing %q:\n%s", want, updatedSource)
		}
	}

	// Verify negative case: adk-agent must NOT be relinked to 9-word ADK note.
	updatedADK := readRawVaultFile(t, adkAbs)
	if strings.Contains(updatedADK, "How to Build Multi-Agent Systems Using ADK") {
		t.Errorf("adk-agent must NEVER auto-relink to low-overlap 9-word note:\n%s", updatedADK)
	}

	// Verify document still parses cleanly after surgical edits.
	if _, perr := document.Parse(sourceAbs, []byte(updatedSource)); perr != nil {
		t.Fatalf("document.Parse(updatedSource): %v", perr)
	}

	// Undo restores every note AND schemas.yaml byte-for-byte.
	undoRes, err := UndoLastFix(v, false)
	if err != nil {
		t.Fatalf("UndoLastFix: %v", err)
	}
	if !undoRes.Reverted || !undoRes.SchemaRestored || undoRes.FilesRestored != 2 {
		t.Fatalf("unexpected undo result: %+v", undoRes)
	}
	if HasFixUndo(v) {
		t.Errorf("HasFixUndo should be false after undo")
	}
	if got := readRawVaultFile(t, sourceAbs); got != sourceOrig {
		t.Errorf("source not restored byte-for-byte:\ngot:\n%s\nwant:\n%s", got, sourceOrig)
	}
	if got := readRawVaultFile(t, adkAbs); got != adkNoteOrig {
		t.Errorf("adk note not restored byte-for-byte:\ngot:\n%s\nwant:\n%s", got, adkNoteOrig)
	}
	if got := readRawVaultFile(t, schemaAbs); got != origSchema {
		t.Errorf("schemas.yaml not restored byte-for-byte:\ngot:\n%s\nwant:\n%s", got, origSchema)
	}
}

func TestFixVault_UndoConflictRefusalAndForce(t *testing.T) {
	v := testutil.NewTestVault(t)
	orig := "---\ntitle: Clean Target\ntype: note\nstatus: draft\n---\n\n# Clean Target\n\nSee [[nonexistent-link]].\n"
	abs := writeRawVaultNote(t, v.Root, "Clean Target.md", orig)

	res, err := FixVault(v, true)
	if err != nil {
		t.Fatalf("FixVault: %v", err)
	}
	if !res.Written || res.LinksUnlinked != 1 {
		t.Fatalf("unexpected FixVault result: %+v", res)
	}

	// Simulate an external edit after `FixVault`.
	externallyEdited := readRawVaultFile(t, abs) + "\nExternal user addition.\n"
	if err := os.WriteFile(abs, []byte(externallyEdited), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := UndoLastFix(v, false); err == nil {
		t.Fatalf("expected UndoLastFix without --force to refuse externally modified file")
	}
	if got := readRawVaultFile(t, abs); got != externallyEdited {
		t.Fatalf("refused undo mutated file on disk")
	}
	if !HasFixUndo(v) {
		t.Fatalf("snapshot must remain intact after conflict refusal")
	}

	forced, err := UndoLastFix(v, true)
	if err != nil {
		t.Fatalf("UndoLastFix(force=true): %v", err)
	}
	if !forced.Reverted || forced.FilesRestored != 1 {
		t.Fatalf("unexpected forced undo result: %+v", forced)
	}
	if got := readRawVaultFile(t, abs); got != orig {
		t.Errorf("forced undo did not restore original content:\ngot:\n%s\nwant:\n%s", got, orig)
	}
}
