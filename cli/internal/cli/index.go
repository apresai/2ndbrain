package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Build or rebuild the vault search index",
	RunE:  runIndex,
}

func init() {
	indexCmd.GroupID = "ai"
	rootCmd.AddCommand(indexCmd)
}

func runIndex(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()

	if !flagPorcelain {
		fmt.Fprintln(os.Stderr, "Indexing vault...")
	}

	stats, err := vault.IndexVault(v, func(path string) {
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
	})
	if err != nil {
		return fmt.Errorf("index vault: %w", err)
	}

	// Generate embeddings if a provider is available
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI
	if err := embedDocuments(ctx, v, cfg); err != nil {
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  embedding skipped: %v\n", err)
		}
	}

	format := getFormat(cmd)
	if format != "" {
		return output.Write(os.Stdout, format, stats)
	}

	if !flagPorcelain {
		fmt.Printf("Indexed %d files, %d chunks, %d links\n", stats.DocsIndexed, stats.ChunksCreated, stats.LinksFound)
		if stats.DocsIndexed > 0 {
			fmt.Fprintln(os.Stderr, "\nReady to search:")
			fmt.Fprintln(os.Stderr, "  2nb search \"your query\"")
			fmt.Fprintln(os.Stderr, "  2nb ask \"your question\"")
		}
	}
	return nil
}

func embedDocuments(ctx context.Context, v *vault.Vault, cfg ai.AIConfig) error {
	embedder, err := ai.DefaultRegistry.Embedder(cfg.Provider)
	if err != nil {
		return fmt.Errorf("no embedding provider %q", cfg.Provider)
	}

	if !embedder.Available(ctx) {
		return fmt.Errorf("provider %q not available", cfg.Provider)
	}

	model := cfg.EmbeddingModel
	docs, err := v.DB.DocumentsNeedingEmbedding(model)
	if err != nil {
		return err
	}

	if len(docs) == 0 {
		if !flagPorcelain {
			fmt.Fprintln(os.Stderr, "  all embeddings up to date")
		}
		return nil
	}

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "  embedding %d documents...\n", len(docs))
	}

	// Look up model pricing for cost estimate
	var pricePerMillion float64
	if models, err := embedder.ListModels(ctx); err == nil && len(models) > 0 {
		pricePerMillion = models[0].PriceIn
	}
	var totalChars int
	embedded := 0

	for i, doc := range docs {
		if i > 0 && cfg.Provider == "openrouter" {
			time.Sleep(100 * time.Millisecond) // throttle to ~10 rps, well under 20 rpm free limit
		}

		absPath := filepath.Join(v.Root, doc.Path)
		parsed, err := document.ParseFile(absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", doc.Path, err)
			continue
		}

		vecs, err := embedder.Embed(ctx, []string{parsed.Body})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  embed error %s: %v\n", doc.Path, err)
			continue
		}

		parsed.ComputeContentHash()
		if err := v.DB.SetEmbedding(doc.ID, vecs[0], model, parsed.ContentHash); err != nil {
			fmt.Fprintf(os.Stderr, "  store error %s: %v\n", doc.Path, err)
			continue
		}

		totalChars += len(parsed.Body)
		embedded++

		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  embedded %d/%d: %s\n", i+1, len(docs), doc.Path)
		}
	}

	// Show cost estimate for non-free providers
	if !flagPorcelain && embedded > 0 {
		estimatedTokens := float64(totalChars) / 4.0 // rough chars→tokens estimate
		if pricePerMillion > 0 {
			cost := (estimatedTokens / 1_000_000) * pricePerMillion
			fmt.Fprintf(os.Stderr, "  cost estimate: $%.4f (%s)\n", cost, model)
			// Suggest local alternative if extrapolated monthly cost > $3
			monthlyCost := cost * 4 // assume ~4 re-indexes per month
			if monthlyCost > 3.0 {
				fmt.Fprintf(os.Stderr, "  estimated monthly cost: ~$%.2f\n", monthlyCost)
				fmt.Fprintf(os.Stderr, "  tip: run `2nb ai setup` to configure free local AI with Ollama\n")
			}
		} else {
			fmt.Fprintf(os.Stderr, "  cost: free (%s)\n", model)
		}
	}

	return nil
}
