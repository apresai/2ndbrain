package ai

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestTrimHistory(t *testing.T) {
	turn := func(role, content string) ChatTurn { return ChatTurn{Role: role, Content: content} }

	t.Run("empty stays empty", func(t *testing.T) {
		if got := TrimHistory(nil); got != nil {
			t.Errorf("TrimHistory(nil) = %v, want nil", got)
		}
	})

	t.Run("caps turn count keeping the most recent", func(t *testing.T) {
		var h []ChatTurn
		for i := 0; i < MaxHistoryTurns+5; i++ {
			h = append(h, turn("user", fmt.Sprintf("q%d", i)))
		}
		got := TrimHistory(h)
		if len(got) != MaxHistoryTurns {
			t.Fatalf("len = %d, want %d", len(got), MaxHistoryTurns)
		}
		if got[len(got)-1].Content != fmt.Sprintf("q%d", MaxHistoryTurns+4) {
			t.Errorf("last turn = %q, want the most recent", got[len(got)-1].Content)
		}
	})

	t.Run("truncates long turns rune-safely", func(t *testing.T) {
		// Multibyte content: must cut on rune boundaries, never mid-codepoint.
		long := strings.Repeat("ü", MaxHistoryTurnRunes+100)
		got := TrimHistory([]ChatTurn{turn("user", long)})
		if want := string([]rune(long)[:MaxHistoryTurnRunes]) + "..."; got[0].Content != want {
			t.Errorf("truncated content mismatch: len=%d", len(got[0].Content))
		}
	})

	t.Run("drops oldest turns to fit the total budget", func(t *testing.T) {
		// 10 turns of 1400 chars = 14000 chars total, over the 8000 budget.
		var h []ChatTurn
		for i := 0; i < 10; i++ {
			h = append(h, turn("user", fmt.Sprintf("%d:%s", i, strings.Repeat("x", 1400))))
		}
		got := TrimHistory(h)
		if len(got) >= 10 {
			t.Fatalf("expected oldest turns dropped, got all %d", len(got))
		}
		// The survivors must be the most recent ones.
		if !strings.HasPrefix(got[len(got)-1].Content, "9:") {
			t.Errorf("last survivor = %q..., want the newest turn", got[len(got)-1].Content[:2])
		}
		total := 0
		for _, tn := range got {
			total += len(tn.Content)
		}
		if total > MaxHistoryChars+1400 { // one turn of slack: cap drops until <= budget or one turn left
			t.Errorf("total after trim = %d, want near %d", total, MaxHistoryChars)
		}
	})
}

func TestSerializeHistory(t *testing.T) {
	got := serializeHistory([]ChatTurn{
		{Role: "user", Content: "what is the deploy rotation?"},
		{Role: "assistant", Content: "It is documented in runbook-deploy."},
		{Role: "tool", Content: "ignored-role content"},
	})
	want := "User: what is the deploy rotation?\nAssistant: It is documented in runbook-deploy.\ntool: ignored-role content"
	if got != want {
		t.Errorf("serializeHistory = %q, want %q", got, want)
	}
}

