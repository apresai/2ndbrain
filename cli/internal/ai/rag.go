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

	prompt := fmt.Sprintf(`Based on the following documents from the knowledge base, answer this question: %s

%s

Answer concisely based only on the provided documents. If the documents don't contain the answer, say so.`, question, strings.Join(contextParts, "\n\n"))

	answer, err := gen.Generate(ctx, prompt, GenOpts{
		MaxTokens:    512,
		Temperature:  0.1,
		SystemPrompt: "You are a helpful assistant answering questions about a knowledge base. Use only the provided context to answer.",
	})
	if err != nil {
		return nil, fmt.Errorf("generation: %w", err)
	}

	return &RAGResult{Answer: answer, Sources: sources}, nil
}

// RAGChunk is a document excerpt for RAG context.
type RAGChunk struct {
	Title   string
	Path    string
	Content string
}
