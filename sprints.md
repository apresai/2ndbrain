# 2ndbrain AI Provider System — Sprint Plan

## Overview

Build the AI provider system that enables local and cloud semantic search + generation. MVP uses AWS Bedrock as the first cloud provider (existing SSO credentials, zero new keys).

**Architecture**: Factory pattern with `EmbeddingProvider` and `GenerationProvider` interfaces. Three providers: Ollama (local), AWS Bedrock, OpenRouter.

**Default cloud models**: Nova Embeddings v2 (embeddings), Claude Haiku 4.5 (generation)
**Default local models** (deferred): EmbeddingGemma 308M (embeddings), Gemma 3 4B (generation)

---

## Progress Tracker

| Sprint | Name | Status | Completed |
|--------|------|--------|-----------|
| 1 | Foundation — Interfaces, Config, Keystore | Not Started | |
| 2 | AWS Bedrock Provider | Not Started | |
| 3 | Vector Storage + Hybrid Search | Not Started | |
| 4 | CLI Commands — models, config, ai status | Not Started | |
| 5 | MCP Integration + kb_ask | Not Started | |
| 6 | OpenRouter Provider | Not Started | |
| 7 | Ollama Provider — Local AI | Not Started | |
| 8 | GUI Integration (stretch) | Not Started | |

**MVP = Sprints 1-4**: AWS Bedrock cloud AI + vector search + CLI commands
**Critical path**: 1 → 2 → 3 → 5

---

## Sprint 1: Foundation — Interfaces, Config, Keystore
**Goal**: Establish the provider system skeleton so all subsequent sprints plug into it.
**Estimated effort**: 1 session
**Status**: Not Started

### Tasks

- [ ] **Create `cli/internal/ai/provider.go`** — Core interfaces
  - `EmbeddingProvider` interface: `Name()`, `Embed(ctx, texts)`, `Dimensions()`, `Available(ctx)`
  - `GenerationProvider` interface: `Name()`, `Generate(ctx, prompt, opts)`, `Available(ctx)`
  - `ModelInfo` struct: ID, Name, Provider, Type, Dimensions, ContextLen, PriceIn, PriceOut, Local
  - `GenOpts` struct: Temperature, MaxTokens, SystemPrompt

- [ ] **Create `cli/internal/ai/registry.go`** — Provider registry
  - `Registry` struct holding registered providers
  - `Register(provider)`, `EmbeddingProvider(name)`, `GenerationProvider(name)`
  - `ListModels(ctx)` aggregates ModelInfo from all providers
  - Global default registry instance

- [ ] **Create `cli/internal/ai/config.go`** — AI config section
  - `AIConfig` struct: Provider, EmbeddingModel, GenerationModel, Dimensions
  - `OllamaConfig`: Endpoint (default localhost:11434)
  - `BedrockConfig`: Profile, Region
  - `OpenRouterConfig`: APIKeyEnv
  - Parse from vault `config.yaml` under `ai:` key

- [ ] **Modify `cli/internal/vault/config.go`** — Add AI config to vault config
  - Add `AI AIConfig` field to `VaultConfig` struct
  - Default values: provider=bedrock, embedding_model=amazon.nova-embed-multimodal-v2, generation_model=anthropic.claude-haiku-4-5, dimensions=1024

- [ ] **Create `cli/internal/ai/keystore.go`** — Credential management
  - `GetAPIKey(provider string) (string, error)` — checks env var, then macOS Keychain
  - `SetAPIKey(provider, key string) error` — stores in macOS Keychain via `security` CLI
  - `DeleteAPIKey(provider string) error`
  - `GetAWSProfile(config BedrockConfig) string` — returns profile name for AWS SDK

- [ ] **Tests**: `cli/internal/ai/config_test.go`, `cli/internal/ai/registry_test.go`

### Definition of Done
- `go build` succeeds with new package
- `make test` passes
- Config can be parsed from YAML with AI section
- Registry can register and retrieve mock providers

---

## Sprint 2: AWS Bedrock Provider
**Goal**: Cloud AI via existing AWS SSO credentials. Nova Embeddings v2 + Claude Haiku 4.5.
**Estimated effort**: 1 session
**Prerequisite**: Sprint 1
**Status**: Not Started

### Tasks

- [ ] **Add AWS SDK v2 dependencies**
  - `go get github.com/aws/aws-sdk-go-v2`
  - `go get github.com/aws/aws-sdk-go-v2/config`
  - `go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime`
  - `go get github.com/aws/aws-sdk-go-v2/service/bedrock`

