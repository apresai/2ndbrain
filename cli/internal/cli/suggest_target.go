package cli

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/metrics"
	"github.com/apresai/2ndbrain/internal/polish"
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
                configured). With --source, the query is the target PLUS the
                surrounding prose of the broken link (context-aware), not the
                bare target alone;
  3. keyword — BM25 over the target words (and the same context when --source
               is set), so word-reorder/typo misses like "models-apresai" ->
               apresai-* notes still surface offline.

Pass --source <path> (the note containing the broken link) to exclude that note
from the candidates AND to seed context-aware search.

--llm re-ranks the shortlist with the active generation model when no candidate
is already high-confidence (unique name drift, or word-match + dominant). The
model may only reorder existing paths (never invent a note), attaches a one-line
reason, and caps confidence at "medium" so LLM picks are recommendations, never
silent auto-fixes. Fail-closed: if generation is unavailable the non-LLM list is
returned unchanged.

Candidates are ordered best-first: confidence tier (high > medium > low), then
score within a tier — so the first candidate is always the pipeline's best
claim.

--verdict wraps the output in a recommendation envelope
{recommendation, llm, candidates}: exactly one machine recommendation per
broken link — relink to the top candidate when it is high (one-click safe) or
medium (needs a human confirm), else unlink (remove the link, keep the text).
An explicit LLM decline ("llm": "declined" — the model judged no candidate
plausible) recommends unlink even over a medium candidate; measured to be the
right call (docs/link-prompt-eval.md). "Create the note" is never machine-
recommended; it stays a user choice.

