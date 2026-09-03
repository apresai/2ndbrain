# Search Tuning Reference

The canonical reference for 2nb's retrieval tuning surface: the similarity threshold, hybrid
(RRF) weighting, the optional rerank stage, embedding concurrency, and the RAG context builder.
The threshold, weighting, and rerank stages apply identically to `2nb search`, `2nb ask`, and the
MCP `kb_search`/`kb_ask` tools because all of them run through the shared `internal/retrieve`
pipeline; the embedding-concurrency and RAG-context sections cover the indexing and
answer-generation sides of the same loop.

## Similarity Threshold

Hybrid search drops vector hits below the active threshold. Resolution chain
(`AIConfig.ResolveSimilarityThresholdFull`):

1. Vault `ai.similarity_threshold` (if > 0)
2. A USER-AUTHORED `recommended_similarity_threshold` in the user catalog
   (`~/.config/2nb/models.yaml` or `.2ndbrain/models.yaml`)
3. The active model's recommendation in `BuiltinCatalog()`
4. `DefaultSimilarityThreshold` (`0.20`)

### What counts as a user calibration (provenance)

Rung 2 is not "any value present in the user file". `models calibrate --save` and
`models add --similarity-threshold` are the only writers, and both stamp the row
`threshold_source: user`. Every other save path (a probe, a benchmark, a
promotion) seeds its row from the MERGED catalog, so before 0.22.1 they copied
the BUILTIN recommendation into the user file, where it read back as a
calibration nobody took and froze out any later change to the builtin value.
`models bench` defaults to `--summary-scope global`, so one bench run in one
vault flipped every vault on the machine.

`ai.IsUserThreshold` is the predicate, and it self-heals contaminated catalogs at
read time with no migration:

| row | verdict |
|---|---|
| stamped `threshold_source: user` | the user's calibration |
| unstamped and EQUAL to the builtin recommendation | a mirror; ignored, so the resolver falls through to the builtin |
| unstamped and DIFFERENT from the builtin | a real calibration written before the stamp existed; kept |

`2nb ai status` names the file and row carrying a calibration when it warns about
a high threshold, and says so plainly when the value is the builtin's own.

Different embedding models have very different baseline distributions. Builtin recommendations:
Nova-2 `0.25` (measured on a real 151-doc vault under the asymmetric query purpose, see below),
Nemotron-VL `0.60`, nomic-embed-text/Titan-v2/Cohere-embed `0.50`, mxbai/snowflake/bge-m3 `0.55`,
all-minilm `0.35`. The rest are estimates from training objectives.

### Nova asymmetric purpose (and why the threshold is low)

Queries embed with Nova's `GENERIC_RETRIEVAL` purpose while documents stay `GENERIC_INDEX`:
`ai.WithPurpose(ai.PurposeQuery)` at the query-embed call sites, while the index path keeps the
default `PurposeIndex`. Measured on a real 151-doc vault, this lifts MRR@10 from 0.951 to 0.962
and Recall@10 from 0.987 to 1.0, and widens match/noise separation from 0.077 to 0.115. But it
collapses the cosine scale: true-match cosine drops to p50≈0.34 and unrelated-pair cosine to
p95≈0.23 (vs ~0.80/~0.72 symmetric). So the Nova-2 threshold is `0.25`, not the old symmetric
`0.65`, which would now reject every real match. The reproducible measurement is
`internal/eval/asymmetry.go` (`2NB_EVAL_VAULT=<vault> go test ./internal/eval/ -run Asymmetric -v`,
credential-gated).

**Migration:** a vault carrying a pre-flip saved calibration (e.g. `0.65` in
`.2ndbrain/models.yaml`) silently degrades to BM25-only. `2nb ai status` warns when an
asymmetric model resolves a threshold above 0.45, and `2nb models calibrate` warns that its
document-to-document sampling overstates the asymmetric search-time threshold (a query-side
calibration is a tracked follow-up).

### Configuring and reading the threshold

Configure via `2nb config set ai.similarity_threshold 0.25`, save calibration via
`2nb models calibrate --save`, or override per-query with `--threshold`. `2nb ai status` prints
the active value and its source. `2nb models list` shows recommendations in a THRESHOLD column.
Search results display `(rrf=X.XXX, cos=Y.YYY)` so semantic relevance is judgable directly.

