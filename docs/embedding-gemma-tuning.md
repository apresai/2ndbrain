# EmbeddingGemma Tuning, Matryoshka Representation Learning & Offline Inference Reference

**Author**: ApresAI Engineering  
**Target Architecture**: EmbeddingGemma 2 (GGUF Q8_0, Apache-2.0) under `llama-server` & `2nb` (legacy EmbeddingGemma 300M supported for existing vaults)  
**File**: `docs/embedding-gemma-tuning.md`  
**Status**: Authoritative Reference  

---

## 1. Executive Summary & Architecture Overview

The `2ndbrain` (2nb) platform provides an entirely local, offline embedding and semantic retrieval subsystem powered by Google DeepMind's **EmbeddingGemma** model family. By hosting local models through an out-of-process, supervised `llama-server` background daemon (`cli/internal/llama/manager.go`), 2nb achieves complete data privacy, zero telemetry leakage, and predictable microsecond-level retrieval latencies without external network dependencies or API token expenses.

### 1.1 Core System Capabilities
1. **Local Dense Embeddings**: EmbeddingGemma 2 (`embeddinggemma-2`) maps arbitrary markdown passages and search queries into high-dimensional geometric representations.
2. **Matryoshka Representation Learning (MRL)**: Native support for nested dimension truncation (768d, 512d, 256d, 128d), unlocking up to **66.7% index storage savings** and **3.0x inner-product acceleration** while retaining $>95\%$ semantic recall.
3. **Mandatory Client-Side L2 Re-Normalization**: Enforces unit Euclidean length $\|\hat{v}\|_2 = 1.0$ post-truncation, guaranteeing mathematical equivalence between inner-product hardware instructions and true cosine similarity, entirely preventing rank inversion errors.
4. **Asymmetric Task Instruction Prefix Conditioning**: Dynamically steers the single-tower encoder for document indexing (`title: ... | text: ...`) versus directed search query modes (`search`, `code`, `qa`), providing an empirical **+0.04 MRR (+5.3%) boost** on technical code retrieval.
5. **Deterministic Process Supervision**: Automates memory safety through `Q8_0` GGUF quantization (eliminating float16 activation overflows), mean pooling, physical micro-batch configuration (`--ctx-size 2048 -b 2048 -ub 2048`), and client sub-batch chunking ($\le 8$ items and $\le 4,500$ runes per request, with 1-by-1 fallback).

---

## 2. Model Foundations & Quantization Invariants

### 2.1 Model Lineage & Non-Causal Architecture
EmbeddingGemma 2 (`google/embeddinggemma-2`) is derived from Google DeepMind's **Gemma 4** encoder architecture into a dense, non-causal (bidirectional self-attention) text embedding model, released under the **Apache-2.0** license. Legacy `embeddinggemma-300m` (Gemma 3 / T5Gemma-style encoder, ~300M parameters, Gemma Terms of Use) remains supported for vaults already indexed with it.

- **License**: **Apache-2.0** for `embeddinggemma-2` (legacy `embeddinggemma-300m` remains governed by the Gemma Terms of Use).
- **Native Embedding Dimension**: $D = 768$ float elements (`128`/`256`/`512`/`768` Matryoshka truncation).
- **Context Window**: 2,048 tokens natively for document passages; 512 tokens for search queries.
- **Absence of Key-Value (KV) Cache**: Unlike autoregressive decoder models (e.g., Gemma 4 E2B/E4B) that generate text token-by-token and retain rolling KV states across attention heads, EmbeddingGemma operates as an encoder. Every sequence is processed in a single forward pass through full bidirectional attention matrices. Consequently, zero KV cache memory is allocated or maintained.
- **Logit Elimination Optimization**: In standard generation setups, transformer final layers project hidden states through an unembedding matrix across the vocabulary ($V = 256{,}000$ tokens). Computing logits for every token across a 2,048-token context requires:
  $$2{,}048 \times 256{,}000 \times 4\text{ bytes} \approx 2{,}097{,}152{,}000\text{ bytes} \approx 2.10\text{ GB RAM}$$
  Because dense embedding models require only pooled hidden states and never sample token probabilities, `llama.cpp` suppresses logit calculation when `--embeddings` is active (`cparams.embeddings && !cparams.causal_attn`), completely eliminating this 2.1 GB memory overhead and enabling the engine to run with a memory footprint under 500 MB.

