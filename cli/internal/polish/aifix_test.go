package polish

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/testutil"
	"github.com/apresai/2ndbrain/internal/vault"
)

func TestFixVaultWithOptions_PreviewClassifiesAllAIFixableIssuesAndCoercesDates(t *testing.T) {
	v := testutil.NewTestVault(t)

	// Target note for broken/ambiguous links.
	writeRawVaultNote(t, v.Root, "one/dup.md", "---\ntitle: Dup One\ntype: note\nstatus: draft\n---\nOne.\n")
	writeRawVaultNote(t, v.Root, "two/dup.md", "---\ntitle: Dup Two\ntype: note\nstatus: draft\n---\nTwo.\n")

	// 14 notes with `status: open` (not in safeWidenNoteEnumSet).
	for i := 1; i <= 14; i++ {
		writeRawVaultNote(t, v.Root, fmt.Sprintf("notes/open-%02d.md", i),
			fmt.Sprintf("---\ntitle: Open Note %02d\ntype: note\nstatus: open\n---\nBody %02d.\n", i, i))
	}

	// Parse error + unclosed fence.
	writeRawVaultNote(t, v.Root, "broken/bad-yaml.md", "---\ntitle: Bad: Colon: Value\ntags:\n\t- alpha\n---\nBody.\n")
	writeRawVaultNote(t, v.Root, "broken/unclosed.md", "---\ntitle: Unclosed\ntype: note\n\n# Unclosed\nBody.\n")

	// Postmortem missing required incident-date + quoted created calendar date (Stage A must coerce to UTC RFC3339) + unparseable modified date.
	pmOrig := "---\ntitle: API Outage\ntype: postmortem\nstatus: draft\ncreated: \"2026-04-20\"\nmodified: \"sometime yesterday\"\n---\nOutage.\n"
	pmAbs := writeRawVaultNote(t, v.Root, "postmortems/outage.md", pmOrig)

	// Calendar date field on postmortem (must keep YYYY-MM-DD in Stage A).
	pmDateOrig := "---\ntitle: DB Outage\ntype: postmortem\nstatus: draft\nincident-date: \"2026-05-01\"\n---\nDB outage.\n"
	writeRawVaultNote(t, v.Root, "postmortems/db.md", pmDateOrig)

	// Ambiguous + missing link note.
	writeRawVaultNote(t, v.Root, "notes/links.md", "---\ntitle: Links\ntype: note\nstatus: draft\n---\nSee [[dup]] and [[nonexistent-paraphrase]].\n")

	// Non-scalar enum (status: [open]).
	writeRawVaultNote(t, v.Root, "notes/seq-status.md", "---\ntitle: Seq Status\ntype: note\nstatus: [open]\n---\nBody.\n")

	report, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: false})
	if err != nil {
		t.Fatalf("FixVaultWithOptions(preview): %v", err)
	}
	if report.Written || report.AIApplied || report.AIFixesApplied != 0 {
		t.Fatalf("preview must not write or apply AI: %+v", report)
	}

	// Non-nil slices check via JSON marshaling.
	rawJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, mustArray := range []string{`"ai_fixable_issues":[`, `"warnings":[`, `"notes":[`, `"ambiguous":[`, `"schema_statuses_added":[]`} {
		if !strings.Contains(string(rawJSON), mustArray) {
			t.Errorf("JSON missing non-nil array %s in:\n%s", mustArray, string(rawJSON))
		}
	}

	countsByKind := map[string]int{}
	for _, is := range report.AIFixableIssues {
		countsByKind[is.Kind]++
	}
	if countsByKind["parse_error"] != 2 {
		t.Errorf("parse_error count = %d, want 2: %+v", countsByKind["parse_error"], report.AIFixableIssues)
	}
	if countsByKind["schema_enum"] != 15 { // 14 open + 1 seq-status
		t.Errorf("schema_enum count = %d, want 15: %+v", countsByKind["schema_enum"], report.AIFixableIssues)
	}
	if countsByKind["missing_required"] != 1 { // outage.md incident-date
		t.Errorf("missing_required count = %d, want 1: %+v", countsByKind["missing_required"], report.AIFixableIssues)
	}
	if countsByKind["formatting"] != 1 { // outage.md modified: "sometime yesterday"
		t.Errorf("formatting count = %d, want 1: %+v", countsByKind["formatting"], report.AIFixableIssues)
	}
	if countsByKind["broken_link"] != 2 { // dup (ambiguous) + nonexistent-paraphrase (missing)
		t.Errorf("broken_link count = %d, want 2: %+v", countsByKind["broken_link"], report.AIFixableIssues)
	}

	// Verify Stage A date coercion distinction: created -> RFC3339, incident-date -> YYYY-MM-DD.
	var sawCreatedRFC3339, sawIncidentYYYYMMDD bool
	for _, n := range report.Notes {
		for _, ff := range n.FrontmatterFixes {
			if n.Path == "postmortems/outage.md" && ff.Field == "created" && ff.To == "2026-04-20T00:00:00Z" {
				sawCreatedRFC3339 = true
			}
			if n.Path == "postmortems/db.md" && ff.Field == "incident-date" && ff.To == "2026-05-01" {
				sawIncidentYYYYMMDD = true
			}
		}
	}
	if !sawCreatedRFC3339 || !sawIncidentYYYYMMDD {
		t.Fatalf("Stage A date coercion mismatch: createdRFC3339=%v incidentYYYYMMDD=%v notes=%+v", sawCreatedRFC3339, sawIncidentYYYYMMDD, report.Notes)
	}

	// Disk untouched in preview.
	if got := readRawVaultFile(t, pmAbs); got != pmOrig {
		t.Fatalf("preview mutated disk file")
	}
}

