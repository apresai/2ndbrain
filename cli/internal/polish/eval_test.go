package polish

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestPolishPromptEval is the LLM-as-judge experiment that selects the copy-edit
// system prompt shipped as DefaultPolishSystem. It is double-gated: it needs
// real Bedrock credentials AND RUN_POLISH_EVAL=1 (it makes many paid calls), so
// a normal `go test` never runs it.
//
//	RUN_POLISH_EVAL=1 go test -tags fts5 ./internal/polish/ -run PromptEval -v
//
// Each candidate prompt is run over a corpus of messy notes on the polish model
// (Haiku 4.5); a stronger, different model (Sonnet 4.6) judges each output on a
// 1-5 rubric. Mechanical gates (code/links/headings preserved, no invented
// links) are disqualifying. The winner is the highest weighted mean among
// prompts with zero mechanical failures.
func TestPolishPromptEval(t *testing.T) {
	ctx := context.Background()
	if os.Getenv("RUN_POLISH_EVAL") == "" {
		t.Skip("set RUN_POLISH_EVAL=1 to run the paid prompt-selection experiment")
	}
	cfg := ai.DefaultAIConfig()
	if !ai.CheckBedrockCredentials(ctx, cfg.Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}

	gen, err := ai.NewBedrockGenerator(ctx, cfg.Bedrock, cfg.GenerationModel)
	if err != nil {
		t.Fatalf("generator: %v", err)
	}
	judge, err := ai.NewBedrockGenerator(ctx, cfg.Bedrock, "us.anthropic.claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("judge: %v", err)
	}

	runs := 1
	if s := os.Getenv("POLISH_EVAL_RUNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			runs = n
		}
	}

	type agg struct {
		name      string
		total     float64
		n         int
		minSample float64
		mechFail  int
	}
	results := make([]agg, 0, len(candidatePrompts))

	for _, cand := range candidatePrompts {
		a := agg{name: cand.name, minSample: 1e9}
		for _, s := range evalSamples {
			var sampleScore float64
			sampleRuns := 0
			for r := 0; r < runs; r++ {
				polished, err := gen.Generate(ctx, s.body, ai.GenOpts{
					Temperature: ai.Ptr(0.2), MaxTokens: 4096, SystemPrompt: cand.prompt,
				})
				if err != nil {
					t.Fatalf("[%s/%s] generate: %v", cand.name, s.name, err)
				}
				polished = strings.TrimSpace(polished)

				// Mechanical gates, disqualifying.
				mechOK := CodeSpansEqual(s.body, polished) &&
					ExistingLinksPreserved(s.body, polished) &&
					HeadingStructureEqual(s.body, polished) &&
					len(NewLinks(s.body, polished)) == 0
				if !mechOK {
					a.mechFail++
					t.Logf("[%s/%s] MECHANICAL FAIL (code/links/headings/invented-link)", cand.name, s.name)
				}

				score := judgeOutput(t, ctx, judge, s.body, polished)
				sampleScore += score
				sampleRuns++
			}
			avg := sampleScore / float64(sampleRuns)
			a.total += avg
			a.n++
			if avg < a.minSample {
				a.minSample = avg
			}
		}
		results = append(results, a)
	}

	// Rank: zero-mechanical-failure prompts first, then by mean, then worst-case.
	sort.SliceStable(results, func(i, j int) bool {
		if (results[i].mechFail == 0) != (results[j].mechFail == 0) {
			return results[i].mechFail == 0
		}
		mi, mj := results[i].total/float64(results[i].n), results[j].total/float64(results[j].n)
		if mi != mj {
			return mi > mj
		}
		return results[i].minSample > results[j].minSample
	})

	t.Logf("=== Polish prompt eval (%d samples x %d runs, judge=sonnet-4-6) ===", len(evalSamples), runs)
	for rank, a := range results {
		t.Logf("#%d  %-16s  mean=%.2f  worst=%.2f  mech_fail=%d",
			rank+1, a.name, a.total/float64(a.n), a.minSample, a.mechFail)
	}
	t.Logf("Top by mean: %s. NOTE: gaps under ~0.1 are within judge noise (rankings", results[0].name)
	t.Logf("flip across runs); when candidates tie and all pass the mechanical gates, pick")
	t.Logf("on robustness, not the noisy #1. See docs/polish-prompt-eval.md.")
}

