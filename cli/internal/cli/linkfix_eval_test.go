package cli

// Link-fix prompt eval: the ground-truth + LLM-as-judge experiment that selects
// the suggest-target re-rank system prompt (suggestTargetRerankSystem) and
// measures whether LLM promotions are precise enough to auto-apply (the
// "Fix all" gate). Method + results: docs/link-prompt-eval.md.
//
// Ground truth is planted: the vault's RESOLVED wikilinks are free labels
// (source note, authored target, intended note). Each sampled link's target is
// corrupted per class — separator/case drift, typo, word reorder, word drop,
// LLM paraphrase — plus fabricated no-match negatives, then the production
// pipeline (gatherSuggestions + llmRerankPicks + applyLLMPicks) runs per prompt
// variant and is scored by exact path match, which is deterministic and free of
// judge noise. The LLM judge covers only what planted truth cannot: it is first
// CALIBRATED on planted-truth cases (judge-vs-truth agreement), then audits
// declined negatives ("was unlink the right call?") and the vault's real broken
// links.
//
// Double-gated like the polish prompt eval: real Bedrock credentials AND
// RUN_LINKFIX_EVAL=1 (it spends real money — roughly n_cases x n_variants
// generation calls plus a few dozen judge calls).
//
//	RUN_LINKFIX_EVAL=1 2NB_EVAL_VAULT=/path/to/vault \
//	  go test ./internal/cli/ -run LinkFixEval -v -count=1
//
// Knobs: LINKFIX_EVAL_N (positives per class, default 6), LINKFIX_EVAL_SEED
// (default 42), LINKFIX_EVAL_JUDGE (default us.anthropic.claude-sonnet-4-6).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/document"
	"github.com/apresai/2ndbrain/internal/polish"
	"github.com/apresai/2ndbrain/internal/store"
	"github.com/apresai/2ndbrain/internal/vault"
)

// ---------- corpus ----------

type linkfixCase struct {
	Class  string `json:"class"`  // drift|typo|reorder|worddrop|paraphrase|negative
	Source string `json:"source"` // vault-relative note containing the (broken) link
	Target string `json:"target"` // corrupted authored target
	Truth  string `json:"truth"`  // vault-relative path of the intended note; "" for negatives
}

type resolvedPair struct {
	source string
	raw    string // authored target as written
	truth  string // resolved vault-relative path
	title  string // truth note title (for paraphrase generation)
}

// harvestResolvedLinks walks the live vault and returns every resolved,
// non-asset, non-self wikilink as a (source, raw target, truth path) pair,
// deduped by source|truth and sorted for determinism.
func harvestResolvedLinks(t *testing.T, root string) []resolvedPair {
	t.Helper()
	docs, aliases, err := vault.CollectLiveDocs(root)
	if err != nil {
		t.Fatalf("collect live docs: %v", err)
	}
	resolver := store.NewResolver(docs, aliases)
	titleByPath := make(map[string]string, len(docs))
	paths := make([]string, 0, len(docs))
	for _, d := range docs {
		titleByPath[d.Path] = d.Title
		paths = append(paths, d.Path)
	}
	sort.Strings(paths)

	seen := make(map[string]bool)
	var pairs []resolvedPair
	for _, p := range paths {
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		content, rerr := os.ReadFile(filepath.Join(root, p))
		if rerr != nil {
			continue
		}
		parsed, perr := document.Parse(filepath.Join(root, p), content)
		if perr != nil {
			continue
		}
		for _, l := range document.ExtractWikiLinks(parsed.IndexableBody()) {
			target := strings.TrimSpace(l.Target)
			if target == "" || l.Embed {
				continue
			}
			if ext := filepath.Ext(target); ext != "" && ext != ".md" {
				continue // asset link
			}
			resolved, rerr := resolver.Resolve(target)
			if rerr != nil || resolved == p {
				continue
			}
			key := p + "|" + resolved
			if seen[key] {
				continue
			}
			seen[key] = true
			pairs = append(pairs, resolvedPair{source: p, raw: target, truth: resolved, title: titleByPath[resolved]})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].source != pairs[j].source {
			return pairs[i].source < pairs[j].source
		}
		return pairs[i].truth < pairs[j].truth
	})
	return pairs
}