func TestFixVaultWithOptions_AutoAIFallbackAndNoAI(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeRawVaultNote(t, v.Root, "notes/open.md", "---\ntitle: Open\ntype: note\nstatus: open\n---\nSee [[ghost-link]].\n")

	// 1. NoAI: true reports disabled reason and runs Tier-4 unwrap.
	noAIReport, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: false, NoAI: true})
	if err != nil {
		t.Fatalf("NoAI run: %v", err)
	}
	if noAIReport.AISkippedReason != "disabled via --no-ai" {
		t.Errorf("AISkippedReason = %q, want disabled via --no-ai", noAIReport.AISkippedReason)
	}
	if noAIReport.LinksUnlinked != 1 {
		t.Errorf("LinksUnlinked = %d, want 1", noAIReport.LinksUnlinked)
	}
	if noAIReport.AIFixableCount != 1 || len(noAIReport.AIFixableIssues) != 1 || noAIReport.AIFixableIssues[0].Kind != "schema_enum" {
		t.Errorf("expected only schema_enum remaining after NoAI unlink, got count=%d issues=%+v", noAIReport.AIFixableCount, noAIReport.AIFixableIssues)
	}

	// 2. Write with ResolveAI returning unavailable skips AI gracefully with warning and still applies deterministic Tier-4 unwrap.
	fallbackReport, err := FixVaultWithOptions(context.Background(), v, FixOptions{
		Write: true,
		ResolveAI: func(ctx context.Context) (*FixAIProviders, string, error) {
			return nil, "generation provider unavailable", nil
		},
	})
	if err != nil {
		t.Fatalf("fallback write: %v", err)
	}
	if !fallbackReport.Written || fallbackReport.AIApplied {
		t.Fatalf("expected Written=true, AIApplied=false: %+v", fallbackReport)
	}
	if fallbackReport.AISkippedReason != "generation provider unavailable" {
		t.Errorf("AISkippedReason = %q", fallbackReport.AISkippedReason)
	}
	if len(fallbackReport.Warnings) == 0 {
		t.Errorf("expected warning on skipped AI")
	}
	if fallbackReport.LinksUnlinked != 1 {
		t.Errorf("expected deterministic Tier-4 fallback unwrap, got LinksUnlinked=%d", fallbackReport.LinksUnlinked)
	}
	if fallbackReport.AIFixableCount != 1 || len(fallbackReport.AIFixableIssues) != 1 || fallbackReport.AIFixableIssues[0].Kind != "schema_enum" {
		t.Errorf("expected only schema_enum remaining after fallback unwrap, got count=%d issues=%+v", fallbackReport.AIFixableCount, fallbackReport.AIFixableIssues)
	}
}

