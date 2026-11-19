package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/vault"
)

func TestPolishFlags_M2ConflictsAndGuards(t *testing.T) {
	_, root := newContractVault(t)

	notePath := filepath.Join(root, "doc.md")
	if err := os.WriteFile(notePath, []byte("---\ntitle: Doc\ntype: note\nstatus: complete\n---\n\nHello.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canvasPath := filepath.Join(root, "board.canvas")
	if err := os.WriteFile(canvasPath, []byte(`{"nodes":[],"edges":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"schema and no-schema conflict", []string{"polish", "doc.md", "--schema", "--no-schema"}},
		{"force without undo", []string{"polish", "doc.md", "--force"}},
		{"apply-backlinks without write", []string{"polish", "doc.md", "--apply-backlinks"}},
		{"undo with goal", []string{"polish", "doc.md", "--undo", "--goal", "shorter"}},
		{"undo with schema", []string{"polish", "doc.md", "--undo", "--schema"}},
		{"undo with backlinks", []string{"polish", "doc.md", "--undo", "--backlinks"}},
		{"undo with apply-backlinks", []string{"polish", "doc.md", "--undo", "--apply-backlinks"}},
		{"canvas with write", []string{"polish", "board.canvas", "--write"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := runCLIArgs(t, root, tc.args...); err == nil {
				t.Fatalf("expected validation error for %v, got nil", tc.args)
			}
		})
	}
}

func TestPolishBatchUndo_ConflictAndForce_Hermetic(t *testing.T) {
	v, root := newContractVault(t)

	origHub := "---\ntitle: Hub\ntype: note\nstatus: complete\n---\n\n# Hub\n\nOriginal hub.\n"
	origCaller := "---\ntitle: Caller\ntype: note\nstatus: complete\n---\n\n# Caller\n\nOriginal [[Hub]].\n"
	writtenHub := "---\ntitle: Hub\ntype: note\nstatus: complete\n---\n\n# Hub\n\nPolished hub.\n"
	writtenCaller := "---\ntitle: Caller\ntype: note\nstatus: complete\n---\n\n# Caller\n\nUpdated [[Hub]].\n"

	if err := os.WriteFile(filepath.Join(root, "hub.md"), []byte(writtenHub), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "caller.md"), []byte(writtenCaller), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := polish.PolishSnapshot{
		Path:          "hub.md",
		OriginalFull:  origHub,
		PolishedBody:  "# Hub\n\nPolished hub.",
		Provider:      "gemini",
		Model:         "gemini-4-hydrogen",
		Timestamp:     "2026-11-18T00:00:00Z",
		PostWriteHash: polish.HashContent([]byte(writtenHub)),
		BatchFiles: []polish.PolishBatchFile{
			{
				Path:          "caller.md",
				OriginalFull:  origCaller,
				PostWriteHash: polish.HashContent([]byte(writtenCaller)),
			},
		},
	}
	if err := polish.WriteSnapshot(v, snap); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}

	// Simulate external edit in caller.md after polish batch.
	externalCaller := writtenCaller + "\nUser edit.\n"
	if err := os.WriteFile(filepath.Join(root, "caller.md"), []byte(externalCaller), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-forced undo must refuse and touch neither hub.md nor caller.md.
	if _, err := runCLIArgs(t, root, "polish", "hub.md", "--undo", "--json", "--porcelain"); err == nil {
		t.Fatalf("expected batch undo conflict refusal")
	} else if !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("expected 'changed since' error, got: %v", err)
	}
	if curHub, _ := os.ReadFile(filepath.Join(root, "hub.md")); string(curHub) != writtenHub {
		t.Fatalf("refused batch undo must not restore hub.md")
	}
	if curCaller, _ := os.ReadFile(filepath.Join(root, "caller.md")); string(curCaller) != externalCaller {
		t.Fatalf("refused batch undo must preserve external edit in caller.md")
	}

	// Forced undo restores both hub.md and caller.md verbatim and reports restored_paths.
	out, err := runCLIArgs(t, root, "polish", "hub.md", "--undo", "--force", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish --undo --force: %v\n%s", err, out)
	}
	var undoRes PolishUndoResult
	if err := json.Unmarshal(out, &undoRes); err != nil {
		t.Fatalf("unmarshal undoRes: %v\n%s", err, out)
	}
	if !undoRes.Reverted || len(undoRes.RestoredPaths) != 2 {
		t.Fatalf("expected 2 restored_paths, got %+v", undoRes)
	}
	if curHub, _ := os.ReadFile(filepath.Join(root, "hub.md")); string(curHub) != origHub {
		t.Fatalf("hub.md not restored:\n%s", string(curHub))
	}
	if curCaller, _ := os.ReadFile(filepath.Join(root, "caller.md")); string(curCaller) != origCaller {
		t.Fatalf("caller.md not restored:\n%s", string(curCaller))
	}
}

func TestPolishM2_GoalSchemaBacklinksBatchUndo_LiveGemini(t *testing.T) {
	v, root := newContractVault(t)
	providers, skipReason := resolveFixAIProviders(context.Background(), v)
	if providers == nil || providers.Generator == nil {
		t.Skipf("live generation provider unavailable: %s", skipReason)
	}

	beacon := "---\ntitle: Project Beacon\ntype: note\nstatus: complete\n---\n\n# Project Beacon\n\nReference architecture.\n"
	if err := os.WriteFile(filepath.Join(root, "Project Beacon.md"), []byte(beacon), 0o644); err != nil {
		t.Fatal(err)
	}

	origHub := "---\r\n" +
		"title: Auth Hub\r\n" +
		"type: note # pending review\r\n" +
		"status: open\r\n" +
		"created: \"2026-04-20T10:00:00Z\"\r\n" +
		"owner: &lead \"@alice\"\r\n" +
		"reviewer: *lead\r\n" +
		"---\r\n\r\n" +
		"# Auth Hub\r\n\r\n" +
		"This note have bad grammer about [[project-beacon]] and `keep_token()`.\r\n\r\n" +
		"```go\r\nfunc keepHub() {}\r\n```\r\n"
	if err := os.WriteFile(filepath.Join(root, "hub.md"), []byte(origHub), 0o644); err != nil {
		t.Fatal(err)
	}

	origCaller := "---\ntitle: Caller\ntype: note\nstatus: complete\n---\n\n# Caller\n\nSee [[auth-hub|Hub Alias]].\n"
	if err := os.WriteFile(filepath.Join(root, "caller.md"), []byte(origCaller), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := vault.IndexVault(v, func(string) {}); err != nil {
		t.Fatalf("index: %v", err)
	}

	// 1. Preview with --goal --links --repair-links --schema --backlinks touches zero files.
	preOut, err := runCLIArgs(t, root, "polish", "hub.md",
		"--goal", "Fix grammar and tighten prose",
		"--links", "--repair-links", "--schema", "--backlinks",
		"--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish preview: %v\n%s", err, preOut)
	}
	var preRes PolishResult
	if err := json.Unmarshal(preOut, &preRes); err != nil {
		t.Fatalf("unmarshal preview: %v\n%s", err, preOut)
	}
	if !preRes.SchemaRepaired || len(preRes.FrontmatterFixes) == 0 {
		t.Fatalf("expected schema repair in preview, got %+v", preRes)
	}
	if len(preRes.BacklinkPreviews) == 0 {
		t.Fatalf("expected backlink previews, got %+v", preRes)
	}
	for _, bp := range preRes.BacklinkPreviews {
		if bp.Applied {
			t.Fatalf("preview must have applied=false: %+v", bp)
		}
	}
	if gotHub, _ := os.ReadFile(filepath.Join(root, "hub.md")); string(gotHub) != origHub {
		t.Fatalf("preview modified hub.md on disk")
	}
	if gotCaller, _ := os.ReadFile(filepath.Join(root, "caller.md")); string(gotCaller) != origCaller {
		t.Fatalf("preview modified caller.md on disk")
	}

	// 2. Apply with --write --backlinks --apply-backlinks.
	appOut, err := runCLIArgs(t, root, "polish", "hub.md",
		"--goal", "Fix grammar and tighten prose",
		"--links", "--repair-links", "--schema", "--write", "--backlinks", "--apply-backlinks",
		"--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish apply: %v\n%s", err, appOut)
	}
	var appRes PolishResult
	if err := json.Unmarshal(appOut, &appRes); err != nil {
		t.Fatalf("unmarshal apply: %v\n%s", err, appOut)
	}
	if !appRes.UndoBatchAvailable || appRes.BacklinksApplied == 0 {
		t.Fatalf("expected batch undo available and backlinks applied, got %+v", appRes)
	}
	diskHubBytes, _ := os.ReadFile(filepath.Join(root, "hub.md"))
	diskHub := string(diskHubBytes)
	if !bytes.Contains(diskHubBytes, []byte("\r\n")) || strings.Contains(strings.ReplaceAll(diskHub, "\r\n", ""), "\n") {
		t.Fatalf("CRLF not preserved on hub.md:\n%q", diskHub)
	}
	for _, keep := range []string{"# pending review", "&lead", "*lead", "`keep_token()`", "func keepHub() {}", "[[Project Beacon]]"} {
		if !strings.Contains(diskHub, keep) {
			t.Fatalf("lost %q on hub.md:\n%s", keep, diskHub)
		}
	}
	diskCaller, _ := os.ReadFile(filepath.Join(root, "caller.md"))
	if !strings.Contains(string(diskCaller), "|Hub Alias") {
		t.Fatalf("caller.md lost |Hub Alias:\n%s", string(diskCaller))
	}

	// 3. Batch undo restores both hub.md and caller.md byte-for-byte.
	undoOut, err := runCLIArgs(t, root, "polish", "hub.md", "--undo", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish --undo: %v\n%s", err, undoOut)
	}
	var undoRes PolishUndoResult
	if err := json.Unmarshal(undoOut, &undoRes); err != nil {
		t.Fatalf("unmarshal undo: %v\n%s", err, undoOut)
	}
	if !undoRes.Reverted || len(undoRes.RestoredPaths) < 2 {
		t.Fatalf("expected batch restore, got %+v", undoRes)
	}
	if gotHub, _ := os.ReadFile(filepath.Join(root, "hub.md")); string(gotHub) != origHub {
		t.Fatalf("hub.md not restored byte-for-byte")
	}
	if gotCaller, _ := os.ReadFile(filepath.Join(root, "caller.md")); string(gotCaller) != origCaller {
		t.Fatalf("caller.md not restored byte-for-byte")
	}
}

func TestEmbedNewDocument_ResolvesMissingIDAndBailsOnMissingRow(t *testing.T) {
	v, root := newContractVault(t)
	note := "---\ntitle: Idless Note\ntype: note\nstatus: complete\n---\n\n# Idless Note\n\nBody.\n"
	abs := filepath.Join(root, "idless.md")
	if err := os.WriteFile(abs, []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vault.IndexSingleFile(v, abs); err != nil {
		t.Fatalf("IndexSingleFile: %v", err)
	}
	dbDoc, err := v.DB.GetDocumentByPath("idless.md")
	if err != nil || dbDoc == nil || dbDoc.ID == "" {
		t.Fatalf("expected indexed doc with surrogate ID, got %+v err=%v", dbDoc, err)
	}

	parsed, err := document.Parse("idless.md", []byte(note))
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "idless.md"
	if parsed.ID != "" {
		t.Fatalf("expected empty parsed ID on id-less note, got %q", parsed.ID)
	}
	embedNewDocument(v, parsed)
	if parsed.ID != dbDoc.ID {
		t.Fatalf("embedNewDocument failed to populate doc.ID from DB: got %q, want %q", parsed.ID, dbDoc.ID)
	}

	unindexed := &document.Document{Path: "never-indexed.md", Body: "No DB row."}
	embedNewDocument(v, unindexed)
	if unindexed.ID != "" {
		t.Fatalf("expected unindexed doc ID to remain empty, got %q", unindexed.ID)
	}
	var tableName string
	if err := v.DB.Conn().QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='vec_chunks'`).Scan(&tableName); err == nil {
		var orphanCount int
		if err := v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM vec_chunks WHERE doc_id = ''`).Scan(&orphanCount); err != nil {
			t.Fatalf("query vec_chunks: %v", err)
		}
		if orphanCount != 0 {
			t.Fatalf("expected 0 orphan vec_chunks rows with doc_id = '', got %d", orphanCount)
		}
	}
}

func TestPolishM2_IdlessNoteReembedsAndUnclosedFrontmatter_LiveGemini(t *testing.T) {
	v, root := newContractVault(t)
	providers, skipReason := resolveFixAIProviders(context.Background(), v)
	if providers == nil || providers.Generator == nil {
		t.Skipf("live generation provider unavailable: %s", skipReason)
	}

	// 1. Bug 1: id-less Obsidian note re-embeds on --write and --undo with 0 orphan vec_chunks.
	if providers.Embedder != nil && providers.EmbedderReady {
		idlessOrig := "---\ntitle: Idless Service\ntype: note\nstatus: complete\n---\n\n# Idless Service\n\nThis note have bad grammer and needs polish.\n"
		idlessAbs := filepath.Join(root, "idless.md")
		if err := os.WriteFile(idlessAbs, []byte(idlessOrig), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := vault.IndexVault(v, func(string) {}); err != nil {
			t.Fatalf("IndexVault: %v", err)
		}
		if _, err := runCLIArgs(t, root, "polish", "idless.md", "--write", "--json", "--porcelain"); err != nil {
			t.Fatalf("polish --write idless: %v", err)
		}
		var afterWriteEmbedHash, afterWriteContentHash string
		if err := v.DB.Conn().QueryRow(`SELECT COALESCE(embedding_hash, ''), COALESCE(content_hash, '') FROM documents WHERE path = 'idless.md'`).Scan(&afterWriteEmbedHash, &afterWriteContentHash); err != nil {
			t.Fatalf("query documents after write: %v", err)
		}
		if afterWriteEmbedHash == "" || afterWriteEmbedHash != afterWriteContentHash {
			t.Fatalf("stale embedding_hash after polish --write on idless note: embed=%q content=%q", afterWriteEmbedHash, afterWriteContentHash)
		}
		var orphanCount int
		if err := v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM vec_chunks WHERE doc_id = ''`).Scan(&orphanCount); err != nil {
			t.Fatal(err)
		}
		if orphanCount != 0 {
			t.Fatalf("orphan vec_chunks rows found after write: %d", orphanCount)
		}

		if _, err := runCLIArgs(t, root, "polish", "idless.md", "--undo", "--json", "--porcelain"); err != nil {
			t.Fatalf("polish --undo idless: %v", err)
		}
		var afterUndoEmbedHash, afterUndoContentHash string
		if err := v.DB.Conn().QueryRow(`SELECT COALESCE(embedding_hash, ''), COALESCE(content_hash, '') FROM documents WHERE path = 'idless.md'`).Scan(&afterUndoEmbedHash, &afterUndoContentHash); err != nil {
			t.Fatalf("query documents after undo: %v", err)
		}
		if afterUndoEmbedHash == "" || afterUndoEmbedHash != afterUndoContentHash {
			t.Fatalf("stale embedding_hash after polish --undo on idless note: embed=%q content=%q", afterUndoEmbedHash, afterUndoContentHash)
		}
		if err := v.DB.Conn().QueryRow(`SELECT COUNT(*) FROM vec_chunks WHERE doc_id = ''`).Scan(&orphanCount); err != nil {
			t.Fatal(err)
		}
		if orphanCount != 0 {
			t.Fatalf("orphan vec_chunks rows found after undo: %d", orphanCount)
		}
	}

	// 2. Bug 2: unclosed YAML frontmatter repaired by `polish --schema` instead of copy-edited as prose.
	unclosedOrig := "---\ntitle: Unclosed Note\ntype: note\nstatus: open\n\n# Unclosed Note\n\nThis note have bad grammer.\n"
	if err := os.WriteFile(filepath.Join(root, "unclosed.md"), []byte(unclosedOrig), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLIArgs(t, root, "polish", "unclosed.md", "--schema", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish --schema unclosed: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal unclosed polish: %v\n%s", err, out)
	}
	if !res.SchemaRepaired || len(res.FrontmatterFixes) == 0 {
		t.Fatalf("expected schema_repaired=true and frontmatter_fixes for unclosed note, got %+v", res)
	}
	if strings.Contains(res.Polished, "title: Unclosed Note") {
		t.Fatalf("YAML frontmatter leaked into polished body:\n%s", res.Polished)
	}
}