### Calibration

`2nb models calibrate` samples random doc pairs, computes the cosine distribution
(p50/p90/p95/p99), and recommends `p95 + 0.01` rounded up. Default 500 samples; small vaults
clamp to `n*(n-1)/2`. `--save` upserts a user-catalog entry carrying the threshold and its
`threshold_source: user` stamp, which is what marks it as a measurement rather
than a copy of the builtin recommendation.

## Hybrid Weighting

`ReciprocalRankFusion` (`cli/internal/search/vector.go`) fuses the BM25 and vector rankings as
`score = Σ weight_i/(k + rank_i)`, `k=60`. `ai.bm25_weight` / `ai.vector_weight` bias the fusion
toward keyword or semantic recall; each defaults to `1.0` (classic equal-weight RRF) via
`AIConfig.ResolveHybridWeights`, applied centrally in the shared `internal/retrieve` pipeline
that backs `search`/`ask`/MCP. Raise `ai.vector_weight` to favor the semantic channel (now that
the asymmetric query purpose has sharpened it); raise `ai.bm25_weight` for exact-term-heavy
vaults. `config set` rejects a negative weight; `0` resolves to the `1.0` default.

**Cross-lingual (Nova's 200-language shared space):** `internal/eval/crosslingual_test.go`
(credential-gated) asserts that the same concept across six languages, including Japanese and
Chinese, embeds closer to the English anchor than an unrelated concept does. It is a
*directional* guard: the absolute cosine is vendor-controlled, so the test deliberately avoids a
brittle hard floor. One measured run: cross-lingual cosine roughly 0.84 to 0.87 vs an
unrelated-concept baseline of roughly 0.66. So semantic search and `ask` retrieve across
languages without translation.

## Reranking (optional, default-OFF)

An optional cross-encoder rerank stage reorders the hybrid candidate pool by relevance before
the caller's limit trims it. It lives in the shared `internal/retrieve` pipeline (so
`search`/`ask`/MCP all get it), reads raw chunk TEXT (decoupled from the embedder), and degrades
to the RRF order on any failure.

Providers:

- **Cohere Rerank 3.5 on Amazon Bedrock** (`bedrockagentruntime.Rerank`, model
  `cohere.rerank-v3-5:0`, **us-east-1 in-region only**, colocated with Nova-2), same AWS auth as
  the embedder (`ai.BedrockReranker`).