func TestGatherTargetSuggestions_FiltersDeletedDBRowsAndPreservesSeedPaths(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Deleted Ghost", "note", "Ghostty configuration guide with lots of ghostty keywords.")
	testutil.CreateAndIndex(t, v, "Source Note", "note", "See [[ghostty]] here.")

	// Delete the indexed note on disk WITHOUT reindexing so index.db holds a stale row.
	if err := os.Remove(filepath.Join(v.Root, "deleted-ghost.md")); err != nil {
		t.Fatal(err)
	}

	// Create 15 other notes on disk & index them so BM25 returns plenty of noise.
	for i := 1; i <= 15; i++ {
		testutil.CreateAndIndex(t, v, fmt.Sprintf("Extra Note %02d", i), "note", "Extra content mentioning ghostty terminal.")
	}
	// Add two ambiguous alias candidates whose titles do not share words with "alias-dup".
	writeRawVaultNote(t, v.Root, "alpha/first.md", "---\ntitle: First Candidate\naliases:\n  - alias-dup\ntype: note\nstatus: draft\n---\nUnrelated text.\n")
	writeRawVaultNote(t, v.Root, "beta/second.md", "---\ntitle: Second Candidate\naliases:\n  - alias-dup\ntype: note\nstatus: draft\n---\nUnrelated text.\n")

	results, _, _ := GatherTargetSuggestions(context.Background(), v, "ghostty", "source-note.md", SuggestTargetOptions{
		PoolLimit: LLMPoolCap,
	})
	for _, r := range results {
		if r.Path == "deleted-ghost.md" {
			t.Fatalf("GatherTargetSuggestions leaked deleted DB ghost path: %+v", results)
		}
	}

	// SeedPaths must survive PoolLimit truncation even if > PoolLimit BM25 hits exist.
	seededResults, _, _ := GatherTargetSuggestions(context.Background(), v, "ghostty", "source-note.md", SuggestTargetOptions{
		SeedPaths: []string{"alpha/first.md", "beta/second.md"},
		PoolLimit: 5,
	})
	foundAlpha, foundBeta := false, false
	for _, r := range seededResults {
		if r.Path == "alpha/first.md" {
			foundAlpha = true
		}
		if r.Path == "beta/second.md" {
			foundBeta = true
		}
	}
	if !foundAlpha || !foundBeta {
		t.Fatalf("SeedPaths were truncated out of pool: %+v", seededResults)
	}
}

func TestStageC_ApplyLLMPicksGroundingAndSyntaxPreservation(t *testing.T) {
	candidates := []SuggestLinkResult{
		{Path: "real/note.md", Title: "Real Note", Score: 0.7, Confidence: "low"},
	}
	picks, err := ParseLLMPicks("```json\n[{\"path\":\"hallucinated/ghost.md\",\"reason\":\"fake\"},{\"path\":\"real/note.md\",\"reason\":\"matches topic\"}]\n```")
	if err != nil {
		t.Fatalf("ParseLLMPicks: %v", err)
	}
	applied := ApplyLLMPicks(candidates, picks, nil)
	if len(applied) != 1 || applied[0].Path != "real/note.md" || applied[0].Confidence != "medium" {
		t.Fatalf("unexpected ApplyLLMPicks result: %+v", applied)
	}

	// Syntax preservation across RewriteExactLinkTarget + UnlinkExactTarget.
	body := "Link [[old-ref#Sec 1|my alias]], block [[old-ref#^blk9]], embed ![[old-ref#Diagram]], and code `[[old-ref]]`.\n"
	rewritten, n := document.RewriteExactLinkTarget(body, "old-ref", "real/note", "real/note.md")
	if n != 3 {
		t.Fatalf("RewriteExactLinkTarget count = %d, want 3", n)
	}
	for _, want := range []string{
		"[[real/note#Sec 1|my alias]]",
		"[[real/note#^blk9]]",
		"![[real/note#Diagram]]",
		"`[[old-ref]]`",
	} {
		if !strings.Contains(rewritten, want) {
			t.Errorf("missing %q in:\n%s", want, rewritten)
		}
	}

	// UnlinkExactTarget must skip ![[embed]] and code spans.
	unlinked, un := document.UnlinkExactTarget(body, "old-ref")
	if un != 2 {
		t.Fatalf("UnlinkExactTarget count = %d, want 2", un)
	}
	if !strings.Contains(unlinked, "![[old-ref#Diagram]]") || !strings.Contains(unlinked, "`[[old-ref]]`") {
		t.Errorf("UnlinkExactTarget touched embed or code:\n%s", unlinked)
	}
}

