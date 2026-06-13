package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// polish --write contract test.
//
// No-mock policy: this drives real Bedrock and skips without credentials,
// mirroring chat_repl_test.go. It seeds a note with an obvious typo, runs
// `polish <path> --write`, and asserts the body was rewritten in place while the
// JSON still carries original + polished for audit. A second assertion confirms
// the default (no --write) leaves the file untouched.

func TestPolishWrite_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	_, root := newContractVault(t)

	const name = "typo-note.md"
	const typo = "This sentance has a deliberate misspeling in it."
	doc := "---\ntitle: Typo Note\ntype: note\n---\n\n# Typo Note\n\n" + typo + "\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(doc), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	// --write: file is rewritten in place.
	out, err := runCLIArgs(t, root, "polish", name, "--write", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish --write: %v\n%s", err, out)
	}

	var res PolishResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal polish result: %v\n%s", err, out)
	}
	if res.Original == "" || res.Polished == "" {
		t.Fatalf("polish result missing original/polished for audit: %+v", res)
	}
	if !strings.Contains(res.Original, "misspeling") {
		t.Errorf("original should preserve the input typo: %q", res.Original)
	}

	// The on-disk body now equals the polished text (frontmatter preserved).
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read back note: %v", err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, "---") || !strings.Contains(text, "title: Typo Note") {
		t.Errorf("frontmatter lost after --write:\n%s", text)
	}
	if !strings.Contains(text, res.Polished) {
		t.Errorf("on-disk body does not contain polished text after --write:\nfile=%q\npolished=%q", text, res.Polished)
	}
}

func TestPolishWrite_DefaultLeavesFileUntouched_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	_, root := newContractVault(t)

	const name = "preview-note.md"
	const body = "This sentance has a typo but the file must not change without --write."
	doc := "---\ntitle: Preview Note\ntype: note\n---\n\n# Preview Note\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(root, name), []byte(doc), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	before, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	// Default polish (no --write) is preview only: emit JSON, write nothing.
	out, err := runCLIArgs(t, root, "polish", name, "--json", "--porcelain")
	if err != nil {
		t.Fatalf("polish preview: %v\n%s", err, out)
	}

	after, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("default polish must not modify the file:\nbefore=%q\nafter=%q", string(before), string(after))
	}
}