// stridePairs picks up to n pairs evenly spread across the sorted slice,
// offset by seed (same reproducible-sampling idea as eval.GenerateQASet).
func stridePairs(pairs []resolvedPair, n int, seed int64) []resolvedPair {
	if len(pairs) <= n {
		return pairs
	}
	step := len(pairs) / n
	offset := int(seed) % step
	out := make([]resolvedPair, 0, n)
	for i := offset; i < len(pairs) && len(out) < n; i += step {
		out = append(out, pairs[i])
	}
	return out
}

// nameWords splits a link target into words on hyphen/underscore/space.
func nameWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
}

// primarySeparator returns the separator style the target already uses so
// corruptions keep the author's form.
func primarySeparator(s string) string {
	if strings.Contains(s, "-") {
		return "-"
	}
	if strings.Contains(s, "_") {
		return "_"
	}
	return " "
}

// driftCorrupt flips the separator/case style: kebab/underscore names become
// Title Case with spaces; spaced names become kebab lowercase. The normalized
// form is unchanged, so the deterministic drift tier must still resolve it.
func driftCorrupt(target string) string {
	words := nameWords(target)
	if len(words) == 0 {
		return ""
	}
	if strings.ContainsAny(target, "-_") {
		titled := make([]string, len(words))
		for i, w := range words {
			r := []rune(w)
			r[0] = unicode.ToUpper(r[0])
			titled[i] = string(r)
		}
		return strings.Join(titled, " ")
	}
	return strings.ToLower(strings.Join(words, "-"))
}

// typoCorrupt drops the middle character of the longest word (>=5 runes).
func typoCorrupt(target string) string {
	words := nameWords(target)
	longest, li := 0, -1
	for i, w := range words {
		if n := len([]rune(w)); n >= 5 && n > longest {
			longest, li = n, i
		}
	}
	if li < 0 {
		return ""
	}
	r := []rune(words[li])
	mid := len(r) / 2
	words[li] = string(r[:mid]) + string(r[mid+1:])
	return strings.Join(words, primarySeparator(target))
}

// reorderCorrupt rotates the words left by one (first word moves to the end).
func reorderCorrupt(target string) string {
	words := nameWords(target)
	if len(words) < 2 {
		return ""
	}
	rotated := append(words[1:], words[0])
	return strings.Join(rotated, primarySeparator(target))
}

// worddropCorrupt drops the last word (needs >=3 words to stay meaningful).
func worddropCorrupt(target string) string {
	words := nameWords(target)
	if len(words) < 3 {
		return ""
	}
	return strings.Join(words[:len(words)-1], primarySeparator(target))
}

// negativeTargets are fabricated link targets that should match no note in a
// personal dev vault; any that accidentally resolve are filtered at build time.
var negativeTargets = []string{
	"quarterly-okr-planning-2019",
	"vendor-contract-renewal-tracker",
	"team-offsite-agenda-day-2",
	"legacy-crm-migration-runbook",
	"employee-onboarding-checklist-v2",
	"warehouse-inventory-audit-2018",
	"holiday-pto-schedule-2020",
	"customer-churn-postmortem-acme",
}

