# Third-Party Notices

## Local model weights (optional, user-downloaded)

The `llama-local` provider runs local GGUF model weights via a bundled llama.cpp
engine. 2ndbrain does **not** bundle or redistribute these weights: they are
downloaded on demand from the ungated Hugging Face repos below (opt-in, never
silent, sha256-verified) into `~/Library/Caches/2nb/models/` when you run
`2nb ai engine pull` or choose `llama-local` in `2nb ai setup`. Your download and
use of each model is governed by that model's license, linked below.

### Gemma models (base model © Google LLC)

- **Gemma 4 E2B (Q4_K_M GGUF)** and **Gemma 4 E4B (Q4_K_M GGUF)** — generation.
  - Distribution: `https://huggingface.co/unsloth/gemma-4-E2B-it-GGUF` and
    `https://huggingface.co/unsloth/gemma-4-E4B-it-GGUF` (GGUF conversion © Unsloth AI).
- **EmbeddingGemma 300M (Q8_0 GGUF)** — embeddings.
  - Distribution: `https://huggingface.co/ggml-org/embeddinggemma-300M-GGUF`.

The Gemma models are governed by the **Gemma Terms of Use**
(https://ai.google.dev/gemma/terms) and the Gemma Prohibited Use Policy
(https://ai.google.dev/gemma/prohibited_use_policy). These GGUF artifacts are
third-party quantizations of the corresponding `gemma-4-*-it` / `embeddinggemma-300m`
base models.

### BGE Reranker v2 m3 (© BAAI)

- **bge-reranker-v2-m3 (Q8_0 GGUF)** — reranking.
  - Distribution: `https://huggingface.co/gpustack/bge-reranker-v2-m3-GGUF`.
  - License: Apache-2.0.

## llama.cpp

The bundled local inference engine is [llama.cpp](https://github.com/ggml-org/llama.cpp)
(MIT License, © 2023 Georgi Gerganov and contributors).
