package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/testutil"
)

// TestPolishUndo_NoSnapshot needs no provider: undoing a note that was never
// polished is a friendly not-found, not a crash.
func TestPolishUndo_NoSnapshot(t *testing.T) {
	_, root := newContractVault(t)
	const name = "unpolished.md"
	doc := "---\ntitle: Unpolished\ntype: note\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(doc), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}

	out, err := runCLIArgs(t, root, "polish", name, "--undo")
	if err == nil {
		t.Fatalf("expected an error undoing a note with no snapshot, got none\n%s", out)
	}
	if !strings.Contains(err.Error(), "no polish snapshot") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPolishWriteSnapshotAndUndo_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	v, root := newContractVault(t)

	const name = "snap-note.md"
	doc := "---\ntitle: Snap Note\ntype: note\n---\n\n# Snap Note\n\nThis sentance has a typo to fix.\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(doc), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// Apply: writes the file AND records a snapshot of the original.
	if _, err := runCLIArgs(t, root, "polish", name, "--write", "--json", "--porcelain"); err != nil {
		t.Fatalf("polish --write: %v", err)
	}

	snap, err := polish.LoadSnapshot(v, name)
	if err != nil || snap == nil {
		t.Fatalf("snapshot missing after --write: snap=%v err=%v", snap, err)
	}
	if snap.OriginalFull != doc {
		t.Errorf("snapshot original_full_content is not byte-exact:\n got: %q\nwant: %q", snap.OriginalFull, doc)
	}
	written, _ := os.ReadFile(filepath.Join(root, name))
	if snap.PostWriteHash != polish.HashContent(written) {
		t.Errorf("post_write_hash does not match the written file")
	}
	if string(written) == doc {
		t.Errorf("polish --write did not change the file")
	}

	// Undo: restores the original byte-for-byte and consumes the snapshot.
	if _, err := runCLIArgs(t, root, "polish", name, "--undo"); err != nil {
		t.Fatalf("polish --undo: %v", err)
	}
	restored, _ := os.ReadFile(filepath.Join(root, name))
	if string(restored) != doc {
		t.Errorf("undo did not restore the original:\n got: %q\nwant: %q", string(restored), doc)
	}
	if gone, _ := polish.LoadSnapshot(v, name); gone != nil {
		t.Errorf("snapshot should be deleted after undo, got %+v", gone)
	}
}

func TestPolishUndo_RefusesAfterExternalEdit_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	_, root := newContractVault(t)

	const name = "edited-note.md"
	doc := "---\ntitle: Edited\ntype: note\n---\n\n# Edited\n\nThis sentance has a typo.\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(doc), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}
	if _, err := runCLIArgs(t, root, "polish", name, "--write", "--json", "--porcelain"); err != nil {
		t.Fatalf("polish --write: %v", err)
	}

	// User edits the note after polishing.
	edited := "---\ntitle: Edited\ntype: note\n---\n\n# Edited\n\nMy own new sentence.\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(edited), 0o644); err != nil {
		t.Fatalf("external edit: %v", err)
	}

	// Plain --undo must refuse.
	if _, err := runCLIArgs(t, root, "polish", name, "--undo"); err == nil {
		t.Fatalf("undo should refuse after an external edit")
	} else if !strings.Contains(err.Error(), "changed since") {
		t.Errorf("unexpected refusal message: %v", err)
	}
	if cur, _ := os.ReadFile(filepath.Join(root, name)); string(cur) != edited {
		t.Errorf("refused undo must leave the edited file intact")
	}

	// --undo --force discards the edit and restores the pre-polish original.
	if _, err := runCLIArgs(t, root, "polish", name, "--undo", "--force"); err != nil {
		t.Fatalf("polish --undo --force: %v", err)
	}
	if cur, _ := os.ReadFile(filepath.Join(root, name)); string(cur) != doc {
		t.Errorf("forced undo did not restore the original:\n got: %q\nwant: %q", string(cur), doc)
	}
}

func TestPolishLinks_GroundedAndNoInvented_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	_, root := newContractVault(t)

	// A real note that can be linked, plus a source that clearly references it.
	target := "---\ntitle: Auth Flow\ntype: note\n---\n\n# Auth Flow\n\nHow authentication works.\n"
	if err := os.WriteFile(filepath.Join(root, "auth-flow.md"), []byte(target), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	const src = "source.md"
	srcBody := "---\ntitle: Source\ntype: note\n---\n\n# Source\n\nThis design builds on the Auth Flow. The Auth Flow handles login and tokens.\n"
	if err := os.WriteFile(filepath.Join(root, src), []byte(srcBody), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "polish", src, "--links", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish --links: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}

	// Grounding invariant: the ONLY linkable note is "Auth Flow", so every added
	// link must be exactly that, the model cannot invent another target, and
	// StripInventedLinks would remove it if it tried.
	for _, target := range res.LinksAdded {
		if polish.NormalizeLinkKey(target) != "auth flow" {
			t.Errorf("invented/ungrounded link target %q in %v", target, res.LinksAdded)
		}
	}
}

func TestPolishLinks_NoMatchingNote_AddsNothing_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	_, root := newContractVault(t)

	// One unrelated note exists; the source talks about something with no note.
	other := "---\ntitle: Cooking Pasta\ntype: note\n---\n\n# Cooking Pasta\n\nBoil water.\n"
	if err := os.WriteFile(filepath.Join(root, "cooking.md"), []byte(other), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	const src = "physics.md"
	srcBody := "---\ntitle: Physics\ntype: note\n---\n\n# Physics\n\nQuantum entanglement links distant particles instantly.\n"
	if err := os.WriteFile(filepath.Join(root, src), []byte(srcBody), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	out, err := runCLIArgs(t, root, "polish", src, "--links", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish --links: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(res.LinksAdded) != 0 {
		t.Errorf("no note matches the prose, so no link should be added; got %v", res.LinksAdded)
	}
	if strings.Contains(res.Polished, "[[Quantum") {
		t.Errorf("model invented a wikilink to a nonexistent note:\n%s", res.Polished)
	}
}

// TestPolishLinks_SubstringFallback_NoEmbeddings_E2E_Bedrock seeds the vault via
// the indexing helper that records titles but NO embeddings, so --links must
// fall back to substring matching without erroring.
func TestPolishLinks_SubstringFallback_NoEmbeddings_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	v, root := newContractVault(t)

	testutil.CreateAndIndex(t, v, "Auth Flow", "note",
		"---\ntitle: Auth Flow\ntype: note\nstatus: draft\n---\n\n# Auth Flow\n\nHow auth works.\n")
	testutil.CreateAndIndex(t, v, "Source", "note",
		"---\ntitle: Source\ntype: note\nstatus: draft\n---\n\n# Source\n\nThis builds on the Auth Flow heavily.\n")

	// No embeddings exist (CreateAndIndex does not embed), so the semantic step
	// is skipped; substring matching still grounds "Auth Flow".
	out, err := runCLIArgs(t, root, "polish", "Source", "--links", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish --links must not error without embeddings: %v\n%s", err, out)
	}
	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	for _, target := range res.LinksAdded {
		if polish.NormalizeLinkKey(target) != "auth flow" {
			t.Errorf("ungrounded link target %q in %v", target, res.LinksAdded)
		}
	}
}
