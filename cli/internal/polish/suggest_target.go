package polish

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/search"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

const (
	// ContextWindowRunes is how much source-note prose is folded into the
	// semantic and BM25 queries when a source note is known.
	ContextWindowRunes = 400

	// LLMPoolCap is how many pre-LLM candidates are offered to the model.
	LLMPoolCap = 12

	// LLMTopN is how many candidates the LLM may promote into the returned list.
	LLMTopN = 3

	// DominantScoreRatio is the score multiple over the best other candidate at
	// which a candidate counts as dominant for confidence grading.
	DominantScoreRatio = 1.4
)

// LLMOutcome values for suggest-target --verdict and Stage C link recovery.
const (
	LLMOutcomeOff      = "off"
	LLMOutcomeSkipped  = "skipped"
	LLMOutcomePromoted = "promoted"
	LLMOutcomeDeclined = "declined"
	LLMOutcomeError    = "error"
)

// SuggestLinkResult is one ranked suggestion returned by `2nb suggest-links`,
// `2nb suggest-target`, and Stage C broken-link recovery.
type SuggestLinkResult struct {
	Path       string  `json:"path"`
	Title      string  `json:"title"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet"`
	Confidence string  `json:"confidence,omitempty"`
	Reason     string  `json:"reason,omitempty"`
}

// SuggestRecommendation is the single machine recommendation for one broken link.
type SuggestRecommendation struct {
	Action     string `json:"action"`               // "relink" | "unlink"
	To         string `json:"to,omitempty"`         // candidate path (relink only)
	Title      string `json:"title,omitempty"`      // candidate display title (relink only)
	Confidence string `json:"confidence,omitempty"` // candidate confidence (relink only)
	Reason     string `json:"reason,omitempty"`     // LLM's one-line reason when promoted
}

// SuggestTargetEnvelope is the --verdict output shape.
type SuggestTargetEnvelope struct {
	Recommendation SuggestRecommendation `json:"recommendation"`
	LLM            string                `json:"llm"`
	Candidates     []SuggestLinkResult   `json:"candidates"`
}

// LLMPick is one entry in the generation model's re-rank JSON response.
type LLMPick struct {
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	Confidence string `json:"confidence"`
}

