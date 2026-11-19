package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/vault"
	"github.com/spf13/cobra"
)

var suggestLinksCmd = &cobra.Command{
	Use:   "suggest-links <path>",
	Short: "Suggest semantically related documents to link from a given document",
	Long: `Uses the configured embedding provider to find the most similar documents
to the target and emits them as a ranked list. Results exclude the source
document itself and any documents it already links to via [[wikilinks]].

The editor uses this to power the Suggest Links panel: click a suggestion
and a wikilink is inserted at the cursor.`,
	Args: cobra.ExactArgs(1),
	RunE: runSuggestLinks,
}

var suggestLinksLimit int

func init() {
	suggestLinksCmd.GroupID = "ai"
	suggestLinksCmd.Flags().IntVar(&suggestLinksLimit, "limit", 10, "Maximum number of suggestions")
	rootCmd.AddCommand(suggestLinksCmd)
}

// SuggestLinkResult is one ranked suggestion returned by `2nb suggest-links`
// and `2nb suggest-target`. Aliased to polish.SuggestLinkResult so the shared
// polish engine and CLI emit the identical JSON schema.
type SuggestLinkResult = polish.SuggestLinkResult

func runSuggestLinks(cmd *cobra.Command, args []string) error {
	// Before the provider work below. This command embeds the source note, a
	// paid call, and asks the embedder whether it is ready, which FAILS on a
	// machine with no credentials. So --format raw/md never reached the refusal
	// and the command errored on credentials instead of on the format it cannot
	// render. Refusing up front costs nothing and is what search and list do.
	if err := refuseBodylessFormat(cmd, "suggest-links"); err != nil {
		return err
	}
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	relArg := args[0]
	absPath := relArg
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(v.Root, relArg)
	}
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("resolve doc path: %w", err)
	}

	parsed, err := document.ParseFile(absPath)
	if err != nil {
		return fmt.Errorf("parse source: %w", err)
	}
	parsed.Path = v.RelPath(absPath)

	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI

	embedProvider := cfg.EffectiveEmbeddingProvider()
	embedder, err := ai.DefaultRegistry.Embedder(embedProvider)
	if err != nil {
		return fmt.Errorf("no embedding provider: %w\nRun `2nb ai status` to check provider configuration", err)
	}
	if ready, code := ai.Availability(ctx, embedder); !ready {
		return embedderNotReadyError(embedProvider, code)
	}

	// Truncate body to the same window used by `ask.go`
	runes := []rune(parsed.Body)
	if len(runes) > 2000 {
		runes = runes[:2000]
	}
	queryText := string(runes)

	start := time.Now()
	queryVecs, err := embedder.Embed(ctx, []string{queryText}, ai.WithPurpose(ai.PurposeQuery))
	{
		op := metrics.Operation{
			Operation:  metrics.OpSuggest,
			DurationMs: time.Since(start).Milliseconds(),
			OK:         err == nil && len(queryVecs) > 0,
			Error:      errString(err),
		}
		if err == nil && len(queryVecs) > 0 {
			attachUsageLegs(&op, buildUsageLeg(v.Root, embedProvider, cfg.EmbeddingModel, "embedding", metrics.OpSuggest, 1, max(1, len(queryText)/4), 0))
		}
		recordMetric(v, op)
	}
	if err != nil {
		return fmt.Errorf("embed source: %w", err)
	}
	if len(queryVecs) == 0 {
		return fmt.Errorf("embedder returned no vectors")
	}

	docIDs, embeddings, err := v.DB.AllEmbeddings()
	if err != nil {
		return fmt.Errorf("load embeddings: %w", err)
	}

	// Over-fetch so we still hit limit after filtering out the source doc and
	// docs it already links to. Apply the vault's similarity threshold so we
	// don't suggest links to docs that happen to be the nearest neighbors
	// but aren't actually related.
	threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
	scored := search.VectorSearchThreshold(
		queryVecs[0], docIDs, embeddings, suggestLinksLimit*3,
		threshold,
	)

	// Resolve the source doc ID and its outgoing links for exclusion.
	var sourceID string
	if dbDoc, err := v.DB.GetDocumentByPath(parsed.Path); err == nil && dbDoc != nil {
		sourceID = dbDoc.ID
	}
	linkedTargets := make(map[string]bool)
	if sourceID != "" {
		rows, err := v.DB.Conn().Query(
			`SELECT target_id FROM links WHERE source_id = ? AND target_id IS NOT NULL AND target_id != ''`,
			sourceID,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var targetID string
				if err := rows.Scan(&targetID); err == nil {
					linkedTargets[targetID] = true
				}
			}
		}
	}

	engine := search.NewEngine(v.DB.Conn())
	results := make([]SuggestLinkResult, 0, suggestLinksLimit)
	for _, s := range scored {
		if s.DocID == sourceID {
			continue
		}
		if linkedTargets[s.DocID] {
			continue
		}
		lookup, ok := engine.GetDocumentByID(s.DocID)
		if !ok {
			continue
		}
		snippet := snippetFromDoc(v, lookup.Path)
		results = append(results, SuggestLinkResult{
			Path:    lookup.Path,
			Title:   lookup.Title,
			Score:   s.Score,
			Snippet: snippet,
		})
		if len(results) >= suggestLinksLimit {
			break
		}
	}

	if done, err := emitStructured(cmd, results); done {
		return err
	}

	if len(results) == 0 {
		fmt.Println("No link suggestions found.")
		return nil
	}
	for i, r := range results {
		fmt.Printf("%d. %s (%s, score %.3f)\n", i+1, r.Title, r.Path, r.Score)
		if r.Snippet != "" {
			fmt.Printf("   %s\n", r.Snippet)
		}
	}
	return nil
}

func snippetFromDoc(v *vault.Vault, path string) string {
	absPath := filepath.Join(v.Root, path)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	parsed, err := document.Parse(absPath, content)
	if err != nil {
		return ""
	}
	runes := []rune(parsed.Body)
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}
