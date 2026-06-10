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

func TestParseHistoryJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr string // empty = success
	}{
		{"valid two turns", `[{"role":"user","content":"q"},{"role":"assistant","content":"a"}]`, 2, ""},
		{"empty array", `[]`, 0, ""},
		{"bad role", `[{"role":"system","content":"x"}]`, 0, "valid roles: user, assistant"},
		{"not an array", `{"role":"user"}`, 0, "must be a JSON array"},
		{"garbage", `not json`, 0, "must be a JSON array"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			turns, err := parseHistoryJSON([]byte(tt.input))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(turns) != tt.wantLen {
					t.Errorf("len = %d, want %d", len(turns), tt.wantLen)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want containing %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestParseHistoryJSON_RejectsOversizedPayload(t *testing.T) {
	// Build a syntactically valid array right at the byte cap; the size
	// rejection must fire before any JSON work.
	big := `[{"role":"user","content":"` + strings.Repeat("x", maxHistoryInputBytes) + `"}]`
	_, err := parseHistoryJSON([]byte(big))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("oversized payload not rejected: %v", err)
	}
}

func TestLoadHistoryArg_FilePath(t *testing.T) {
	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "history.json")
		if err := os.WriteFile(path, []byte(`[{"role":"user","content":"q"}]`), 0o644); err != nil {
			t.Fatal(err)
		}
		turns, err := loadHistoryArg(path)
		if err != nil {
			t.Fatalf("loadHistoryArg(file): %v", err)
		}
		if len(turns) != 1 || turns[0].Content != "q" {
			t.Errorf("turns = %+v, want one user turn", turns)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, err := loadHistoryArg(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Error("missing history file should error")
		}
	})
	t.Run("empty flag is no history", func(t *testing.T) {
		turns, err := loadHistoryArg("")
		if err != nil || turns != nil {
			t.Errorf("loadHistoryArg(\"\") = (%v, %v), want (nil, nil)", turns, err)
		}
	})
}

// TestAskWithHistory_E2E_Bedrock runs the full multi-turn pipeline against
// real Bedrock (no-mock policy; skips without credentials): index one doc
// with a known fact, ask a pronoun follow-up with --history from a file, and
// assert the answer is grounded in the document. The condense rewrite itself
// is asserted loosely in TestCondenseQuestion_Bedrock; here it just has to
// not break retrieval.
func TestAskWithHistory_E2E_Bedrock(t *testing.T) {
	if !ai.CheckBedrockCredentials(context.Background(), ai.DefaultAIConfig().Bedrock) {
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

	histPath := filepath.Join(t.TempDir(), "history.json")
	hist := `[{"role":"user","content":"tell me about the deploy rotation"},{"role":"assistant","content":"The deploy rotation is documented; engineers rotate."}]`
	if err := os.WriteFile(histPath, []byte(hist), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "ask", "--history", histPath, "which team owns it?", "--json")
	if err != nil {
		t.Fatalf("ask --history: %v", err)
	}
	var resp AskResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("decode envelope %q: %v", out, err)
	}
	if resp.Answer == "" {
		t.Fatal("empty answer")
	}
	// Grounding: the document's fact must reach the answer.
	if !strings.Contains(strings.ToLower(resp.Answer), "platform") {
		t.Errorf("answer not grounded in the document: %q", resp.Answer)
	}
	t.Logf("rewritten_query: %q", resp.RewrittenQuery)
}

// TestAskResponse_RewrittenQueryEnvelope pins the additive envelope contract:
// rewritten_query appears only when set, and its absence keeps the envelope
// byte-compatible with pre-multi-turn consumers (Swift CLIAskResponse, the
// Obsidian plugin's parseAskResponse).
func TestAskResponse_RewrittenQueryEnvelope(t *testing.T) {
	plain, err := json.Marshal(AskResponse{Mode: "hybrid", Answer: "a", Sources: []string{"s.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "rewritten_query") {
		t.Errorf("empty rewritten_query must be omitted, got %s", plain)
	}

	with, err := json.Marshal(AskResponse{Mode: "hybrid", Answer: "a", Sources: []string{"s.md"}, RewrittenQuery: "standalone q"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(with, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["rewritten_query"] != "standalone q" {
		t.Errorf("rewritten_query missing from envelope: %s", with)
	}
}
