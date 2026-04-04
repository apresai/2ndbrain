package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var (
	searchType    string
	searchStatus  string
	searchTag     string
	searchLimit   int
	searchBM25Only bool
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search the vault with hybrid BM25 + semantic search",
	Args:  cobra.ArbitraryArgs,
	RunE:  runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&searchType, "type", "", "Filter by document type")
	searchCmd.Flags().StringVar(&searchStatus, "status", "", "Filter by status")
	searchCmd.Flags().StringVar(&searchTag, "tag", "", "Filter by tag")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "Maximum number of results")
	searchCmd.Flags().BoolVar(&searchBM25Only, "bm25-only", false, "Use keyword search only (skip vector search)")
	rootCmd.AddCommand(searchCmd)
}

func runSearch(cmd *cobra.Command, args []string) error {
	v, err := vault.Open(".")
	if err != nil {
		return fmt.Errorf("open vault: %w", err)
	}
	defer v.Close()

	// Ensure index exists
	var count int
	v.DB.Conn().QueryRow("SELECT COUNT(*) FROM documents").Scan(&count)
	if count == 0 {
		fmt.Fprintln(os.Stderr, "notice: index empty, building now...")
		if _, err := vault.IndexVault(v, nil); err != nil {
			return fmt.Errorf("build index: %w", err)
		}
	}

	query := strings.Join(args, " ")

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
	opts := search.Options{
		Query:    strings.TrimSpace(query),
		Type:     searchType,
		Status:   searchStatus,
		Tag:      searchTag,
		Limit:    searchLimit,
		BM25Only: searchBM25Only,
	}

	// Try hybrid search if embeddings are available
	var results []search.Result
	var mode search.SearchMode
	ctx := context.Background()
	cfg := v.Config.AI

	embedder, embErr := ai.DefaultRegistry.Embedder(cfg.Provider)
	embCount, _ := v.DB.EmbeddingCount()

	if !opts.BM25Only && embErr == nil && embedder.Available(ctx) && embCount > 0 && opts.Query != "" {
		// Embed the query
		queryVecs, err := embedder.Embed(ctx, []string{opts.Query})
		if err == nil && len(queryVecs) > 0 {
			docIDs, embeddings, err := v.DB.AllEmbeddings()
			if err == nil {
				results, mode, err = engine.HybridSearch(opts, queryVecs[0], docIDs, embeddings)
				if err != nil {
					return fmt.Errorf("hybrid search: %w", err)
				}
			}
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

	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "search mode: %s\n", mode)
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "No results found.")
		return output.Write(os.Stdout, getFormat(cmd), []search.Result{})
	}

	format := getFormat(cmd)
	return output.Write(os.Stdout, format, results)
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
