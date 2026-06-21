package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/output"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/spf13/cobra"
)

var suggestTargetCmd = &cobra.Command{
	Use:   "suggest-target <target>",
	Short: "Suggest existing notes a broken [[wikilink]] target might have meant",
	Long: `Given a single broken wikilink target, returns ranked existing notes it could
point at — the "did you mean?" candidates behind the Validation tab's link-fix
sheet. Candidates come from three tiers, surfaced best-first:

  1. drift  — the same normalized-name index "repair-links" uses (case,
              hyphen/underscore, and whitespace folded), INCLUDING the ambiguous
              matches repair refuses to guess on its own;
  2. semantic — nearest notes by embedding similarity (when an embedder is
                configured);
  3. keyword — BM25 over the target words, so word-reorder/typo misses like
               "models-apresai" -> apresai-* notes still surface offline.

Read-only. Pair with "2nb relink <path> --from <target> --to <pick>" to apply a
chosen candidate.`,
	Args: cobra.ExactArgs(1),
	RunE: runSuggestTarget,
}

var suggestTargetLimit int

func init() {
	suggestTargetCmd.GroupID = "ai"
	suggestTargetCmd.Flags().IntVar(&suggestTargetLimit, "limit", 6, "Maximum number of candidates")
	rootCmd.AddCommand(suggestTargetCmd)
}

func runSuggestTarget(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	defer v.Close()
	setupFileLogging(v)

	target := strings.TrimSpace(args[0])
	if target == "" {
		return exitWithError(ExitValidation, "error: suggest-target requires a non-empty target")
	}

	engine := search.NewEngine(v.DB.Conn())
	results := make([]SuggestLinkResult, 0, suggestTargetLimit)
	seen := make(map[string]bool)
	add := func(path, title string, score float64) {
		if path == "" || seen[path] || len(results) >= suggestTargetLimit {
			return
		}
		seen[path] = true
		results = append(results, SuggestLinkResult{
			Path:    path,
			Title:   title,
			Score:   score,
			Snippet: snippetFromDoc(v, path),
		})
	}

	// Tier 1 — drift: the highest-confidence candidates, the existing notes the
	// broken name maps to via repair's fuzzy index (surfacing ambiguity repair
	// would skip). Resolve each canonical name to a concrete path.
	if cands, cerr := polish.SuggestRepairTargets(v.DB, target); cerr == nil {
		for _, c := range cands {
			path, rerr := v.DB.ResolveTarget(c)
			if rerr != nil {
				continue
			}
			title := ""
			if d, derr := v.DB.GetDocumentByPath(path); derr == nil && d != nil {
				title = d.Title
			}
			add(path, title, 1.0)
		}
	}

	// Tier 2 — semantic: nearest notes by embedding. Skipped (not an error) when
	// no embedder is configured, mirroring suggest-links/GatherCandidates.
	initAIProviders(v)
	ctx := context.Background()
	cfg := v.Config.AI
	if embedder, eerr := ai.DefaultRegistry.Embedder(cfg.Provider); eerr == nil && embedder.Available(ctx) {
		if qv, verr := embedder.Embed(ctx, []string{target}); verr == nil && len(qv) > 0 {
			if docIDs, embeddings, lerr := v.DB.AllEmbeddings(); lerr == nil {
				threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
				for _, s := range search.VectorSearchThreshold(qv[0], docIDs, embeddings, suggestTargetLimit*3, threshold) {
					if lookup, ok := engine.GetDocumentByID(s.DocID); ok {
						add(lookup.Path, lookup.Title, s.Score)
					}
				}
			}
		}
	}

	// Tier 3 — keyword: BM25 over the target words, always available offline.
	if hits, serr := engine.Search(search.Options{Query: target, Limit: suggestTargetLimit * 2, BM25Only: true}); serr == nil {
		for _, h := range hits {
			add(h.Path, h.Title, h.Score)
		}
	}

	if getFormat(cmd) == output.FormatJSON {
		data, err := json.Marshal(results)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}

	if len(results) == 0 {
		fmt.Printf("No existing note matches [[%s]]. Create it or unlink it.\n", target)
		return nil
	}
	for i, r := range results {
		title := r.Title
		if title == "" {
			title = r.Path
		}
		fmt.Printf("%d. %s (%s, score %.3f)\n", i+1, title, r.Path, r.Score)
	}
	return nil
}
