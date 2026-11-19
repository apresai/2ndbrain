package polish

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/testutil"
)

func TestPolishPrompt_EmptyGoalVsGoalComposition(t *testing.T) {
	if got := BuildPolishSystemPrompt("", "", false); got != DefaultPolishSystem {
		t.Fatalf("empty goal must keep DefaultPolishSystem byte-identical:\n got: %q\nwant: %q", got, DefaultPolishSystem)
	}
	if got := BuildPolishSystemPrompt("", "", true); got != DefaultPolishSystem+LinkInstructions {
		t.Fatalf("empty goal + links must equal DefaultPolishSystem+LinkInstructions")
	}

	withGoal := BuildPolishSystemPrompt("", "Executive bullet tone", true)
	if !strings.Contains(withGoal, "POLISH GOAL / LENS") || !strings.Contains(withGoal, "Executive bullet tone") {
		t.Fatalf("goal block missing:\n%s", withGoal)
	}
	if !strings.Contains(withGoal, "Hard constraints") || !strings.HasSuffix(withGoal, LinkInstructions) {
		t.Fatalf("hard constraints or link instructions missing:\n%s", withGoal)
	}
}

func TestPolishValidate_EnforcesInvariantsAndPreservesTrailingCode(t *testing.T) {
	orig := "# Migration ADR\n\nUses [[GCP Vertex AI]] and `keepMe()`.\n\n```go\nfunc keepMe() {}\n```\n"
	allowed := AllowedLinkSet(nil, orig)

	// 1. Wrapping markdown fence around valid body with trailing code block is stripped cleanly.
	wrapped := "```markdown\n# Migration ADR\n\nUses [[GCP Vertex AI]] and `keepMe()` cleanly.\n\n```go\nfunc keepMe() {}\n```\n```"
	cleaned, stripped, violations := ValidatePolishedBody(orig, wrapped, allowed)
	if len(violations) != 0 || len(stripped) != 0 {
		t.Fatalf("unexpected violations=%v stripped=%v\ncleaned:\n%s", violations, stripped, cleaned)
	}
	if !strings.Contains(cleaned, "func keepMe() {}") {
		t.Fatalf("trailing code block lost:\n%s", cleaned)
	}

	// 2. Invented links are stripped unconditionally and added alias on existing link is flagged.
	mutated := "# Migration ADR\n\nUses [[GCP Vertex AI|Vertex]] and [[Phantom Note]] and `keepMe()`.\n\n```go\nfunc keepMe() {}\n```"
	cleaned, stripped, violations = ValidatePolishedBody(orig, mutated, allowed)
	if len(stripped) != 1 || stripped[0] != "Phantom Note" {
		t.Fatalf("expected Phantom Note stripped, got %v", stripped)
	}
	if strings.Contains(cleaned, "[[Phantom Note]]") {
		t.Fatalf("invented link remained:\n%s", cleaned)
	}
	foundLinkViolation := false
	for _, v := range violations {
		if v == "existing_links_dropped" {
			foundLinkViolation = true
		}
	}
	if !foundLinkViolation {
		t.Fatalf("expected existing_links_dropped when [[GCP Vertex AI]] was aliased to [[GCP Vertex AI|Vertex]], got %v", violations)
	}

	// 3. Injected YAML frontmatter is stripped when original had none.
	injected := "---\ntype: hacked\n---\n\n# Migration ADR\n\nUses [[GCP Vertex AI]] and `keepMe()`.\n\n```go\nfunc keepMe() {}\n```"
	cleaned, _, violations = ValidatePolishedBody(orig, injected, allowed)
	if len(violations) != 0 || strings.Contains(cleaned, "type: hacked") {
		t.Fatalf("injected frontmatter should be stripped cleanly; violations=%v\n%s", violations, cleaned)
	}
}