Read-only. Pair with "2nb relink <path> --from <target> --to <pick>" to apply a
chosen candidate, or "2nb unlink <path> --target <target>" to remove it.`,
	Args: cobra.ExactArgs(1),
	RunE: runSuggestTarget,
}

var (
	suggestTargetLimit   int
	suggestTargetSource  string
	suggestTargetLLM     bool
	suggestTargetVerdict bool
)

func init() {
	suggestTargetCmd.GroupID = "ai"
	suggestTargetCmd.Flags().IntVar(&suggestTargetLimit, "limit", 6, "Maximum number of candidates")
	suggestTargetCmd.Flags().StringVar(&suggestTargetSource, "source", "",
		"Vault-relative path of the note containing the broken link; excluded from candidates and used as search context")
	suggestTargetCmd.Flags().BoolVar(&suggestTargetLLM, "llm", false,
		"Re-rank candidates with the generation model when none is high-confidence (grounded; fail-closed)")
	suggestTargetCmd.Flags().BoolVar(&suggestTargetVerdict, "verdict", false,
		"Emit the recommendation envelope {recommendation, llm, candidates} instead of the bare candidate array")
	rootCmd.AddCommand(suggestTargetCmd)
}

const (
	contextWindowRunes        = polish.ContextWindowRunes
	llmPoolCap                = polish.LLMPoolCap
	llmTopN                   = polish.LLMTopN
	dominantScoreRatio        = polish.DominantScoreRatio
	llmOutcomeOff             = polish.LLMOutcomeOff
	llmOutcomeSkipped         = polish.LLMOutcomeSkipped
	llmOutcomePromoted        = polish.LLMOutcomePromoted
	llmOutcomeDeclined        = polish.LLMOutcomeDeclined
	llmOutcomeError           = polish.LLMOutcomeError
	suggestTargetRerankSystem = polish.TargetRerankSystem
)

type (
	SuggestRecommendation = polish.SuggestRecommendation
	SuggestTargetEnvelope = polish.SuggestTargetEnvelope
	llmPick               = polish.LLMPick
)

func runSuggestTarget(cmd *cobra.Command, args []string) error {
	if err := refuseBodylessFormat(cmd, "suggest-target"); err != nil {
		return err
	}
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

	poolLimit := suggestTargetLimit
	if poolLimit < llmPoolCap {
		poolLimit = llmPoolCap
	}

	suggestStart := time.Now()
	ctx := context.Background()
	cfg := v.Config.AI
	results, contextSnippet, embedInTok := gatherSuggestionsWithUsage(ctx, v, target, sourcePath, poolLimit)

	llmOutcome := llmOutcomeOff
	var llmUsage ai.GenUsage
	if suggestTargetLLM {
		llmOutcome = llmOutcomeSkipped
		if !polish.HasHighConfidence(results) && len(results) > 0 {
			reranked, picks, u, rerr := rerankSuggestTargetLLMWithUsage(ctx, cfg, suggestTargetRerankSystem, target, contextSnippet, results)
			llmUsage = u
			switch {
			case rerr != nil:
				llmOutcome = llmOutcomeError
				slog.Debug("suggest-target: llm re-rank skipped", "target", target, "err", rerr)
			case picks == 0:
				llmOutcome = llmOutcomeDeclined
				slog.Debug("suggest-target: llm declined to promote any candidate", "target", target)
				results = reranked
			default:
				llmOutcome = llmOutcomePromoted
				results = reranked
			}
		}
	}
	if embedInTok > 0 || llmUsage.InputTokens > 0 || llmUsage.OutputTokens > 0 {
		op := metrics.Operation{
			Operation:   metrics.OpSuggest,
			DurationMs:  time.Since(suggestStart).Milliseconds(),
			OK:          true,
			ResultCount: len(results),
		}
		var legs []metrics.UsageLeg
		if embedInTok > 0 {
			legs = append(legs, buildUsageLeg(v.Root, cfg.EffectiveEmbeddingProvider(), cfg.EmbeddingModel, "embedding", metrics.OpSuggest, 1, embedInTok, 0))
		}
		if llmUsage.InputTokens > 0 || llmUsage.OutputTokens > 0 {
			legs = append(legs, buildUsageLeg(v.Root, cfg.EffectiveGenerationProvider(), cfg.GenerationModel, "generation", metrics.OpSuggest, 1, llmUsage.InputTokens, llmUsage.OutputTokens))
		}
		attachUsageLegs(&op, legs...)
		recordMetric(v, op)
	}

	if len(results) > suggestTargetLimit {
		results = results[:suggestTargetLimit]
	}

	recommendation := computeSuggestRecommendation(results, llmOutcome)

	var payload any = results
	if suggestTargetVerdict {
		payload = SuggestTargetEnvelope{
			Recommendation: recommendation,
			LLM:            llmOutcome,
			Candidates:     results,
		}
	}
	if done, err := emitStructured(cmd, payload); done {
		return err
	}

	if suggestTargetVerdict {
		switch recommendation.Action {
		case "relink":
			display := recommendation.Title
			if display == "" {
				display = recommendation.To
			}
			line := fmt.Sprintf("Recommendation: relink to %s (%s, %s)", display, recommendation.To, recommendation.Confidence)
			if recommendation.Reason != "" {
				line += ": " + recommendation.Reason
			}
			fmt.Println(line)
		default:
			fmt.Printf("Recommendation: unlink [[%s]] — no existing note matches confidently (llm: %s).\n", target, llmOutcome)
		}
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
		conf := r.Confidence
		if conf == "" {
			conf = "?"
		}
		line := fmt.Sprintf("%d. %s (%s, score %.3f, %s)", i+1, title, r.Path, r.Score, conf)
		if r.Reason != "" {
			line += ": " + r.Reason
		}
		fmt.Println(line)
	}
	return nil
}

func gatherSuggestions(ctx context.Context, v *vault.Vault, target, sourcePath string, poolLimit int) ([]SuggestLinkResult, string) {
	results, snippet, _ := gatherSuggestionsWithUsage(ctx, v, target, sourcePath, poolLimit)
	return results, snippet
}

func gatherSuggestionsWithUsage(ctx context.Context, v *vault.Vault, target, sourcePath string, poolLimit int) ([]SuggestLinkResult, string, int) {
	initAIProviders(v)
	cfg := v.Config.AI
	embedder, eerr := ai.DefaultRegistry.Embedder(cfg.EffectiveEmbeddingProvider())
	embedReady := false
	if eerr == nil && embedder != nil {
		embedReady = embedder.Available(ctx)
	} else {
		embedder = nil
	}
	return polish.GatherTargetSuggestions(ctx, v, target, sourcePath, polish.SuggestTargetOptions{
		Embedder:      embedder,
		EmbedderReady: embedReady,
		PoolLimit:     poolLimit,
	})
}

func buildSourceContextQuery(vaultRoot, sourcePath, target string) (query, contextSnippet string) {
	return polish.BuildSourceContextQuery(vaultRoot, sourcePath, target)
}

func contextWindowAroundTarget(body, target string, windowRunes int) string {
	return polish.ContextWindowAroundTarget(body, target, windowRunes)
}

func splitTargetWords(target string) string {
	return polish.SplitTargetWords(target)
}

func assignConfidence(results []SuggestLinkResult, target string, uniqueDrift map[string]bool) {
	polish.AssignTargetConfidence(results, target, uniqueDrift)
}

func hasHighConfidence(results []SuggestLinkResult) bool {
	return polish.HasHighConfidence(results)
}

func targetWordMatch(target, title, path string) bool {
	return polish.TargetWordMatch(target, title, path)
}

func computeSuggestRecommendation(results []SuggestLinkResult, llmOutcome string) SuggestRecommendation {
	return polish.ComputeTargetRecommendation(results, llmOutcome)
}

func sortSuggestionsByConfidence(results []SuggestLinkResult) {
	polish.SortSuggestionsByConfidence(results)
}

func rerankSuggestTargetLLM(ctx context.Context, cfg ai.AIConfig, system, target, contextSnippet string, candidates []SuggestLinkResult) ([]SuggestLinkResult, int, error) {
	reranked, applied, _, err := rerankSuggestTargetLLMWithUsage(ctx, cfg, system, target, contextSnippet, candidates)
	return reranked, applied, err
}

func rerankSuggestTargetLLMWithUsage(ctx context.Context, cfg ai.AIConfig, system, target, contextSnippet string, candidates []SuggestLinkResult) ([]SuggestLinkResult, int, ai.GenUsage, error) {
	genProvider := cfg.EffectiveGenerationProvider()
	gen, err := ai.DefaultRegistry.Generator(genProvider)
	if err != nil {
		return nil, 0, ai.GenUsage{}, err
	}
	if ready, code := ai.Availability(ctx, gen); !ready {
		return nil, 0, ai.GenUsage{}, generatorNotReadyError(genProvider, code)
	}
	return polish.RerankTargetCandidatesLLM(ctx, gen, system, target, contextSnippet, candidates)
}

func buildRerankCatalog(candidates []SuggestLinkResult, includeScores bool) string {
	return polish.BuildRerankCatalog(candidates, includeScores)
}

func buildRerankUser(target, contextSnippet, catalog string) string {
	return polish.BuildRerankUser(target, contextSnippet, catalog)
}

func llmRerankPicks(ctx context.Context, cfg ai.AIConfig, system, user string) ([]llmPick, error) {
	picks, _, err := llmRerankPicksWithUsage(ctx, cfg, system, user)
	return picks, err
}

func llmRerankPicksWithUsage(ctx context.Context, cfg ai.AIConfig, system, user string) ([]llmPick, ai.GenUsage, error) {
	genProvider := cfg.EffectiveGenerationProvider()
	gen, err := ai.DefaultRegistry.Generator(genProvider)
	if err != nil {
		return nil, ai.GenUsage{}, err
	}
	if ready, code := ai.Availability(ctx, gen); !ready {
		return nil, ai.GenUsage{}, generatorNotReadyError(genProvider, code)
	}
	opts := ai.GenOpts{
		Temperature:     nil,
		MaxTokens:       1024,
		SystemPrompt:    system,
		ReasoningEffort: "none",
	}
	var out string
	var usage ai.GenUsage
	if ug, ok := gen.(ai.UsageGenerator); ok {
		out, usage, err = ug.GenerateWithUsage(ctx, user, opts)
	} else {
		out, err = gen.Generate(ctx, user, opts)
	}
	if err != nil {
		return nil, ai.GenUsage{}, err
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		usage = ai.GenUsage{
			InputTokens:  max(1, (len(system)+len(user))/4),
			OutputTokens: max(1, len(out)/4),
		}
	}
	picks, err := polish.ParseLLMPicks(out)
	return picks, usage, err
}

func parseLLMPicks(raw string) ([]llmPick, error) {
	return polish.ParseLLMPicks(raw)
}

func applyLLMPicks(original []SuggestLinkResult, picks []llmPick, byPath map[string]SuggestLinkResult) []SuggestLinkResult {
	return polish.ApplyLLMPicks(original, picks, byPath)
}
