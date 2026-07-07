package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
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

Pass --source <path> (the note containing the broken link) to exclude that note
from the candidates: a note is never a fix for its own broken link.

Read-only. Pair with "2nb relink <path> --from <target> --to <pick>" to apply a
chosen candidate.`,
	Args: cobra.ExactArgs(1),
	RunE: runSuggestTarget,
}

var (
	suggestTargetLimit  int
	suggestTargetSource string
)

func init() {
	suggestTargetCmd.GroupID = "ai"
	suggestTargetCmd.Flags().IntVar(&suggestTargetLimit, "limit", 6, "Maximum number of candidates")
	suggestTargetCmd.Flags().StringVar(&suggestTargetSource, "source", "",
		"Vault-relative path of the note containing the broken link; it is excluded from candidates")
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

	// The note containing the broken link is never a candidate for its own
	// fix. Resolution is lenient and best-effort: a missing --source still
	// resolves to its vault-relative path (auto mode returns it without an
	// error), while an ambiguous or otherwise unresolvable one falls back to
	// the cleaned raw path so the command still runs; that fallback excludes
	// only an exact vault-relative match, so exclusion can no-op there.
	sourcePath := strings.TrimSpace(suggestTargetSource)
	if sourcePath != "" {
		if _, rel, rerr := resolveTargetArg(v, sourcePath); rerr == nil && rel != "" {
			sourcePath = rel
		} else {
			slog.Debug("suggest-target: --source did not resolve, excluding by cleaned raw path",
				"source", suggestTargetSource, "err", rerr)
			sourcePath = filepath.Clean(sourcePath)
		}
	}

	engine := search.NewEngine(v.DB.Conn())
	results := make([]SuggestLinkResult, 0, suggestTargetLimit)
	seen := make(map[string]bool)
	// Every tier funnels through add, so no tier can leak the source note.
	add := func(path, title string, score float64) {
		if path == "" || path == sourcePath || seen[path] || len(results) >= suggestTargetLimit {
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
		if qv, verr := embedder.Embed(ctx, []string{target}, ai.WithPurpose(ai.PurposeQuery)); verr == nil && len(qv) > 0 {
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