### 2.2 Quantization & The Float16 Activation Hazard
Default EmbeddingGemma weights in 2nb are pinned to the official `ggml-org/embeddinggemma-2-GGUF` repository in `Q8_0` quantization (Apache-2.0):
- **File**: `embeddinggemma-2-Q8_0.gguf`
- **File Size**: 309,855,456 bytes (~310 MB)
- **Pinned SHA-256**: `2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135`
- **Cache Path**: `~/Library/Caches/2nb/models/embeddinggemma-2/embeddinggemma-2-Q8_0.gguf`
- *(Legacy option)* `embeddinggemma-300m` (`embeddinggemma-300m-Q8_0.gguf`, 333,590,944 bytes / ~334 MB, SHA-256 `b5ce9d77a3fc4b3b39ccb5643c36777911cc4eb46a66962eadfa3f5f60490d63`, Gemma Terms of Use) remains available in the manifest and catalog so previously indexed vaults stay portable without forced migration.

#### The Float16 Activation Overflow Mechanism
Unquantized half-precision `float16` (`fp16`) GGUF files **must never be used** with EmbeddingGemma. 

Gemma transformer architectures implement pre-attention embedding scaling by $\sqrt{d_{\text{model}}} = \sqrt{768} \approx 27.7128$. In deep layers with multi-head attention projections and RMSNorm variance calculations across large sequence lengths, intermediate hidden activations regularly exceed the maximum finite value representable in standard IEEE 754 half-precision float16:
$$\text{MAX}_{\text{fp16}} = (2 - 2^{-10}) \times 2^{15} = 65{,}504$$

When an intermediate activation exceeds $65{,}504$, IEEE 754 arithmetic overflows directly to $+\infty$. Subsequent layer operations (subtractions, divisions, or normalizations involving infinity) produce `NaN` (Not a Number) or cause catastrophic numerical collapse where the resulting embedding vector contains all zeros or randomized values. On Apple Silicon Metal GPU kernels, this failure occurs silently without hardware traps.

#### Why Q8_0 Guarantees Numerical Stability
In `Q8_0` quantization, weight matrices are partitioned into discrete blocks of 32 elements (`QK8_0 = 32`). Each block contains 32 signed 8-bit integers (`int8_t`) and an associated 32-bit floating-point scale factor $d$. Matrix-vector inner products dequantize weights into `float32` accumulation registers. Because `float32` provides an exponent range up to $\approx 3.4 \times 10^{38}$, intermediate activation spikes do not overflow. Furthermore, `Q8_0` retains $>0.999$ cosine similarity fidelity relative to full 32-bit unquantized baselines while reducing file size from 600+ MB down to ~310 MB (`309,855,456` bytes).

### 2.3 Text-Only Encoder Scope on Apple Silicon
2nb standardizes on the pure-text `embeddinggemma-2-Q8_0.gguf` artifact (SHA-256: `2188ac1deca4...`, ~310 MB) with native Metal GPU offload (`-ngl 999`), omitting the optional multimodal vision/audio projector (`mmproj-embeddinggemma-2-Q8_0.gguf`, 555 MB) since Obsidian vaults consist of markdown prose, code, and structured notes. Execution enforces mandatory `Q8_0` weights, Metal GPU acceleration, out-of-process supervision, mean pooling, and L2 unit-norm re-normalization, and `2nb ai engine pull embeddinggemma-2` can seed directly from a SHA-256-verified local Hugging Face hub cache.

---

## 3. Matryoshka Representation Learning (MRL) Deep Dive

### 3.1 Mathematical Principles of MRL
Standard dense embedding encoders train representations $F(x; \theta) \in \mathbb{R}^D$ to optimize contrastive retrieval at a single fixed dimensionality $D$. Truncating a standard model's vectors to a lower dimension destroys retrieval performance because semantic variance is diffused arbitrarily across all coordinates.

Matryoshka Representation Learning (Kusupati et al., NeurIPS 2022) addresses this by training an encoder against an explicit multi-scale objective across a designated set of nested sub-vector dimensions $\mathcal{M} = \{m_1, m_2, \dots, m_K\}$:
$$\min_{\theta, \{W^{(m)}\}_{m \in \mathcal{M}}} \frac{1}{N} \sum_{i=1}^N \sum_{m \in \mathcal{M}} c_m \cdot \mathcal{L}\left( W^{(m)} \cdot F(x_i; \theta)_{1:m} \,;\; y_i \right)$$

For EmbeddingGemma, the nested dimensionality set is:
$$\mathcal{M} = \{128, 256, 512, 768\}$$

During contrastive pre-training, the early coordinates ($1 \dots 128$) are forced independently to solve the retrieval loss. Consequently:
1. **Leading Subspaces ($1 \dots 128, 1 \dots 256$)**: Capture primary semantic coordinates—topic category, entity types, core syntax, and coarse intent.
2. **Trailing Coordinates ($257 \dots 768$)**: Capture fine-grained nuance, subtle stylistic attributes, and domain-specific terminology.

