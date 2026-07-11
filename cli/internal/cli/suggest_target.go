package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
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

Read-only. Pair with "2nb relink <path> --from <target> --to <pick>" to apply a
chosen candidate.`,
	Args: cobra.ExactArgs(1),
	RunE: runSuggestTarget,
}

var (
	suggestTargetLimit  int
	suggestTargetSource string
	suggestTargetLLM    bool
)

func init() {
	suggestTargetCmd.GroupID = "ai"
	suggestTargetCmd.Flags().IntVar(&suggestTargetLimit, "limit", 6, "Maximum number of candidates")
	suggestTargetCmd.Flags().StringVar(&suggestTargetSource, "source", "",
		"Vault-relative path of the note containing the broken link; excluded from candidates and used as search context")
	suggestTargetCmd.Flags().BoolVar(&suggestTargetLLM, "llm", false,
		"Re-rank candidates with the generation model when none is high-confidence (grounded; fail-closed)")
	rootCmd.AddCommand(suggestTargetCmd)
}

// contextWindowRunes is how much source-note prose is folded into the semantic
// and BM25 queries when --source is set. Large enough to capture a trailing
// "related" block, small enough to stay query-like for Nova.
const contextWindowRunes = 400

// llmPoolCap is how many pre-LLM candidates we offer the model. Larger than the
// default --limit so context hits that ranked 4–10 still get a chance.
const llmPoolCap = 12

// llmTopN is how many candidates the LLM may promote into the returned list.
const llmTopN = 3

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

	// Gather into an internal pool that may exceed --limit so the LLM (and the
	// final trim) see more context hits. Cap at max(limit, llmPoolCap).
	poolLimit := suggestTargetLimit
	if poolLimit < llmPoolCap {
		poolLimit = llmPoolCap
	}

	ctx := context.Background()
	cfg := v.Config.AI
	results, contextSnippet := gatherSuggestions(ctx, v, target, sourcePath, poolLimit)

	// Tier 4 (optional) — LLM re-rank when no candidate is already high-confidence.
	// Fail-closed: any generation error leaves the deterministic list intact.
	if suggestTargetLLM && !hasHighConfidence(results) && len(results) > 0 {
		if reranked, rerr := rerankSuggestTargetLLM(ctx, cfg, suggestTargetRerankSystem, target, contextSnippet, results); rerr == nil && len(reranked) > 0 {
			results = reranked
		} else if rerr != nil {
			slog.Debug("suggest-target: llm re-rank skipped", "err", rerr)
		}
	}

	// Trim to the caller's --limit after re-rank.
	if len(results) > suggestTargetLimit {
		results = results[:suggestTargetLimit]
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

// gatherSuggestions runs the three deterministic tiers (drift, semantic, BM25)
// for one broken target and returns the confidence-graded candidate pool plus
// the context snippet used for LLM re-ranking. Shared by runSuggestTarget and
// the link-fix prompt eval (linkfix_eval_test.go), so an eval case exercises
// exactly the production pipeline.
func gatherSuggestions(ctx context.Context, v *vault.Vault, target, sourcePath string, poolLimit int) ([]SuggestLinkResult, string) {
	// Context-aware query: bare target when no --source, else target + prose
	// around the broken link (or the head of the note if the link isn't found).
	searchQuery, contextSnippet := buildSourceContextQuery(v.Root, sourcePath, target)

	engine := search.NewEngine(v.DB.Conn())
	results := make([]SuggestLinkResult, 0, poolLimit)
	seen := make(map[string]bool)
	// Every tier funnels through add, so no tier can leak the source note.
	add := func(path, title string, score float64) {
		if path == "" || path == sourcePath || seen[path] || len(results) >= poolLimit {
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

	// Tier 2 — semantic: nearest notes by embedding. Query is context-aware when
	// --source is set. Skipped (not an error) when no embedder is configured.
	initAIProviders(v)
	cfg := v.Config.AI
	if embedder, eerr := ai.DefaultRegistry.Embedder(cfg.Provider); eerr == nil && embedder.Available(ctx) {
		if qv, verr := embedder.Embed(ctx, []string{searchQuery}, ai.WithPurpose(ai.PurposeQuery)); verr == nil && len(qv) > 0 {
			if docIDs, embeddings, lerr := v.DB.AllEmbeddings(); lerr == nil {
				threshold, _ := cfg.ResolveSimilarityThresholdFull(v.Root)
				for _, s := range search.VectorSearchThreshold(qv[0], docIDs, embeddings, poolLimit*3, threshold) {
					if lookup, ok := engine.GetDocumentByID(s.DocID); ok {
						add(lookup.Path, lookup.Title, s.Score)
					}
				}
			}
		}
	}

	// Tier 3 — keyword: BM25 over the context-aware query, always offline.
	if hits, serr := engine.Search(search.Options{Query: searchQuery, Limit: poolLimit * 2, BM25Only: true}); serr == nil {
		for _, h := range hits {
			add(h.Path, h.Title, h.Score)
		}
	}
	// Also run bare-target BM25 so pure word-reorder typos still win when the
	// context dilutes the query (e.g. a long related-links footer).
	if searchQuery != target {
		if hits, serr := engine.Search(search.Options{Query: target, Limit: poolLimit, BM25Only: true}); serr == nil {
			for _, h := range hits {
				add(h.Path, h.Title, h.Score)
			}
		}
	}

	assignConfidence(results, target, uniqueDrift)
	return results, contextSnippet
}

// buildSourceContextQuery returns the search query and a short context snippet
// for LLM re-ranking. Without a source path, query is the bare target. With a
// source, the query is "target\n" + a window of surrounding prose so semantic
// and BM25 channels can use the note's topic, not just the broken name.
//
// Pure helper (root + path only) so unit tests don't need a vault DB.
func buildSourceContextQuery(vaultRoot, sourcePath, target string) (query, contextSnippet string) {
	target = strings.TrimSpace(target)
	if sourcePath == "" {
		return target, ""
	}
	abs := sourcePath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(vaultRoot, sourcePath)
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return target, ""
	}
	parsed, err := document.Parse(abs, content)
	if err != nil {
		return target, ""
	}
	body := parsed.IndexableBody()
	if strings.TrimSpace(body) == "" {
		return target, ""
	}

	// Prefer a window centered on the first prose occurrence of [[target]].
	// Fall back to the head of the note when the link is missing (e.g. a stale
	// finding whose body changed) so context still helps.
	window := contextWindowAroundTarget(body, target, contextWindowRunes)
	window = collapseWhitespace(window)
	if window == "" {
		return target, ""
	}
	// Query form: target first so BM25 still hits the broken name, then context.
	return target + "\n" + window, window
}

// contextWindowAroundTarget returns up to windowRunes of body text centered on
// the first [[target]] occurrence (case-insensitive match on the raw target
// stem). When the link is not found, returns the head of the body.
func contextWindowAroundTarget(body, target string, windowRunes int) string {
	if windowRunes <= 0 {
		windowRunes = contextWindowRunes
	}
	runes := []rune(body)
	if len(runes) == 0 {
		return ""
	}

	// Locate [[target]] / [[target#…]] / [[target|…]] case-insensitively.
	needle := "[[" + target
	lowerBody := strings.ToLower(body)
	lowerNeedle := strings.ToLower(needle)
	byteIdx := strings.Index(lowerBody, lowerNeedle)
	center := 0
	if byteIdx >= 0 {
		center = utf8.RuneCountInString(body[:byteIdx])
	} else {
		// No link found: use the head of the note as context.
		if len(runes) > windowRunes {
			return string(runes[:windowRunes])
		}
		return body
	}

	half := windowRunes / 2
	start := center - half
	if start < 0 {
		start = 0
	}
	end := start + windowRunes
	if end > len(runes) {
		end = len(runes)
		start = end - windowRunes
		if start < 0 {
			start = 0
		}
	}
	return string(runes[start:end])
}

// collapseWhitespace folds runs of whitespace (including newlines) to a single
// space so a multi-line context window is a clean one-line query fragment.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
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

// hasHighConfidence reports whether any candidate is already safe for a
// one-click / Fix-all apply.
func hasHighConfidence(results []SuggestLinkResult) bool {
	for _, r := range results {
		if r.Confidence == "high" {
			return true
		}
	}
	return false
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

// llmPick is one entry in the generation model's re-rank JSON response.
// Confidence is only emitted by prompts that ask for it (the eval's
// per-pick-confidence variant); absent in the shipped prompt's output.
type llmPick struct {
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	Confidence string `json:"confidence"`
}

// suggestTargetRerankSystem is the shipped system prompt for the LLM re-rank
// tier: the "strict_plausibility" winner of the link-fix prompt eval
// (docs/link-prompt-eval.md; RUN_LINKFIX_EVAL=1 harness in
// linkfix_eval_test.go). It beat the previous pick-3 prompt on promotion
// precision (0.83 vs 0.75) with identical ranking metrics, by declining the
// hopeless cases instead of guessing — and a decline is itself a signal (the
// GUI recommends unlinking). Change it only with a fresh eval run.
const suggestTargetRerankSystem = `You help fix broken Obsidian [[wikilinks]]. Decide which EXISTING note, if any, the author meant when they wrote the broken target.

