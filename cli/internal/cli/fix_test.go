package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/vault"
)

func TestFixCLI_PreviewWriteLintZeroAndUndo(t *testing.T) {
	_, root := newContractVault(t)

	targetAbs := filepath.Join(root, "Guides", "Claude Code CLI.md")
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetAbs, []byte("---\ntitle: Claude Code CLI\ntype: note\nstatus: active\n---\n\n# Claude Code CLI\n\nSetup guide.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	brokenAbs := filepath.Join(root, "Notes", "broken.md")
	if err := os.MkdirAll(filepath.Dir(brokenAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	origBroken := "---\ncreated: \"2026-04-20T10:00:00Z\"\ntype: note\nstatus: reference\n---\n\n# Broken Links\n\nSee [[Guides\\Claude Code CLI.md]], [[claude-code]], and [[dead-ghost-link]].\n"
	if err := os.WriteFile(brokenAbs, []byte(origBroken), 0o644); err != nil {
		t.Fatal(err)
	}

	schemaAbs := filepath.Join(root, ".2ndbrain", "schemas.yaml")
	origSchemaBytes, err := os.ReadFile(schemaAbs)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Preview mode must not touch disk.
	out, err := runCLIArgs(t, root, "fix", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix preview: %v\n%s", err, out)
	}
	var preview polish.FixVaultReport
	if err := json.Unmarshal(out, &preview); err != nil {
		t.Fatalf("unmarshal preview: %v\n%s", err, out)
	}
	if preview.Written {
		t.Fatalf("preview.Written = true, want false")
	}
	if preview.TotalFixes == 0 {
		t.Fatalf("expected non-zero preview.TotalFixes: %+v", preview)
	}
	gotBroken, _ := os.ReadFile(brokenAbs)
	if string(gotBroken) != origBroken {
		t.Fatalf("preview modified %s on disk", brokenAbs)
	}

	// 2. Write mode applies fixes and snapshots recovery bundle.
	out, err = runCLIArgs(t, root, "fix", "--write", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix --write: %v\n%s", err, out)
	}
	var applied polish.FixVaultReport
	if err := json.Unmarshal(out, &applied); err != nil {
		t.Fatalf("unmarshal applied: %v\n%s", err, out)
	}
	if !applied.Written || !applied.UndoAvailable {
		t.Fatalf("expected Written=true, UndoAvailable=true: %+v", applied)
	}
	if !applied.SchemaUpdated {
		t.Fatalf("expected SchemaUpdated=true: %+v", applied)
	}

	// 3. `2nb lint` after `2nb fix --write` must report 0 errors & 0 warnings.
	lintOut, err := runCLIArgs(t, root, "lint", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("lint after fix: %v\n%s", err, lintOut)
	}
	var lintRep LintReport
	if err := json.Unmarshal(lintOut, &lintRep); err != nil {
		t.Fatalf("unmarshal lint: %v\n%s", err, lintOut)
	}
	if lintRep.Errors != 0 || lintRep.Warns != 0 || len(lintRep.Issues) != 0 {
		t.Fatalf("expected 0 lint issues after fix, got %+v", lintRep)
	}

	// 4. `2nb fix --undo` restores broken note & schemas.yaml byte-for-byte.
	undoOut, err := runCLIArgs(t, root, "fix", "--undo", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix --undo: %v\n%s", err, undoOut)
	}
	var undoRes polish.FixUndoResult
	if err := json.Unmarshal(undoOut, &undoRes); err != nil {
		t.Fatalf("unmarshal undo: %v\n%s", err, undoOut)
	}
	if !undoRes.Reverted || !undoRes.SchemaRestored || undoRes.FilesRestored < 1 {
		t.Fatalf("unexpected undo result: %+v", undoRes)
	}
	restoredBroken, _ := os.ReadFile(brokenAbs)
	if string(restoredBroken) != origBroken {
		t.Fatalf("broken note not restored byte-for-byte:\ngot:\n%s\nwant:\n%s", string(restoredBroken), origBroken)
	}
	restoredSchema, _ := os.ReadFile(schemaAbs)
	if string(restoredSchema) != string(origSchemaBytes) {
		t.Fatalf("schemas.yaml not restored byte-for-byte")
	}
}

func TestFixCLI_FlagValidation(t *testing.T) {
	_, root := newContractVault(t)
	if out, err := runCLIArgs(t, root, "fix", "--write", "--undo"); err == nil {
		t.Fatalf("expected error combining --write and --undo, got output:\n%s", out)
	}
	if out, err := runCLIArgs(t, root, "fix", "--force"); err == nil {
		t.Fatalf("expected error passing --force without --undo, got output:\n%s", out)
	} else if !strings.Contains(err.Error()+string(out), "--force") {
		t.Logf("flag validation returned error: %v (%s)", err, out)
	}
	if out, err := runCLIArgs(t, root, "fix", "--ai", "--no-ai"); err == nil {
		t.Fatalf("expected error combining --ai and --no-ai, got output:\n%s", out)
	}
	if out, err := runCLIArgs(t, root, "fix", "--undo", "--ai"); err == nil {
		t.Fatalf("expected error combining --undo and --ai, got output:\n%s", out)
	}
	if out, err := runCLIArgs(t, root, "fix", "--undo", "--no-ai"); err == nil {
		t.Fatalf("expected error combining --undo and --no-ai, got output:\n%s", out)
	}
	for _, args := range [][]string{
		{"fix", "--format", "raw"},
		{"fix", "--ai", "--format", "md"},
		{"fix", "--undo", "--format", "raw"},
	} {
		out, err := runCLIArgs(t, root, args...)
		if err == nil {
			t.Fatalf("expected format refusal for %v, got:\n%s", args, out)
		}
		if !strings.Contains(err.Error(), "--format raw/md") {
			t.Fatalf("expected --format raw/md refusal for %v, got: %v", args, err)
		}
	}
}

func TestFixCLI_ExplicitAIFailsClosedWithoutProvider(t *testing.T) {
	v, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "open.md"),
		[]byte("---\ntitle: Open\ntype: note\nstatus: open\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Configure a non-existent generation provider so even under test-live with
	// ambient credentials, this test deterministically verifies fail-closed semantics.
	v.Config.AI.GenerationProvider = "unconfigured-provider"
	if err := v.Config.Save(v.DotDir); err != nil {
		t.Fatalf("save config: %v", err)
	}
	out, err := runCLIArgs(t, root, "fix", "--ai", "--json", "--porcelain")
	if err == nil {
		t.Fatalf("expected fix --ai to fail closed when generation provider is unavailable, got:\n%s", out)
	}
}

func TestFixCLI_OpFixMeteringInMetricsReport(t *testing.T) {
	v, root := newContractVault(t)
	op := metrics.Operation{
		Operation:    metrics.OpFix,
		DurationMs:   250,
		OK:           true,
		FilesScanned: 3,
		DocsIndexed:  2,
	}
	attachUsageLegs(&op,
		buildUsageLeg(root, "gemini", "gemini-4-hydrogen", "generation", metrics.OpFix, 2, 1200, 300),
		buildUsageLeg(root, "gemini", "gemini-embedding-2", "embedding", metrics.OpFix, 1, 50, 0),
	)
	recordMetric(v, op)

	out, err := runCLIArgs(t, root, "metrics", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("metrics --json: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"fix"`) {
		t.Fatalf("expected fix operation in metrics JSON output:\n%s", out)
	}
	outText, err := runCLIArgs(t, root, "metrics")
	if err != nil {
		t.Fatalf("metrics text: %v\n%s", err, outText)
	}
	if !strings.Contains(string(outText), "fix") {
		t.Fatalf("expected fix operation in human metrics output:\n%s", outText)
	}
}

func TestFixCLI_LiveAI_EndToEndRepairLintAndUndo(t *testing.T) {
	v, root := newContractVault(t)
	providers, reason := resolveFixAIProviders(context.Background(), v)
	if providers == nil || providers.Generator == nil {
		t.Skipf("live generation provider unavailable: %s", reason)
	}

	write := func(rel, content string) string {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return abs
	}

	// Target note for typo/paraphrase link recovery.
	write("Guides/Kubernetes Deployment Checklist.md",
		"---\ntitle: Kubernetes Deployment Checklist\ntype: note\nstatus: draft\n---\n\n# Kubernetes Deployment Checklist\n\nRollout steps, health probes, and rollback instructions.\n")

	// 14 notes with `status: open` (cannot be widened deterministically).
	openOrigs := make(map[string]string, 14)
	for i := 1; i <= 14; i++ {
		rel := fmt.Sprintf("notes/open-%02d.md", i)
		orig := fmt.Sprintf("---\ntitle: Open Item %02d\ntype: note\nstatus: open\n---\n\nWorking note %02d.\n", i, i)
		openOrigs[rel] = orig
		write(rel, orig)
	}

	// Postmortem missing required incident-date (clearly stated in prose).
	pmOrig := "---\ntitle: Checkout Latency Incident\ntype: postmortem\nstatus: draft\n---\n\n# Checkout Latency Incident\n\nIncident date: 2026-07-14. Latency spiked on checkout API.\n"
	pmAbs := write("postmortems/checkout.md", pmOrig)

	// Unclosed frontmatter fence (including body HR) + malformed YAML parse error.
	unclosedOrig := "---\ntitle: Unclosed Note\ntype: note\nstatus: draft\n\n# Unclosed Note\n\nBody text.\n"
	unclosedAbs := write("notes/unclosed.md", unclosedOrig)
	unclosedHROrig := "---\ntitle: Unclosed With HR\ntype: note\nstatus: draft\n\n# Section 1\n\nSome body text.\n\n---\n\n# Section 2\n"
	unclosedHRAbs := write("notes/unclosed-with-hr.md", unclosedHROrig)
	malformedOrig := "---\ntitle: Broken YAML Note\ntags: [alpha, beta\ntype: note\nstatus: draft\n---\n\n# Broken YAML Note\n\nMalformed YAML body.\n"
	malformedAbs := write("notes/malformed.md", malformedOrig)

	// Typo/paraphrase link with #heading|alias + embed + confirmed dead link.
	linksOrig := "---\ntitle: Deploy Runbook\ntype: runbook\nstatus: draft\n---\n\n# Deploy Runbook\n\nFollow [[Kubernets Deploymnt Checklst#Rollout|the k8s rollout guide]], embed ![[Kubernets Deploymnt Checklst#Probes]], and ignore [[zzzz-completely-nonexistent-topic-9999]].\n"
	linksAbs := write("runbooks/deploy.md", linksOrig)

	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// 1. Preview with --ai (read-only).
	previewOut, err := runCLIArgs(t, root, "fix", "--ai", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix --ai preview: %v\n%s", err, previewOut)
	}
	var preview polish.FixVaultReport
	if err := json.Unmarshal(previewOut, &preview); err != nil {
		t.Fatalf("unmarshal preview: %v\n%s", err, previewOut)
	}
	if preview.Written || !preview.AIApplied || preview.AIFixesApplied == 0 {
		t.Fatalf("unexpected fix --ai preview report: %+v", preview)
	}
	if got, _ := os.ReadFile(pmAbs); string(got) != pmOrig {
		t.Fatalf("fix --ai preview mutated %s on disk", pmAbs)
	}

	// 2. Apply with --write --ai.
	writeOut, err := runCLIArgs(t, root, "fix", "--write", "--ai", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix --write --ai: %v\n%s", err, writeOut)
	}
	var applied polish.FixVaultReport
	if err := json.Unmarshal(writeOut, &applied); err != nil {
		t.Fatalf("unmarshal applied: %v\n%s", err, writeOut)
	}
	if !applied.Written || !applied.UndoAvailable || !applied.AIApplied || applied.AIFixesApplied == 0 {
		t.Fatalf("unexpected fix --write --ai report: %+v", applied)
	}
	if applied.AIFixableCount != 0 || len(applied.AIFixableIssues) != 0 {
		t.Fatalf("expected 0 remaining AIFixableIssues after fix --write --ai, got count=%d issues=%+v", applied.AIFixableCount, applied.AIFixableIssues)
	}

	// Verify unclosed frontmatter + body HR preserved all sections.
	updatedHR, _ := os.ReadFile(unclosedHRAbs)
	for _, mustKeep := range []string{"# Section 1", "Some body text.", "\n---\n", "# Section 2"} {
		if !strings.Contains(string(updatedHR), mustKeep) {
			t.Errorf("unclosed-with-hr.md lost %q during fix --write --ai:\n%s", mustKeep, string(updatedHR))
		}
	}

	// 3. Verify post-fix lint has 0 errors.
	rv, err := vault.Open(root)
	if err != nil {
		t.Fatalf("reopen vault: %v", err)
	}
	lintRep, err := buildLintReport(rv, nil)
	rv.Close()
	if err != nil {
		t.Fatalf("buildLintReport: %v", err)
	}
	if lintRep.Errors != 0 {
		t.Fatalf("expected 0 lint errors after fix --write --ai, got %d: %+v", lintRep.Errors, lintRep.Issues)
	}

	// Verify links preservation (#Rollout|the k8s rollout guide, ![[...#Probes]]).
	updatedLinks, _ := os.ReadFile(linksAbs)
	if !strings.Contains(string(updatedLinks), "[[Kubernetes Deployment Checklist#Rollout|the k8s rollout guide]]") {
		t.Errorf("expected typo wikilink relinked with #heading|alias preserved, got:\n%s", string(updatedLinks))
	}
	if !strings.Contains(string(updatedLinks), "![[Kubernetes Deployment Checklist#Probes]]") {
		t.Errorf("expected embed relinked with ! and #Probes preserved, got:\n%s", string(updatedLinks))
	}
	if strings.Contains(string(updatedLinks), "[[zzzz-completely-nonexistent-topic-9999]]") {
		t.Errorf("expected confirmed dead link unwrapped, got:\n%s", string(updatedLinks))
	}

	// 4. Verify metrics.db recorded OpFix.
	metricsOut, err := runCLIArgs(t, root, "metrics", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("metrics --json: %v\n%s", err, metricsOut)
	}
	if !strings.Contains(string(metricsOut), `"fix"`) {
		t.Fatalf("expected fix operation metered in metrics.db:\n%s", string(metricsOut))
	}

	// 5. Undo restores every note byte-for-byte.
	undoOut, err := runCLIArgs(t, root, "fix", "--undo", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix --undo: %v\n%s", err, undoOut)
	}
	for rel, orig := range openOrigs {
		got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if string(got) != orig {
			t.Errorf("%s not restored byte-for-byte", rel)
		}
	}
	if got, _ := os.ReadFile(pmAbs); string(got) != pmOrig {
		t.Errorf("postmortem not restored byte-for-byte")
	}
	if got, _ := os.ReadFile(unclosedAbs); string(got) != unclosedOrig {
		t.Errorf("unclosed note not restored byte-for-byte")
	}
	if got, _ := os.ReadFile(unclosedHRAbs); string(got) != unclosedHROrig {
		t.Errorf("unclosed-with-hr note not restored byte-for-byte")
	}
	if got, _ := os.ReadFile(malformedAbs); string(got) != malformedOrig {
		t.Errorf("malformed note not restored byte-for-byte")
	}
	if got, _ := os.ReadFile(linksAbs); string(got) != linksOrig {
		t.Errorf("links note not restored byte-for-byte")
	}
}

func TestFixCLI_WriteNoAI_AIFixableCountZeroAndQuotedTemplateSkipped(t *testing.T) {
	_, root := newContractVault(t)
	if err := os.WriteFile(filepath.Join(root, "dead-only.md"),
		[]byte("---\ntitle: Dead Only\ntype: note\nstatus: draft\n---\nSee [[totally-missing-target]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hr-prose.md"),
		[]byte("---\n\nNote: this is prose after a horizontal rule, not frontmatter.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "quoted-tmpl.md"),
		[]byte("---\ntitle: \"{{title}}\"\ncreated: \"{{date}}\"\ntype: note\nstatus: draft\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templater-quoted.md"),
		[]byte("---\ntitle: \"<% tp.file.title %>\"\ncreated: \"<% tp.date.now() %>\"\ntype: note\nstatus: draft\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "templater-unquoted.md"),
		[]byte("---\ntitle: <% tp.file.title %>\ncreated: <% tp.date.now() %>\ntype: note\nstatus: draft\n---\nBody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "fix", "--write", "--no-ai", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix --write --no-ai: %v\n%s", err, out)
	}
	var rep polish.FixVaultReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if !rep.Written || rep.LinksUnlinked != 1 {
		t.Fatalf("expected Written=true and LinksUnlinked=1: %+v", rep)
	}
	if rep.AIFixableCount != 0 || len(rep.AIFixableIssues) != 0 {
		t.Fatalf("expected AIFixableCount=0 after --write --no-ai unlinked dead link, got %+v", rep.AIFixableIssues)
	}

	lintOut, err := runCLIArgs(t, root, "lint", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("lint: %v\n%s", err, lintOut)
	}
	var lintRep LintReport
	if err := json.Unmarshal(lintOut, &lintRep); err != nil {
		t.Fatalf("unmarshal lint: %v\n%s", err, lintOut)
	}
	if lintRep.Errors != 0 || lintRep.Warns != 0 || len(lintRep.Issues) != 0 {
		t.Fatalf("expected 0 lint issues, got %+v", lintRep)
	}
}

func TestFixCLI_StageATier1DriftUnderTwoPlusBasenameCollisions(t *testing.T) {
	_, root := newContractVault(t)
	write := func(rel, content string) string {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return abs
	}

	write("notes/ab.md", "---\ntitle: SharedAB\ntype: note\nstatus: draft\n---\nAB note.\n")
	write("x/SharedAB.md", "---\ntitle: Wrong AB X\ntype: note\nstatus: draft\n---\nX.\n")
	write("y/SharedAB.md", "---\ntitle: Wrong AB Y\ntype: note\nstatus: draft\n---\nY.\n")

	write("notes/foo-a.md", "---\ntitle: SharedName\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nFoo A.\n")
	write("x/SharedName.md", "---\ntitle: Wrong X\ntype: note\nstatus: draft\n---\nX.\n")
	write("y/SharedName.md", "---\ntitle: Wrong Y\ntype: note\nstatus: draft\n---\nY.\n")

	origSource := "---\ntitle: Source\ntype: note\nstatus: draft\n---\nShort [[AB]], alias [[my-special-alias]], and moved [[old/foo-a]].\n"
	srcAbs := write("source.md", origSource)

	out, err := runCLIArgs(t, root, "fix", "--write", "--no-ai", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("fix --write --no-ai: %v\n%s", err, out)
	}
	var rep polish.FixVaultReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if !rep.Written || rep.LinksUpdated != 3 || rep.LinksUnlinked != 0 {
		t.Fatalf("expected Written=true, LinksUpdated=3, LinksUnlinked=0, got %+v", rep)
	}

	gotBytes, _ := os.ReadFile(srcAbs)
	want := "Short [[notes/ab]], alias [[notes/foo-a]], and moved [[notes/foo-a]]."
	if !strings.Contains(string(gotBytes), want) {
		t.Fatalf("expected %q in source.md, got:\n%s", want, string(gotBytes))
	}

	lintOut, err := runCLIArgs(t, root, "lint", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("lint: %v\n%s", err, lintOut)
	}
	var lintRep LintReport
	if err := json.Unmarshal(lintOut, &lintRep); err != nil {
		t.Fatalf("unmarshal lint: %v\n%s", err, lintOut)
	}
	if lintRep.Errors != 0 || lintRep.Warns != 0 || len(lintRep.Issues) != 0 {
		t.Fatalf("expected 0 lint issues after relink, got %+v", lintRep)
	}

	if _, err := runCLIArgs(t, root, "fix", "--undo", "--json", "--porcelain"); err != nil {
		t.Fatalf("fix --undo: %v", err)
	}
	restored, _ := os.ReadFile(srcAbs)
	if string(restored) != origSource {
		t.Fatalf("source.md not restored byte-for-byte after undo")
	}
}

func TestFixCLI_RegressionBugs1To3AndProbe2DE(t *testing.T) {
	t.Run("Bug 1 closed malformed YAML with flow list blank line does not split mid-YAML", func(t *testing.T) {
		_, root := newContractVault(t)
		flowOrig := "---\ntitle: Flow List Note\ntags: [\n  alpha,\n\n  beta\n]\nbad: [unclosed\ntype: note\nstatus: draft\n---\n\n# Flow List Note\n\nBody text.\n"
		flowAbs := filepath.Join(root, "flow-blank.md")
		if err := os.WriteFile(flowAbs, []byte(flowOrig), 0o644); err != nil {
			t.Fatal(err)
		}

		out, err := runCLIArgs(t, root, "fix", "--write", "--no-ai", "--json", "--porcelain")
		if err != nil {
			t.Fatalf("fix --write --no-ai: %v\n%s", err, out)
		}
		var rep polish.FixVaultReport
		if err := json.Unmarshal(out, &rep); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, out)
		}
		if rep.AIFixableCount != 1 || len(rep.AIFixableIssues) != 1 || rep.AIFixableIssues[0].Kind != "parse_error" {
			t.Fatalf("expected 1 parse_error remaining without AI, got %+v", rep.AIFixableIssues)
		}
		gotBytes, _ := os.ReadFile(flowAbs)
		if string(gotBytes) != flowOrig {
			t.Fatalf("malformed flow note was corrupted on disk:\n%s", string(gotBytes))
		}
	})

	t.Run("Bug 2 leading HR note mentioning templater is linted and fixed while real templates are skipped", func(t *testing.T) {
		_, root := newContractVault(t)
		hrDoc := "---\n\n# Guide to Templater\n\nUse `<% tp.date.now() %>` in templates, and see [[definitely-broken-link]].\n"
		hrAbs := filepath.Join(root, "hr-templater-doc.md")
		if err := os.WriteFile(hrAbs, []byte(hrDoc), 0o644); err != nil {
			t.Fatal(err)
		}
		tmplExec := "---\n<%* let d = tp.date.now(); %>\ntitle: <% d %>\n\nBody\n"
		tmplAbs := filepath.Join(root, "templater-exec.md")
		if err := os.WriteFile(tmplAbs, []byte(tmplExec), 0o644); err != nil {
			t.Fatal(err)
		}

		lintOut, err := runCLIArgs(t, root, "lint", "--json", "--porcelain")
		if err != nil {
			t.Fatalf("lint: %v\n%s", err, lintOut)
		}
		var lintRep LintReport
		if err := json.Unmarshal(lintOut, &lintRep); err != nil {
			t.Fatalf("unmarshal lint: %v\n%s", err, lintOut)
		}
		if lintRep.Files != 1 || lintRep.Warns != 1 || len(lintRep.Issues) != 1 || lintRep.Issues[0].Target != "definitely-broken-link" {
			t.Fatalf("expected hr-templater-doc.md checked with 1 broken link and templater-exec.md skipped, got %+v", lintRep)
		}

		fixOut, err := runCLIArgs(t, root, "fix", "--write", "--no-ai", "--json", "--porcelain")
		if err != nil {
			t.Fatalf("fix --write --no-ai: %v\n%s", err, fixOut)
		}
		var fixRep polish.FixVaultReport
		if err := json.Unmarshal(fixOut, &fixRep); err != nil {
			t.Fatalf("unmarshal fix: %v\n%s", err, fixOut)
		}
		if fixRep.FilesScanned != 1 || fixRep.LinksUnlinked != 1 {
			t.Fatalf("expected FilesScanned=1 and LinksUnlinked=1, got %+v", fixRep)
		}
		if gotTmpl, _ := os.ReadFile(tmplAbs); string(gotTmpl) != tmplExec {
			t.Fatalf("templater-exec.md was modified on disk")
		}
		if gotHR, _ := os.ReadFile(hrAbs); strings.Contains(string(gotHR), "[[definitely-broken-link]]") {
			t.Fatalf("expected broken link unwrapped in hr-templater-doc.md:\n%s", string(gotHR))
		}
	})

	t.Run("Bug 3 unique alias under title+basename double collision relinks cleanly and Probe 2E stays ambiguous", func(t *testing.T) {
		_, root := newContractVault(t)
		write := func(rel, content string) string {
			abs := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			return abs
		}

		write("a/item.md", "---\ntitle: SharedName\naliases:\n  - Unique Alias\ntype: note\nstatus: draft\n---\nA item.\n")
		write("b/item.md", "---\ntitle: Other Item\ntype: note\nstatus: draft\n---\nB item.\n")
		write("x/SharedName.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nX.\n")
		write("y/SharedName.md", "---\ntitle: SharedName\ntype: note\nstatus: draft\n---\nY.\n")

		// Double-colliding alias ambiguity peer (Probe 2E).
		write("notes/foo-a.md", "---\ntitle: UniqueA\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nFoo A.\n")
		write("other/dup.md", "---\ntitle: DupTitle\naliases:\n  - My Special Alias\ntype: note\nstatus: draft\n---\nOther dup.\n")
		write("z/dup.md", "---\ntitle: DupTitle\ntype: note\nstatus: draft\n---\nZ dup.\n")

		origSource := "---\ntitle: Source\ntype: note\nstatus: draft\n---\nUnique [[unique-alias]] and amb [[my-special-alias]].\n"
		srcAbs := write("source.md", origSource)

		out, err := runCLIArgs(t, root, "fix", "--write", "--no-ai", "--json", "--porcelain")
		if err != nil {
			t.Fatalf("fix --write --no-ai: %v\n%s", err, out)
		}
		var rep polish.FixVaultReport
		if err := json.Unmarshal(out, &rep); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, out)
		}
		if !rep.Written || rep.LinksUpdated != 1 || rep.LinksUnlinked != 0 {
			t.Fatalf("expected Written=true, LinksUpdated=1, LinksUnlinked=0, got %+v", rep)
		}
		if len(rep.Ambiguous) != 1 || rep.Ambiguous[0].Target != "my-special-alias" {
			t.Fatalf("expected Ambiguous for my-special-alias, got %+v", rep.Ambiguous)
		}
		gotBytes, _ := os.ReadFile(srcAbs)
		if !strings.Contains(string(gotBytes), "Unique [[a/item]] and amb [[my-special-alias]].") {
			t.Fatalf("unexpected source.md contents:\n%s", string(gotBytes))
		}

		if _, err := runCLIArgs(t, root, "fix", "--undo", "--json", "--porcelain"); err != nil {
			t.Fatalf("fix --undo: %v", err)
		}
		restored, _ := os.ReadFile(srcAbs)
		if string(restored) != origSource {
			t.Fatalf("source.md not restored byte-for-byte after undo")
		}
	})
}