// TargetRerankSystem is the shipped strict_plausibility system prompt for the
// LLM re-rank tier (docs/link-prompt-eval.md).
const TargetRerankSystem = `You help fix broken Obsidian [[wikilinks]]. Decide which EXISTING note, if any, the author meant when they wrote the broken target.

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

// SuggestTargetOptions provides dependency-injected providers and optional
// preloaded vault state for GatherTargetSuggestions.
type SuggestTargetOptions struct {
	Embedder            ai.EmbeddingProvider
	EmbedderReady       bool
	LiveDocs            []store.DocInfo
	LiveAliases         map[string][]string
	LiveResolver        *store.Resolver
	RepairIdx           *RepairIndex
	DocIDs              []string
	Embeddings          [][]float32
	SimilarityThreshold float64
	SourceBody          string
	SeedPaths           []string
	PoolLimit           int
}

// GatherTargetSuggestions runs the three deterministic tiers (drift, semantic,
// BM25) for one broken target and returns the confidence-graded candidate pool
// plus the context snippet used for LLM re-ranking.
func GatherTargetSuggestions(ctx context.Context, v *vault.Vault, target, sourcePath string, opts SuggestTargetOptions) ([]SuggestLinkResult, string, int) {
	poolLimit := opts.PoolLimit
	if poolLimit <= 0 {
		poolLimit = LLMPoolCap
	}

	var searchQuery, contextSnippet string
	if opts.SourceBody != "" {
		searchQuery, contextSnippet = BuildSourceContextQueryFromBody(opts.SourceBody, target)
	} else if v != nil {
		searchQuery, contextSnippet = BuildSourceContextQuery(v.Root, sourcePath, target)
	} else {
		searchQuery = strings.TrimSpace(target)
	}

	results := make([]SuggestLinkResult, 0, poolLimit*2)
	seen := make(map[string]bool)

	docs := opts.LiveDocs
	aliases := opts.LiveAliases
	if docs == nil && v != nil {
		if collectedDocs, collectedAliases, lerr := vault.CollectLiveDocs(v.Root); lerr == nil {
			docs = collectedDocs
			aliases = collectedAliases
		} else {
			slog.Debug("suggest-target: live walk failed, drift tier skipped", "err", lerr)
		}
	}

	livePathSet := make(map[string]bool, len(docs))
	titleByPath := make(map[string]string, len(docs))
	for _, d := range docs {
		livePathSet[d.Path] = true
		titleByPath[d.Path] = d.Title
	}

	add := func(path, title string, score float64) bool {
		if path == "" || path == sourcePath || seen[path] {
			return false
		}
		// Ground DB-backed hits (Tier 2/3) against the live filesystem so deleted
		// index.db ghost paths never enter the shortlist.
		if len(livePathSet) > 0 && !livePathSet[path] {
			return false
		}
		seen[path] = true
		if title == "" {
			title = titleByPath[path]
		}
		snippet := ""
		if v != nil {
			snippet = SnippetFromDoc(v, path)
		}
		results = append(results, SuggestLinkResult{
			Path:    path,
			Title:   title,
			Score:   score,
			Snippet: snippet,
		})
		return true
	}

	// Tier 1 (drift + live word-match) against live filesystem docs.
	uniqueDrift := make(map[string]bool, 1)
	if len(docs) > 0 {
		liveResolver := opts.LiveResolver
		if liveResolver == nil {
			liveResolver = store.NewResolver(docs, aliases)
		}
		repairIdx := opts.RepairIdx
		if repairIdx == nil {
			repairIdx = NewRepairIndex(docs, aliases)
		}
		driftCandidates := repairIdx.Lookup(target)
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

		// Tier 1b (live word-match): scan live docs for whole-word title/basename matches.
		cleanTarget := BasenameOrTarget(strings.TrimSuffix(document.DecodeLinkTarget(target), ".md"))
		wordMatchAdded := 0
		for _, d := range docs {
			if wordMatchAdded >= poolLimit {
				break
			}
			if TargetWordMatch(target, d.Title, d.Path) ||
				(cleanTarget != "" && TargetWordMatch(cleanTarget, d.Title, d.Path)) {
				if add(d.Path, d.Title, 0.95) {
					wordMatchAdded++
				}
			}
		}
	}

	// Tier 2 — semantic: nearest notes by embedding.
	embedInTok := 0
	if v != nil && v.DB != nil && opts.Embedder != nil && (opts.EmbedderReady || opts.Embedder.Available(ctx)) {
		if qv, verr := opts.Embedder.Embed(ctx, []string{searchQuery}, ai.WithPurpose(ai.PurposeQuery)); verr == nil && len(qv) > 0 {
			embedInTok = max(1, len(searchQuery)/4)
			docIDs, embeddings := opts.DocIDs, opts.Embeddings
			var lerr error
			if len(embeddings) == 0 {
				docIDs, embeddings, lerr = v.DB.AllEmbeddings()
			}
			if lerr == nil && len(embeddings) > 0 && len(embeddings) == len(docIDs) {
				threshold := opts.SimilarityThreshold
				if threshold <= 0 && v.Config != nil {
					threshold, _ = v.Config.AI.ResolveSimilarityThresholdFull(v.Root)
				}
				engine := search.NewEngine(v.DB.Conn())
				semAdded := 0
				for _, s := range search.VectorSearchThreshold(qv[0], docIDs, embeddings, poolLimit*3, threshold) {
					if semAdded >= poolLimit {
						break
					}
					if lookup, ok := engine.GetDocumentByID(s.DocID); ok {
						if add(lookup.Path, lookup.Title, s.Score) {
							semAdded++
						}
					}
				}
			}
		}
	}

	// Tier 3 — keyword: BM25 over context-aware query, bare target, and split words.
	if v != nil && v.DB != nil {
		engine := search.NewEngine(v.DB.Conn())
		if hits, serr := engine.Search(search.Options{Query: searchQuery, Limit: poolLimit * 2, BM25Only: true}); serr == nil {
			bm25Added := 0
			for _, h := range hits {
				if bm25Added >= poolLimit {
					break
				}
				if add(h.Path, h.Title, h.Score) {
					bm25Added++
				}
			}
		}
		if searchQuery != target {
			if hits, serr := engine.Search(search.Options{Query: target, Limit: poolLimit, BM25Only: true}); serr == nil {
				bm25BareAdded := 0
				for _, h := range hits {
					if bm25BareAdded >= poolLimit {
						break
					}
					if add(h.Path, h.Title, h.Score) {
						bm25BareAdded++
					}
				}
			}
		}
		if words := SplitTargetWords(target); words != target && words != searchQuery {
			if hits, serr := engine.Search(search.Options{Query: words, Limit: poolLimit, BM25Only: true}); serr == nil {
				bm25WordsAdded := 0
				for _, h := range hits {
					if bm25WordsAdded >= poolLimit {
						break
					}
					if add(h.Path, h.Title, h.Score) {
						bm25WordsAdded++
					}
				}
			}
		}
	}

	// Seed any pre-known ambiguous/candidate paths into the shortlist before truncation.
	seedSet := make(map[string]bool, len(opts.SeedPaths))
	for _, sp := range opts.SeedPaths {
		if sp == "" || sp == sourcePath {
			continue
		}
		seedSet[sp] = true
		add(sp, titleByPath[sp], 0.90)
	}

	AssignTargetConfidence(results, target, uniqueDrift)
	SortSuggestionsByConfidence(results)

	if len(results) > poolLimit {
		// Ensure seeded paths survive pool truncation.
		if len(seedSet) > 0 {
			kept := make([]SuggestLinkResult, 0, poolLimit)
			var tailSeeds []SuggestLinkResult
			for _, r := range results[:poolLimit] {
				kept = append(kept, r)
			}
			for _, r := range results[poolLimit:] {
				if seedSet[r.Path] {
					tailSeeds = append(tailSeeds, r)
				}
			}
			for _, s := range tailSeeds {
				// Replace the last non-seed entry in kept.
				for i := len(kept) - 1; i >= 0; i-- {
					if !seedSet[kept[i].Path] {
						kept[i] = s
						break
					}
				}
			}
			SortSuggestionsByConfidence(kept)
			results = kept
		} else {
			results = results[:poolLimit]
		}
	}
	return results, contextSnippet, embedInTok
}

// SnippetFromDoc reads a short body excerpt for a vault-relative note path.
func SnippetFromDoc(v *vault.Vault, path string) string {
	if v == nil {
		return ""
	}
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

// BuildSourceContextQuery returns the search query and a short context snippet
// for LLM re-ranking by reading sourcePath from disk.
func BuildSourceContextQuery(vaultRoot, sourcePath, target string) (query, contextSnippet string) {
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
	return BuildSourceContextQueryFromBody(parsed.IndexableBody(), target)
}

// BuildSourceContextQueryFromBody builds the search query and context snippet
// from an in-memory note body (useful when Stage A/B already modified the body).
func BuildSourceContextQueryFromBody(body, target string) (query, contextSnippet string) {
	target = strings.TrimSpace(target)
	indexable := document.StripComments(body)
	if strings.TrimSpace(indexable) == "" {
		return target, ""
	}
	window := ContextWindowAroundTarget(indexable, target, ContextWindowRunes)
	window = collapseWhitespace(window)
	if window == "" {
		return target, ""
	}
	return target + "\n" + window, window
}

// ContextWindowAroundTarget returns up to windowRunes of body text centered on
// the first [[target]] occurrence (case-insensitive).
func ContextWindowAroundTarget(body, target string, windowRunes int) string {
	if windowRunes <= 0 {
		windowRunes = ContextWindowRunes
	}
	runes := []rune(body)
	if len(runes) == 0 {
		return ""
	}

	needle := "[[" + target
	lowerBody := strings.ToLower(body)
	lowerNeedle := strings.ToLower(needle)
	byteIdx := strings.Index(lowerBody, lowerNeedle)
	center := 0
	if byteIdx >= 0 {
		center = utf8.RuneCountInString(body[:byteIdx])
	} else {
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

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// SplitTargetWords replaces a link target's separators with spaces so BM25 sees
// individual words instead of one phrase.
func SplitTargetWords(target string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', '/':
			return ' '
		}
		return r
	}, target)
}

// AssignTargetConfidence stamps each candidate's Confidence per the deterministic
// word-match x dominance rule.
func AssignTargetConfidence(results []SuggestLinkResult, target string, uniqueDrift map[string]bool) {
	decoded := strings.TrimSuffix(document.DecodeLinkTarget(target), ".md")
	cleanTarget := BasenameOrTarget(decoded)

	matchesTarget := func(r SuggestLinkResult) bool {
		if TargetWordMatch(decoded, r.Title, r.Path) {
			return true
		}
		return cleanTarget != "" && cleanTarget != decoded && TargetWordMatch(cleanTarget, r.Title, r.Path)
	}

	wordMatches := make([]bool, len(results))
	wordMatchCount := 0
	for i := range results {
		if matchesTarget(results[i]) {
			wordMatches[i] = true
			wordMatchCount++
		}
	}

	matchQuery := decoded
	if cleanTarget != "" {
		matchQuery = cleanTarget
	}

	for i := range results {
		if uniqueDrift[results[i].Path] {
			results[i].Confidence = "high"
			continue
		}
		wordMatch := wordMatches[i]
		obviousSole := wordMatch && wordMatchCount == 1 && IsObviousTargetMatch(matchQuery, results[i].Title, results[i].Path)
		dominant := dominantAmong(i, results)
		switch {
		case wordMatch && (dominant || obviousSole):
			results[i].Confidence = "high"
		case wordMatch || dominant:
			results[i].Confidence = "medium"
		default:
			results[i].Confidence = "low"
		}
	}
}

// HasHighConfidence reports whether any candidate is graded "high".
func HasHighConfidence(results []SuggestLinkResult) bool {
	for _, r := range results {
		if r.Confidence == "high" {
			return true
		}
	}
	return false
}

// TargetWordMatch reports whether target, folded with NormalizeName, equals or
// is a whole-word subset of the candidate's folded title or basename.
func TargetWordMatch(target, title, path string) bool {
	folded := NormalizeName(target)
	base := strings.TrimSuffix(filepath.Base(path), ".md")
	return isWholeWordSubset(folded, NormalizeName(title)) ||
		isWholeWordSubset(folded, NormalizeName(base))
}

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
	return results[i].Score >= DominantScoreRatio*bestOther
}

// ComputeTargetRecommendation derives the recommendation from the final
// candidate list and the LLM outcome.
func ComputeTargetRecommendation(results []SuggestLinkResult, llmOutcome string) SuggestRecommendation {
	relinkTo := func(c SuggestLinkResult) SuggestRecommendation {
		return SuggestRecommendation{
			Action: "relink", To: c.Path, Title: c.Title,
			Confidence: c.Confidence, Reason: c.Reason,
		}
	}
	if len(results) > 0 && results[0].Confidence == "high" {
		return relinkTo(results[0])
	}
	if llmOutcome == LLMOutcomeDeclined {
		return SuggestRecommendation{Action: "unlink"}
	}
	if len(results) > 0 && results[0].Confidence == "medium" {
		return relinkTo(results[0])
	}
	return SuggestRecommendation{Action: "unlink"}
}

func confidenceRank(c string) int {
	switch c {
	case "high":
		return 0
	case "medium":
		return 1
	case "low":
		return 2
	default:
		return 3
	}
}

// SortSuggestionsByConfidence stably orders candidates by confidence tier, then
// score descending within a tier.
func SortSuggestionsByConfidence(results []SuggestLinkResult) {
	sort.SliceStable(results, func(i, j int) bool {
		ri, rj := confidenceRank(results[i].Confidence), confidenceRank(results[j].Confidence)
		if ri != rj {
			return ri < rj
		}
		return results[i].Score > results[j].Score
	})
}

// RerankTargetCandidatesLLM asks the injected GenerationProvider to order the
// grounded shortlist using the given system prompt.
func RerankTargetCandidatesLLM(ctx context.Context, generator ai.GenerationProvider, system, target, contextSnippet string, candidates []SuggestLinkResult) ([]SuggestLinkResult, int, ai.GenUsage, error) {
	if generator == nil {
		return nil, 0, ai.GenUsage{}, fmt.Errorf("rerank target: nil generator")
	}
	byPath := make(map[string]SuggestLinkResult, len(candidates))
	for _, c := range candidates {
		byPath[c.Path] = c
	}
	user := BuildRerankUser(target, contextSnippet, BuildRerankCatalog(candidates, true))
	opts := ai.GenOpts{
		Temperature:     nil,
		MaxTokens:       1024,
		SystemPrompt:    system,
		ReasoningEffort: "none",
	}
	var out string
	var usage ai.GenUsage
	var err error
	if ug, ok := generator.(ai.UsageGenerator); ok {
		out, usage, err = ug.GenerateWithUsage(ctx, user, opts)
	} else {
		out, err = generator.Generate(ctx, user, opts)
	}
	if err != nil {
		return nil, 0, ai.GenUsage{}, err
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		usage = ai.GenUsage{
			InputTokens:  max(1, (len(system)+len(user))/4),
			OutputTokens: max(1, len(out)/4),
		}
	}
	picks, err := ParseLLMPicks(out)
	if err != nil {
		return nil, 0, usage, err
	}
	applied := 0
	for _, p := range picks {
		if _, ok := byPath[strings.TrimSpace(p.Path)]; ok {
			applied++
		}
	}
	return ApplyLLMPicks(candidates, picks, byPath), applied, usage, nil
}

// BuildRerankCatalog renders the grounded shortlist for the model.
func BuildRerankCatalog(candidates []SuggestLinkResult, includeScores bool) string {
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

// BuildRerankUser composes the re-rank user message.
func BuildRerankUser(target, contextSnippet, catalog string) string {
	var user strings.Builder
	fmt.Fprintf(&user, "Broken wikilink target: %q\n", target)
	if contextSnippet != "" {
		fmt.Fprintf(&user, "Surrounding note context:\n%s\n\n", contextSnippet)
	}
	user.WriteString("Shortlist (grounded existing notes):\n")
	user.WriteString(catalog)
	return user.String()
}

// ParseLLMPicks extracts a JSON array of {path, reason} from a model response.
func ParseLLMPicks(raw string) ([]LLMPick, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimSpace(s)
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
		s = strings.TrimSpace(s)
	}
	if i := strings.IndexByte(s, '['); i >= 0 {
		if j := strings.LastIndexByte(s, ']'); j > i {
			s = s[i : j+1]
		}
	}
	var picks []LLMPick
	if err := json.Unmarshal([]byte(s), &picks); err != nil {
		return nil, fmt.Errorf("parse llm picks: %w", err)
	}
	return picks, nil
}

// ApplyLLMPicks reorders the candidate list: LLM-chosen paths first (with
// reason + confidence capped at medium), then unused original candidates.
func ApplyLLMPicks(original []SuggestLinkResult, picks []LLMPick, byPath map[string]SuggestLinkResult) []SuggestLinkResult {
	if byPath == nil {
		byPath = make(map[string]SuggestLinkResult, len(original))
		for _, c := range original {
			byPath[c.Path] = c
		}
	}
	seen := make(map[string]bool)
	out := make([]SuggestLinkResult, 0, len(original))

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
			continue
		}
		if c.Confidence != "high" {
			if c.Confidence == "" || c.Confidence == "low" {
				c.Confidence = "medium"
			}
		}
		c.Reason = strings.TrimSpace(p.Reason)
		out = append(out, c)
		seen[path] = true
		n++
		if n >= LLMTopN {
			break
		}
	}

	for _, c := range original {
		if seen[c.Path] {
			continue
		}
		out = append(out, c)
	}
	return out
}