// judgeOutput asks the judge model to score a polished body against the original
// and returns the weighted 0-5 score (structure/voice/correctness weighted over
// restraint). On a parse failure it fails the test loudly rather than guessing.
func judgeOutput(t *testing.T, ctx context.Context, judge ai.GenerationProvider, original, polished string) float64 {
	t.Helper()
	user := "ORIGINAL:\n" + original + "\n\nPOLISHED:\n" + polished
	out, err := judge.Generate(ctx, user, ai.GenOpts{
		Temperature: ai.Ptr(0.0), MaxTokens: 200, SystemPrompt: judgeSystem,
	})
	if err != nil {
		t.Fatalf("judge generate: %v", err)
	}
	var score struct {
		ErrorCorrection       float64 `json:"error_correction"`
		VoiceMeaning          float64 `json:"voice_meaning"`
		StructurePreservation float64 `json:"structure_preservation"`
		Restraint             float64 `json:"restraint"`
	}
	js := extractJSON(out)
	if err := json.Unmarshal([]byte(js), &score); err != nil {
		t.Fatalf("parse judge JSON %q: %v", out, err)
	}
	// Weighted mean (weights sum to 7): correctness/voice/structure 2x, restraint 1x.
	return (2*score.ErrorCorrection + 2*score.VoiceMeaning + 2*score.StructurePreservation + score.Restraint) / 7.0
}

func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}

const judgeSystem = `You are a strict evaluator of copy-editing quality. You compare an ORIGINAL markdown note to a POLISHED version produced by a copy editor whose job was to fix spelling/grammar/punctuation and improve clarity while preserving the author's voice, meaning, wikilinks, code, headings, and lists.

Score the POLISHED version on four dimensions, each an integer from 1 to 5 (5 = excellent):
- error_correction: were spelling, grammar, and punctuation errors actually fixed?
- voice_meaning: was the author's voice and meaning preserved, with nothing added or removed?
- structure_preservation: were wikilinks, code (fenced and inline), headings, and lists preserved exactly?
- restraint: did it avoid unnecessary or preference-only rewrites?

Respond with ONLY a JSON object and nothing else:
{"error_correction":N,"voice_meaning":N,"structure_preservation":N,"restraint":N}`

type candidate struct {
	name   string
	prompt string
}

// candidatePrompts is the fixed, reproducible 4-way comparison: the shipped
// prompt (the "structured rules" winner) versus the three alternatives it beat.
var candidatePrompts = []candidate{
	{
		name:   "current_shipped",
		prompt: DefaultPolishSystem,
	},
	{
		name:   "alt_terse",
		prompt: `You are a copy editor. Fix spelling, grammar, and punctuation errors in the markdown below. Improve clarity where wording is awkward, but preserve the author's voice, all wikilinks like [[foo]], all code blocks (fenced and inline), and the heading structure exactly. Do not add or remove sections. Do not reformat lists. Return ONLY the corrected markdown body with no explanation, no commentary, and no surrounding code fences.`,
	},
	{
		name: "alt_meticulous",
		prompt: `You are a meticulous copy editor. Correct the markdown below: fix spelling, grammar, and punctuation, and rephrase only where wording is genuinely awkward or unclear. Make the smallest set of changes that achieves a clean, readable result.

Preserve exactly, byte for byte:
- the author's voice, tone, and meaning (never add facts, opinions, or new sentences, and never delete content);
- every wikilink ([[note]], [[note#heading]], [[note|alias]]) and every markdown link;
- every code block, fenced or inline (never edit text inside backticks);
- the heading structure and every list (do not merge, split, reorder, or reformat them).

Return ONLY the corrected markdown body. No preamble, no commentary, no explanation, and no surrounding code fences.`,
	},
	{
		name:   "alt_minimal_touch",
		prompt: `You are a careful copy editor. Make the SMALLEST set of edits that fixes clear spelling, grammar, and punctuation mistakes and resolves genuinely confusing wording. If a passage is already correct, leave it exactly as written. Never add new ideas, sentences, or sections; never delete content; never reword for mere preference. Keep every wikilink ([[...]]), markdown link, and code span (fenced or inline) byte-for-byte, and keep all headings and lists exactly as they are. Output ONLY the corrected markdown body, with no preamble, no commentary, and no code fences around it.`,
	},
}

type evalSample struct {
	name string
	body string
}

var evalSamples = []evalSample{
	{
		name: "typos",
		body: "# Meeting Notes\n\nWe discused the new feture and agreed it shoud ship by friday. The teh API needs more tests befor we can relase.\n",
	},
	{
		name: "awkward_clarity",
		body: "# Design\n\nThe thing that the system does when a user does the login is it checks the password and then it does the session thing which is what keeps them logged in.\n",
	},
	{
		name: "code_preservation",
		body: "# Snippet\n\nRun the command below to start the servver:\n\n```bash\necho \"teh server is startng\"  # keep this typo as-is\n```\n\nUse `git stauts` inline as well. The sourounding prose has typos to fix.\n",
	},
	{
		name: "wikilinks",
		body: "# Index\n\nSee [[Auth Flow]] and [[Token Store|tokens]] for the detials. The auth systen is documented their.\n",
	},
	{
		name: "lists_headings",
		body: "# Plan\n\n## Phase one\n\n- buy the ingredeints\n- prep the vegtables\n- cook everyting\n\n## Phase two\n\n1. plate the food\n2. serve it warm\n",
	},
	{
		name: "already_clean",
		body: "# Summary\n\nThis note records the final decision. We chose option B because it is simpler and cheaper to maintain. No further action is required.\n",
	},
}