// buildLinkfixCorpus assembles the planted-truth cases. Every corrupted target
// is verified to still be broken (does not resolve), and non-drift classes are
// verified to be OUTSIDE the deterministic repair index (Lookup returns 0), so
// each class isolates the tier it is meant to exercise.
func buildLinkfixCorpus(t *testing.T, ctx context.Context, root string, gen ai.GenerationProvider, n int, seed int64) []linkfixCase {
	t.Helper()
	docs, aliases, err := vault.CollectLiveDocs(root)
	if err != nil {
		t.Fatalf("collect live docs: %v", err)
	}
	resolver := store.NewResolver(docs, aliases)
	stillBroken := func(target string) bool {
		_, rerr := resolver.Resolve(target)
		return errors.Is(rerr, store.ErrTargetNotFound)
	}
	driftHits := func(target string) int {
		return len(polish.SuggestRepairTargets(docs, aliases, target))
	}

	pairs := harvestResolvedLinks(t, root)
	if len(pairs) < 5 {
		t.Skipf("vault has only %d resolved links; need >=5 for a meaningful corpus", len(pairs))
	}
	t.Logf("corpus: %d resolved links harvested", len(pairs))

	type corrupter struct {
		class     string
		fn        func(string) string
		wantDrift bool // corrupted form should stay inside the repair index
	}
	corrupters := []corrupter{
		{"drift", driftCorrupt, true},
		{"typo", typoCorrupt, false},
		{"reorder", reorderCorrupt, false},
		{"worddrop", worddropCorrupt, false},
	}

	var cases []linkfixCase
	for _, c := range corrupters {
		// Spread each class across the vault with a class-distinct seed so
		// classes don't all sample the same links.
		sampled := stridePairs(pairs, n*3, seed+int64(len(c.class)))
		count := 0
		for _, p := range sampled {
			if count >= n {
				break
			}
			corrupted := c.fn(p.raw)
			if corrupted == "" || strings.EqualFold(corrupted, p.raw) || !stillBroken(corrupted) {
				continue
			}
			hits := driftHits(corrupted)
			if c.wantDrift && hits != 1 {
				continue // drift class must be exactly the repairable case
			}
			if !c.wantDrift && hits != 0 {
				continue // keep non-drift classes outside the deterministic index
			}
			cases = append(cases, linkfixCase{Class: c.class, Source: p.source, Target: corrupted, Truth: p.truth})
			count++
		}
		if count < n {
			t.Logf("corpus: class %s got %d/%d cases (vault shape limits)", c.class, count, n)
		}
	}

	// Paraphrase class: LLM-fabricated conceptual renames, cached per vault+seed
	// so re-runs are free and deterministic.
	cases = append(cases, paraphraseCases(t, ctx, root, gen, pairs, stillBroken, driftHits, n, seed)...)

	// Negatives: fabricated targets matching nothing, attached to real source
	// notes so the pipeline sees realistic (unrelated) context.
	negSources := stridePairs(pairs, len(negativeTargets), seed+101)
	for i, neg := range negativeTargets {
		if !stillBroken(neg) || driftHits(neg) != 0 {
			continue
		}
		src := ""
		if len(negSources) > 0 {
			src = negSources[i%len(negSources)].source
		}
		cases = append(cases, linkfixCase{Class: "negative", Source: src, Target: neg, Truth: ""})
	}
	return cases
}

// paraphraseCases generates the conceptual-rename class: the model writes a
// plausible from-memory link target for a real note using different wording.
// Cached under os.TempDir() (never inside the vault tree).
func paraphraseCases(t *testing.T, ctx context.Context, root string, gen ai.GenerationProvider,
	pairs []resolvedPair, stillBroken func(string) bool, driftHits func(string) int, n int, seed int64) []linkfixCase {
	t.Helper()
	cachePath := filepath.Join(os.TempDir(), fmt.Sprintf("2nb-linkfix-paraphrase-%x-%d.json", hashString(root), seed))
	if data, err := os.ReadFile(cachePath); err == nil {
		var cached []linkfixCase
		if json.Unmarshal(data, &cached) == nil && len(cached) > 0 {
			t.Logf("corpus: paraphrase class loaded from cache (%s)", cachePath)
			return cached
		}
	}

	var out []linkfixCase
	sampled := stridePairs(pairs, n*3, seed+977)
	for _, p := range sampled {
		if len(out) >= n {
			break
		}
		title := p.title
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(p.truth), ".md")
		}
		user := fmt.Sprintf(`The author meant the note titled %q (file %s). Write ONE plausible broken wikilink target they might have typed from memory months later: the same specific concept but DIFFERENT wording (synonyms, abbreviations, reordering), kebab-case, 2-6 words. It must NOT reuse more than half of the title's words. Respond with ONLY the target text, nothing else.`, title, filepath.Base(p.truth))
		resp, err := gen.Generate(ctx, user, ai.GenOpts{
			Temperature: ai.Ptr(0.3), MaxTokens: 40, ReasoningEffort: "none",
			SystemPrompt: "You fabricate realistic broken Obsidian wikilink targets for an evaluation corpus. Output only the bare target text.",
		})
		if err != nil {
			t.Logf("corpus: paraphrase generation failed for %s: %v", p.truth, err)
			continue
		}
		corrupted := strings.Trim(strings.TrimSpace(resp), "[]`\"' ")
		if corrupted == "" || strings.EqualFold(corrupted, p.raw) || !stillBroken(corrupted) || driftHits(corrupted) != 0 {
			continue
		}
		out = append(out, linkfixCase{Class: "paraphrase", Source: p.source, Target: corrupted, Truth: p.truth})
	}
	if data, err := json.MarshalIndent(out, "", "  "); err == nil {
		_ = os.WriteFile(cachePath, data, 0o644)
	}
	return out
}