- [ ] **Create `cli/internal/ai/bedrock.go`** — Bedrock provider
  - `BedrockEmbedder` implementing `EmbeddingProvider`
    - Model: `amazon.nova-embed-multimodal-v2:0`
    - `Embed(ctx, texts)`: call `InvokeModel` with JSON payload
    - `Available(ctx)`: try `ListFoundationModels` with timeout
  - `BedrockGenerator` implementing `GenerationProvider`
    - Model: `anthropic.claude-haiku-4-5-20251001-v1:0`
    - `Generate(ctx, prompt, opts)`: call `InvokeModel` with Messages API format
  - AWS config loading: `config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(profile))`

- [ ] **Create `cli/internal/ai/bedrock_models.go`** — Model discovery
  - `ListBedrockModels(ctx) ([]ModelInfo, error)`
  - Call `bedrock.ListFoundationModels`, filter for embedding + text generation
  - Map to ModelInfo with pricing (hardcoded pricing table since Bedrock API doesn't return prices)
  - Cache results to `~/.2ndbrain/cache/bedrock_models.json`

- [ ] **Register Bedrock provider in registry**

- [ ] **Tests**: Mock Bedrock client test, config loading test

### Definition of Done
- `2nb config set ai.provider bedrock` switches to cloud
- `2nb index` generates embeddings via Nova Embeddings v2
- `2nb search "auth"` uses cloud embeddings for hybrid search
- Uses existing AWS SSO credentials (no new keys needed)
- `make test` passes

---

## Sprint 3: Vector Storage + Hybrid Search
**Goal**: Store embeddings in SQLite and combine with BM25 for hybrid search.
**Estimated effort**: 1 session
**Prerequisite**: Sprint 2
**Status**: Not Started

### Tasks

- [ ] **Modify `cli/internal/store/migrations.go`** — Schema migration
  - Add `embedding BLOB` column to `documents` table
  - Add `embedding_model TEXT` column (tracks which model generated it)
  - Add `embedding_hash TEXT` column (content hash to detect stale embeddings)
  - Migration version bump

- [ ] **Modify `cli/internal/store/db.go`** — CRUD for embeddings
  - `SetEmbedding(docID string, embedding []float32, model string, hash string) error`
  - `GetEmbedding(docID string) ([]float32, error)`
  - `DocumentsNeedingEmbedding(model string) ([]Document, error)` — where hash changed or model differs
  - Serialize `[]float32` as binary BLOB (little-endian)

- [ ] **Create `cli/internal/search/vector.go`** — Vector search
  - `CosineSimilarity(a, b []float32) float32`
  - `VectorSearch(ctx, query []float32, limit int) ([]ScoredDoc, error)` — brute-force scan over all embeddings in SQLite
  - Return doc ID + similarity score

- [ ] **Modify `cli/internal/search/engine.go`** — Hybrid search
  - Add `HybridSearch(ctx, query string, opts SearchOpts) ([]Result, error)`
  - Embed the query text using configured EmbeddingProvider
  - Run BM25 search (existing)
  - Run vector search
  - Combine via Reciprocal Rank Fusion (RRF): `score = Σ 1/(k + rank_i)` with k=60
  - Fall back to BM25-only if no embeddings available

- [ ] **Modify `cli/internal/cli/index.go`** — Add embedding to index command
  - After building BM25 index, generate embeddings for all documents
  - Show progress: "Embedding 42/100 documents..."
  - Skip documents whose content hash hasn't changed
  - Use configured provider

- [ ] **Modify `cli/internal/cli/search.go`** — Use hybrid search
  - Default to hybrid when embeddings exist
  - `--bm25-only` flag to force keyword search
  - Show search mode in output ("hybrid" vs "keyword")

- [ ] **Tests**: Cosine similarity unit test, RRF unit test, hybrid search integration test

### Definition of Done
- `2nb index` generates embeddings via configured provider and stores in SQLite
- `2nb search "auth patterns"` returns hybrid results
- `2nb search --bm25-only "auth"` returns keyword-only results
- Re-indexing skips unchanged documents
- `make test` passes

---

## Sprint 4: CLI Commands — models, config, ai status
**Goal**: User-facing commands for managing providers, models, and credentials.
**Estimated effort**: 1 session
**Prerequisite**: Sprint 1
**Status**: Not Started

### Tasks

- [ ] **Create `cli/internal/cli/models.go`** — `2nb models` command
  - `2nb models list` — query all providers, display table with: Provider, Type, Model, Price, Dims, CTX
  - `2nb models list --free` — filter to free models only
  - `2nb models list --type embed` — filter by type
  - `2nb models pull <model>` — wraps `ollama pull` for local models
  - Reads from cache first, refreshes if stale (>24h)

- [ ] **Create `cli/internal/cli/ai_cmd.go`** — `2nb ai` command
  - `2nb ai status` — show current provider, models, readiness, memory usage
  - `2nb ai embed <text>` — generate embedding for text (debug/testing)

- [ ] **Create `cli/internal/cli/config_cmd.go`** — `2nb config` command
  - `2nb config get <key>` — read config value (e.g., `ai.provider`)
  - `2nb config set <key> <value>` — update config value
  - `2nb config set-key <provider>` — prompts for API key, stores in Keychain
  - `2nb config show` — dump full config

- [ ] **Create `cli/internal/ai/cache.go`** — Model list caching
  - Cache ModelInfo list to `~/.2ndbrain/cache/models.json`
  - TTL: 24 hours
  - `RefreshIfStale(ctx) error`

- [ ] **Tests**: Command output format tests, cache TTL test

### Definition of Done
- `2nb models list` shows models from all configured providers with pricing
- `2nb ai status` shows provider readiness
- `2nb config set ai.provider bedrock` persists to config.yaml
- `2nb config set-key openrouter` stores key in Keychain
- `make test` passes

---

## Sprint 5: MCP Integration + kb_ask
**Goal**: Update MCP tools to use hybrid search. Add Q&A command.
**Estimated effort**: 1 session
**Prerequisite**: Sprint 3
**Status**: Not Started

### Tasks

- [ ] **Modify `cli/internal/mcp/server.go`** — Update kb_search
  - `kb_search` uses hybrid search when embeddings available
  - Add `search_mode` field to response ("hybrid" or "keyword")
  - Add optional `semantic_only` parameter for pure vector search

- [ ] **Add MCP tool: `kb_ask`** — Q&A over vault
  - Takes a question string
  - Retrieves top-5 chunks via hybrid search
  - Passes chunks + question to GenerationProvider
  - Returns generated answer with source citations
  - Requires generation provider to be available

- [ ] **Add MCP tool: `kb_embed`** — Generate embedding for text
  - Debug/testing tool
  - Returns raw embedding vector as JSON array

- [ ] **Add CLI command: `2nb ask`** — Interactive Q&A
  - `2nb ask "how does auth work?"` — RAG pipeline: embed query → vector search → generate answer
  - Streams response to stdout
  - Shows source documents used

- [ ] **End-to-end integration test**
  - Create test vault with 10 docs
  - Index with embeddings
  - Verify hybrid search returns better results than BM25 alone
  - Verify `2nb ask` generates coherent answer
  - Verify MCP `kb_search` includes semantic results

- [ ] **Update CLAUDE.md** with new AI commands and provider setup

### Definition of Done
- MCP `kb_search` returns hybrid results
- MCP `kb_ask` answers questions using RAG
- `2nb ask "question"` works end-to-end
- Integration tests pass
- CLAUDE.md updated
- `make test` passes

---

## Sprint 6: OpenRouter Provider
**Goal**: Add OpenRouter as a provider option with model discovery and free embeddings.
**Estimated effort**: 1 session
**Prerequisite**: Sprint 1, Sprint 4
**Status**: Not Started

### Tasks

- [ ] **Create `cli/internal/ai/openrouter.go`** — OpenRouter provider
  - `OpenRouterEmbedder` implementing `EmbeddingProvider`
    - Default model: `nvidia/llama-nemotron-embed-vl-1b-v2:free`
    - `Embed(ctx, texts)`: POST to `https://openrouter.ai/api/v1/embeddings`
    - OpenAI-compatible API format
  - `OpenRouterGenerator` implementing `GenerationProvider`
    - Default model: user-configured
    - `Generate(ctx, prompt, opts)`: POST to `https://openrouter.ai/api/v1/chat/completions`

- [ ] **Create `cli/internal/ai/openrouter_models.go`** — Model discovery
  - `ListOpenRouterModels(ctx) ([]ModelInfo, error)`
  - GET `https://openrouter.ai/api/v1/embeddings/models` for embedding models
  - GET `https://openrouter.ai/api/v1/models` for generation models
  - Parse pricing from response
  - Cache to `~/.2ndbrain/cache/openrouter_models.json`

- [ ] **Register OpenRouter provider in registry**

- [ ] **Tests**: HTTP mock test for OpenRouter API

### Definition of Done
- `2nb config set ai.provider openrouter` + `OPENROUTER_API_KEY` env var
- `2nb models list` includes OpenRouter models with pricing
- `2nb index` works with OpenRouter Nemotron embeddings (free)
- `make test` passes

---

## Sprint 7: Ollama Provider — Local AI
**Goal**: Working local AI via Ollama. Requires model downloads (~3 GB).
**Estimated effort**: 1 session
**Prerequisite**: Sprint 1
**Status**: Not Started
**Note**: Defer until on fast network — models are ~3 GB total download.

### Tasks

- [ ] **Add Ollama Go SDK dependency**
  - `go get github.com/ollama/ollama`
  - Use the official `api` package for typed requests

- [ ] **Create `cli/internal/ai/ollama.go`** — Ollama provider
  - `OllamaEmbedder` implementing `EmbeddingProvider`
    - `Available(ctx)`: heartbeat to localhost:11434
    - `Embed(ctx, texts)`: call `client.Embed()` with model name + keep_alive=-1
    - `ListModels(ctx)`: call `client.List()`, filter embedding vs generation models
  - `OllamaGenerator` implementing `GenerationProvider`
    - `Generate(ctx, prompt, opts)`: call `client.Generate()` with temperature, max_tokens
  - Auto-detect installed models via `client.List()`

- [ ] **Create `cli/internal/ai/llamacpp.go`** — Fallback embedder (no daemon)
  - `LlamaCppEmbedder` implementing `EmbeddingProvider`
  - Uses `github.com/amikos-tech/llamacpp-embedder/bindings/go`
  - Model path: `~/.2ndbrain/models/embeddinggemma-q4.gguf`
  - `Available(ctx)`: check if GGUF file exists
  - `EnsureModel()`: download from HuggingFace if not present (with progress bar)

- [ ] **Create `cli/internal/ai/model_download.go`** — Model downloader
  - Download GGUF from HuggingFace Hub URL
  - Progress bar to stderr
  - Verify file size after download
  - Store in `~/.2ndbrain/models/`

- [ ] **Register Ollama + LlamaCpp providers in registry**

- [ ] **Tests**: Mock Ollama server test, LlamaCpp embedder test with small fixture

### Definition of Done
- `2nb ai status` shows Ollama provider status (if running)
- Can call Ollama embed API from Go
- LlamaCpp fallback downloads and loads EmbeddingGemma GGUF
- `make test` passes

---

## Sprint 8: GUI Integration (stretch)
**Goal**: Surface AI features in the macOS editor.
**Estimated effort**: 1-2 sessions
**Prerequisite**: Sprint 5
**Status**: Not Started

### Tasks

- [ ] Add AI status indicator to status bar
- [ ] Add "Ask AI" panel (Cmd+Shift+A)
- [ ] Show embedding progress during index rebuild
- [ ] Semantic search option in search panel
- [ ] "Find Similar" context menu item

### Definition of Done
- GUI shows AI status and allows Q&A
- Search panel supports semantic mode
- `make test-gui` passes with new tests

---

## Summary

| Sprint | Focus | Effort | Dependencies | Status |
|--------|-------|--------|-------------|--------|
| 1 | Interfaces + Config + Keystore | 1 session | None | Not Started |
| 2 | AWS Bedrock Provider | 1 session | Sprint 1 | Not Started |
| 3 | Vector Storage + Hybrid Search | 1 session | Sprint 2 | Not Started |
| 4 | CLI Commands (models, config, ai) | 1 session | Sprint 1 | Not Started |
| 5 | MCP Integration + kb_ask | 1 session | Sprint 3 | Not Started |
| 6 | OpenRouter Provider | 1 session | Sprint 1, 4 | Not Started |
| 7 | Ollama Provider (local AI) | 1 session | Sprint 1 | Not Started |
| 8 | GUI Integration (stretch) | 1-2 sessions | Sprint 5 | Not Started |

**Critical path**: 1 → 2 → 3 → 5 (AWS cloud AI end-to-end)
**MVP = Sprints 1-4**: AWS Bedrock + vector search + CLI commands
**Deferred**: Sprint 7 (Ollama/local) requires ~3 GB model downloads
