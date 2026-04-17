package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
	Long:  "Builds the BM25 keyword index and, if AI is configured, the embedding index for semantic search. Safe to run repeatedly — only changed documents are re-embedded.",
	Example: `  2nb index                              # build / update the whole vault
  2nb index --doc my-note.md             # re-index one file (editors use this on save)
  2nb index --force-reembed              # invalidate all embeddings (use after switching providers)`,
	RunE: runIndex,
}

var (
	indexDocFlag     string
	indexForceReembed bool
)

func init() {
	indexCmd.GroupID = "ai"
	indexCmd.Flags().StringVar(&indexDocFlag, "doc", "", "Re-index a single document (relative or absolute path) instead of the whole vault")
	indexCmd.Flags().BoolVar(&indexForceReembed, "force-reembed", false, "Re-embed every document (use after intentionally switching AI providers)")
	_ = indexCmd.RegisterFlagCompletionFunc("doc", completeDocPaths)
	rootCmd.AddCommand(indexCmd)
}

// IndexDocResult is the JSON summary returned by `2nb index --doc <path>`.
// Editors use this to know whether a save triggered real re-embedding work.
type IndexDocResult struct {
	Path       string `json:"path"`
	Embedded   bool   `json:"embedded"`
	DurationMs int64  `json:"duration_ms"`
}

func runIndex(cmd *cobra.Command, args []string) error {
	v, err := openVaultAndSetActive()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	if indexDocFlag != "" {
		return runIndexSingleDoc(cmd, v, indexDocFlag)
	}

	startTime := time.Now()
	slog.Info("index started", "vault", v.Root)

	if !flagPorcelain {
		fmt.Fprintln(os.Stderr, "Indexing vault...")
	}

	stats, err := vault.IndexVault(v, func(path string) {
		slog.Debug("indexed file", "path", path)
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  %s\n", path)
		}
	})
	if err != nil {
		slog.Error("index failed", "error", err, "elapsed", time.Since(startTime))
		return fmt.Errorf("index vault: %w", err)
	}

	slog.Info("index complete",
		"docs", stats.DocsIndexed,
		"chunks", stats.ChunksCreated,
		"links", stats.LinksFound,
		"elapsed", time.Since(startTime),
	)

	// Generate embeddings if a provider is available
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	// --force-reembed clears embedding_hash on every embedded row so
	// DocumentsNeedingEmbedding returns all of them. Used when the
	// user intentionally switches providers and wants a full rebuild
	// immediately instead of per-document drift re-embedding.
	if indexForceReembed {
		n, err := v.DB.InvalidateAllEmbeddings()
		if err != nil {
			return fmt.Errorf("invalidate embeddings: %w", err)
		}
		slog.Info("force-reembed: invalidated embeddings", "count", n)
		if !flagPorcelain {
			fmt.Fprintf(os.Stderr, "  force-reembed: invalidated %d embeddings, re-embedding...\n", n)
		}
	}

	if err := embedDocuments(ctx, v, cfg); err != nil {
		slog.Debug("embedding skipped", "reason", err.Error())
		if !flagPorcelain {
			// When no provider is configured at all, guide the user
			// directly to `2nb ai setup` instead of printing a raw
			// registry-lookup error — that's the "just works"
			// onboarding path for receivers of a shipped vault.
			if cfg.Provider == "" {
				fmt.Fprintln(os.Stderr, "  no AI provider configured — run '2nb ai setup' to enable semantic search (BM25 index built)")
			} else {
				fmt.Fprintf(os.Stderr, "  embedding skipped: %v\n", err)
			}
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

func runIndexSingleDoc(cmd *cobra.Command, v *vault.Vault, docArg string) error {
	start := time.Now()

	absPath := docArg
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(v.Root, docArg)
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("resolve doc path: %w", err)
	}

	if err := vault.IndexSingleFile(v, absPath); err != nil {
		return err
	}

	// Re-run embeddings. DocumentsNeedingEmbedding will only include docs
	// whose content_hash changed since the last embed, so this is cheap when
	// nothing actually changed.
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI
	embedded := false
	if err := embedDocuments(ctx, v, cfg); err != nil {
		slog.Debug("incremental embed skipped", "reason", err.Error())
	} else {
		embedded = true
	}

	result := IndexDocResult{
		Path:       v.RelPath(absPath),
		Embedded:   embedded,
		DurationMs: time.Since(start).Milliseconds(),
	}

	if getFormat(cmd) == output.FormatJSON {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if !flagPorcelain {
		fmt.Printf("Indexed %s in %dms\n", result.Path, result.DurationMs)
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
		slog.Debug("all embeddings up to date", "model", model)
		if !flagPorcelain {
			fmt.Fprintln(os.Stderr, "  all embeddings up to date")
		}
		return nil
	}

	slog.Info("embedding documents", "count", len(docs), "model", model, "provider", cfg.Provider)
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "  embedding %d documents...\n", len(docs))
	}
	embedStart := time.Now()

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

	slog.Info("embedding complete", "embedded", embedded, "total", len(docs), "elapsed", time.Since(embedStart))
	return nil
}