func TestUndoLastFix_UnparseableNotePurgesIndexAndResolvesLinks(t *testing.T) {
	v := testutil.NewTestVault(t)
	testutil.CreateAndIndex(t, v, "Hub", "note", "Links to [[Bad YAML Note]].\n")

	badOrig := "---\ntitle: Bad YAML Note\ntags:\n\t- broken\n---\nBody.\n"
	badAbs := writeRawVaultNote(t, v.Root, "bad-yaml.md", badOrig)

	// Stage B mechanical tab repair fixes bad-yaml.md without needing an AI provider
	// when a ResolveAI provider is present, or we can simulate the repaired write via mechanical repair:
	// Verify repairParseErrorNote fixes tab indentation cleanly even with nil generator when mechanical fix works!
	ns := &fixNoteState{
		absPath:      badAbs,
		relPath:      "bad-yaml.md",
		origBytes:    []byte(badOrig),
		currentBytes: []byte(badOrig),
		parseError:   true,
		parseErrMsg:  "yaml: found character that cannot start any token",
	}
	var warnings []string
	if !repairParseErrorNote(context.Background(), ns, v.Schemas, nil, "", nil, &warnings) {
		t.Fatalf("expected mechanical tab repair to succeed, warnings=%v", warnings)
	}
	if ns.parseError || ns.doc == nil || ns.doc.Title != "Bad YAML Note" {
		t.Fatalf("unexpected repaired state: %+v", ns)
	}

	// Now write repaired file, index it, save fix-last.json, and verify UndoLastFix purges index row on ErrUnparseable.
	if err := writeFileAtomic(badAbs, ns.currentBytes); err != nil {
		t.Fatal(err)
	}
	if err := vault.IndexSingleFile(v, badAbs); err != nil {
		t.Fatalf("IndexSingleFile(repaired): %v", err)
	}
	if got, _ := v.DB.GetDocumentByPath("bad-yaml.md"); got == nil {
		t.Fatal("expected bad-yaml.md to be indexed after repair")
	}

	batch := fixBatchSnapshot{
		Version:   fixBatchVersion,
		Timestamp: "2026-11-18T00:00:00Z",
		Files: []fixFileSnapshot{
			{
				Path:          "bad-yaml.md",
				OriginalFull:  badOrig,
				PostWriteHash: HashContent(ns.currentBytes),
			},
		},
	}
	if err := writeFixBatchSnapshot(v, batch); err != nil {
		t.Fatal(err)
	}

	res, err := UndoLastFix(v, false)
	if err != nil {
		t.Fatalf("UndoLastFix: %v", err)
	}
	if !res.Reverted || res.FilesRestored != 1 {
		t.Fatalf("unexpected undo result: %+v", res)
	}
	if got := readRawVaultFile(t, badAbs); got != badOrig {
		t.Fatalf("file not restored byte-for-byte")
	}
	if got, _ := v.DB.GetDocumentByPath("bad-yaml.md"); got != nil {
		t.Fatalf("expected stale index row for restored unparseable note to be deleted, got: %+v", got)
	}
}

func TestParseAISchemaUpdates_AndStripYAMLCodeFences(t *testing.T) {
	updates, err := parseAISchemaUpdates("Here is the fix:\n```json\n{\"updates\":{\"status\":\"draft\",\"incident-date\":\"2026-06-01\"}}\n```")
	if err != nil {
		t.Fatalf("parseAISchemaUpdates: %v", err)
	}
	if updates["status"] != "draft" || updates["incident-date"] != "2026-06-01" {
		t.Fatalf("unexpected updates: %+v", updates)
	}

	cleaned := stripYAMLCodeFences("```yaml\n---\ntitle: Fixed\nstatus: draft\n---\n```")
	if cleaned != "title: Fixed\nstatus: draft" {
		t.Fatalf("stripYAMLCodeFences = %q", cleaned)
	}
}

func TestRepairParseErrorNote_UnclosedWithBodyHRPreservesBodySections(t *testing.T) {
	v := testutil.NewTestVault(t)
	orig := "---\ntitle: Unclosed With HR\ntype: note\nstatus: draft\n\n# Section 1\n\nSome body text.\n\n---\n\n# Section 2\n"
	abs := writeRawVaultNote(t, v.Root, "notes/unclosed-with-hr.md", orig)

	ns := &fixNoteState{
		absPath:      abs,
		relPath:      "notes/unclosed-with-hr.md",
		origBytes:    []byte(orig),
		currentBytes: []byte(orig),
		parseError:   true,
		parseErrMsg:  "malformed YAML frontmatter",
	}
	var warnings []string
	if !repairParseErrorNote(context.Background(), ns, v.Schemas, nil, "", nil, &warnings) {
		t.Fatalf("expected mechanical unclosed+HR repair to succeed, warnings=%v", warnings)
	}
	if ns.parseError || ns.doc == nil {
		t.Fatalf("expected repaired doc, got %+v", ns)
	}
	repaired := string(ns.currentBytes)
	for _, want := range []string{
		"---\ntitle: Unclosed With HR\ntype: note\nstatus: draft\n---",
		"# Section 1",
		"Some body text.",
		"\n---\n",
		"# Section 2",
	} {
		if !strings.Contains(repaired, want) {
			t.Errorf("repaired note missing %q:\n%s", want, repaired)
		}
	}
}