func hashString(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// ---------- prompt variants ----------

type linkfixVariant struct {
	name          string
	system        string
	includeScores bool
}

// promptBaselinePick3 is the pre-eval shipped prompt, kept as the comparison
// baseline (it lost to strict_plausibility, now suggestTargetRerankSystem, on
// promotion precision 0.75 vs 0.83 with identical ranking metrics).
const promptBaselinePick3 = `You help fix broken Obsidian [[wikilinks]] by picking the best EXISTING notes from a shortlist.

Rules:
- Choose at most 3 candidates from the shortlist, best first.
- You may ONLY return paths that appear in the shortlist. Never invent a path or title.
- If none of the shortlist notes is a plausible meaning for the broken target, return an empty list [].
- Prefer notes the author likely meant (typo, rename, case/separator drift, related topic in context) over weak topical neighbors.
- Each reason is one short plain sentence (no markdown).

Respond with ONLY a JSON array, no prose:
[{"path":"exact/path.md","reason":"why this note"}, ...]`

const promptConfidenceVerdict = `You help fix broken Obsidian [[wikilinks]]. A note in the user's vault contains a link whose target no longer matches any note. From a grounded shortlist of EXISTING notes, identify what the author actually meant.

Rules:
- Return at most 3 candidates, best first, each with a confidence:
  - "high": you are confident this IS the note the author meant (the broken target reads as a typo, rename, reordering, or paraphrase of this exact note). It would be safe to rewrite the link automatically.
  - "medium": plausibly the intended note, but a human should confirm.
  - "low": only weakly related; include only if nothing better exists.
- You may ONLY return paths that appear in the shortlist. Never invent a path or title.
- If none of the shortlist notes is a plausible meaning for the broken target, return an empty list [] — that tells the user to remove the link.
- A note being on the same TOPIC is not enough: the broken target must read as a name for THAT note.
- Each reason is one short plain sentence (no markdown).

Respond with ONLY a JSON array, no prose:
[{"path":"exact/path.md","confidence":"high|medium|low","reason":"why this note"}, ...]`

const promptFewshotDecline = `You help fix broken Obsidian [[wikilinks]] by picking the best EXISTING notes from a shortlist.

Rules:
- Choose at most 3 candidates from the shortlist, best first.
- You may ONLY return paths that appear in the shortlist. Never invent a path or title.
- If none of the shortlist notes is a plausible meaning for the broken target, return an empty list [].
- Prefer notes the author likely meant (typo, rename, case/separator drift, reordered or paraphrased name) over weak topical neighbors.
- Each reason is one short plain sentence (no markdown).

Examples:
Target "notarytool-sigbus-crash-fix", shortlist has "notarytool-submit-wait-sigbus-crash-and-the-poll-info-workaround.md":
[{"path":"resources/notarytool-submit-wait-sigbus-crash-and-the-poll-info-workaround.md","reason":"Same specific crash and fix, shortened name"}]
Target "bedrock-models-aws-latest", shortlist has "aws-bedrock-latest-models-ids-endpoints-access-2026-06.md":
[{"path":"resources/aws-bedrock-latest-models-ids-endpoints-access-2026-06.md","reason":"Identical words reordered"}]
Target "team-standup-notes-march", shortlist has only notes about CLI tools and AWS billing:
[]

Respond with ONLY a JSON array, no prose:
[{"path":"exact/path.md","reason":"why this note"}, ...]`

var linkfixVariants = []linkfixVariant{
	{"current_shipped", suggestTargetRerankSystem, true}, // = strict_plausibility, the 2026-07 winner
	{"baseline_pick3", promptBaselinePick3, true},
	{"no_scores", promptBaselinePick3, false},
	{"confidence_verdict", promptConfidenceVerdict, false},
	{"fewshot_decline", promptFewshotDecline, true},
}

// ---------- scoring ----------

type classStats struct {
	n, truthInPool                 int
	top1, top3                     int
	llmSkipped, declined, llmError int
	promoted, promotedCorrect      int
	modelHigh, modelHighCorrect    int
	mrrSum                         float64
}

func rankOf(results []SuggestLinkResult, truth string, k int) int {
	for i, r := range results {
		if i >= k {
			break
		}
		if r.Path == truth {
			return i + 1
		}
	}
	return 0
}

// ---------- judge ----------

const linkfixJudgeSystem = `You judge broken-link repairs in a personal Obsidian vault. A note contained a wikilink whose target matches no existing note. A repair tool proposes ONE candidate note as what the author actually meant.

Decide:
- "yes": the candidate is clearly the note the author meant — the broken target reads as a typo, rename, truncation, reordering, or natural paraphrase of this specific note.
- "no": the candidate is a different thing (even if topically related).
- "unsure": genuinely undecidable from the evidence.

Also grade reason_quality 1-5: how accurate and specific the tool's stated reason is (5 = precise and correct; 1 = wrong or vacuous). Use 0 when no reason is given.

Respond with ONLY a JSON object:
{"verdict":"yes|no|unsure","reason_quality":N}`

type judgeVerdict struct {
	Verdict       string `json:"verdict"`
	ReasonQuality int    `json:"reason_quality"`
}

func judgeMatch(t *testing.T, ctx context.Context, judge ai.GenerationProvider, target, snippet string, cand SuggestLinkResult, reason string) (judgeVerdict, error) {
	t.Helper()
	if reason == "" {
		reason = "(none)"
	}
	var user strings.Builder
	fmt.Fprintf(&user, "Broken wikilink target: %q\n", target)
	if snippet != "" {
		fmt.Fprintf(&user, "Context around the link in the source note:\n%s\n\n", snippet)
	}
	title := cand.Title
	if title == "" {
		title = cand.Path
	}
	fmt.Fprintf(&user, "Proposed candidate note:\npath=%s\ntitle=%q\nsnippet=%s\n\nTool's stated reason: %s\n", cand.Path, title, cand.Snippet, reason)
	out, err := judge.Generate(ctx, user.String(), ai.GenOpts{
		Temperature: ai.Ptr(0.0), MaxTokens: 60, SystemPrompt: linkfixJudgeSystem, ReasoningEffort: "none",
	})
	if err != nil {
		return judgeVerdict{}, err
	}
	js := out
	if i := strings.IndexByte(out, '{'); i >= 0 {
		if j := strings.LastIndexByte(out, '}'); j > i {
			js = out[i : j+1]
		}
	}
	var v judgeVerdict
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		return judgeVerdict{}, fmt.Errorf("parse judge %q: %w", out, err)
	}
	return v, nil
}