Because early coordinates preserve subspace integrity, slicing a vector to any $m \in \mathcal{M}$ produces a valid low-dimensional representation that retains the dominant geometric clustering of the full 768-dimensional space.

### 3.2 Supported Dimensionality Tiers in 2nb
The 2nb model catalog (`cli/internal/ai/catalog.go`) registers `embeddinggemma-2` (and legacy `embeddinggemma-300m`) with `SupportedDimensions: []int{128, 256, 512, 768}`.

| Dimension ($d$) | Role in 2nb | Float32 Bytes/Vec | Float16 Bytes/Vec | Storage Reduction | Recall Retention (NDCG@10) |
|:---|:---|:---|:---|:---|:---|
| **768** | Full Precision (Default) | 3,072 bytes (3.00 KB) | 1,536 bytes (1.50 KB) | **0.0% (Baseline)** | **100.0%** |
| **512** | Balanced Performance | 2,048 bytes (2.00 KB) | 1,024 bytes (1.00 KB) | **33.3% smaller** | **99.1%** |
| **256** | High Efficiency (Recommended) | 1,024 bytes (1.00 KB) | 512 bytes (0.50 KB) | **66.7% smaller (3.0x)** | **96.4%** |
| **128** | Minimal Footprint | 512 bytes (0.50 KB) | 256 bytes (0.25 KB) | **83.3% smaller (6.0x)** | **91.2%** |

### 3.3 Quantitative Storage Scaling Across Vault Sizes
In 2nb, vectors are persisted in SQLite `vec0` virtual tables (`.2ndbrain/index.db`). The table below outlines the raw vector storage footprint across corpus sizes:

| Vault Corpus Size ($N$) | 768d (fp32 / fp16) | 512d (fp32 / fp16) | 256d (fp32 / fp16) | 128d (fp32 / fp16) |
|:---|:---|:---|:---|:---|
| **1,000 chunks** | 3.07 MB / 1.54 MB | 2.05 MB / 1.02 MB | 1.02 MB / 0.51 MB | 0.51 MB / 0.26 MB |
| **10,000 chunks** | 30.72 MB / 15.36 MB | 20.48 MB / 10.24 MB | 10.24 MB / 5.12 MB | 5.12 MB / 2.56 MB |
| **50,000 chunks** | 153.60 MB / 76.80 MB | 102.40 MB / 51.20 MB | 51.20 MB / 25.60 MB | 25.60 MB / 12.80 MB |
| **100,000 chunks** | 307.20 MB / 153.60 MB | 204.80 MB / 102.40 MB | 102.40 MB / 51.20 MB | 51.20 MB / 25.60 MB |
| **1,000,000 chunks** | 3.07 GB / 1.54 GB | 2.05 GB / 1.02 GB | 1.02 GB / 512.00 MB | 512.00 MB / 256.00 MB |

### 3.4 Hardware Cache Line & Memory Bandwidth Benefits
Vector similarity search during interactive user queries (`2nb search <query>`) evaluates an exhaustive matrix-vector multiplication between the query vector $q \in \mathbb{R}^d$ and all stored chunk vectors $D \in \mathbb{R}^{N \times d}$ using SIMD vectorized inner-product kernels (`vDSP_mmul` on Apple Silicon).

1. **Cache Line Alignment**:
   - Modern Apple Silicon M-series chips utilize 128-byte cache lines (or 64-byte L1 lines).
   - In float16 storage, a 256-dimensional vector occupies exactly $256 \times 2 = 512\text{ bytes}$, fitting perfectly across **4 cache lines** (128 bytes each).
2. **System Level Cache (SLC) Residency**:
   - Apple Silicon chips feature a System Level Cache (SLC) ranging from 48 MB (M-series base/Pro) to 96+ MB (Max/Ultra).
   - For a large vault of **100,000 note chunks**, the total vector matrix at 768d float32 occupies **307.2 MB**, far exceeding SLC capacity and forcing continuous DRAM bus fetches ($>150\text{ GB/s}$ bandwidth contention).
   - At **256d float16**, the entire 100,000-vector index occupies only **51.2 MB**, fitting entirely inside the SLC cache. Memory bus transactions are reduced by $>80\%$, eliminating DRAM latency bottlenecks and enabling sub-millisecond similarity scans.
3. **Fused Multiply-Add (FMA) Throughput**:
   - Evaluating $N$ candidate vectors requires $N \times d$ FMA operations.
   - Reducing $d$ from 768 to 256 reduces required floating-point arithmetic by $\frac{768}{256} = 3.0\times$, providing a direct $3\times$ search throughput speedup on CPU and Metal.

---