func TestFixVaultWithOptions_StageATier1To3CollisionGuard(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeRawVaultNote(t, v.Root, "notes/foo-a.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nTarget note.\n")
	writeRawVaultNote(t, v.Root, "a/SharedName.md", "---\ntitle: Wrong Target\ntype: note\nstatus: draft\n---\nWrong note.\n")
	sourceAbs := writeRawVaultNote(t, v.Root, "source.md", "---\ntitle: Source\ntype: note\nstatus: draft\n---\nMoved [[old/foo-a]] and drift [[foo_a]].\n")

	rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: true, NoAI: true})
	if err != nil {
		t.Fatalf("FixVaultWithOptions: %v", err)
	}
	if rep.LinksUpdated != 2 {
		t.Fatalf("LinksUpdated = %d, want 2: %+v", rep.LinksUpdated, rep)
	}
	got := readRawVaultFile(t, sourceAbs)
	if strings.Contains(got, "[[SharedName]]") {
		t.Fatalf("source.md silently retargeted to colliding basename [[SharedName]]:\n%s", got)
	}
	if !strings.Contains(got, "Moved [[notes/foo-a]] and drift [[notes/foo-a]].") {
		t.Fatalf("expected path-qualified [[notes/foo-a]], got:\n%s", got)
	}

	docs, aliases, err := vault.CollectLiveDocs(v.Root)
	if err != nil {
		t.Fatal(err)
	}
	res := store.NewResolver(docs, aliases)
	if resolved, rerr := res.Resolve("notes/foo-a"); rerr != nil || resolved != "notes/foo-a.md" {
		t.Fatalf("Resolve(notes/foo-a) = (%q, %v), want notes/foo-a.md", resolved, rerr)
	}
}

func TestFixVaultWithOptions_SkipsQuotedTemplatePlaceholdersOutsideTemplateFolders(t *testing.T) {
	v := testutil.NewTestVault(t)
	origMustache := "---\ntitle: \"{{title}}\"\ncreated: \"{{date}}\"\ntype: note\nstatus: draft\n---\nBody\n"
	absMustache := writeRawVaultNote(t, v.Root, "outside-quoted-tmpl.md", origMustache)
	origTemplaterQuoted := "---\ntitle: \"<% tp.file.title %>\"\ncreated: \"<% tp.date.now() %>\"\ntype: note\nstatus: draft\n---\nBody\n"
	absTemplaterQuoted := writeRawVaultNote(t, v.Root, "outside-templater-quoted.md", origTemplaterQuoted)
	origTemplaterUnquoted := "---\ntitle: <% tp.file.title %>\ncreated: <% tp.date.now() %>\ntype: note\nstatus: draft\n---\nBody\n"
	absTemplaterUnquoted := writeRawVaultNote(t, v.Root, "outside-templater-unquoted.md", origTemplaterUnquoted)

	rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: false})
	if err != nil {
		t.Fatalf("FixVaultWithOptions: %v", err)
	}
	if rep.FilesScanned != 0 {
		t.Errorf("FilesScanned = %d, want 0 (templates must be skipped)", rep.FilesScanned)
	}
	if rep.AIFixableCount != 0 || len(rep.AIFixableIssues) != 0 {
		t.Errorf("expected 0 AIFixableIssues for templates, got %+v", rep.AIFixableIssues)
	}
	if got := readRawVaultFile(t, absMustache); got != origMustache {
		t.Fatalf("mustache template note mutated on disk")
	}
	if got := readRawVaultFile(t, absTemplaterQuoted); got != origTemplaterQuoted {
		t.Fatalf("quoted templater note mutated on disk")
	}
	if got := readRawVaultFile(t, absTemplaterUnquoted); got != origTemplaterUnquoted {
		t.Fatalf("unquoted templater note mutated on disk")
	}
}

