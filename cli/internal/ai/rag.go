package ai

import (
	"context"
	"fmt"
	"strings"
)

// RAGResult is the output of a RAG Q&A pipeline.
type RAGResult struct {
	Answer  string   `json:"answer"`
	Sources []string `json:"sources"`
}

// RAG executes a retrieval-augmented generation pipeline.
// contextChunks are pre-retrieved document excerpts with titles and paths.
func RAG(ctx context.Context, gen GenerationProvider, question string, contextChunks []RAGChunk) (*RAGResult, error) {
	return RAGWithHistory(ctx, gen, question, nil, contextChunks)
}

const ragSystemPrompt = "You are a helpful assistant answering questions about a knowledge base. Use only the provided context to answer."

// historySystemSuffix is appended to the system prompt only when history is
// present: prior assistant answers must stay subordinate to the retrieved
// documents, or the model treats its own earlier (possibly wrong) answers
// as established facts.
const historySystemSuffix = " The conversation history in the prompt is context, not a source of facts."

// RAGWithHistory is RAG with optional multi-turn conversation context. The
// history is serialized into the single generation prompt (no provider
// messages-array API exists, and history-as-data keeps small models grounded
// in the documents rather than in their own prior answers). The question
// should be the user's original wording; history-aware retrieval rewriting
// happens upstream via CondenseQuestion. With nil history the behavior is
// byte-identical to the original single-shot RAG (pinned by a golden test).
func RAGWithHistory(ctx context.Context, gen GenerationProvider, question string, history []ChatTurn, contextChunks []RAGChunk) (*RAGResult, error) {
	if len(contextChunks) == 0 {
		return nil, fmt.Errorf("no context chunks provided")
	}

	var contextParts []string
	var sources []string
	seen := make(map[string]bool)
	for _, c := range contextChunks {
		contextParts = append(contextParts, fmt.Sprintf("--- %s (%s) ---\n%s", c.Title, c.Path, c.Content))
		if c.Path != "" && !seen[c.Path] {
			sources = append(sources, c.Path)
			seen[c.Path] = true
		}
	}

	history = TrimHistory(history)
	prompt := buildRAGPrompt(question, history, contextParts)
	system := ragSystemPrompt
	if len(history) > 0 {
		system += historySystemSuffix
	}

	answer, err := gen.Generate(ctx, prompt, GenOpts{
		MaxTokens:    512,
		Temperature:  Ptr(0.1),
		SystemPrompt: system,
	})
	if err != nil {
		return nil, fmt.Errorf("generation: %w", err)
	}

	return &RAGResult{Answer: answer, Sources: sources}, nil
}

// buildRAGPrompt assembles the generation prompt. With empty history the
// output must remain byte-identical to the pre-multi-turn prompt so every
// existing ask/kb_ask caller's behavior is unchanged; a golden test pins it.
func buildRAGPrompt(question string, history []ChatTurn, contextParts []string) string {
	if len(history) == 0 {
		return fmt.Sprintf(`Based on the following documents from the knowledge base, answer this question: %s

%s

Answer concisely based only on the provided documents. If the documents don't contain the answer, say so.`, question, strings.Join(contextParts, "\n\n"))
	}

	return fmt.Sprintf(`Based on the following documents from the knowledge base, answer this question: %s

Conversation so far (for reference only; the documents below are the only source of facts):
%s

%s

Answer concisely based only on the provided documents. Use the conversation only to understand what the user is referring to. If the documents don't contain the answer, say so, even if an earlier answer in the conversation suggested otherwise.`,
		question, serializeHistory(history), strings.Join(contextParts, "\n\n"))
}

// RAGChunk is a document excerpt for RAG context.
type RAGChunk struct {
	Title   string
	Path    string
	Content string
}