// preparedCase is one corpus case with its shared deterministic pool.
type preparedCase struct {
	linkfixCase
	pool    []SuggestLinkResult
	snippet string
	skipLLM bool // a high-confidence candidate short-circuits the LLM tier
}

// ---------- the eval ----------

func TestLinkFixEval(t *testing.T) {
	if os.Getenv("RUN_LINKFIX_EVAL") != "1" {
		t.Skip("set RUN_LINKFIX_EVAL=1 (and 2NB_EVAL_VAULT) to run the link-fix prompt eval; it calls paid Bedrock APIs")
	}
	vpath := os.Getenv("2NB_EVAL_VAULT")
	if vpath == "" {
		t.Skip("set 2NB_EVAL_VAULT to a Nova-embedded vault")
	}
	v, err := vault.Open(vpath)
	if err != nil {
		t.Skipf("open vault %q: %v", vpath, err)
	}
	defer v.Close()
	cfg := v.Config.AI
	if cfg.Provider != "bedrock" {
		t.Skipf("eval needs a bedrock vault, got provider %q", cfg.Provider)
	}
	ctx := context.Background()
	initAIProviders(v)
	gen, err := ai.DefaultRegistry.Generator("bedrock")
	if err != nil {
		t.Skipf("no generator (creds?): %v", err)
	}
	if !gen.Available(ctx) {
		t.Skip("bedrock generator unavailable (no creds)")
	}
	judgeModel := os.Getenv("LINKFIX_EVAL_JUDGE")
	if judgeModel == "" {
		judgeModel = "us.anthropic.claude-sonnet-4-6"
	}
	judge, err := ai.NewBedrockGenerator(ctx, cfg.Bedrock, judgeModel)
	if err != nil {
		t.Skipf("judge: %v", err)
	}

	n := 6
	if s := os.Getenv("LINKFIX_EVAL_N"); s != "" {
		if k, err := strconv.Atoi(s); err == nil && k > 0 {
			n = k
		}
	}
	seed := int64(42)
	if s := os.Getenv("LINKFIX_EVAL_SEED"); s != "" {
		if k, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = k
		}
	}

	cases := buildLinkfixCorpus(t, ctx, v.Root, gen, n, seed)
	if len(cases) < 8 {
		t.Skipf("only %d eval cases could be built from this vault", len(cases))
	}
	byClass := map[string]int{}
	for _, c := range cases {
		byClass[c.Class]++
	}
	t.Logf("corpus: %d cases %v; ~%d generation calls across %d variants (Haiku) + judge calls (Sonnet)",
		len(cases), byClass, len(cases)*len(linkfixVariants), len(linkfixVariants))

	// Gather the deterministic pool ONCE per case (one embed call each);
	// variants share it, exactly as production would.
	prepared := make([]preparedCase, 0, len(cases))
	for _, c := range cases {
		pool, snippet := gatherSuggestions(ctx, v, c.Target, c.Source, llmPoolCap)
		prepared = append(prepared, preparedCase{c, pool, snippet, hasHighConfidence(pool)})
	}

	// Per-variant scoring over the shared pools.
	stats := make(map[string]map[string]*classStats, len(linkfixVariants))
	for _, variant := range linkfixVariants {
		perClass := map[string]*classStats{}
		stats[variant.name] = perClass
		for _, pc := range prepared {
			cs := perClass[pc.Class]
			if cs == nil {
				cs = &classStats{}
				perClass[pc.Class] = cs
			}
			cs.n++
			truthInPool := pc.Truth != "" && rankOf(pc.pool, pc.Truth, len(pc.pool)) > 0
			if truthInPool {
				cs.truthInPool++
			}

			final := pc.pool
			var picks []llmPick
			switch {
			case pc.skipLLM || len(pc.pool) == 0:
				cs.llmSkipped++
			default:
				user := buildRerankUser(pc.Target, pc.snippet, buildRerankCatalog(pc.pool, variant.includeScores))
				var perr error
				picks, perr = llmRerankPicks(ctx, cfg, variant.system, user)
				switch {
				case perr != nil:
					cs.llmError++
					t.Logf("[%s] %s [[%s]]: llm error: %v", variant.name, pc.Class, pc.Target, perr)
				case len(picks) == 0:
					cs.declined++
				default:
					cs.promoted++
					final = applyLLMPicks(pc.pool, picks, nil)
					if pc.Truth != "" && len(final) > 0 && final[0].Path == pc.Truth {
						cs.promotedCorrect++
					}
					// Per-pick model confidence (only emitted by prompts that ask).
					if strings.EqualFold(picks[0].Confidence, "high") {
						cs.modelHigh++
						if pc.Truth != "" && len(final) > 0 && final[0].Path == pc.Truth {
							cs.modelHighCorrect++
						}
					}
				}
			}

			if pc.Truth != "" {
				if r := rankOf(final, pc.Truth, 3); r > 0 {
					cs.top3++
					cs.mrrSum += 1.0 / float64(r)
					if r == 1 {
						cs.top1++
					}
				}
			}
		}
	}

	// ---- report ----
	classOrder := []string{"drift", "typo", "reorder", "worddrop", "paraphrase", "negative"}
	for _, variant := range linkfixVariants {
		t.Logf("=== variant %s ===", variant.name)
		perClass := stats[variant.name]
		var pos, posTop1, posTop3, posInPool int
		var promoted, promotedCorrect, modelHigh, modelHighCorrect int
		var mrr float64
		for _, cl := range classOrder {
			cs := perClass[cl]
			if cs == nil {
				continue
			}
			if cl == "negative" {
				t.Logf("  %-10s n=%-2d declined=%d falsePromote=%d llmErr=%d",
					cl, cs.n, cs.declined, cs.promoted, cs.llmError)
				continue
			}
			t.Logf("  %-10s n=%-2d inPool=%-2d top1=%-2d top3=%-2d mrr@3=%.3f skip=%d declined=%d promoted=%d(ok %d) llmErr=%d",
				cl, cs.n, cs.truthInPool, cs.top1, cs.top3, safeDiv(cs.mrrSum, cs.n), cs.llmSkipped, cs.declined, cs.promoted, cs.promotedCorrect, cs.llmError)
			pos += cs.n
			posTop1 += cs.top1
			posTop3 += cs.top3
			posInPool += cs.truthInPool
			promoted += cs.promoted
			promotedCorrect += cs.promotedCorrect
			modelHigh += cs.modelHigh
			modelHighCorrect += cs.modelHighCorrect
			mrr += cs.mrrSum
		}
		neg := perClass["negative"]
		t.Logf("  TOTAL positives n=%d top1=%.2f top3=%.2f mrr@3=%.3f retrievalMiss=%d promotionPrecision=%s modelHighPrecision=%s",
			pos, safeDiv(float64(posTop1), pos), safeDiv(float64(posTop3), pos), safeDiv(mrr, pos),
			pos-posInPool, ratio(promotedCorrect, promoted), ratio(modelHighCorrect, modelHigh))
		if neg != nil {
			t.Logf("  TOTAL negatives n=%d abstention=%s falsePromotions=%d", neg.n, ratio(neg.declined, neg.n-neg.llmError), neg.promoted)
		}
	}

	// ---- judge calibration on planted truth ----
	runJudgeCalibration(t, ctx, judge, prepared)

	// ---- decline audit: for negatives, was "remove the link" the right call? ----
	runDeclineAudit(t, ctx, judge, prepared)

	// ---- the vault's real broken links through the pipeline (no truth; judged) ----
	runRealBrokenLinks(t, ctx, v, cfg, judge)
}

