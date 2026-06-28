# ADR 0001 — Vector search: brute-force today, sqlite-vec next, HNSW deferred

- Status: Accepted
- Date: 2026-06-28
- Prompted by: [issue #70](https://github.com/apresai/2ndbrain/issues/70) (@cschanhniem) — "add an approximate nearest neighbor vector index for larger vaults"

> [!IMPORTANT]
> **Superseded by implementation (the body below is the original proposal).** The shipped design differs from this ADR in three ways: (1) sqlite-vec is the **CGO-free `modernc.org/sqlite/vec`** package, not the `github.com/asg017/sqlite-vec-go-bindings/cgo` C amalgamation — the CLI builds `CGO_ENABLED=0` with no C toolchain, so the "new CGO dependency / Homebrew-from-source" consequences below do **not** apply. (2) The vec0 table is **per-chunk `vec_chunks`** (`chunk_id` PK), written via `embed.Document`, with `documents.embedding` holding the **mean** of the chunk vectors — not the per-document `vec_documents` table written in `SetEmbedding`. (3) The `2NB_DISABLE_VEC=1` escape hatch was **not** implemented; the brute-force fallback triggers automatically when `vec_chunks` is absent or does not yet cover the whole corpus.

## Context

Vector search is brute-force cosine over every stored embedding. `search.VectorSearchThreshold`
(`cli/internal/search/vector.go`) scores all embeddings per query; the six CLI commands that do
semantic retrieval (`search`, `ask`, `suggest-links`, `polish`, `suggest-target`, `models calibrate`)
load the whole corpus per process via `store.AllEmbeddings()` (`SELECT id, embedding FROM documents WHERE embedding IS NOT NULL`,
no LIMIT, decode each BLOB) with no cache. The long-lived paths (MCP `kb_search`/`kb_ask`, the macOS
GUI) cache the loaded set per session. Embeddings are stored **per document**, so N is the document
count, and the default dimension is 1024 (Amazon Nova-2).

This was a deliberate MVP choice (`sprints.md` specs "brute-force scan over all embeddings in SQLite")
within explicit, bounded targets — `reqs.md` `PERF-EV-002`: search <300 ms for vaults up to 10,000 docs;
`PERF-UB-002`: <200 MB RAM at 5,000 docs. It had simply never been written down, which is why an
external contributor reasonably read it as a missing piece. This ADR records the decision and the
trigger for revisiting it.

## What the numbers actually say

Measured on an Apple M4, dim=1024 (`search.BenchmarkVectorBruteForce`, `store.BenchmarkAllEmbeddings`):

| N (docs) | cosine scan | `AllEmbeddings` load+decode | total, cold CLI | transient alloc |
|---------:|------------:|----------------------------:|----------------:|----------------:|
|    1,000 |     1.5 ms  |                       2.5 ms |          ~4 ms  |          12 MB  |
|   10,000 |     9.4 ms  |                        25 ms |         ~34 ms  |         125 MB  |
|   50,000 |      49 ms  |                       129 ms |        ~178 ms  |         630 MB  |
|  100,000 |      99 ms  |                       259 ms |        ~358 ms  |        1.26 GB  |

Reading:
- **At the documented 10K target the cold CLI path is ~34 ms — well under 300 ms.** Brute-force is not a
  bug at our stated scale; the issue's "won't scale to thousands of docs" premise is inside the envelope.
- **The cosine arithmetic is cheap** even at 100K (~99 ms). It is not the bottleneck.
- **The load+decode dominates (~73% of query time)** and its allocation grows linearly: 1.26 GB transient
  per query at 100K, far past the 200 MB RAM target. The session caches (MCP/GUI) trade that for a large
  resident copy (~400 MB at 100K). Either way the embedding *load*, not the scan, is the real cost.

## Decision

1. **Adopt sqlite-vec** (`vec0` virtual table, `github.com/asg017/sqlite-vec-go-bindings/cgo`) as the
   primary vector path, with the existing brute-force scan kept as a fallback (no vec table / sqlite-vec
   unavailable / `2NB_DISABLE_VEC=1`). It is the only index option that keeps everything inside the single
   `index.db` file, so the "tar-and-go" portable vault invariant holds.
2. **Be precise about what sqlite-vec is:** *exact* SIMD KNN, **not** ANN/HNSW. It does not make search
   sub-linear; it (a) runs the scan in C against resident BLOBs, eliminating the per-query Go-side
   load+decode and its allocation, and (b) uses SIMD for the distance math. Results stay exact (no recall
   loss). The win is the ~73% of query time that is load+decode today, plus the memory.
3. **Keep `documents.embedding` as the source of truth.** The `vec_documents` table is additive and
   regenerable, written alongside the BLOB in `SetEmbedding`. Do **not** bump `schema_version` for it, so
   older CLIs and the app's GRDB reader keep reading the DB (no false "DB TOO NEW").
4. **Defer true ANN (HNSW/IVF).** sqlite-vec has no HNSW today; bolting on a separate ANN store would
   break portability and add a dependency for a problem we do not have yet.

## Revisit trigger for HNSW / a real ANN index

Reconsider only when **either** holds on a real (not synthetic) vault:
- embedded document count exceeds **~100,000**, **or**
- measured `2nb search` p95 exceeds the **300 ms** budget after sqlite-vec is in place.

At that point evaluate sqlite-vec's own ANN support (if it has shipped) before any external dependency,
to preserve the single-file vault.

## Consequences

- New CGO dependency (sqlite-vec C amalgamation) compiled in the existing `CGO_ENABLED=1 -tags fts5`
  build; must build on the GoReleaser macOS arm64 + x86_64 matrix (Homebrew-from-source).
- `vec0` requires a fixed `float[dim]`, so the table is (re)built for the active embedding dimension and
  dropped+recreated on dimension change / `--force-reembed`.
- The MCP/GUI session embedding cache becomes a fallback-only concern.
- A latency benchmark now exists (`search.BenchmarkVectorBruteForce`, `store.BenchmarkAllEmbeddings`);
  the pre-existing `models bench --probe search` measures BM25 only and is unaffected.