- A **local** reranker via the bundled llama.cpp engine (`llama-local`: `bge-reranker-v2-m3`
  over `llama-server`'s `/v1/rerank`, `ai.LlamaReranker`). The native engine is the route here
  because ONNX needs CGO in the pure-Go build, and Ollama has no rerank endpoint.

Both rerankers are in `BuiltinCatalog` as `type: "rerank"`, so `2nb models list` and the macOS
AI Hub (a Reranking catalog group plus an Active rerank slot with an on/off toggle) list and
select them. Config: `ai.rerank.enabled` (default **false**), `ai.rerank.model`,
`ai.rerank.candidate_docs` (over-fetch pool, default 50, ≤100 per the Bedrock per-query cap).
`ai status` (and its `--json` `rerank_enabled`/`rerank_model`/`rerank_available`) shows the
slot; `retrieve.New` resolves the reranker from the registry only when enabled, so off means
zero cloud calls.

> [!IMPORTANT]
> **Measured to NOT help; kept off by default; do not enable on a small vault.** On a real
> ~160-doc vault, reranking *hurt* every metric tested (`internal/eval/rerank_test.go`,
> credential-gated): retrieval **R@1 −0.15** (chunk input) / **−0.20** (full-note input), and
> end-to-end LLM-jury **answer quality −0.125 composite** (grounding −0.21). This is the
> textbook "retrieval is already saturated (R@10≈1.0), so a cross-encoder only reshuffles
> already-good results and adds latency/cost" outcome, the same reason measuring killed
> `TEXT_RETRIEVAL`. The stage ships because it is correct (the live Bedrock test ranks clear
> cases right) and may help a much larger or noisier vault where retrieval isn't solved, but it
> is **off by default and not recommended at this scale**. Re-measure before enabling:
> `env 2NB_EVAL_VAULT=<vault> go test ./internal/eval/ -run 'RerankRetrievalAB|RerankAnswerAB|RerankFullNoteRetrievalAB' -v -count=1`
> (retrieval chunk-input, answer-quality jury, and retrieval full-note-input respectively).

## Embedding Concurrency

The bulk embed/re-embed pass (`vault.EmbedDocuments`, `cli/internal/vault/embedpass.go`, wrapped
by the CLI's `embedDocumentsWithProvider` and called directly by the MCP `kb_index` tool) is
**concurrent**: a bounded worker pool embeds docs in parallel (`embed.Document` is
concurrency-safe per doc; the WAL store plus `_txlock=immediate` serialize writes;
`EnsureVecChunks` is mutex-guarded so concurrent workers can't race the lazy `vec_chunks`
create). It replaced a sequential loop with a fixed per-doc throttle sleep: measured ~**5×
faster** on a 30-doc vault (64s→12s at concurrency 4), and the `reembed`/`index` rows in
`.2ndbrain/metrics.db` chart the gain.

The cap is `ai.embed_concurrency` (`config set`, 1 to 64, or **`0` to return to automatic**;
`0` was previously rejected by the setter despite its own error text promising it resolved to
the per-provider default, so the only way back to automatic was hand-editing `config.yaml`). It
defaults per-provider via `ProviderEmbedConcurrencyDefault` (`ai.ResolveEmbedConcurrency`):
**bedrock 4**, openrouter 3, ollama 2 (`cli/internal/ai/ratelimit.go`).

The pass is self-correcting under throttling: `isBedrockRetryable` retries
`ThrottlingException` (a client-fault 429 the old server-only predicate ignored) plus
`ModelTimeoutException`/`ServiceUnavailableException`, with **exponential backoff + equal
jitter** (`bedrockRetryDelay`, up to `maxBedrockAttempts`=5), so an over-set concurrency
degrades to retries rather than failures. Find an account's real ceiling with
`2nb ai embed-probe`, which ramps concurrency over a discarded sample of vault chunks and
recommends the lowest level at ≥90% of peak throughput before throttling.

Both the CLI `index`/`--force-reembed` path and the MCP `kb_index` tool share this one pass (the
worker pool was extracted into `vault.EmbedDocuments`), so an agent-driven reindex gets the same
speedup and honors `ai.embed_concurrency`; the pass also cooperatively honors context
cancellation, so an MCP client that disconnects mid-index gets the partial result (`Cancelled`)
rather than a hang. Nova's `InvokeModel` takes one text per call (no in-request batch), so
concurrency, not batching, is the sync speedup; async S3 batch inference is reserved for ~50k+
docs (backlog).

## RAG Context (parent-document)

`ask` / `kb_ask` build context via `internal/ragctx.Build` (shared, so the CLI and MCP paths
can't diverge): retrieval matches on precise per-chunk vectors, but the **full parent note** is
fed to the generator, the small-to-big / parent-document pattern. Each unique source note is
included whole when it fits the budget; only a note that exceeds it is **windowed** around the
matched heading section (`Result.HeadingPath`, expanding forward-first since answers usually
follow the matched heading), with `...` elision markers. This replaced a from-the-top 2000-rune
truncation that silently dropped answer-bearing sections deep in long notes.

Retrieval over-fetches `DefaultRAGCandidateDocs` (12) candidates; the budget and a
`DefaultRAGMaxNotes` (10) cap decide how many notes actually fit. The matched chunk is surfaced
from **both** channels: BM25 carries `HeadingPath` natively, and `vecChunkSearchByDoc` also
returns the winning `chunk_id`/heading (joined to the `chunks` table) so a vector-only hit
windows precisely. Notes are read via `document.ParseFile` plus `IndexableBody()`, so
`.canvas`/`.base` feed their synthetic markdown view (not raw JSON/YAML) and Obsidian
`%%comments%%` never leak to the model.

Budget defaults (runes ≈ chars; tokens ≈ runes/4, generous within Haiku's 200k window): total
`60000`, per-note `20000`. Configure via `ai.rag_context_budget`/`ai.rag_note_budget`
(`config set` rejects negative or >400000; `0` resolves to the default). All RAG-budget defaults
live in `internal/ai` (single source).