func safeDiv(a float64, b int) float64 {
	if b == 0 {
		return 0
	}
	return a / float64(b)
}

func ratio(num, den int) string {
	if den == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.2f (%d/%d)", float64(num)/float64(den), num, den)
}

// runJudgeCalibration measures the judge against planted truth BEFORE it is
// trusted on anything truthless (U2 in the plan): for sampled positive cases
// whose truth made the pool, the judge must say "yes" to the true note and
// "no" to the strongest wrong candidate. Reported as agreement fractions.
func runJudgeCalibration(t *testing.T, ctx context.Context, judge ai.GenerationProvider, prepared []preparedCase) {
	t.Helper()
	const maxCalib = 12
	var yesOK, yesN, noOK, noN int
	for _, pc := range prepared {
		if yesN >= maxCalib {
			break
		}
		if pc.Truth == "" {
			continue
		}
		var truthCand *SuggestLinkResult
		var decoy *SuggestLinkResult
		for i := range pc.pool {
			if pc.pool[i].Path == pc.Truth {
				truthCand = &pc.pool[i]
			} else if decoy == nil {
				decoy = &pc.pool[i]
			}
		}
		if truthCand == nil {
			continue // retrieval miss; nothing to calibrate on
		}
		if v, err := judgeMatch(t, ctx, judge, pc.Target, pc.snippet, *truthCand, ""); err == nil {
			yesN++
			if v.Verdict == "yes" {
				yesOK++
			} else {
				t.Logf("judge calib: expected yes, got %q for [[%s]] -> %s (class %s)", v.Verdict, pc.Target, truthCand.Path, pc.Class)
			}
		}
		if decoy != nil {
			if v, err := judgeMatch(t, ctx, judge, pc.Target, pc.snippet, *decoy, ""); err == nil {
				noN++
				if v.Verdict != "yes" {
					noOK++
				} else {
					t.Logf("judge calib: expected no/unsure, got yes for [[%s]] -> %s (class %s)", pc.Target, decoy.Path, pc.Class)
				}
			}
		}
	}
	t.Logf("=== judge calibration (planted truth) === truth-accepted=%s decoy-rejected=%s", ratio(yesOK, yesN), ratio(noOK, noN))
}