## 4. Mandatory Client-Side L2 Re-Normalization: Proof & Analysis

When a vector is truncated to a lower Matryoshka dimension, client-side L2 re-normalization is not optional—it is a strict mathematical requirement for search validity.

### 4.1 Theorem 1: Sliced Sub-Vectors Strictly Lose Unit Norm
Let $v = (v_1, v_2, \dots, v_D)^T \in \mathbb{R}^D$ be a unit-norm embedding emitted by `llama-server` ($D = 768$):
$$\|v\|_2 = \sqrt{\sum_{i=1}^D v_i^2} = 1.0 \implies \sum_{i=1}^D v_i^2 = 1.0$$

Define the truncation projection operator $\pi_d: \mathbb{R}^D \to \mathbb{R}^d$ for $1 \le d < D$:
$$u = \pi_d(v) = (v_1, v_2, \dots, v_d)^T \in \mathbb{R}^d$$

The squared Euclidean norm of $u$ is:
$$\|u\|_2^2 = \sum_{i=1}^d v_i^2$$

Decomposing the full sum:
$$\sum_{i=1}^D v_i^2 = \sum_{i=1}^d v_i^2 + \sum_{i=d+1}^D v_i^2 = 1.0 \implies \|u\|_2^2 = 1.0 - \sum_{i=d+1}^D v_i^2$$

In any non-degenerate neural embedding model, the trailing coordinates $v_{d+1 \dots D}$ carry non-zero variance across natural language inputs:
$$\sum_{i=d+1}^D v_i^2 > 0$$

Therefore:
$$\|u\|_2^2 < 1.0 \implies \|u\|_2 = \sqrt{1.0 - \sum_{i=d+1}^D v_i^2} < 1.0 \quad \blacksquare$$

#### Empirical Norm Degradation Measurements
Probing `llama-server` on the EmbeddingGemma architecture (baseline measured on `embeddinggemma-300m-Q8_0.gguf`) over real note text demonstrates this degradation empirically:
- **768d (Unmodified)**: $\|v_{1:768}\|_2 = 1.00000$ (Retained Energy: 100.0%)
- **512d (Truncated)**: $\|v_{1:512}\|_2 = 0.84318$ (Retained Energy: 71.1%, Tail Energy Lost: 28.9%)
- **256d (Truncated)**: $\|v_{1:256}\|_2 = 0.62381$ (Retained Energy: 38.9%, Tail Energy Lost: 61.1%)
- **128d (Truncated)**: $\|v_{1:128}\|_2 = 0.47378$ (Retained Energy: 22.5%, Tail Energy Lost: 77.5%)

### 4.2 Theorem 2: Restoration of Unit Norm via Scalar Re-Normalization
For any truncated sub-vector $u \in \mathbb{R}^d$ with $\|u\|_2 > 0$, define the normalized vector $\hat{u}$:
$$\hat{u} = \frac{u}{\|u\|_2} = \frac{1}{\sqrt{\sum_{i=1}^d u_i^2}} \begin{pmatrix} u_1 \\ u_2 \\ \vdots \\ u_d \end{pmatrix}$$

Evaluating the squared norm of $\hat{u}$:
$$\|\hat{u}\|_2^2 = \sum_{i=1}^d \left( \frac{u_i}{\|u\|_2} \right)^2 = \frac{1}{\|u\|_2^2} \sum_{i=1}^d u_i^2 = \frac{\|u\|_2^2}{\|u\|_2^2} = 1.0 \implies \|\hat{u}\|_2 = 1.0 \quad \blacksquare$$

Client-side re-normalization strictly maps the truncated vector back onto the unit hypersphere $\mathbb{S}^{d-1}$.

### 4.3 Theorem 3: Strict Necessity of Unit Norm for Dot-Product Equivalence
In vector database engines (such as SQLite `vec0`, Apple Accelerate `vDSP_mmul`, and BLAS), similarity search is executed using the inner product $\langle x, y \rangle$ rather than full cosine similarity because inner products require only FMAs without dynamic square roots or divisions.

Cosine similarity is defined as:
$$S_{\cos}(x, y) = \frac{\langle x, y \rangle}{\|x\|_2 \|y\|_2}$$

Inner-product similarity is defined as:
$$S_{\text{IP}}(x, y) = \langle x, y \rangle$$

Equivalence holds if and only if:
$$S_{\text{IP}}(x, y) \equiv S_{\cos}(x, y) \iff \langle x, y \rangle = \frac{\langle x, y \rangle}{\|x\|_2 \|y\|_2} \iff \|x\|_2 \|y\|_2 = 1.0$$