// TestBuildRAGPrompt_GoldenNoHistory pins the no-history prompt to the exact
// bytes ask/kb_ask send. If this fails, every existing ask/kb_ask caller's
// generation behavior changed; do not update the expectation without
// understanding that. The "concisely" adverb was removed deliberately in the
// prompt-tuning A/B (see buildRAGPrompt's comment / internal/eval); this golden
// value reflects that intended change.
func TestBuildRAGPrompt_GoldenNoHistory(t *testing.T) {
	parts := []string{
		"--- Doc A (a.md) ---\nalpha content",
		"--- Doc B (b.md) ---\nbeta content",
	}
	got := buildRAGPrompt("how does auth work?", nil, parts)
	want := `Based on the following documents from the knowledge base, answer this question: how does auth work?

--- Doc A (a.md) ---
alpha content

--- Doc B (b.md) ---
beta content

Answer based only on the provided documents. If the documents don't contain the answer, say so.`
	if got != want {
		t.Errorf("no-history prompt drifted from the golden bytes:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildRAGPrompt_WithHistory(t *testing.T) {
	parts := []string{"--- Doc A (a.md) ---\nalpha"}
	history := []ChatTurn{
		{Role: "user", Content: "tell me about auth"},
		{Role: "assistant", Content: "Auth uses JWT."},
	}
	got := buildRAGPrompt("who owns it?", history, parts)

	for _, must := range []string{
		"Conversation so far (for reference only; the documents below are the only source of facts):",
		"User: tell me about auth",
		"Assistant: Auth uses JWT.",
		"answer this question: who owns it?",
		"even if an earlier answer in the conversation suggested otherwise",
	} {
		if !strings.Contains(got, must) {
			t.Errorf("history prompt missing %q in:\n%s", must, got)
		}
	}
	// History must sit between the question line and the document chunks.
	if strings.Index(got, "Conversation so far") > strings.Index(got, "--- Doc A") {
		t.Error("history block must precede the document chunks")
	}
}

func TestSanitizeCondensed(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		want         string
		wantFallback bool
	}{
		{"clean", "Who owns the deploy rotation?", "Who owns the deploy rotation?", false},
		{"surrounding quotes", `"Who owns the deploy rotation?"`, "Who owns the deploy rotation?", false},
		{"single quotes", `'Who owns it?'`, "Who owns it?", false},
		{"leading blank lines", "\n\n  Who owns it?  \n", "Who owns it?", false},
		{"multi-line keeps first", "Who owns it?\nLet me explain why...", "Who owns it?", false},
		{"empty falls back", "   \n  ", "original question", true},
		{"quotes-only falls back", `""`, "original question", true},
		{"overlong falls back", strings.Repeat("x", 501), "original question", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fellBack := sanitizeCondensed(tt.raw, "original question")
			if got != tt.want || fellBack != tt.wantFallback {
				t.Errorf("sanitizeCondensed(%q) = (%q, %v), want (%q, %v)", tt.raw, got, fellBack, tt.want, tt.wantFallback)
			}
		})
	}
}

func TestCondenseQuestion_EmptyHistoryNoAPICall(t *testing.T) {
	// With no history the question must pass through without any provider
	// call: gen is nil and must not be touched.
	got, err := CondenseQuestion(context.Background(), nil, nil, "standalone question")
	if err != nil || got != "standalone question" {
		t.Errorf("CondenseQuestion(no history) = (%q, %v), want passthrough", got, err)
	}
}

// --- Real-Bedrock tests (no-mock policy: skip without credentials) --------

func TestCondenseQuestion_Bedrock(t *testing.T) {
	_, gen := requireBedrock(t)
	ctx := context.Background()

	history := []ChatTurn{
		{Role: "user", Content: "tell me about the deploy rotation schedule"},
		{Role: "assistant", Content: "The deploy rotation is documented in the runbook; engineers rotate weekly."},
	}
	got, err := CondenseQuestion(ctx, gen, history, "who owns it?")
	if err != nil {
		t.Fatalf("CondenseQuestion: %v", err)
	}
	// Loose assertion (LLM output): the rewrite must carry the topic forward.
	if !strings.Contains(strings.ToLower(got), "rotation") && !strings.Contains(strings.ToLower(got), "deploy") {
		t.Errorf("rewrite lost the topic: %q", got)
	}

	// A standalone question should survive roughly unchanged (topic intact).
	got2, err := CondenseQuestion(ctx, gen, history, "what is the database backup policy?")
	if err != nil {
		t.Fatalf("CondenseQuestion standalone: %v", err)
	}
	if !strings.Contains(strings.ToLower(got2), "backup") {
		t.Errorf("standalone question lost its topic: %q", got2)
	}
}

func TestRAGWithHistory_Grounding_Bedrock(t *testing.T) {
	_, gen := requireBedrock(t)
	ctx := context.Background()

	// Documents state Y; the conversation's assistant turn claims X.
	// The answer must follow the documents, not the history.
	chunks := []RAGChunk{{
		Title:   "Deploy Rotation",
		Path:    "deploy-rotation.md",
		Content: "The deploy rotation is owned by the Platform team. Rotation happens every two weeks.",
	}}
	history := []ChatTurn{
		{Role: "user", Content: "who owns the deploy rotation?"},
		{Role: "assistant", Content: "The deploy rotation is owned by the Security team."},
	}
	result, err := RAGWithHistory(ctx, gen, "remind me, which team owns the deploy rotation?", history, chunks)
	if err != nil {
		t.Fatalf("RAGWithHistory: %v", err)
	}
	lower := strings.ToLower(result.Answer)
	if !strings.Contains(lower, "platform") {
		t.Errorf("answer not grounded in documents (want Platform team): %q", result.Answer)
	}
}