func TestFixVaultWithOptions_StageATier1CollisionWithTwoPlusBasenames(t *testing.T) {
	t.Run("Case A short basename drift under 2+ basename collisions", func(t *testing.T) {
		v := testutil.NewTestVault(t)
		writeRawVaultNote(t, v.Root, "notes/ab.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nTarget AB.\n")
		writeRawVaultNote(t, v.Root, "x/SharedName.md", "---\ntitle: Wrong X\ntype: note\nstatus: draft\n---\nWrong X.\n")
		writeRawVaultNote(t, v.Root, "y/SharedName.md", "---\ntitle: Wrong Y\ntype: note\nstatus: draft\n---\nWrong Y.\n")
		sourceAbs := writeRawVaultNote(t, v.Root, "source.md", "---\ntitle: Source\ntype: note\nstatus: draft\n---\nCase drift [[AB]].\n")

		rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: true, NoAI: true})
		if err != nil {
			t.Fatalf("FixVaultWithOptions: %v", err)
		}
		if rep.LinksUpdated != 1 || rep.LinksUnlinked != 0 {
			t.Fatalf("expected LinksUpdated=1, LinksUnlinked=0, got %+v", rep)
		}
		got := readRawVaultFile(t, sourceAbs)
		if !strings.Contains(got, "Case drift [[notes/ab]].") {
			t.Fatalf("expected relink to [[notes/ab]] with zero unlinking, got:\n%s", got)
		}

		docs, aliases, err := vault.CollectLiveDocs(v.Root)
		if err != nil {
			t.Fatal(err)
		}
		res := store.NewResolver(docs, aliases)
		if resolved, rerr := res.Resolve("notes/ab"); rerr != nil || resolved != "notes/ab.md" {
			t.Fatalf("Resolve(notes/ab) = (%q, %v), want notes/ab.md", resolved, rerr)
		}
	})

	t.Run("Case B alias drift under 2+ basename collisions", func(t *testing.T) {
		v := testutil.NewTestVault(t)
		writeRawVaultNote(t, v.Root, "notes/foo-a.md", "---\ntitle: SharedName\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nTarget Foo A.\n")
		writeRawVaultNote(t, v.Root, "x/SharedName.md", "---\ntitle: Wrong X\ntype: note\nstatus: draft\n---\nWrong X.\n")
		writeRawVaultNote(t, v.Root, "y/SharedName.md", "---\ntitle: Wrong Y\ntype: note\nstatus: draft\n---\nWrong Y.\n")
		sourceAbs := writeRawVaultNote(t, v.Root, "source.md", "---\ntitle: Source\ntype: note\nstatus: draft\n---\nAlias drift [[my-special-alias]] and Tier2 [[old/foo-a]].\n")

		rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: true, NoAI: true})
		if err != nil {
			t.Fatalf("FixVaultWithOptions: %v", err)
		}
		if rep.LinksUpdated != 2 || rep.LinksUnlinked != 0 {
			t.Fatalf("expected LinksUpdated=2, LinksUnlinked=0, got %+v", rep)
		}
		got := readRawVaultFile(t, sourceAbs)
		if !strings.Contains(got, "Alias drift [[notes/foo-a]] and Tier2 [[notes/foo-a]].") {
			t.Fatalf("expected relink to [[notes/foo-a]] with zero unlinking, got:\n%s", got)
		}

		docs, aliases, err := vault.CollectLiveDocs(v.Root)
		if err != nil {
			t.Fatal(err)
		}
		res := store.NewResolver(docs, aliases)
		if resolved, rerr := res.Resolve("notes/foo-a"); rerr != nil || resolved != "notes/foo-a.md" {
			t.Fatalf("Resolve(notes/foo-a) = (%q, %v), want notes/foo-a.md", resolved, rerr)
		}
	})
}

func TestStageC_SeesStageBRepairedTitleAndAliasInMemoryAndPrunesDeferred(t *testing.T) {
	v := testutil.NewTestVault(t)
	// Target note has an unclosed frontmatter fence so initial CollectLiveDocs misses its title & alias.
	targetOrig := "---\ntitle: Restored Target\naliases:\n  - restored-alias\ntype: note\nstatus: draft\n\n# Restored Target\n\nBody.\n"
	targetAbs := writeRawVaultNote(t, v.Root, "notes/target-file.md", targetOrig)
	sourceOrig := "---\ntitle: Source\ntype: note\nstatus: draft\n---\nSee [[Restored Target]], [[restored-alias]], drift [[restored_target]], and dead [[no-such-page-anywhere-999]].\n"
	sourceAbs := writeRawVaultNote(t, v.Root, "source.md", sourceOrig)

	origDocs, origAliases, err := vault.CollectLiveDocs(v.Root)
	if err != nil {
		t.Fatal(err)
	}
	origResolver := store.NewResolver(origDocs, origAliases)
	if _, rerr := origResolver.Resolve("Restored Target"); rerr == nil {
		t.Fatalf("expected pre-repair resolver NOT to resolve Restored Target")
	}

	// Simulate Stage B mechanical parse repair in memory ONLY (disk remains unclosed).
	targetNS := &fixNoteState{
		absPath:       targetAbs,
		relPath:       "notes/target-file.md",
		origBytes:     []byte(targetOrig),
		currentBytes:  []byte(targetOrig),
		parseError:    true,
		unclosedFence: true,
		parseErrMsg:   "unclosed YAML frontmatter fence",
		change:        FixNoteChange{Path: "notes/target-file.md", LinkActions: []FixLinkAction{}, FrontmatterFixes: []FixFieldChange{}},
	}
	var warnings []string
	if !repairParseErrorNote(context.Background(), targetNS, v.Schemas, nil, "", nil, &warnings) {
		t.Fatalf("expected mechanical parse repair to succeed: %v", warnings)
	}

	sourceDoc, err := document.Parse("source.md", []byte(sourceOrig))
	if err != nil {
		t.Fatal(err)
	}
	sourceNS := &fixNoteState{
		absPath:      sourceAbs,
		relPath:      "source.md",
		origBytes:    []byte(sourceOrig),
		currentBytes: []byte(sourceOrig),
		doc:          sourceDoc,
		currentBody:  sourceDoc.Body,
		change:       FixNoteChange{Path: "source.md", LinkActions: []FixLinkAction{}, FrontmatterFixes: []FixFieldChange{}},
	}

	report := &FixVaultReport{
		SchemaStatusesAdded: []string{},
		Notes:               []FixNoteChange{},
		Ambiguous:           []FixAmbiguousLink{},
		AIFixableIssues:     []AIFixableIssue{},
		Warnings:            []string{},
	}
	states := []*fixNoteState{targetNS, sourceNS}

	origRepairIdx := NewRepairIndex(origDocs, origAliases)
	origCanon := buildCanonicalTargetLookup(origDocs)
	origDocAliases := map[string][]string{}
	applyStageADeterministicToNote(sourceNS, v.Schemas, origDocs, origDocAliases, origResolver, origRepairIdx, origCanon, true, false, report)

	// Should have deferred all 4 targets before Stage B refresh.
	if len(sourceNS.deferredLinks) != 4 {
		t.Fatalf("expected 4 deferred links before Stage B refresh, got %+v", sourceNS.deferredLinks)
	}

	// Refresh in memory from states (without touching disk!) and run Stage C.
	docs, aliases := refreshLiveDocsFromStates(origDocs, origAliases, states)
	liveResolver := store.NewResolver(docs, aliases)
	repairIdx := NewRepairIndex(docs, aliases)
	canon := buildCanonicalTargetLookup(docs)

	if gotPath, rerr := liveResolver.Resolve("restored-alias"); rerr != nil || gotPath != "notes/target-file.md" {
		t.Fatalf("refreshed in-memory resolver failed for restored-alias: (%q, %v)", gotPath, rerr)
	}

	runStageCLinkRecovery(context.Background(), v, states, docs, aliases, liveResolver, repairIdx, canon, &FixAIProviders{}, FixOptions{Write: false, AI: true}, report)

	if !strings.Contains(sourceNS.currentBody, "[[Restored Target]]") || !strings.Contains(sourceNS.currentBody, "[[restored-alias]]") {
		t.Errorf("resolved links should remain intact:\n%s", sourceNS.currentBody)
	}
	if strings.Contains(sourceNS.currentBody, "[[no-such-page-anywhere-999]]") {
		t.Errorf("dead link should have been unwrapped:\n%s", sourceNS.currentBody)
	}
	remaining := collectRemainingAIIssues(states, v.Schemas)
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining AIFixableIssues after Stage C resolved/relinked/unlinked all links, got %+v", remaining)
	}
}