func TestPolishGatherCandidates_UnindexedVault(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeUnindexedNote(t, v, "Project Beacon.md", "Project Beacon", "Architecture spec.")
	writeUnindexedNote(t, v, "source.md", "Source", "Discusses Project Beacon rollout.")

	srcDoc, err := document.ParseFile(filepath.Join(v.Root, "source.md"))
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	cands, _, err := GatherCandidates(context.Background(), v, nil, CandidateInput{
		Source:     srcDoc,
		SourcePath: "source.md",
		Max:        10,
	})
	if err != nil {
		t.Fatalf("GatherCandidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Title != "Project Beacon" {
		t.Fatalf("expected Project Beacon from unindexed live walk, got %+v", cands)
	}
}

func TestPolishSchemaAndCRLF_PreservesCommentsAnchorsAndLineEndings(t *testing.T) {
	v := testutil.NewTestVault(t)
	raw := []byte("---\r\n" +
		"id: \"007\"\r\n" +
		"title: ADR 004 Caching\r\n" +
		"type: adr\r\n" +
		"status: proposed # pending review\r\n" +
		"created: \"2026-04-20T10:00:00Z\"\r\n" +
		"owner: &lead \"@alice\"\r\n" +
		"reviewer: *lead\r\n" +
		"---\r\n\r\n" +
		"# ADR 004\r\n\r\n" +
		"Old prose.\r\n")
	abs := filepath.Join(v.Root, "adrs", "004-caching.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := document.Parse("adrs/004-caching.md", raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	sres, err := RepairNoteSchema(context.Background(), v, "adrs/004-caching.md", raw, doc, nil, "")
	if err != nil {
		t.Fatalf("RepairNoteSchema: %v", err)
	}
	if !sres.Repaired || len(sres.Fixes) != 1 || sres.Fixes[0].Field != "created" {
		t.Fatalf("expected created date unquote fix, got repaired=%v fixes=%+v", sres.Repaired, sres.Fixes)
	}
	// Preview must not touch disk.
	onDisk, _ := os.ReadFile(abs)
	if !bytes.Equal(onDisk, raw) {
		t.Fatalf("RepairNoteSchema touched disk in preview mode")
	}

	disp, diskBody := FormatBodyLikeOriginal(sres.Doc.Body, sres.Bytes, "# ADR 004\n\nPolished prose.")
	if !strings.Contains(disp, "\r\n") {
		t.Fatalf("expected CRLF display body, got %q", disp)
	}
	outBytes, err := ApplyBodyWithFrontmatterPreservation(sres.Bytes, sres.Doc, abs, diskBody)
	if err != nil {
		t.Fatalf("ApplyBodyWithFrontmatterPreservation: %v", err)
	}
	outStr := string(outBytes)
	if strings.Contains(strings.ReplaceAll(outStr, "\r\n", ""), "\n") {
		t.Fatalf("bare LF introduced in CRLF note:\n%q", outStr)
	}
	for _, keep := range []string{"# pending review", "&lead", "*lead", "id: \"007\"", "created: 2026-04-20T10:00:00Z"} {
		if !strings.Contains(outStr, keep) {
			t.Fatalf("missing preserved token %q in:\n%s", keep, outStr)
		}
	}
}

func TestPolishRepairOutgoingLinks_LeavesDeadLinksSkipped(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeUnindexedNote(t, v, "Guides/Target.md", "Target", "Canonical target.")
	body := "See [[Guides\\Target.md]] and [[nonexistent-note]].\n"

	res, err := RepairOutgoingLinks(context.Background(), v, "source.md", body, nil, nil, false, 0, false)
	if err != nil {
		t.Fatalf("RepairOutgoingLinks: %v", err)
	}
	if !strings.Contains(res.Body, "[[Guides/Target]]") && !strings.Contains(res.Body, "[[Target]]") {
		t.Fatalf("drifted link was not repaired:\n%s", res.Body)
	}
	if !strings.Contains(res.Body, "[[nonexistent-note]]") {
		t.Fatalf("dead link should stay intact in polish --repair-links:\n%s", res.Body)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Raw != "nonexistent-note" || res.Skipped[0].Reason != "no_match" {
		t.Fatalf("expected nonexistent-note in Skipped, got %+v", res.Skipped)
	}
}

func TestPolishBacklinks_DiscoveryRepairMentionAndExclusions(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Core Engine", "note",
		"---\ntitle: Core Engine\ntype: note\nstatus: complete\n---\n\n# Core Engine\n\nSpec.\n")
	if err := os.Rename(filepath.Join(v.Root, "core-engine.md"), filepath.Join(v.Root, "core.md")); err != nil {
		t.Fatal(err)
	}
	// Reindex after rename to core.md
	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("a.md", "---\ntitle: A\ntype: note\nstatus: complete\n---\n\n# A\n\nUses [[core]].\n")
	writeFile("b.md", "---\ntitle: B\ntype: note\nstatus: complete\n---\n\n# B\n\nUses [[core|Core Alias]].\n")
	writeFile("c.md", "---\ntitle: C\ntype: note\nstatus: complete\n---\n\n# C\n\nUses [[core#Architecture]].\n")
	writeFile("d.md", "---\ntitle: D\ntype: note\nstatus: complete\n---\n\n# D\n\nMentions Core Engine in prose.\n")
	writeFile("code-only.md", "---\ntitle: Code Only\ntype: note\nstatus: complete\n---\n\n# Code Only\n\n```markdown\n[[Core Engine]] and Core Engine\n```\n")
	writeFile("templates/tmpl.md", "---\ndate: {{date}}\n---\n\nCore Engine template.\n")

	coreBytes, _ := os.ReadFile(filepath.Join(v.Root, "core.md"))
	coreDoc, _ := document.Parse("core.md", coreBytes)

	plan, err := PlanBacklinkUpdates(context.Background(), v, BacklinkPlanInput{
		TargetRel:      "core.md",
		TargetDoc:      coreDoc,
		TargetOrigBody: coreDoc.Body,
		TargetNewBody:  coreDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates: %v", err)
	}

	byPath := make(map[string]PolishBacklinkChange)
	for _, item := range plan.Items {
		if item.Change.Applied {
			t.Fatalf("preview item unexpectedly marked applied=true: %+v", item.Change)
		}
		byPath[item.Change.Path] = item.Change
	}
	if _, ok := byPath["code-only.md"]; ok {
		t.Fatalf("code-block-only file must not appear in backlink previews")
	}
	if _, ok := byPath["templates/tmpl.md"]; ok {
		t.Fatalf("template file must not appear in backlink previews")
	}
	if !strings.Contains(byPath["b.md"].Polished, "|Core Alias") {
		t.Fatalf("b.md lost |Core Alias: %+v", byPath["b.md"])
	}
	if !strings.Contains(byPath["c.md"].Polished, "#Architecture") {
		t.Fatalf("c.md lost #Architecture: %+v", byPath["c.md"])
	}
	if !strings.Contains(byPath["d.md"].Polished, "[[Core Engine]]") {
		t.Fatalf("d.md unlinked prose mention was not wrapped: %+v", byPath["d.md"])
	}
}

func TestPolishSnapshot_BatchUndoClassification(t *testing.T) {
	v := testutil.NewTestVault(t)
	snap := PolishSnapshot{
		Path:          "hub.md",
		OriginalFull:  "orig hub",
		PolishedBody:  "new hub",
		Provider:      "gemini",
		Model:         "gemini-4-hydrogen",
		Timestamp:     "2026-11-18T00:00:00Z",
		PostWriteHash: HashContent([]byte("written hub")),
		BatchFiles: []PolishBatchFile{
			{
				Path:          "notes/a.md",
				OriginalFull:  "orig a",
				PostWriteHash: HashContent([]byte("written a")),
			},
		},
	}
	if err := WriteSnapshot(v, snap); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	loaded, err := LoadSnapshot(v, "hub.md")
	if err != nil || loaded == nil || len(loaded.BatchFiles) != 1 {
		t.Fatalf("LoadSnapshot batch mismatch: %+v err=%v", loaded, err)
	}
	if d := ClassifyUndoFile([]byte("written a"), loaded.BatchFiles[0].OriginalFull, loaded.BatchFiles[0].PostWriteHash); d != UndoProceed {
		t.Fatalf("expected UndoProceed, got %v", d)
	}
	if d := ClassifyUndoFile([]byte("external edit"), loaded.BatchFiles[0].OriginalFull, loaded.BatchFiles[0].PostWriteHash); d != UndoConflict {
		t.Fatalf("expected UndoConflict, got %v", d)
	}
}

func TestPolishBacklinks_AmbiguousTitleAndAliasCollision(t *testing.T) {
	// Case 1 (Probe 2): Two notes share title "Readme" and basename "readme".
	// Polishing a/readme.md must NOT hijack b/readme.md, caller_b.md ([[b/readme]]),
	// or caller_unlinked.md via Pass 2 prose mention linking.
	v := testutil.NewTestVault(t)
	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("a/readme.md", "---\ntitle: Readme\ntype: note\nstatus: complete\n---\n\n# Readme\n\nReadme for folder A.\n")
	writeFile("b/readme.md", "---\ntitle: Readme\ntype: note\nstatus: complete\n---\n\n# Readme\n\nReadme for folder B.\n")
	writeFile("caller_b.md", "---\ntitle: Caller B\ntype: note\nstatus: complete\n---\n\n# Caller B\n\nSee [[b/readme]] and check readme or Readme in prose.\n")
	writeFile("caller_unlinked.md", "---\ntitle: Caller Unlinked\ntype: note\nstatus: complete\n---\n\n# Caller Unlinked\n\nPlease consult Readme before deploying.\n")

	aBytes, _ := os.ReadFile(filepath.Join(v.Root, "a/readme.md"))
	aDoc, _ := document.Parse("a/readme.md", aBytes)
	plan, err := PlanBacklinkUpdates(context.Background(), v, BacklinkPlanInput{
		TargetRel:      "a/readme.md",
		TargetDoc:      aDoc,
		TargetOrigBody: aDoc.Body,
		TargetNewBody:  aDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates (readme collision): %v", err)
	}
	if len(plan.Items) != 0 {
		t.Fatalf("expected 0 backlink updates when title/basename collide, got %+v", plan.Items)
	}

	// Case 2 (Probe 6): a/alpha.md has unique title "Alpha Note" and colliding alias
	// "SharedTerm" (collides with b/other.md title "SharedTerm").
	// Polishing a/alpha.md must leave b/other.md untouched, while still linking
	// unambiguous "Alpha Note" in caller_alpha.md.
	v2 := testutil.NewTestVault(t)
	writeFile2 := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v2.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile2("a/alpha.md", "---\ntitle: Alpha Note\ntype: note\nstatus: complete\naliases:\n  - SharedTerm\n---\n\n# Alpha Note\n\nBody A.\n")
	writeFile2("b/other.md", "---\ntitle: SharedTerm\ntype: note\nstatus: complete\n---\n\n# SharedTerm\n\nUnrelated note titled SharedTerm.\n")
	writeFile2("caller_alpha.md", "---\ntitle: Caller Alpha\ntype: note\nstatus: complete\n---\n\n# Caller Alpha\n\nMentions Alpha Note in prose.\n")

	alphaBytes, _ := os.ReadFile(filepath.Join(v2.Root, "a/alpha.md"))
	alphaDoc, _ := document.Parse("a/alpha.md", alphaBytes)
	plan2, err := PlanBacklinkUpdates(context.Background(), v2, BacklinkPlanInput{
		TargetRel:      "a/alpha.md",
		TargetDoc:      alphaDoc,
		TargetOrigBody: alphaDoc.Body,
		TargetNewBody:  alphaDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates (alias collision): %v", err)
	}
	if len(plan2.Items) != 1 || plan2.Items[0].Change.Path != "caller_alpha.md" {
		t.Fatalf("expected only caller_alpha.md to be updated, got %+v", plan2.Items)
	}
	if !strings.Contains(plan2.Items[0].Change.Polished, "[[Alpha Note]]") {
		t.Fatalf("expected caller_alpha.md to link [[Alpha Note]], got %+v", plan2.Items[0].Change)
	}
}

func TestPolishBacklinks_IndentedAndNestedCodeFencesUntouched(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("sub/target.md", "---\ntitle: Core Target\ntype: note\nstatus: complete\n---\n\n# Core Target\n\nBody.\n")
	writeFile("trap_indented_fence.md", "---\ntitle: Trap Indented\ntype: note\nstatus: complete\n---\n\n# Trap Indented\n\n- Example:\n  ```markdown\n  [[target.md]] and Core Target\n  ```\n")
	writeFile("trap_four_tick.md", "---\ntitle: Trap Four Tick\ntype: note\nstatus: complete\n---\n\n# Trap Four Tick\n\n````markdown\nExample fence:\n```md\n[[target.md]] and Core Target\n```\n````\n")

	targetBytes, _ := os.ReadFile(filepath.Join(v.Root, "sub/target.md"))
	targetDoc, _ := document.Parse("sub/target.md", targetBytes)
	plan, err := PlanBacklinkUpdates(context.Background(), v, BacklinkPlanInput{
		TargetRel:      "sub/target.md",
		TargetDoc:      targetDoc,
		TargetOrigBody: targetDoc.Body,
		TargetNewBody:  targetDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates: %v", err)
	}
	if len(plan.Items) != 0 {
		t.Fatalf("expected 0 backlink updates for indented/nested fenced code blocks, got %+v", plan.Items)
	}
}

func TestPolishBacklinks_BodyTemplatePlaceholdersAndStaleDBExcludedFolder(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("sub/target.md", "---\ntitle: Core Target\ntype: note\nstatus: complete\n---\n\n# Core Target\n\nBody.\n")
	// Indexed BEFORE late_tmpl is configured in .obsidian/templates.json so DB holds stale backlinks.
	writeFile("late_tmpl/t.md", "---\ntitle: Late Template\ntype: note\nstatus: complete\n---\n\n# Late Template\n\nSee [[target]].\n")
	if _, err := v.DB.Conn().Exec(`SELECT 1`); err != nil {
		t.Fatal(err)
	}
	writeFile("misc/body_only_tmpl.md", "---\ntitle: Body Tmpl\ntype: note\nstatus: complete\n---\n\nToday is {{date}} <% tp.date.now() %>.\nSee [[target]] and Core Target.\n")
	writeFile("misc/code_discussing_tmpl.md", "---\ntitle: Tmpl Guide\ntype: note\nstatus: complete\n---\n\nUse `{{date}}` and:\n```markdown\n<% tp.date.now() %>\n```\nSee [[target]].\n")

	// Index everything while late_tmpl is not yet configured as a template folder.
	for _, rel := range []string{"sub/target.md", "late_tmpl/t.md", "misc/body_only_tmpl.md", "misc/code_discussing_tmpl.md"} {
		abs := filepath.Join(v.Root, rel)
		raw, _ := os.ReadFile(abs)
		doc, err := document.Parse(rel, raw)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		doc.Path = rel
		if err := v.DB.UpsertDocument(doc); err != nil {
			t.Fatalf("upsert %s: %v", rel, err)
		}
	}
	if err := v.DB.ResolveLinks(); err != nil {
		t.Fatalf("resolve links: %v", err)
	}

	// Now configure late_tmpl as an Obsidian template folder without reindexing.
	writeFile(".obsidian/templates.json", `{"folder":"late_tmpl"}`)
	writeFile(".obsidian/hidden_note.md", "---\ntitle: Hidden\ntype: note\nstatus: complete\n---\n\nSee [[target]].\n")

	targetBytes, _ := os.ReadFile(filepath.Join(v.Root, "sub/target.md"))
	targetDoc, _ := document.Parse("sub/target.md", targetBytes)
	plan, err := PlanBacklinkUpdates(context.Background(), v, BacklinkPlanInput{
		TargetRel:      "sub/target.md",
		TargetDoc:      targetDoc,
		TargetOrigBody: targetDoc.Body,
		TargetNewBody:  targetDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates: %v", err)
	}

	if len(plan.Items) != 1 || plan.Items[0].Change.Path != "misc/code_discussing_tmpl.md" {
		t.Fatalf("expected ONLY misc/code_discussing_tmpl.md to be updated (body_only_tmpl, late_tmpl, .obsidian must be excluded), got %+v", plan.Items)
	}
	if !strings.Contains(plan.Items[0].Change.Polished, "[[Core Target]]") {
		t.Fatalf("expected [[target]] in misc/code_discussing_tmpl.md to canonicalize to [[Core Target]], got %+v", plan.Items[0].Change)
	}
}

func TestPolishBacklinks_BlockquoteFenceDepthTracking(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("sub/target.md", "---\ntitle: Core Target\ntype: note\nstatus: complete\n---\n\n# Core Target\n\nBody.\n")
	writeFile("trap_bq_inside_fence.md", "---\ntitle: Trap BQ Inside Fence\ntype: note\nstatus: complete\n---\n\n# Trap BQ Inside Fence\n\n```md\n> ```\n[[broken-and-target]] and [[target]] and Core Target\n```\n")
	writeFile("bq_fence.md", "---\ntitle: BQ Fence\ntype: note\nstatus: complete\n---\n\n# BQ Fence\n\n> ```md\n> [[target]] and Core Target\n> ```\n\nReal [[target]] outside.\n")

	targetBytes, _ := os.ReadFile(filepath.Join(v.Root, "sub/target.md"))
	targetDoc, _ := document.Parse("sub/target.md", targetBytes)
	plan, err := PlanBacklinkUpdates(context.Background(), v, BacklinkPlanInput{
		TargetRel:      "sub/target.md",
		TargetDoc:      targetDoc,
		TargetOrigBody: targetDoc.Body,
		TargetNewBody:  targetDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates: %v", err)
	}

	if len(plan.Items) != 1 || plan.Items[0].Change.Path != "bq_fence.md" {
		t.Fatalf("expected only bq_fence.md to be updated (trap_bq_inside_fence.md must stay untouched), got %+v", plan.Items)
	}
	wantPolished := "# BQ Fence\n\n> ```md\n> [[target]] and Core Target\n> ```\n\nReal [[Core Target]] outside."
	if plan.Items[0].Change.Polished != wantPolished {
		t.Fatalf("bq_fence.md mutated inside blockquote fence:\n got:\n%s\nwant:\n%s", plan.Items[0].Change.Polished, wantPolished)
	}
}

func TestPolishCandidatesAndValidate_MultiBacktickInlineCodeWithInnerBackticks(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeUnindexedNote(t, v, "Core Target.md", "Core Target", "Target body.")
	body := "Check ``foo ` bar Core Target baz ` qux`` and read that spec.\n"
	writeUnindexedNote(t, v, "double_leak.md", "Double Leak", body)

	masked := proseOnly(body)
	if strings.Contains(strings.ToLower(masked), "core target") {
		t.Fatalf("proseOnly leaked Core Target from multi-backtick inline span: %q", masked)
	}

	srcDoc, err := document.ParseFile(filepath.Join(v.Root, "double_leak.md"))
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}
	cands, _, err := GatherCandidates(context.Background(), v, nil, CandidateInput{
		Source:     srcDoc,
		SourcePath: "double_leak.md",
		Max:        10,
	})
	if err != nil {
		t.Fatalf("GatherCandidates: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("expected 0 candidates when Core Target appears only inside multi-backtick inline code, got %+v", cands)
	}

	rewordedProse := "Inspect ``foo ` bar Core Target baz ` qux`` and read that spec carefully."
	if !CodeSpansEqual(body, rewordedProse) {
		t.Fatalf("CodeSpansEqual should pass when multi-backtick inline span is unchanged")
	}
	mutatedMiddle := "Check ``foo ` bar Other Target baz ` qux`` and read that spec."
	if CodeSpansEqual(body, mutatedMiddle) {
		t.Fatalf("CodeSpansEqual failed to detect mutation inside multi-backtick inline span with inner single backticks")
	}
}

func TestPolishBacklinks_ShallowerQuoteFenceTrapAndUnmatchedBacktickProse(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("sub/target.md", "---\ntitle: Core Target\ntype: note\nstatus: complete\n---\n\n# Core Target\n\nBody.\n")
	// 1. Shallower blockquote fence markers inside deeper blockquote fences must NOT close the block.
	writeFile("bq1_with_top_trap.md", "---\ntitle: BQ1 With Top Trap\ntype: note\nstatus: complete\n---\n\n# BQ1 With Top Trap\n\n> ```md\n```\n> [[broken-inside-bq1-after-top]] and [[target]]\n> ```\n")
	writeFile("bq2_with_bq1_trap.md", "---\ntitle: BQ2 With BQ1 Trap\ntype: note\nstatus: complete\n---\n\n# BQ2 With BQ1 Trap\n\n> > ```md\n> ```\n> > [[broken-inside-bq2-after-bq1]] and [[target]]\n> > ```\n")
	// 2. Closed single and multi-backtick inline code spans must stay excluded.
	writeFile("masked_single.md", "---\ntitle: Masked Single\ntype: note\nstatus: complete\n---\n\n# Masked Single\n\nSee `Core Target` in prose.\n")
	writeFile("masked_multitick.md", "---\ntitle: Masked Multi\ntype: note\nstatus: complete\n---\n\n# Masked Multi\n\nSee ``foo ` Core Target ` bar`` in prose.\n")
	// 3. Unmatched backtick runs must NOT suppress linking subsequent prose mentions.
	writeFile("unclosed_prose.md", "---\ntitle: Unclosed Prose\ntype: note\nstatus: complete\n---\n\n# Unclosed Prose\n\nNote the backtick ` char and read Core Target in prose.\n")
	writeFile("unclosed_double_then_single.md", "---\ntitle: Unclosed Double\ntype: note\nstatus: complete\n---\n\n# Unclosed Double\n\nSee ``literal and `code` and Core Target in prose.\n")

	targetBytes, _ := os.ReadFile(filepath.Join(v.Root, "sub/target.md"))
	targetDoc, _ := document.Parse("sub/target.md", targetBytes)
	plan, err := PlanBacklinkUpdates(context.Background(), v, BacklinkPlanInput{
		TargetRel:      "sub/target.md",
		TargetDoc:      targetDoc,
		TargetOrigBody: targetDoc.Body,
		TargetNewBody:  targetDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates: %v", err)
	}

	byPath := make(map[string]PolishBacklinkChange)
	for _, item := range plan.Items {
		byPath[item.Change.Path] = item.Change
	}

	for _, excluded := range []string{"bq1_with_top_trap.md", "bq2_with_bq1_trap.md", "masked_single.md", "masked_multitick.md"} {
		if ch, ok := byPath[excluded]; ok {
			t.Fatalf("%s must stay untouched, got %+v", excluded, ch)
		}
	}

	if ch, ok := byPath["unclosed_prose.md"]; !ok || !strings.Contains(ch.Polished, "read [[Core Target]] in prose.") {
		t.Fatalf("expected unclosed_prose.md to link [[Core Target]], got %+v", byPath["unclosed_prose.md"])
	}
	if ch, ok := byPath["unclosed_double_then_single.md"]; !ok || !strings.Contains(ch.Polished, "and [[Core Target]] in prose.") {
		t.Fatalf("expected unclosed_double_then_single.md to link [[Core Target]], got %+v", byPath["unclosed_double_then_single.md"])
	}
}

func TestValidateBacklinkAlignedBody_AllowsTargetLabelUpdateButProtectsOtherLinksAndSuffixes(t *testing.T) {
	docs := []store.DocInfo{
		{ID: "Notes/hub.md", Path: "Notes/hub.md", Title: "Service Hub"},
		{ID: "Notes/other.md", Path: "Notes/other.md", Title: "Other Note"},
	}
	resolver := store.NewResolver(docs, nil)
	targetRel := "Notes/hub.md"

	orig := "# Caller\n\nLinks to [[Service Hub]] and [[Other Note]] with `code()`.\n"
	allowed := AllowedLinkSet(nil, orig)
	allowed[NormalizeLinkKey("Service Hub")] = true
	allowed[NormalizeLinkKey("Notes/hub")] = true

	// 1. Target link label updated via |alias is accepted when non-target link stays verbatim.
	aliased := "# Caller\n\nLinks to [[Service Hub|Core Service Registry]] and [[Other Note]] with `code()`.\n"
	cleaned, stripped, violations := ValidateBacklinkAlignedBody(orig, aliased, allowed, targetRel, resolver)
	if len(violations) != 0 || len(stripped) != 0 {
		t.Fatalf("expected valid target alias update, got violations=%v stripped=%v cleaned=%q", violations, stripped, cleaned)
	}
	if !targetLinkAndSuffixesPreserved(cleaned, targetRel, document.ExtractWikiLinks(orig), resolver) {
		t.Fatalf("targetLinkAndSuffixesPreserved failed on valid target alias update")
	}

	// 2. Unresolvable target replacement [[Core Service Registry]] is stripped and rejected.
	unresolvable := "# Caller\n\nLinks to [[Core Service Registry]] and [[Other Note]] with `code()`.\n"
	cleaned2, _, violations2 := ValidateBacklinkAlignedBody(orig, unresolvable, allowed, targetRel, resolver)
	if len(violations2) == 0 || targetLinkAndSuffixesPreserved(cleaned2, targetRel, document.ExtractWikiLinks(orig), resolver) {
		t.Fatalf("expected unresolvable target replacement to fail, got violations=%v cleaned=%q", violations2, cleaned2)
	}

	// 3. Altering a non-target link ([[Other Note]] -> [[Other Note|Alias]]) is rejected.
	mutatedOther := "# Caller\n\nLinks to [[Service Hub|Core Service Registry]] and [[Other Note|Alias]] with `code()`.\n"
	if _, _, v3 := ValidateBacklinkAlignedBody(orig, mutatedOther, allowed, targetRel, resolver); len(v3) == 0 {
		t.Fatalf("expected mutation of non-target link to be rejected")
	}

	// 4. Altering code spans or headings is rejected.
	mutatedCode := "# Caller\n\nLinks to [[Service Hub|Core Service Registry]] and [[Other Note]] with `other()`.\n"
	if _, _, v4 := ValidateBacklinkAlignedBody(orig, mutatedCode, allowed, targetRel, resolver); len(v4) == 0 {
		t.Fatalf("expected code span mutation to be rejected")
	}
	mutatedHeading := "# Renamed Caller\n\nLinks to [[Service Hub|Core Service Registry]] and [[Other Note]] with `code()`.\n"
	if _, _, v5 := ValidateBacklinkAlignedBody(orig, mutatedHeading, allowed, targetRel, resolver); len(v5) == 0 {
		t.Fatalf("expected heading mutation to be rejected")
	}

	// 5. Dropping existing #heading or |alias suffix on target link is rejected by targetLinkAndSuffixesPreserved.
	origAnchored := "# Caller\n\nSee [[Service Hub#API|Old Label]] and [[Other Note]].\n"
	origLinks := document.ExtractWikiLinks(origAnchored)
	var targetLinks []document.WikiLink
	for _, l := range origLinks {
		if r, err := resolver.Resolve(l.Target); err == nil && r == targetRel {
			targetLinks = append(targetLinks, l)
		}
	}
	droppedHeading := "# Caller\n\nSee [[Service Hub|Old Label]] and [[Other Note]].\n"
	if targetLinkAndSuffixesPreserved(droppedHeading, targetRel, targetLinks, resolver) {
		t.Fatalf("expected dropped #API anchor to be rejected")
	}
	droppedAlias := "# Caller\n\nSee [[Service Hub#API|New Label]] and [[Other Note]].\n"
	if targetLinkAndSuffixesPreserved(droppedAlias, targetRel, targetLinks, resolver) {
		t.Fatalf("expected dropped existing |Old Label alias to be rejected")
	}
}

func TestRepairNoteSchema_ClosesUnclosedFrontmatterBeforeBody(t *testing.T) {
	v := testutil.NewTestVault(t)
	raw := []byte("---\ntitle: Unclosed Note\ntype: note\nstatus: draft\n\n# Unclosed Note\n\nThis note have bad grammer.\n")
	doc, err := document.Parse("unclosed.md", raw)
	if err != nil {
		t.Fatalf("document.Parse: %v", err)
	}
	if doc.Frontmatter != nil {
		t.Fatalf("expected nil Frontmatter on unclosed fence input, got %+v", doc.Frontmatter)
	}

	sres, err := RepairNoteSchema(context.Background(), v, "unclosed.md", raw, doc, nil, "")
	if err != nil {
		t.Fatalf("RepairNoteSchema: %v", err)
	}
	if !sres.Repaired || sres.Doc == nil || sres.Doc.Frontmatter == nil {
		t.Fatalf("expected unclosed frontmatter to be repaired, got %+v", sres)
	}
	if strings.Contains(sres.Doc.Body, "title: Unclosed Note") {
		t.Fatalf("YAML frontmatter remained in body:\n%s", sres.Doc.Body)
	}
	if !strings.Contains(string(sres.Bytes), "---\ntitle: Unclosed Note\ntype: note\nstatus: draft\n---") {
		t.Fatalf("repaired bytes missing closed YAML frontmatter:\n%s", string(sres.Bytes))
	}
}

func TestPlanBacklinkUpdates_RefDefPaddedLinksUnclosedAndUnicodeCaseFolding(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeFile := func(rel, content string) {
		t.Helper()
		abs := filepath.Join(v.Root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("Core Engine.md", "---\ntitle: Core Engine\ntype: note\nstatus: complete\n---\n\n# Core Engine\n\nSpec.\n")
	writeFile("ref-def.md", "---\ntitle: Ref Def\ntype: note\nstatus: complete\n---\n\n# Ref Def\n\n[Core Engine]: https://example.com\n\nSee [Core Engine][ref] and [Core Engine] later.\n")
	writeFile("padded.md", "---\ntitle: Padded\ntype: note\nstatus: complete\n---\n\n# Padded\n\nSee [[ Core Engine ]].\n")
	writeFile("unicode.md", "---\ntitle: Unicode\ntype: note\nstatus: complete\n---\n\n# Unicode\n\nİstanbul and Kelvin connect to Core Engine.\n")
	writeFile("unclosed-caller.md", "---\ntitle: Unclosed Caller\ntype: note\n\n# Unclosed Caller\n\nSee [[Core Engine]].\n")

	coreBytes, _ := os.ReadFile(filepath.Join(v.Root, "Core Engine.md"))
	coreDoc, _ := document.Parse("Core Engine.md", coreBytes)
	plan, err := PlanBacklinkUpdates(context.Background(), v, BacklinkPlanInput{
		TargetRel:      "Core Engine.md",
		TargetDoc:      coreDoc,
		TargetOrigBody: coreDoc.Body,
		TargetNewBody:  coreDoc.Body,
	})
	if err != nil {
		t.Fatalf("PlanBacklinkUpdates: %v", err)
	}

	byPath := make(map[string]PolishBacklinkChange)
	for _, item := range plan.Items {
		byPath[item.Change.Path] = item.Change
	}

	if ch, ok := byPath["ref-def.md"]; ok {
		t.Fatalf("ref-def.md must not wrap [Core Engine]: url or [Core Engine][ref] or [Core Engine] into [[[Core Engine]]]: %+v", ch)
	}
	if _, ok := byPath["unclosed-caller.md"]; ok {
		t.Fatalf("unclosed-caller.md must be skipped by PlanBacklinkUpdates")
	}
	if ch, ok := byPath["padded.md"]; !ok || !strings.Contains(ch.Polished, "[[Core Engine]]") || strings.Contains(ch.Polished, "[[ Core Engine ]]") {
		t.Fatalf("padded.md whitespace-padded link was not canonicalized: %+v", byPath["padded.md"])
	}
	if ch, ok := byPath["unicode.md"]; !ok || !strings.Contains(ch.Polished, "İstanbul and Kelvin connect to [[Core Engine]].") {
		t.Fatalf("unicode.md prose mention on İ/K line was not wrapped cleanly: %+v", byPath["unicode.md"])
	}
}
