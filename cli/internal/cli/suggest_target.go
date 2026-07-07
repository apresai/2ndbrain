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
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
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

	// Tier 1 (drift): the highest-confidence candidates, the existing notes the
	// broken name maps to via repair's fuzzy index (surfacing ambiguity repair
	// would skip). Sourced from the LIVE FILESYSTEM (vault.CollectLiveDocs, the
	// same walk lint reports from), so a note created in Obsidian but not yet
	// indexed still surfaces, and a note deleted on disk but still in the DB
	// never does. One walk feeds both the fuzzy index and the resolver that
	// turns each canonical name into a concrete path.
	// uniqueDrift marks the candidate that is the repair index's single match
	// for the target: it is what repair-links itself would rewrite to, so its
	// confidence is inherently "high" (see SuggestLinkResult.Confidence).
	uniqueDrift := make(map[string]bool, 1)
	if docs, aliases, lerr := vault.CollectLiveDocs(v.Root); lerr == nil {
		liveResolver := store.NewResolver(docs, aliases)
		titleByPath := make(map[string]string, len(docs))
		for _, d := range docs {
			titleByPath[d.Path] = d.Title
		}
		driftCandidates := polish.SuggestRepairTargets(docs, aliases, target)
		for _, c := range driftCandidates {
			path, rerr := liveResolver.Resolve(c)
			if rerr != nil {
				continue
			}
			if len(driftCandidates) == 1 {
				uniqueDrift[path] = true
			}
			add(path, titleByPath[path], 1.0)
		}
	} else {
		slog.Debug("suggest-target: live walk failed, drift tier skipped", "err", lerr)
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

	assignConfidence(results, target, uniqueDrift)

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

// dominantScoreRatio is the score multiple over the best OTHER candidate at
// which a candidate counts as dominant for confidence grading.
const dominantScoreRatio = 1.4

// assignConfidence stamps each candidate's Confidence per the deterministic
// rule documented on SuggestLinkResult.Confidence: word match x dominance,
// with the unique tier-1 drift match pinned to "high" (it is exactly what
// repair-links would rewrite to, and it may have matched via an alias the
// title/basename word check cannot see).
func assignConfidence(results []SuggestLinkResult, target string, uniqueDrift map[string]bool) {
	for i := range results {
		if uniqueDrift[results[i].Path] {
			results[i].Confidence = "high"
			continue
		}
		wordMatch := targetWordMatch(target, results[i].Title, results[i].Path)
		dominant := dominantAmong(i, results)
		switch {
		case wordMatch && dominant:
			results[i].Confidence = "high"
		case wordMatch || dominant:
			results[i].Confidence = "medium"
		default:
			results[i].Confidence = "low"
		}
	}
}

// targetWordMatch reports whether the target, folded with the same
// normalization the repair index uses (polish.NormalizeName), equals or is a
// whole-word subset of the candidate's folded title or basename.
func targetWordMatch(target, title, path string) bool {
	folded := polish.NormalizeName(target)
	base := strings.TrimSuffix(filepath.Base(path), ".md")
	return isWholeWordSubset(folded, polish.NormalizeName(title)) ||
		isWholeWordSubset(folded, polish.NormalizeName(base))
}

// isWholeWordSubset reports whether every word of the folded target appears as
// a whole word in the folded name (equality is the trivial subset). Both inputs
// must already be NormalizeName-folded, so words split on single spaces.
func isWholeWordSubset(target, name string) bool {
	targetWords := strings.Fields(target)
	if len(targetWords) == 0 {
		return false
	}
	nameWords := make(map[string]bool)
	for _, w := range strings.Fields(name) {
		nameWords[w] = true
	}
	for _, w := range targetWords {
		if !nameWords[w] {
			return false
		}
	}
	return true
}

// dominantAmong reports whether candidate i is the sole candidate or scores at
// least dominantScoreRatio times the best other candidate. Only the top-scoring
// candidate can be dominant; scores are compared raw across tiers (drift 1.0,
// cosine, BM25), which is deliberate: dominance is a within-list separation
// signal, not a cross-tier calibration.
func dominantAmong(i int, results []SuggestLinkResult) bool {
	if len(results) == 1 {
		return true
	}
	bestOther := 0.0
	for j := range results {
		if j != i && results[j].Score > bestOther {
			bestOther = results[j].Score
		}
	}
	return results[i].Score >= dominantScoreRatio*bestOther
}