func TestFixVaultWithOptions_StageATier1UniqueAliasUnderDoubleCollision(t *testing.T) {
	v := testutil.NewTestVault(t)
	writeRawVaultNote(t, v.Root, "a/item.md", "---\ntitle: SharedName\naliases:\n  - Unique Alias\ntype: note\nstatus: draft\n---\nTarget item A.\n")
	writeRawVaultNote(t, v.Root, "b/item.md", "---\ntitle: Other Item\ntype: note\nstatus: draft\n---\nItem B.\n")
	writeRawVaultNote(t, v.Root, "x/SharedName.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nX.\n")
	writeRawVaultNote(t, v.Root, "y/SharedName.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nY.\n")
	sourceAbs := writeRawVaultNote(t, v.Root, "source.md", "---\ntitle: Source\ntype: note\nstatus: draft\n---\nLink [[unique-alias]].\n")

	rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: true, NoAI: true})
	if err != nil {
		t.Fatalf("FixVaultWithOptions: %v", err)
	}
	if rep.LinksUpdated != 1 || rep.LinksUnlinked != 0 {
		t.Fatalf("expected LinksUpdated=1, LinksUnlinked=0, got %+v", rep)
	}
	got := readRawVaultFile(t, sourceAbs)
	if !strings.Contains(got, "Link [[a/item]].") {
		t.Fatalf("expected relink to [[a/item]], got:\n%s", got)
	}

	docs, aliases, err := vault.CollectLiveDocs(v.Root)
	if err != nil {
		t.Fatal(err)
	}
	res := store.NewResolver(docs, aliases)
	if resolved, rerr := res.Resolve("a/item"); rerr != nil || resolved != "a/item.md" {
		t.Fatalf("Resolve(a/item) = (%q, %v), want a/item.md", resolved, rerr)
	}
}