Decision test — a candidate qualifies ONLY if the broken target reads as a NAME for that candidate:
- a typo, truncation, or misspelling of its title or filename;
- its words reordered, dropped, or separated differently;
- a paraphrase or abbreviation a person would naturally use for that specific note.
Sharing a topic is NOT enough. If the target names a thing the candidate merely discusses, it does not qualify.

The shortlist lines may include retrieval scores; they come from different retrievers and are NOT comparable with each other — judge by name and meaning, not score.

Rules:
- Return at most 3 qualifying candidates, best first.
- You may ONLY return paths that appear in the shortlist. Never invent a path or title.
- If no candidate qualifies, return [] — the author should remove or retype the link.
- Each reason is one short plain sentence (no markdown).

Respond with ONLY a JSON array, no prose:
[{"path":"exact/path.md","reason":"why this note"}, ...]`

// rerankSuggestTargetLLM asks the active generation model to order the grounded
// shortlist using the given system prompt (production passes
// suggestTargetRerankSystem; the eval passes candidate variants). It never
// invents paths (unknown paths are dropped), never promotes confidence above
// "medium" (LLM picks are recommendations, not auto-fixes), and preserves any
// pre-existing high-confidence entries at the front.
func rerankSuggestTargetLLM(ctx context.Context, cfg ai.AIConfig, system, target, contextSnippet string, candidates []SuggestLinkResult) ([]SuggestLinkResult, error) {
	byPath := make(map[string]SuggestLinkResult, len(candidates))
	for _, c := range candidates {
		byPath[c.Path] = c
	}
	user := buildRerankUser(target, contextSnippet, buildRerankCatalog(candidates, true))
	picks, err := llmRerankPicks(ctx, cfg, system, user)
	if err != nil {
		return nil, err
	}
	return applyLLMPicks(candidates, picks, byPath), nil
}

// buildRerankCatalog renders the grounded shortlist for the model. With
// includeScores the line carries the deterministic confidence + raw score
// (current shipped format); without, only path and title — the eval compares
// both because the raw scores are cross-tier-incomparable (drift 1.0 vs cosine
// vs BM25) and can anchor the model.
func buildRerankCatalog(candidates []SuggestLinkResult, includeScores bool) string {
	var catalog strings.Builder
	for i, c := range candidates {
		title := c.Title
		if title == "" {
			title = c.Path
		}
		if includeScores {
			fmt.Fprintf(&catalog, "%d. path=%s title=%q conf=%s score=%.3f\n", i+1, c.Path, title, c.Confidence, c.Score)
		} else {
			fmt.Fprintf(&catalog, "%d. path=%s title=%q\n", i+1, c.Path, title)
		}
	}
	return catalog.String()
}

// buildRerankUser composes the re-rank user message from the broken target,
// the optional source-note context window, and the rendered shortlist.
func buildRerankUser(target, contextSnippet, catalog string) string {
	var user strings.Builder
	fmt.Fprintf(&user, "Broken wikilink target: %q\n", target)
	if contextSnippet != "" {
		fmt.Fprintf(&user, "Surrounding note context:\n%s\n\n", contextSnippet)
	}
	user.WriteString("Shortlist (grounded existing notes):\n")
	user.WriteString(catalog)
	return user.String()
}

// llmRerankPicks runs one re-rank generation and parses the picks. An empty
// slice with a nil error is the model's explicit "none of these is plausible"
// verdict (distinct from a generation/parse failure). ReasoningEffort is
// suppressed so a reasoning-default model (mantle plane) cannot starve the
// small MaxTokens budget and truncate the JSON.
func llmRerankPicks(ctx context.Context, cfg ai.AIConfig, system, user string) ([]llmPick, error) {
	gen, err := ai.DefaultRegistry.Generator(cfg.Provider)
	if err != nil {
		return nil, err
	}
	if !gen.Available(ctx) {
		return nil, fmt.Errorf("generation provider %q unavailable", cfg.Provider)
	}
	out, err := gen.Generate(ctx, user, ai.GenOpts{
		Temperature:     ai.Ptr(0.0),
		MaxTokens:       400,
		SystemPrompt:    system,
		ReasoningEffort: "none",
	})
	if err != nil {
		return nil, err
	}
	return parseLLMPicks(out)
}

// parseLLMPicks extracts a JSON array of {path, reason} from a model response,
// tolerating optional markdown fences / leading prose.
func parseLLMPicks(raw string) ([]llmPick, error) {
	s := strings.TrimSpace(raw)
	// Strip common ```json ... ``` fences.
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimSpace(s)
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	// Slice to the outermost JSON array when the model adds preamble.
	if i := strings.IndexByte(s, '['); i >= 0 {
		if j := strings.LastIndexByte(s, ']'); j > i {
			s = s[i : j+1]
		}
	}
	var picks []llmPick
	if err := json.Unmarshal([]byte(s), &picks); err != nil {
		return nil, fmt.Errorf("parse llm picks: %w", err)
	}
	return picks, nil
}

// applyLLMPicks reorders the candidate list: LLM-chosen paths first (with
// reason + confidence capped at medium), then any unused original candidates
// in their prior order. Unknown paths are dropped. Pure so unit-testable.
func applyLLMPicks(original []SuggestLinkResult, picks []llmPick, byPath map[string]SuggestLinkResult) []SuggestLinkResult {
	if byPath == nil {
		byPath = make(map[string]SuggestLinkResult, len(original))
		for _, c := range original {
			byPath[c.Path] = c
		}
	}
	seen := make(map[string]bool)
	out := make([]SuggestLinkResult, 0, len(original))

	// Preserve any pre-existing high-confidence hits at the front (should be
	// none when LLM is invoked, but keep the invariant).
	for _, c := range original {
		if c.Confidence == "high" {
			out = append(out, c)
			seen[c.Path] = true
		}
	}

	n := 0
	for _, p := range picks {
		path := strings.TrimSpace(p.Path)
		if path == "" || seen[path] {
			continue
		}
		c, ok := byPath[path]
		if !ok {
			// Model invented a path — drop it.
			continue
		}
		// Cap at medium: LLM ranking is a recommendation, never a silent auto-fix.
		if c.Confidence != "high" {
			if c.Confidence == "" || c.Confidence == "low" {
				c.Confidence = "medium"
			}
		}
		c.Reason = strings.TrimSpace(p.Reason)
		out = append(out, c)
		seen[path] = true
		n++
		if n >= llmTopN {
			break
		}
	}

	// Append unused originals so --limit can still fill beyond the LLM top-N.
	for _, c := range original {
		if seen[c.Path] {
			continue
		}
		out = append(out, c)
	}
	return out
}