When all stored vectors and queries satisfy $\|x\|_2 = 1.0$ and $\|y\|_2 = 1.0$:
$$S_{\text{IP}}(x, y) = \frac{\langle x, y \rangle}{1.0 \times 1.0} \equiv S_{\cos}(x, y) \quad \blacksquare$$

### 4.4 The Rank Inversion Counterexample (Unnormalized Search Failure)
If vectors are truncated to dimension $d$ without re-normalization, the inner product computed between query $q' = \pi_d(q)$ and document $d' = \pi_d(d)$ is:
$$\langle q', d' \rangle = \|q'\|_2 \cdot \|d'\|_2 \cdot S_{\cos}(q', d')$$

Because different documents distribute their semantic energy across dimensions differently, **unnormalized sub-vectors carry varying Euclidean norms**.

#### Concrete Rank Inversion Scenario:
Consider a query $q'$ truncated to 256d with $\|q'\|_2 = 0.62$.
- **Document A**: General overview note with 85% of its energy in the first 256 dimensions:
  $$\|A'\|_2 = 0.85, \quad S_{\cos}(q', A') = 0.65$$
- **Document B**: Specific technical note with only 55% of its energy in the first 256 dimensions (the remainder is in tail nuance dimensions $257 \dots 768$), but having **much stronger semantic alignment**:
  $$\|B'\|_2 = 0.55, \quad S_{\cos}(q', B') = 0.80$$

**True Ground Truth**: Document B ($S_{\cos} = 0.80$) is far more relevant than Document A ($S_{\cos} = 0.65$). The correct ranking is **#1: Document B, #2: Document A**.

Now calculate the unnormalized inner products scored by the search engine:
$$\langle q', A' \rangle = 0.62 \times 0.85 \times 0.65 = 0.34255$$
$$\langle q', B' \rangle = 0.62 \times 0.55 \times 0.80 = 0.27280$$

