package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestChatLoop_E2E_Bedrock drives the REPL with a scripted stdin against real
// Bedrock (no-mock policy; skips without credentials): a question, then a
// pronoun follow-up that only answers correctly if the first turn's context
// carried over, then exit.
func TestChatLoop_E2E_Bedrock(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}
	_, root := newContractVault(t)

	doc := "---\ntitle: Deploy Rotation\ntype: note\n---\n# Deploy Rotation\n\nThe deploy rotation is owned by the Platform team. Rotation happens every two weeks.\n"
	if err := os.WriteFile(filepath.Join(root, "deploy-rotation.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "index"); err != nil {
		t.Fatalf("index: %v", err)
	}

	v, err := vault.Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer v.Close()
	initAIProviders(v)
	generator, err := ai.DefaultRegistry.Generator(v.Config.AI.Provider)
	if err != nil {
		t.Skipf("generator unavailable: %v", err)
	}

	stdin := strings.NewReader("what is the deploy rotation?\nwhich team owns it?\nexit\n")
	var out bytes.Buffer
	if err := chatLoop(ctx, v, generator, stdin, &out); err != nil {
		t.Fatalf("chatLoop: %v", err)
	}

	answers := strings.ToLower(out.String())
	if !strings.Contains(answers, "rotation") {
		t.Errorf("first answer missing topic: %q", out.String())
	}
	// The follow-up only resolves "it" via the carried conversation.
	if !strings.Contains(answers, "platform") {
		t.Errorf("follow-up answer not grounded via history: %q", out.String())
	}
}

// TestChatLoop_ErrorContinuesSession is API-free: asking in an EMPTY vault
// makes askOnce fail with "no relevant documents" before the generator is
// ever touched (so nil is safe), and the session must survive the error,
// record no history, and still exit cleanly on the next command.
func TestChatLoop_ErrorContinuesSession(t *testing.T) {
	_, root := newContractVault(t) // empty vault: retrieval finds nothing
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	stdin := strings.NewReader("what is the deploy rotation?\nexit\n")
	var out bytes.Buffer
	if err := chatLoop(context.Background(), v, nil, stdin, &out); err != nil {
		t.Fatalf("chatLoop must survive a failed ask, got: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("failed ask must produce no answer, got %q", out.String())
	}
}

// TestChatLoop_ExitImmediately is API-free: an empty session must end cleanly
// without touching the provider (generator is never called for blank input,
// "exit", or EOF).
func TestChatLoop_ExitImmediately(t *testing.T) {
	_, root := newContractVault(t)
	v, err := vault.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	for name, script := range map[string]string{
		"exit":  "exit\n",
		"quit":  "quit\n",
		"blank": "\n\n",
		"eof":   "",
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := chatLoop(context.Background(), v, nil, strings.NewReader(script), &out); err != nil {
				t.Fatalf("chatLoop(%q): %v", script, err)
			}
			if out.Len() != 0 {
				t.Errorf("no answers expected, got %q", out.String())
			}
		})
	}
}
