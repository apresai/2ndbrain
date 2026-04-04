package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/spf13/cobra"
)

var askCmd = &cobra.Command{
	Use:   "ask <question>",
	Short: "Ask a question about your knowledge base (RAG)",
	Long:  "Uses hybrid search to find relevant documents, then generates an answer using the configured AI provider.",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runAsk,
}

func init() {
	rootCmd.AddCommand(askCmd)
}

func runAsk(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI
	question := strings.Join(args, " ")

	// Check generator availability
	generator, err := ai.DefaultRegistry.Generator(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no generation provider: %w\nRun `2nb ai status` to check provider configuration", err)
	}
	if !generator.Available(ctx) {
		return fmt.Errorf("generation provider %q not available", cfg.Provider)
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Searching for relevant context...\n")
	}

	// Search for relevant context
	engine := search.NewEngine(v.DB.Conn())
	opts := search.Options{Query: question, Limit: 5}

	var results []search.Result
	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	embCount, _ := v.DB.EmbeddingCount()

	if embErr == nil && embedder.Available(ctx) && embCount > 0 {
		queryVecs, err := embedder.Embed(ctx, []string{question})
		if err == nil && len(queryVecs) > 0 {
			docIDs, embeddings, _ := v.DB.AllEmbeddings()
			results, _, _ = engine.HybridSearch(opts, queryVecs[0], docIDs, embeddings)
		}
	}
	if results == nil {
		results, _ = engine.Search(opts)
	}

	if len(results) == 0 {
		return fmt.Errorf("no relevant documents found for: %s", question)
	}

	// Build RAG context from search results
	var chunks []ai.RAGChunk
	seen := make(map[string]bool)
	for _, r := range results {
		if r.Path == "" || seen[r.Path] {
			continue
		}
		seen[r.Path] = true
		content, err := os.ReadFile(filepath.Join(v.Root, r.Path))
		if err != nil {
			continue
		}
		// Truncate to first 2000 runes (M3: rune-safe)
		runes := []rune(string(content))
		if len(runes) > 2000 {
			runes = runes[:2000]
		}
		text := string(runes)
		if len(runes) == 2000 {
			text += "..."
		}
		chunks = append(chunks, ai.RAGChunk{Title: r.Title, Path: r.Path, Content: text})
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "Found %d relevant chunks. Generating answer...\n", len(chunks))
	}

	result, err := ai.RAG(ctx, generator, question, chunks)
	if err != nil {
		return fmt.Errorf("RAG failed: %w", err)
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, result)
	}

	fmt.Println(result.Answer)
	if len(result.Sources) > 0 && !flagPorcelain {
		fmt.Fprintf(os.Stderr, "\nSources: %s\n", strings.Join(result.Sources, ", "))
	}
	return nil
}