// runDeclineAudit judges the abstention behavior on negatives: when the target
// matches no real note, is "remove the link" the right call? The judge sees the
// pipeline's best deterministic candidate; a "no"/"unsure" verdict confirms the
// unlink recommendation.
func runDeclineAudit(t *testing.T, ctx context.Context, judge ai.GenerationProvider, prepared []preparedCase) {
	t.Helper()
	var confirmed, n int
	for _, pc := range prepared {
		if pc.Class != "negative" || len(pc.pool) == 0 {
			continue
		}
		v, err := judgeMatch(t, ctx, judge, pc.Target, pc.snippet, pc.pool[0], "")
		if err != nil {
			continue
		}
		n++
		if v.Verdict != "yes" {
			confirmed++
		} else {
			t.Logf("decline audit: judge says the best candidate DOES match negative [[%s]] -> %s", pc.Target, pc.pool[0].Path)
		}
	}
	t.Logf("=== decline audit (negatives) === unlink-confirmed=%s", ratio(confirmed, n))
}

// runRealBrokenLinks runs the vault's genuinely broken links (no planted
// truth) through every variant and has the judge grade any promoted top pick —
// the truthless leg the calibrated judge exists for.
func runRealBrokenLinks(t *testing.T, ctx context.Context, v *vault.Vault, cfg ai.AIConfig, judge ai.GenerationProvider) {
	t.Helper()
	docs, aliases, err := vault.CollectLiveDocs(v.Root)
	if err != nil {
		return
	}
	resolver := store.NewResolver(docs, aliases)
	type brokenLink struct{ source, target string }
	var broken []brokenLink
	pairsSeen := map[string]bool{}
	paths := make([]string, 0, len(docs))
	for _, d := range docs {
		paths = append(paths, d.Path)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if len(broken) >= 8 {
			break
		}
		if !strings.HasSuffix(p, ".md") {
			continue
		}
		content, rerr := os.ReadFile(filepath.Join(v.Root, p))
		if rerr != nil {
			continue
		}
		parsed, perr := document.Parse(filepath.Join(v.Root, p), content)
		if perr != nil {
			continue
		}
		for _, l := range document.ExtractWikiLinks(parsed.IndexableBody()) {
			target := strings.TrimSpace(l.Target)
			if target == "" || l.Embed {
				continue
			}
			if ext := filepath.Ext(target); ext != "" && ext != ".md" {
				continue
			}
			if _, rerr := resolver.Resolve(target); !errors.Is(rerr, store.ErrTargetNotFound) {
				continue
			}
			key := p + "|" + target
			if pairsSeen[key] {
				continue
			}
			pairsSeen[key] = true
			broken = append(broken, brokenLink{p, target})
		}
	}
	if len(broken) == 0 {
		t.Logf("=== real broken links === none in vault")
		return
	}
	t.Logf("=== real broken links (%d, judged, no planted truth) ===", len(broken))
	for _, b := range broken {
		pool, snippet := gatherSuggestions(ctx, v, b.target, b.source, llmPoolCap)
		if hasHighConfidence(pool) || len(pool) == 0 {
			t.Logf("  [[%s]] (%s): deterministic outcome (pool=%d, high=%v)", b.target, b.source, len(pool), hasHighConfidence(pool))
			continue
		}
		for _, variant := range linkfixVariants {
			user := buildRerankUser(b.target, snippet, buildRerankCatalog(pool, variant.includeScores))
			picks, perr := llmRerankPicks(ctx, cfg, variant.system, user)
			switch {
			case perr != nil:
				t.Logf("  [[%s]] %s: error %v", b.target, variant.name, perr)
			case len(picks) == 0:
				t.Logf("  [[%s]] %s: declined -> would recommend unlink", b.target, variant.name)
			default:
				final := applyLLMPicks(pool, picks, nil)
				verdict := "?"
				if jv, jerr := judgeMatch(t, ctx, judge, b.target, snippet, final[0], picks[0].Reason); jerr == nil {
					verdict = jv.Verdict
				}
				t.Logf("  [[%s]] %s: promoted %s (conf=%s, judge=%s) reason=%s",
					b.target, variant.name, final[0].Path, picks[0].Confidence, verdict, picks[0].Reason)
			}
		}
	}
}
