package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/retrieve"
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
	Warnings []string        `json:"warnings"`
	Results  []search.Result `json:"results"`
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search the vault with hybrid BM25 + semantic search",
	Long:  "Search your knowledge base using keywords and semantic similarity. Runs BM25 when AI isn't configured; runs hybrid (BM25 + vector) when embeddings are available. Inline filters like `type:adr` and `tag:auth` work inside the query string.",
	Example: `  2nb search "authentication"                       # hybrid search
  2nb search "how does auth work" --type adr        # filter by document type
  2nb search "retry tag:network"                    # inline tag filter
  2nb search "database" --bm25-only                 # skip semantic search`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSearch,
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

func runSearch(cmd *cobra.Command, args []string) (err error) {
	// cobra's MinimumNArgs(1) refuses an ABSENT query, but a blank one used to
	// reach the query-less listing path and return EVERY document with score 0,
	// rendered as ranked hits. `2nb search ""` is what an empty shell variable
	// produces, so the caller least able to notice got the most misleading
	// answer. Refuse it here, matching kb_search on the MCP surface.
	if strings.TrimSpace(strings.Join(args, " ")) == "" {
		return fmt.Errorf("search needs a query; pass one, or use `2nb list` with filters to enumerate documents")
	}
	// Before anything with a side effect. A hybrid search embeds the query,
	// which is a paid provider call, and there is no point paying for a result
	// this format cannot render.
	if err := refuseBodylessFormat(cmd, "search"); err != nil {
		return err
	}
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

	threshold := searchThreshold
	if threshold == 0 {
		threshold, _ = v.Config.AI.ResolveSimilarityThresholdFull(v.Root)
	}

	// initAIProviders must run before retrieve.New resolves the active embedder.
	initAIProviders(v)
	var results []search.Result
	var mode search.SearchMode
	var warnings []string
	ctx := context.Background()

	// Record the query (best-effort) on every return path, including
	// zero-result and error cases. mode is "hybrid" or "keyword".
	defer func() {
		// Token estimate: a hybrid search embeds the query once (chars/4); a
		// BM25-only search makes no embedding call.
		inTok := 0
		if mode == search.ModeHybrid {
			inTok = len(query) / 4
		}
		recordMetric(v, metrics.Operation{
			Operation:   metrics.OpSearch,
			DurationMs:  time.Since(startTime).Milliseconds(),
			OK:          err == nil,
			Error:       errString(err),
			ResultCount: len(results),
			Mode:        string(mode),
			InputTokens: inTok,
			// ctx carries the counter slowCallNotice attached below; the
			// closure reads the variable, so this is the query embedding's
			// real retry count.
			EmbedRetries: ai.RetriesFrom(ctx),
		})
	}()

	// The query embedding is one provider call and can be the whole latency of
	// a search on a throttled account (10.8s measured); say so rather than
	// looking hung.
	ctx, stopNotice := slowCallNotice(ctx, "embedding query")
	res, rerr := retrieve.New(v).Retrieve(ctx, retrieve.Options{
		Query:     strings.TrimSpace(query),
		Type:      searchType,
		Status:    searchStatus,
		Tag:       searchTag,
		Limit:     searchLimit,
		BM25Only:  searchBM25Only,
		Threshold: threshold,
	})
	stopNotice()
	if rerr != nil {
		return rerr
	}
	results, mode, warnings = res.Results, res.Mode, res.Warnings
	// Surface any degradation (compat / embed / hybrid failure) loudly, matching
	// the prior inline stderr behavior; the warnings also ride the --json envelope.
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "  "+w)
	}

	slog.Info("search complete", "query", query, "results", len(results), "elapsed", time.Since(startTime))
	if !flagPorcelain {
		fmt.Fprintf(os.Stderr, "search mode: %s\n", mode)
	}

	format := getFormat(cmd)
	if len(results) == 0 {
		if !flagPorcelain {
			fmt.Fprintln(os.Stderr, "No results found. Try broader terms, remove filters, or run `2nb list` to see all documents.")
		} else {
			fmt.Fprintln(os.Stderr, "No results found.")
		}
		// Deliberately NOT returning here. The hint above is stderr, for a human;
		// a machine consumer still needs a parseable document on stdout. Returning
		// early emitted nothing at all, so `search --json | jq` failed on the
		// ordinary case of a query that matched nothing. Worse: a DEGRADED vault
		// with zero results returned no `mode` and no `warnings` either, so an
		// agent could not tell "nothing matched" from "semantic search is off".
		// Scoped to JSON deliberately: it is the format with a documented
		// envelope. csv/tsv/raw would be CORRUPTED by a literal "[]" where an
		// empty stream is the correct rendering of zero rows, so they keep
		// returning nothing, and so does the human path.
		if format != output.FormatJSON {
			return nil
		}
	}

	if format == output.FormatJSON {
		// JSON consumers (the Swift macOS app, automation) need to see
		// mode + warnings alongside results, so the `--json` output is
		// wrapped in an envelope. CSV/YAML stay flat — envelope only
		// makes sense for JSON.
		return writeOut(cmd, format, SearchResponse{
			Mode:     string(mode),
			Warnings: nonNilSlice(warnings),
			Results:  nonNilSlice(results),
		})
	}
	if format != "" {
		return writeOut(cmd, format, results)
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
