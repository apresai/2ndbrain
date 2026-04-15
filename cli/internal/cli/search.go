package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	searchType      string
	searchStatus    string
	searchTag       string
	searchLimit     int
	searchBM25Only  bool
	searchThreshold float64
)

// SearchResponse is the JSON envelope returned by `2nb search --json`.
// Consumers that previously decoded []search.Result must now extract
// `.results` — this is a breaking change to the JSON contract,
// acceptable at pre-1.0 and documented in the release notes.
type SearchResponse struct {
	Mode     string          `json:"mode"` // "hybrid" or "keyword"
	Warnings []string        `json:"warnings,omitempty"`
	Results  []search.Result `json:"results"`
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search the vault with hybrid BM25 + semantic search",
	Long:  "Search your knowledge base using keywords and semantic similarity.\nExamples:\n  2nb search \"authentication\"\n  2nb search \"how does auth work\" --type adr",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&searchType, "type", "", "Filter by document type")
	searchCmd.Flags().StringVar(&searchStatus, "status", "", "Filter by status")
	searchCmd.Flags().StringVar(&searchTag, "tag", "", "Filter by tag")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "Maximum number of results")
	searchCmd.Flags().BoolVar(&searchBM25Only, "bm25-only", false, "Use keyword search only (skip vector search)")
	searchCmd.Flags().Float64Var(&searchThreshold, "threshold", 0, "Minimum cosine similarity for vector hits (default: ai.similarity_threshold, typically 0.20)")
	searchCmd.GroupID = "ai"
	rootCmd.AddCommand(searchCmd)
}

func runSearch(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	// Ensure index exists
	var count int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&count)
	if count == 0 {
		fmt.Fprintln(os.Stderr, "Vault hasn't been indexed yet — building index now...")
		if _, err := vault.IndexVault(v, nil); err != nil {
			return fmt.Errorf("build index: %w", err)
		}
	}

	query := strings.Join(args, " ")
	startTime := time.Now()
	slog.Info("search", "query", query)

	// Parse inline filters from query: tag:foo type:adr status:accepted
	query, inlineType := extractPrefix(query, "type:")
	query, inlineStatus := extractPrefix(query, "status:")
	query, inlineTag := extractPrefix(query, "tag:")

	if inlineType != "" && searchType == "" {
		searchType = inlineType
	}
	if inlineStatus != "" && searchStatus == "" {
		searchStatus = inlineStatus
	}
	if inlineTag != "" && searchTag == "" {
		searchTag = inlineTag
	}

	engine := search.NewEngine(v.DB.Conn())
	threshold := searchThreshold
	if threshold == 0 {
		threshold = v.Config.AI.ResolveSimilarityThreshold()
	}
	opts := search.Options{
		Query:          strings.TrimSpace(query),
		Type:           searchType,
		Status:         searchStatus,
		Tag:            searchTag,
		Limit:          searchLimit,
		BM25Only:       searchBM25Only,
		MinVectorScore: threshold,
	}

	// Try hybrid search if embeddings are available
	initAIProviders(v)
	var results []search.Result
	var mode search.SearchMode
	var warnings []string
	ctx := context.Background()
	cfg := v.Config.AI

	embedder, _ := ai.DefaultRegistry.Embedder(cfg.Provider)

	// VectorCompat is the single decision point for "can we run hybrid?".
	// It returns a human message when the vault's embeddings are
	// incompatible with the current provider (dim break, model mismatch,
	// provider unavailable) so the user sees the degradation instead of
	// silently falling back to BM25.
	if !opts.BM25Only && opts.Query != "" {
		if ready, msg := VectorCompat(ctx, v, embedder); !ready {
			if msg != "" {
				fmt.Fprintln(os.Stderr, "  "+msg)
				warnings = append(warnings, msg)
			}
			opts.BM25Only = true
		}
	}

	if !opts.BM25Only && opts.Query != "" {
		// VectorCompat passed — embedder is usable and dim matches.
		queryVecs, err := embedder.Embed(ctx, []string{opts.Query})
		if err == nil && len(queryVecs) > 0 {
			docIDs, embeddings, err := v.DB.AllEmbeddings()
			if err == nil {
				results, mode, err = engine.HybridSearch(opts, queryVecs[0], docIDs, embeddings)
				if err != nil {
					return fmt.Errorf("hybrid search: %w", err)
				}
			}
		} else if err != nil {
			// Provider was Available() but Embed() failed — most common
			// cause is Ollama's daemon up but model not pulled. Warn
			// loudly and degrade to BM25 so the user can diagnose.
			msg := fmt.Sprintf("semantic search disabled: embedder returned error (%v) — if using Ollama, verify the model is pulled", err)
			fmt.Fprintln(os.Stderr, "  "+msg)
			warnings = append(warnings, msg)
		}
	}

	// Fall back to BM25 if hybrid didn't run
	if results == nil {
		var err error
		results, err = engine.Search(opts)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		mode = search.ModeKeyword
	}

	slog.Info("search complete", "query", query, "results", len(results), "elapsed", time.Since(startTime))
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "search mode: %s\n", mode)
	}

	if len(results) == 0 {
		if !flagPorcelain {
			fmt.Fprintln(os.Stderr, "No results found. Try broader terms, remove filters, or run `2nb list` to see all documents.")
		} else {
			fmt.Fprintln(os.Stderr, "No results found.")
		}
		return nil
	}

	format := getFormat(cmd)
	if format == output.FormatJSON {
		// JSON consumers (the Swift macOS app, automation) need to see
		// mode + warnings alongside results, so the `--json` output is
		// wrapped in an envelope. CSV/YAML stay flat — envelope only
		// makes sense for JSON.
		return output.Write(os.Stdout, format, SearchResponse{
			Mode:     string(mode),
			Warnings: warnings,
			Results:  results,
		})
	}
	if format != "" {
		return output.Write(os.Stdout, format, results)
	}

	// Pretty output
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = r.Path
		}
		fmt.Printf("%d. %s", i+1, title)
		if r.DocType != "" {
			fmt.Printf(" [%s]", r.DocType)
		}
		if r.VectorScore > 0 {
			fmt.Printf(" (rrf=%.3f, cos=%.3f)\n", r.Score, r.VectorScore)
		} else {
			fmt.Printf(" (rrf=%.3f)\n", r.Score)
		}
		if r.Content != "" {
			// Show first 120 chars of content
			snippet := r.Content
			if len(snippet) > 120 {
				snippet = snippet[:120] + "..."
			}
			fmt.Printf("   %s\n", snippet)
		}
		if r.Path != "" {
			fmt.Printf("   %s\n", r.Path)
		}
		fmt.Println()
	}
	return nil
}

// extractPrefix extracts a "prefix:value" from the query string.
func extractPrefix(query, prefix string) (string, string) {
	idx := strings.Index(query, prefix)
	if idx == -1 {
		return query, ""
	}

	rest := query[idx+len(prefix):]
	// Find end of value (next space or end of string)
	end := strings.IndexByte(rest, ' ')
	var value string
	if end == -1 {
		value = rest
		rest = ""
	} else {
		value = rest[:end]
		rest = rest[end+1:]
	}

	before := query[:idx]
	return strings.TrimSpace(before + " " + rest), value
}
