package ai

import (
	"context"
	"fmt"
	"strings"
)

// ChatTurn is one prior message in a multi-turn conversation. Clients (the
// Obsidian plugin, `2nb chat`, scripts) hold the conversation and pass it
// with each ask; the engine stays stateless.
type ChatTurn struct {
	Role    string `json:"role"` // "user" or "assistant"
	Content string `json:"content"`
}

// History caps. Character-based, not token-based: no tokenizer exists in
// this codebase and chars/4 is an adequate budget proxy at this scale.
// The caps exist to keep small generation models focused (and costs flat),
// not to fit a context window: Haiku's window is far larger than this.
const (
	// MaxHistoryTurns is the maximum number of prior turns included.
	MaxHistoryTurns = 12
	// MaxHistoryTurnRunes truncates any single turn, mirroring the
	// 2000-rune chunk truncation in the ask retrieval path.
	MaxHistoryTurnRunes = 1500
	// MaxHistoryChars bounds the total serialized history.
	MaxHistoryChars = 8000
	// condenseHistoryTurns is the tighter tail the condense step sees.
	condenseHistoryTurns = 6
)

// TrimHistory enforces the history caps: most recent MaxHistoryTurns turns,
// each truncated to MaxHistoryTurnRunes (rune-safe), and oldest turns
// dropped until the total content is within MaxHistoryChars. Always applied
// engine-side so every caller (plugin, REPL, scripts) gets the same bound.
func TrimHistory(history []ChatTurn) []ChatTurn {
	if len(history) == 0 {
		return nil
	}
	if len(history) > MaxHistoryTurns {
		history = history[len(history)-MaxHistoryTurns:]
	}

	trimmed := make([]ChatTurn, len(history))
	for i, t := range history {
		runes := []rune(t.Content)
		if len(runes) > MaxHistoryTurnRunes {
			t.Content = string(runes[:MaxHistoryTurnRunes]) + "..."
		}
		trimmed[i] = t
	}

	// Drop oldest-first until the total fits the overall budget.
	total := 0
	for _, t := range trimmed {
		total += len(t.Content)
	}
	start := 0
	for start < len(trimmed)-1 && total > MaxHistoryChars {
		total -= len(trimmed[start].Content)
		start++
	}
	return trimmed[start:]
}

// serializeHistory renders turns as "User: ...\nAssistant: ..." lines for
// embedding in a prompt. Unknown roles render capitalized as-is so a
// malformed turn is visible rather than silently dropped.
func serializeHistory(history []ChatTurn) string {
	var b strings.Builder
	for i, t := range history {
		if i > 0 {
			b.WriteString("\n")
		}
		switch t.Role {
		case "user":
			b.WriteString("User: ")
		case "assistant":
			b.WriteString("Assistant: ")
		default:
			b.WriteString(t.Role + ": ")
		}
		b.WriteString(t.Content)
	}
	return b.String()
}

const condenseSystemPrompt = "You rewrite follow-up questions into standalone questions for searching a personal knowledge base. Output only the rewritten question. No preamble, no quotes, no explanation."

// CondenseQuestion rewrites a follow-up question into a standalone retrieval
// query using the conversation tail ("who owns it?" becomes "who owns the
// deploy rotation?"). With empty history the question is returned unchanged
// and no API call is made, so single-shot asks are unaffected. Callers must
// treat an error as non-fatal: fall back to the raw question and warn.
func CondenseQuestion(ctx context.Context, gen GenerationProvider, history []ChatTurn, question string) (string, error) {
	if len(history) == 0 {
		return question, nil
	}
	tail := TrimHistory(history)
	if len(tail) > condenseHistoryTurns {
		tail = tail[len(tail)-condenseHistoryTurns:]
	}

	prompt := fmt.Sprintf(`Conversation:
%s

Follow-up question: %s

Rewrite the follow-up question as a single standalone question that can be understood with no conversation context. Keep all specific names, paths, and terms. If it is already standalone, return it exactly unchanged.`, serializeHistory(tail), question)

	raw, err := gen.Generate(ctx, prompt, GenOpts{
		MaxTokens:    128,
		Temperature:  Ptr(0.0),
		SystemPrompt: condenseSystemPrompt,
	})
	if err != nil {
		return "", err
	}
	result, _ := sanitizeCondensed(raw, question)
	return result, nil
}

// sanitizeCondensed normalizes a condense-model response: first non-empty
// line, surrounding quotes stripped. Small models occasionally emit preamble
// or wrap the rewrite in quotes; an empty or implausibly long result falls
// back to the original question (reported via the second return).
func sanitizeCondensed(raw, fallback string) (string, bool) {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Trim(line, `"'`)
		line = strings.TrimSpace(line)
		if line == "" || len(line) > 500 {
			break
		}
		return line, false
	}
	return fallback, true
}