Comparing the computed scores:
$$0.34255 > 0.27280 \implies \text{Document } A \text{ is erroneously ranked #1!}$$

**Result**: Unnormalized dot-product retrieval inverts the search results. Document A is ranked first solely because its sub-vector had a larger Euclidean norm, completely overriding true semantic cosine similarity.

#### Restoration via Client-Side L2 Re-Normalization:
Applying client-side re-normalization:
$$\hat{A} = \frac{A'}{\|A'\|_2}, \quad \hat{B} = \frac{B'}{\|B'\|_2}, \quad \hat{q} = \frac{q'}{\|q'\|_2}$$
$$\langle \hat{q}, \hat{B} \rangle = 1.0 \times 1.0 \times 0.80 = 0.80$$
$$\langle \hat{q}, \hat{A} \rangle = 1.0 \times 1.0 \times 0.65 = 0.65$$
$$0.80 > 0.65 \implies \text{Document } B \text{ is correctly ranked #1.}$$

### 4.5 Implementation in 2nb Go CLI
This mathematical invariant is implemented in `cli/internal/ai/llamacpp.go:453-471`:
```go
func truncateNormalize(v []float32, dim int) []float32 {
    if dim <= 0 || dim >= len(v) {
        return v
    }
    out := make([]float32, dim)
    copy(out, v[:dim])
    var norm float64
    for _, x := range out {
        norm += float64(x) * float64(x)
    }
    if norm == 0 {
        return out
    }
    inv := float32(1.0 / math.Sqrt(norm))
    for i := range out {
        out[i] *= inv
    }
    return out
}
```
If `dim >= len(v)` (e.g., full 768d), the original slice is returned directly without extra allocations because `llama-server` with `--pooling mean` already guarantees unit norm ($\|v\|_2 = 1.000000019$).

---

## 5. Asymmetric Task Instruction Prefix Prompts

### 5.1 Directional Conditioning Mandate
EmbeddingGemma employs a single shared transformer encoder rather than two distinct networks for queries and documents.

In information retrieval, queries and documents exhibit severe structural asymmetry:
- **Queries**: Short (3–15 tokens), interrogative, conversational, low lexical density.
- **Documents**: Long (50–500 tokens), declarative, structured markdown, code blocks, high lexical density.

If passed to a bidirectional transformer without task conditioning, self-attention maps queries and documents symmetrically, optimizing for topical similarity (matching questions to questions) rather than relevance (matching questions to answers). Prepending directional task instruction prefixes conditions early self-attention heads to steer the sequence representation into the target semantic manifold.

### 5.2 Document Chunk Formatting
All document chunks during vault indexing (`2nb index`) are formatted as:
```
title: {title} | text: {content}
```
If a note lacks a frontmatter title or filename title, it safely falls back to:
```
title: none | text: {content}
```
This guarantees that attention heads always encounter the expected delimiter structure (`title: ... | text: ...`) established during pre-training.

### 5.3 Query Instruction Presets
For queries, 2nb provides four configurable instruction presets:

| Preset | Query Format String | Intended Workload |
|:---|:---|:---|
| `search` (Default) | `task: search result \| query: {query}` | General vault search over notes, journals, literature |
| `code` | `task: code retrieval \| query: {query}` | Technical developer vaults, scripts, algorithms, code blocks |
| `qa` | `task: question answering \| query: {query}` | Direct question-answering, fact retrieval, FAQ notes |
| `none` | `{query}` | Raw unprompted query (symmetric bi-encoder emulation) |

### 5.4 Empirical Evaluation & Benchmark Results
In a 27-query developer code-retrieval benchmark against source files:
- **Generic Search Prefix (`task: search result | query: ...`)**: 22 of 27 queries placed the target in the top 5; **MRR = 0.75**.
- **Specialized Code Prefix (`task: code retrieval | query: ...`)**: 24 of 27 queries placed the target in the top 5; **MRR = 0.79**.
- **Impact**: Prepending the domain-specific `code retrieval` instruction delivered an immediate **+0.04 MRR (+5.3% relative improvement)** without retraining or modifying model weights.

### 5.5 Collision-Free Idempotency Guard in Formatting
To prevent accidental prefix duplication if callers pass pre-formatted strings, while strictly avoiding false-positive bypasses on user markdown notes starting with `task:` or `title:`, `formatLlamaEmbedText` (`cli/internal/ai/llamacpp.go`) enforces robust, purpose-aware idempotency:
- For queries (`PurposeQuery` / `PurposeQueryText`), formatting is bypassed only if the trimmed query is already a pre-formatted instruction envelope—satisfying both case-insensitive `strings.HasPrefix(strings.ToLower(trimmed), "task:")` AND `strings.Contains(text, " | query:")`. Queries starting with `task:` (such as `task: fix memory leak`) without the ` | query:` delimiter are correctly conditioned with the active task prefix.
- For documents (`PurposeIndex` or default), formatting is bypassed only if the text is already a pre-formatted chunk envelope—satisfying both case-insensitive `strings.HasPrefix(strings.ToLower(trimmed), "title:")` AND `strings.Contains(text, " | text:")`.
- Markdown notes beginning with `task:` (e.g. todo items) or `title:` (e.g. note headers) are correctly wrapped in the `title: ... | text: ...` document envelope without false-positive collision.

```go
func formatLlamaEmbedText(text string, cfg EmbedConfig, taskPreset string) string {
    trimmed := strings.TrimSpace(text)
    switch cfg.Purpose {
    case PurposeQuery, PurposeQueryText:
        if strings.HasPrefix(strings.ToLower(trimmed), "task:") && strings.Contains(text, " | query:") {
            return text
        }
        preset := strings.ToLower(strings.TrimSpace(taskPreset))
        switch preset {
        case "code":
            return "task: code retrieval | query: " + text
        case "qa":
            return "task: question answering | query: " + text
        case "none", "symmetric":
            return text
        case "search", "":
            fallthrough
        default:
            return "task: search result | query: " + text
        }
    case PurposeIndex:
        fallthrough
    default:
        if strings.HasPrefix(strings.ToLower(trimmed), "title:") && strings.Contains(text, " | text:") {
            return text
        }
        title := cfg.Title
        if title == "" {
            title = "none"
        }
        return "title: " + title + " | text: " + text
    }
}
```

---

## 6. Pooling Methods & Server Execution Constraints

### 6.1 Mean Pooling vs CLS vs Last Token
`llama-server` supports three pooling modes for embeddings: `--pooling mean`, `--pooling cls`, and `--pooling last`. 

**Mean pooling is an architectural invariant for EmbeddingGemma.** Contrastive pre-training loss was computed across the sequence mean vector:
$$\vec{v}_{\text{mean}} = \frac{1}{L} \sum_{i=1}^L \vec{h}_i$$

Unlike BERT-derived models, Gemma does not feature a dedicated `[CLS]` classification head or Next Sentence Prediction pre-training objective. In bidirectional attention, the first token hidden state reflects syntax delimiters rather than document semantics.

#### Empirical Live Probe Comparison
A controlled empirical probe on the EmbeddingGemma architecture (measured on the `embeddinggemma-300m-Q8_0.gguf` baseline under `llama-server`):
- **Query**: `"retry a failed call with exponential backoff"`
- **Target Document A (Relevant Code)**: Python function implementing exponential backoff retry.
- **Target Document B (Irrelevant Distractor)**: Chocolate chip cookie recipe.

| Pooling Mode | Query Prefix Variant | Code Note Cosine Sim | Cookie Recipe Cosine Sim | Separation Margin ($\Delta$) | Retrieval Verdict |
|:---|:---|:---:|:---:|:---:|:---|
| **MEAN** | `task: code retrieval \| query: ...` | **0.5568** | **0.0528** | **+0.5040** | **Optimal Separation** |
| **MEAN** | `task: search result \| query: ...` | 0.4930 | 0.0426 | **+0.4504** | Robust Separation |
| **MEAN** | Raw Query (No task prefix) | 0.5266 | 0.1146 | **+0.4120** | 2.17x higher noise floor |
| **CLS** | `task: code retrieval \| query: ...` | **0.0895** | -0.0188 | **+0.1083** | **Collapsed (78.5% loss)** |
| **CLS** | `task: search result \| query: ...` | 0.0474 | -0.0585 | **+0.1060** | **Collapsed** |
| **LAST** | `task: code retrieval \| query: ...` | 0.7851 | **0.5503** | **+0.2348** | Distractor noise explodes |
| **LAST** | `task: search result \| query: ...` | 0.8110 | **0.5541** | **+0.2570** | Distractor noise explodes |

**Key Findings**:
1. **CLS Pooling Fails Completely**: On structured notes (`"title: ... | text: ..."`), the CLS token isolates on the delimiter. Relevant code similarity collapses from 0.5568 to **0.0895**, destroying search separation.
2. **Last Token Pooling Inflates Noise**: The distractor cookie recipe similarity explodes from 0.0528 to **0.5503**, severely compressing margins and preventing threshold-based noise filtering.
3. **Mean Pooling Delivers Clean Margins**: Distractor scores near zero (0.04–0.05) while relevant documents score 0.50–0.56, providing an exceptional **0.5040** separation margin.

### 6.2 Sub-Batching and Physical Micro-Batch Sizing
To ensure stable out-of-process execution under `llama-server`, two distinct batch dimensions must be coordinated:

#### 1. Physical Server Arguments (`--ctx-size 2048 -b 2048 -ub 2048`)
- In `llama-server`, `-b` sets the *logical* maximum batch size, while `-ub` (`--ubatch-size`) sets the *physical* execution micro-batch size.
- In upstream `llama-server` builds, `-ub` defaults strictly to **512**.
- **Failure Mode**: If a user indexes a markdown note of 1,200 tokens with `-b 2048` but default `-ub 512`, the server returns an immediate HTTP 500 error:
  ```json
  {"error":{"code":500,"message":"input (1200 tokens) is too large to process. increase the physical batch size (current batch size: 512)","type":"server_error"}}
  ```
- **Fix**: Setting `-ub 2048` alongside `-b 2048` in `cli/internal/llama/manager.go` guarantees that single prompts up to the full 2,048-token context window execute cleanly.

#### 2. Client-Side Chunk Sub-Batching ($\le 8$ items, $\le 4,500$ runes)
When re-indexing a vault, the client may produce hundreds of chunk strings. Sending too many chunks or dense characters in a single HTTP POST request creates reliability issues:
- Large JSON payloads cause heap allocation spikes and can overflow the 2,048-token `--ctx-size` / `-b` / `-ub` budget on dense notes (HTTP 500).
- Processing hundreds of chunks in one turn takes several seconds, causing HTTP socket timeouts.
- Long-running bulk requests block `llama-server`, preventing interactive user search queries from executing (head-of-line blocking).

2nb implements rune-bounded client sub-batching in `cli/internal/ai/llamacpp.go`:
```go
const (
    llamaEmbedMaxBatchSize  = 8
    maxLlamaEmbedInputRunes = 4500
    maxLlamaEmbedBatchRunes = 4500
)
```
Individual inputs are capped at `4,500` runes, chunk arrays are partitioned into contiguous batches of $\le 8$ items and $\le 4,500$ total runes, and any multi-item batch error falls back to 1-by-1 embedding so one dense chunk cannot fail its neighbors.

A rune cap is not a token cap. Dense text (tables, code, URLs, hex, non-Latin scripts) runs under two runes per token, so a 4,500-rune input can tokenize past the 2,048-token batch and the server answers HTTP 500 `input (N tokens) is too large to process`. A single input that draws that error is truncated to 3/4 of its runes and retried, up to `llamaEmbedShrinkAttempts` (5) times, with one INFO line per truncation (`embedOne`). Before this the chunk failed on every attempt, so its note never got an embedding and stayed out of semantic search. Do not raise `-b`/`-ub` to fix it: 2,048 is the model's context.

---

## 7. User-Facing Controls vs. Automated System Settings Matrix

To balance user flexibility against operational reliability, 2nb establishes a strict division between options exposed to users and parameters managed automatically by the system.

| Parameter | Expose to User? | Setting / Flag | Options / Range | Technical Rationale |
|:---|:---:|:---|:---|:---|
| **Matryoshka Dimensions** | **YES** | `ai.dimensions`<br>(CLI & GUI) | `128` (CLI), `256`, `512`, `768`<br>(Default: `768`) | Direct user trade-off between index disk space, memory footprint, and recall accuracy. |
| **Task Prefix Preset** | **YES** | `ai.task_preset`<br>(CLI & GUI) | `search`, `code`, `qa`, `none`<br>(Default: `search`) | Allows tailoring retrieval to vault domain (e.g., +0.04 MRR boost on technical notes with `code`). |
| **Similarity Threshold** | **YES** | `ai.similarity_threshold`<br>(CLI & Settings) | Float `0.00` – `1.00`<br>(Default: `0.60` for `embeddinggemma-2`; legacy `embeddinggemma-300m`: `0.35`) | Configures cut-off for noise filtering in hybrid BM25/vector search. |
| **Pooling Method** | **NO** (Automated) | Server flag | `--pooling mean` | Hard architectural requirement of model; non-mean pooling breaks retrieval. |
| **Physical Context & Micro-Batch** | **NO** (Automated) | Server flags | `--ctx-size 2048`<br>`-b 2048 -ub 2048` | System reliability invariant; required to prevent HTTP 500 errors on documents >512 tokens. |
| **Client Sub-Batch Chunk Slices** | **NO** (Automated) | Internal constants | $\le 8$ chunks, $\le 4,500$ runes + 1-by-1 fallback | Internal transport management; prevents 2,048-token batch overflows and socket timeouts. |
| **Client-Side L2 Re-Normalization** | **NO** (Automated) | Internal algorithm | Mandatory post-slice | Mathematical invariant; required for dot-product equivalence to cosine similarity. |
| **Quantization Scheme** | **NO** (Automated) | Pinned GGUF | `Q8_0` | Required to avoid IEEE 754 float16 activation overflows ($>65,504$). |

---

## 8. CLI Configuration, Verification & Workflows

### 8.1 Configuring Local Gemma in the CLI
To activate local offline execution with EmbeddingGemma 2:

```bash
# 1. Select the local provider
2nb config set ai.provider llama-local

# 2. Select EmbeddingGemma 2 (or omit: embeddinggemma-2 is the llama-local default)
2nb config set ai.embedding_model embeddinggemma-2

# 3. Configure Matryoshka dimensions (e.g. 256 for 66.7% index reduction)
2nb config set ai.dimensions 256

# 4. Optional: Select specialized task instruction preset
2nb config set ai.task_preset code
```

### 8.2 Validating Configuration & Health
2nb includes built-in diagnostics to verify that local model configurations are healthy:

```bash
# Verify AI configuration and dimension compatibility
2nb config doctor

# Run a smoke probe against the local model
2nb models test embeddinggemma-2 --json

# Run end-to-end system verification
2nb doctor
```

### 8.3 Vault Re-Indexing Workflow
Because changing `ai.dimensions` updates the vector column width in SQLite (`vec_chunks`), and content hashes of existing markdown notes remain unchanged, standard `2nb index` will skip already-indexed notes. 

Whenever modifying `ai.dimensions`, users **must execute a forced re-embedding**:
```bash
2nb index --force-reembed
```
This drops existing vector tables, re-slices embeddings to the new dimension, re-normalizes vectors to unit length, and rebuilds the sqlite-vec index.

---

## 9. Conclusion & Reference Links

By strictly enforcing `Q8_0` quantization, `--pooling mean`, physical micro-batch buffer sizing (`-ub 2048`), rune-bounded client sub-batching ($\le 8$ items, $\le 4,500$ runes), and post-truncation L2 re-normalization, 2nb provides a rock-solid, production-grade local embedding runtime. Users gain the flexibility to tailor index storage and domain retrieval via Matryoshka dimensions and task presets, while all low-level numerical stability and process supervision invariants remain completely automated.

- **Model Lineage**: Google DeepMind Gemma 4 Encoder (`google/embeddinggemma-2`, Apache-2.0; legacy `embeddinggemma-300m` on Gemma 3 / T5Gemma Encoder)
- **Matryoshka Foundation**: Kusupati et al., *Matryoshka Representation Learning*, NeurIPS 2022
- **Supervisor Source**: `cli/internal/llama/manager.go`
- **Embedder Implementation**: `cli/internal/ai/llamacpp.go`
- **CLI Reference**: `docs/cli-reference.md`
- **Local Engine Reference**: `docs/llama-local-engine.md`
