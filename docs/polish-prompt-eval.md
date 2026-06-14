# Polish prompt selection (LLM-as-judge)

How `polish.DefaultPolishSystem` (the copy-edit system prompt shared by `2nb polish` and the `kb_polish` MCP tool) was chosen, and how to re-run the experiment.

## What was measured

Four candidate system prompts were run over a corpus of six deliberately messy notes on the polish model (Claude Haiku 4.5), then a stronger, different model (Claude Sonnet 4.6) judged each output. The harness is `cli/internal/polish/eval_test.go`; the corpus and candidates live there as fixtures so the experiment is reproducible.

**Candidates** (`candidatePrompts`):
- `current_shipped` — the structured-rules prompt now shipped as `DefaultPolishSystem`.
- `alt_terse` — the original one-paragraph prompt.
- `alt_meticulous` — a "preserve byte-for-byte" bulleted variant.
- `alt_minimal_touch` — a "smallest set of edits" variant.

**Corpus** (`evalSamples`): typos/grammar; awkward-clarity prose; a fenced + inline code block with intentional typos that must NOT change; existing `[[wikilinks]]` that must survive; lists + headings that must not be reformatted; and an already-clean note (tests restraint).

**Mechanical gates** (deterministic, disqualifying) from `validate.go`: `CodeSpansEqual`, `ExistingLinksPreserved`, `HeadingStructureEqual`, and "no invented links" (`NewLinks` empty, since this eval runs copy-edit only). Any failure scores 0 and disqualifies the prompt.

**Judge rubric** (1–5 each, weighted 2× correctness / 2× voice+meaning / 2× structure / 1× restraint): `error_correction`, `voice_meaning`, `structure_preservation`, `restraint`. Judge runs at temperature 0 and must return strict JSON.

## Results

| run | top by mean | spread (best→worst prompt) | mechanical failures |
|-----|-------------|----------------------------|---------------------|
| k=2 | the structured-rules variant (`current_shipped`) (4.83) | 4.71–4.83 | 0 across all prompts |
| k=2 (cleaned prompts) | `alt_terse` (4.80) | 4.71–4.80 | 0 |
| k=5 (30 judgments/prompt) | `alt_terse` (4.76), `alt_meticulous` (4.76); `current_shipped` 4.74 | 4.71–4.76 | 0 |

(The first run predated the candidate rename, so the structured-rules prompt appeared under a working label; it is `current_shipped` in `eval_test.go` today.)

## Conclusion and decision

**The four prompts are statistically indistinguishable on this task.** Means cluster in a ~0.05–0.1 band and the top-ranked prompt *changed between runs* (structured → terse), which is the signature of judge/generation noise dominating any real difference. Critically, **every candidate passed every mechanical gate on every generation** — none mangled code, headings, lists, or existing links.

Two takeaways:

1. **At this model tier, system-prompt wording barely moves copy-edit quality.** Haiku 4.5 does careful copy-editing well as long as the constraints are stated at all. The real safety guarantee is not the prompt; it is the deterministic layer: the mechanical gates here, and `StripInventedLinks` at runtime (which removes any link to a note that does not exist, regardless of what the model emits).

2. **When candidates tie within noise, pick on robustness, not the noisy #1.** The shipped prompt (`current_shipped`, the "structured rules" variant) enumerates the hard preservation constraints (links, code, headings, lists) most explicitly. Explicit constraint enumeration is the most defensible choice as the corpus and models drift, and it tied the nominal winner to within 0.02 (far inside run-to-run variance). Chasing a 0.02 margin to a terser prompt would be fitting to noise.

## Re-running

The experiment is double-gated (it makes many paid Bedrock calls), so a normal `go test` skips it:

```bash
RUN_POLISH_EVAL=1 POLISH_EVAL_RUNS=5 \
  CGO_ENABLED=1 go test -tags fts5 ./internal/polish/ -run PromptEval -v -timeout 1800s
```

`POLISH_EVAL_RUNS` averages each (prompt × sample) over k generations to damp noise (default 1; use 5+ for a stable ranking). To evaluate a new candidate, add it to `candidatePrompts` and re-run; to change what ships, set `DefaultPolishSystem` in `prompt.go` (the eval's `current_shipped` candidate references it, so it is always compared against the alternatives).
