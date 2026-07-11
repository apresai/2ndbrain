# How the suggest-target re-rank prompt was chosen (planted truth + LLM-as-judge)

The Validation tab's link-fix sheet re-ranks broken-`[[wikilink]]` candidates with the active
generation model (`2nb suggest-target --llm`, tier 4 in `cli/internal/cli/suggest_target.go`). This
page documents how that system prompt (`suggestTargetRerankSystem`) was selected, how the LLM judge
is used, and the measured numbers behind the auto-fix ("Fix all") gate decision.

The reproducible harness is `cli/internal/cli/linkfix_eval_test.go`, double-gated like the polish
prompt eval (real Bedrock credentials AND an explicit opt-in, since it spends real money):

```bash
env RUN_LINKFIX_EVAL=1 2NB_EVAL_VAULT=/path/to/vault \
  go test ./internal/cli/ -run LinkFixEval -v -count=1
```

Knobs: `LINKFIX_EVAL_N` (positives per class, default 6), `LINKFIX_EVAL_SEED` (default 42),
`LINKFIX_EVAL_JUDGE` (default `us.anthropic.claude-sonnet-4-6`).

## Why planted truth, not judge scores, is the primary metric

Unlike copy-edit quality (the polish eval's problem), link matching has free, exact ground truth:
the vault's RESOLVED wikilinks are labels. Take a resolved link (source note, authored target,
intended note), corrupt the target, and you know precisely which note the pipeline should recover.
Correctness is then an exact path comparison — deterministic, reproducible, zero judge noise. The
polish eval's documented lesson (candidates tie within ~0.1 judge noise) says to lean on
deterministic scoring wherever it exists; here it exists for the core question.

## Corpus: corruption classes

Each sampled resolved link's target is corrupted by class, and every corrupted form is verified to
(a) still be broken (resolves to nothing) and (b) sit in the intended tier — drift-class targets
must still map to exactly one note in the deterministic repair index (`polish.SuggestRepairTargets`),
all other classes must map to zero (so they exercise the semantic/BM25/LLM tiers, not the repair
index):

| Class | Corruption | Exercises |
|---|---|---|
| `drift` | separator/case flip (kebab ↔ Title Case) | tier 1 sanity — must stay 100% deterministic |
| `typo` | middle character dropped from the longest word | word-subset confidence + LLM |
| `reorder` | words rotated (first word moved to the end) | LLM (defeats whole-word-subset matching) |
| `worddrop` | last word dropped (≥3-word names) | word-subset + dominance |
| `paraphrase` | LLM-fabricated conceptual rename (different wording, same concept; cached per vault+seed under `os.TempDir()`) | semantic tier + LLM |
| `negative` | fabricated targets matching no note, attached to real source notes | abstention — the correct answer is "decline → recommend unlink" |

Sampling is deterministic (sort + stride with a seed, the `eval.GenerateQASet` pattern), so runs are
reproducible; the paraphrase class is generated once and cached.

Each case runs the PRODUCTION pipeline: `gatherSuggestions` (drift/semantic/BM25 tiers, context-aware
query from the real source note) builds one shared candidate pool per case, then each prompt variant
re-ranks that same pool via `llmRerankPicks` + `applyLLMPicks` — exactly the code path
`suggest-target --llm` executes.

## Metrics

Positives (per class and total):

- **top-1 / top-3 / MRR@3** — rank of the intended note in the final trimmed list (what the GUI shows).
- **retrieval miss** — cases where the truth never entered the 12-candidate pool. The LLM cannot fix
  these (it may only reorder grounded candidates); they measure retrieval, not the prompt.
- **promotion precision** — P(top pick is the intended note | the model promoted anything). This is
  the number that guards "Fix all": an auto-applied wrong pick is the failure mode that matters.
- **model-high precision** (confidence-emitting variants only) — P(correct | the model labeled its
  pick "high"). This is the U1 gate for letting LLM picks auto-fix.

Negatives:

- **abstention rate** — how often the model correctly returned `[]` (→ recommend unlink).
- **false promotions** — a pick on a target that matches nothing (the confabulation rate).

## Where the LLM judge fits (and where it deliberately doesn't)

The judge (default Sonnet 4.6, temperature 0, strict-JSON verdict
`{"verdict":"yes|no|unsure","reason_quality":N}`) is used only where planted truth cannot reach:

1. **Calibration first (U2):** before trusting it anywhere, the judge is scored ON the planted-truth
   cases — it must say "yes" to the intended note and "no" to the strongest wrong candidate. If
   judge-vs-truth agreement were low, its truthless verdicts would be discarded.
2. **Decline audit:** for negatives, the judge sees the pipeline's best deterministic candidate and
   confirms (or refutes) that removing the link is the right call.
3. **Real broken links:** the vault's genuinely broken links (no truth exists) run through every
   variant; the judge grades any promoted pick and its stated reason.

## Prompt variants compared