func TestFixVaultWithOptions_StageATier1AliasAmbiguousWhenSecondNoteDoubleCollides(t *testing.T) {
	t.Run("Probe 2E unique title plus double-colliding alias peer is marked ambiguous", func(t *testing.T) {
		v := testutil.NewTestVault(t)
		writeRawVaultNote(t, v.Root, "notes/foo-a.md", "---\ntitle: UniqueA\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nFoo A.\n")
		writeRawVaultNote(t, v.Root, "other/dup.md", "---\ntitle: DupTitle\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nOther dup.\n")
		writeRawVaultNote(t, v.Root, "z/dup.md", "---\ntitle: DupTitle\ntype: note\nstatus: draft\n---\nZ dup.\n")
		sourceOrig := "---\ntitle: Source\ntype: note\nstatus: draft\n---\nSee [[my-special-alias]].\n"
		sourceAbs := writeRawVaultNote(t, v.Root, "source.md", sourceOrig)

		rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: true, NoAI: true})
		if err != nil {
			t.Fatalf("FixVaultWithOptions: %v", err)
		}
		if rep.LinksUpdated != 0 || rep.LinksUnlinked != 0 {
			t.Fatalf("expected no relink/unlink for ambiguous alias, got %+v", rep)
		}
		if len(rep.Ambiguous) != 1 || rep.Ambiguous[0].Target != "my-special-alias" {
			t.Fatalf("expected Ambiguous entry for my-special-alias, got %+v", rep.Ambiguous)
		}
		if got := readRawVaultFile(t, sourceAbs); got != sourceOrig {
			t.Fatalf("source.md should remain untouched on ambiguity, got:\n%s", got)
		}
	})

	t.Run("Probe 2D shared title plus double-colliding alias peer is marked ambiguous instead of unlinked", func(t *testing.T) {
		v := testutil.NewTestVault(t)
		writeRawVaultNote(t, v.Root, "notes/foo-a.md", "---\ntitle: SharedName\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nFoo A.\n")
		writeRawVaultNote(t, v.Root, "x/SharedName.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nX.\n")
		writeRawVaultNote(t, v.Root, "y/SharedName.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nY.\n")
		writeRawVaultNote(t, v.Root, "other/dup.md", "---\ntitle: DupTitle\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nOther dup.\n")
		writeRawVaultNote(t, v.Root, "z/dup.md", "---\ntitle: DupTitle\ntype: note\nstatus: draft\n---\nZ dup.\n")
		sourceOrig := "---\ntitle: Source\ntype: note\nstatus: draft\n---\nSee [[my-special-alias]].\n"
		sourceAbs := writeRawVaultNote(t, v.Root, "source.md", sourceOrig)

		rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: true, NoAI: true})
		if err != nil {
			t.Fatalf("FixVaultWithOptions: %v", err)
		}
		if rep.LinksUpdated != 0 || rep.LinksUnlinked != 0 {
			t.Fatalf("expected no relink/unlink for ambiguous alias, got %+v", rep)
		}
		if len(rep.Ambiguous) != 1 || rep.Ambiguous[0].Target != "my-special-alias" {
			t.Fatalf("expected Ambiguous entry for my-special-alias, got %+v", rep.Ambiguous)
		}
		if got := readRawVaultFile(t, sourceAbs); got != sourceOrig {
			t.Fatalf("source.md should remain untouched on ambiguity, got:\n%s", got)
		}
	})
}

func TestFixVaultWithOptions_TabNestedChildKeyAndRunbookListStatus(t *testing.T) {
	v := testutil.NewTestVault(t)

	// Bug 5 regression: runbook with non-scalar status: [active] must NOT be overwritten to draft
	// as missing_required in Stage A; it must be deferred to schema_enum.
	rbOrig := "---\ntitle: DB Failover\ntype: runbook\nstatus: [active]\n---\nBody.\n"
	rbAbs := writeRawVaultNote(t, v.Root, "runbooks/failover.md", rbOrig)

	rep, err := FixVaultWithOptions(context.Background(), v, FixOptions{Write: true, NoAI: true})
	if err != nil {
		t.Fatalf("FixVaultWithOptions(NoAI): %v", err)
	}
	if got := readRawVaultFile(t, rbAbs); got != rbOrig {
		t.Fatalf("Stage A overwrote non-scalar status: [active] on runbook:\ngot:\n%s\nwant:\n%s", got, rbOrig)
	}
	foundEnum := false
	for _, is := range rep.AIFixableIssues {
		if is.Path == "runbooks/failover.md" && is.Kind == "schema_enum" {
			foundEnum = true
		}
		if is.Path == "runbooks/failover.md" && is.Kind == "missing_required" {
			t.Fatalf("unexpected missing_required issue for status: [active]: %+v", is)
		}
	}
	if !foundEnum {
		t.Fatalf("expected schema_enum in AIFixableIssues for runbook status: [active], got %+v", rep.AIFixableIssues)
	}

	// Bug 4 regression: ExtractRawYAMLKeys on tab-indented nested key under meta:
	// must not include nested child key "owner" in origKeys, so AI YAML repair is not rejected.
	raw := "title: Bad: Title\nmeta:\n\towner: alice\n"
	keys := document.ExtractRawYAMLKeys(raw)
	for _, k := range keys {
		if k == "owner" {
			t.Fatalf("nested tab key 'owner' under 'meta:' must not be reported as a top-level key: %v", keys)
		}
	}
}