| Variant | Idea |
|---|---|
| `baseline_pick3` | the pre-eval shipped prompt (pick ≤3, may return `[]`), catalog lines include deterministic conf + raw score |
| `no_scores` | same prompt, catalog stripped to path+title (raw scores are cross-tier-incomparable and can anchor the model) |
| `confidence_verdict` | asks for per-pick `confidence` (high = "safe to rewrite automatically"), explicit `[]`-means-remove framing, "same TOPIC is not enough" rule |
| `fewshot_decline` | baseline + three worked examples including an explicit decline |
| `strict_plausibility` | a decision test ("the target must read as a NAME for the candidate"), explicit note that scores are incomparable — **the winner, now shipped as `suggestTargetRerankSystem`** |

## Results (2026-07-11, vault = 648 resolved links, generator = Haiku 4.5, judge = Sonnet 4.6)

Corpus: 38 cases — 6 each of drift/typo/reorder/worddrop/paraphrase + 8 negatives, seed 42.

Positives (n=30; 7 were retrieval misses where the truth never entered the 12-candidate pool — the
LLM may only reorder grounded candidates, so those are un-winnable for every variant; conditioned on
the truth reaching the pool, top-1 is 21/23 = 0.91):

| Variant | top-1 | top-3 | MRR@3 | promotion precision | negatives abstained | false promotions |
|---|---|---|---|---|---|---|
| `strict_plausibility` (winner) | 0.70 | 0.73 | 0.717 | **0.83** (15/18) | 6/8 | **0** |
| `baseline_pick3` (old shipped) | 0.70 | 0.73 | 0.717 | 0.75 (15/20) | 6/8 | 0 |
| `no_scores` | 0.70 | 0.73 | 0.717 | 0.75 (15/20) | 6/8 | 0 |
| `fewshot_decline` | 0.70 | 0.73 | 0.717 | 0.75 (15/20) | 6/8 | 0 |
| `confidence_verdict` | 0.70 | 0.73 | 0.711 | 0.79 (15/19) | 5/8 | **1** |

Per-class (identical across variants except paraphrase declines): drift 6/6 top-1 with the LLM never
invoked (the deterministic tier short-circuits it — the 100% sanity check held); reorder 6/6 (every
one recovered by the LLM after ranking below wrong candidates deterministically); typo 3/6 and
paraphrase 2/6 (all losses were retrieval misses, not wrong picks).

Ranking metrics tie across variants — consistent with the polish eval's judge-noise lesson that
prompt wording rarely moves the middle of the distribution. The differences are in the tails:
`strict_plausibility` made the same 15 correct promotions as the baseline while declining 2 more of
the hopeless cases (its declines on paraphrase were exactly the retrieval-miss cases where any pick
would have been wrong), which is where its 0.83 promotion precision comes from. `confidence_verdict`
was the only variant to promote a candidate for a fabricated negative.

## Where the judge landed

- **Calibration (U2):** decoy-rejected **12/12**, truth-accepted 9/12. The judge is perfect on the
  "no" side and conservative on the "yes" side (it rejected two Title-Case drift pairs and one
  word-reorder pair that were in fact the same note). That asymmetry is the safe direction for both
  of its jobs here — auditing declines and flagging bad promotions — and means a judge-gated
  auto-fix would under-approve, never over-approve.
- **Decline audit:** **6/6** of the winner's declines on negatives were confirmed ("removing the
  link is the right call").
- **Real broken links:** the vault's one qualifying real broken link (`npm-package-ships-broken`)
  was declined by all five variants — the correct outcome per manual inspection; the GUI should
  surface that as a removal recommendation.

## Decision

1. **Shipped prompt: `strict_plausibility`** (as `suggestTargetRerankSystem` in
   `cli/internal/cli/suggest_target.go`). Identical ranking, strictly better promotion precision,
   zero confabulation on negatives, and its extra declines are exactly the cases the removal
   recommendation wants surfaced. Caveat: the precision gaps (0.83 vs 0.75 on n≈20) are within
   sampling noise, so the pick leans on the tie-breakers the polish eval established — robustness
   and explicitness (the prompt states the scores-incomparability fact and a crisp decision test).
2. **U1 auto-fix gate: NOT cleared.** Unconditional promotion precision is 0.75–0.83 and even the
   confidence-emitting variant's self-labeled "high" picks are only 0.88 precise (14/16) — below
   the ≥0.95 bar for entering "Fix all". **LLM picks remain capped at medium confidence**
   (recommendations, never silent auto-fixes). Re-measure if the default generation model changes.
3. **The decline signal is trustworthy and should be surfaced:** 75% abstention on negatives with
   zero false promotions (winner), 100% judge-confirmed. This is the basis for the `--verdict`
   envelope's unlink recommendation (declined → recommend removal).
4. **The bigger ranking lever is retrieval, not prompting:** 23% of positives never reached the
   candidate pool (typo and paraphrase classes). Improving pool recall (e.g. fuzzy/edit-distance
   candidates, alias-aware BM25) would lift top-1 more than any prompt change — tracked as a
   follow-up, out of scope for the prompt eval.

## Re-running

The harness is deliberately cheap (~200 Haiku generation calls + ~40 Sonnet judge calls at default
sizing). Re-run it whenever `suggestTargetRerankSystem`, the tier logic in `gatherSuggestions`, or
the default generation model changes, and update this page's numbers in the same PR.
