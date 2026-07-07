package ai

import (
	"fmt"
	"strings"
)

// BuiltinCatalog returns all models that 2nb has verified API harnesses for.
// These models have been tested with the current provider implementations
// and are known to work correctly.
func BuiltinCatalog() []ModelInfo {
	var models []ModelInfo

	// --- Bedrock Embedding (Nova Embeddings v2 format) ---
	// RecommendedSimilarityThreshold=0.25: queries embed with Nova's asymmetric
	// GENERIC_RETRIEVAL purpose (documents stay GENERIC_INDEX), which collapses
	// the cosine scale — measured on a real 151-doc vault, true-match cosine
	// sits at p50≈0.34 and unrelated-pair cosine at p95≈0.23, vs ~0.80/~0.72
	// under the old symmetric (GENERIC_INDEX-for-queries) embedding. So 0.25
	// sits just above the negative p95 and well under the true-match p50; the
	// old 0.65 would reject every real match post-flip. (Asymmetric purpose
	// also widened match/noise separation 0.077 -> 0.115 and lifted Recall@10
	// to 1.0; see internal/eval/asymmetry.go.) Bump toward ~0.30 if a vault
	// surfaces off-topic neighbors; drop toward ~0.20 if legit matches get cut.
	models = append(models, ModelInfo{
		ID:                             "amazon.nova-2-multimodal-embeddings-v1:0",
		Name:                           "Amazon Nova Embeddings v2",
		Provider:                       "bedrock",
		Type:                           "embedding",
		Dimensions:                     1024,
		SupportedDimensions:            []int{256, 384, 1024, 3072},
		Modalities:                     []string{"text", "image", "video", "audio"},
		ContextLen:                     8192,
		PriceIn:                        0.135,
		PriceOut:                       0,
		Local:                          false,
		Tier:                           TierVerified,
		Recommended:                    true,
		ConfigHint:                     configHint("bedrock", "embedding", "amazon.nova-2-multimodal-embeddings-v1:0"),
		RecommendedSimilarityThreshold: 0.25,
		InvokeStrategy:                 StrategyBedrockInvokeNovaEmbed,
	})

	// --- Bedrock Embedding (Titan Text Embeddings v2 format) ---
	// Threshold 0.50: estimated from model training objective (bidirectional
	// encoder, cosine-normalized). Run `2nb models calibrate` to tune per vault.
	models = append(models, ModelInfo{
		ID:                             "amazon.titan-embed-text-v2:0",
		Name:                           "Amazon Titan Text Embeddings v2",
		Provider:                       "bedrock",
		Type:                           "embedding",
		Dimensions:                     1024,
		ContextLen:                     8192,
		PriceIn:                        0.020,
		PriceOut:                       0,
		Local:                          false,
		Tier:                           TierVerified,
		ConfigHint:                     configHint("bedrock", "embedding", "amazon.titan-embed-text-v2:0"),
		RecommendedSimilarityThreshold: 0.50,
		Notes:                          "supports 256/512/1024 dims",
		InvokeStrategy:                 StrategyBedrockInvokeTitanEmbed,
	})

	// --- Bedrock Embedding (Cohere Embed v3 format — batched, fixed 1024 dims) ---
	// Threshold 0.50: estimated. Cohere v3 uses contrastive training similar to
	// OpenAI ada-002; real-vault calibration recommended.
	for _, m := range []struct {
		id, name string
		notes    string
	}{
		{"cohere.embed-english-v3", "Cohere Embed English v3", "English-optimized"},
		{"cohere.embed-multilingual-v3", "Cohere Embed Multilingual v3", "100+ languages"},
	} {
		models = append(models, ModelInfo{
			ID:                             m.id,
			Name:                           m.name,
			Provider:                       "bedrock",
			Type:                           "embedding",
			Dimensions:                     1024,
			ContextLen:                     512,
			PriceIn:                        0.100,
			PriceOut:                       0,
			Local:                          false,
			Tier:                           TierVerified,
			ConfigHint:                     configHint("bedrock", "embedding", m.id),
			RecommendedSimilarityThreshold: 0.50,
			Notes:                          m.notes,
			InvokeStrategy:                 StrategyBedrockInvokeCohereEmbed,
		})
	}

	// --- Bedrock Generation (Converse API — works across all Bedrock models) ---
	//
	// Anthropic line policy: carry the tested default (Haiku 4.5) plus the
	// current Sonnet and Opus versions. Newer frontier entries (Sonnet 5,
	// Opus 4.8) ship with zero prices so live AWS offer-file enrichment
	// resolves them, and carry a staged-rollout note: AWS gates these
	// per-account (the console can show access granted while bedrock-runtime
	// still 403s), so a real test probe is the only way to know. Opus 4.7 is
	// intentionally absent (superseded by 4.8; still reachable via discovery).
	// Fable 5 is intentionally absent from the curated set (premium pricing
	// and materially different API semantics: always-on thinking, refusal stop
	// reason); it remains discoverable and testable via `models list --discover`.
	const stagedRolloutNote = "1M context; AWS staged rollout gates this per account — run `2nb models verify` to check yours"
	for _, m := range []struct {
		id, name    string
		ctxLen      int
		priceIn     float64
		priceOut    float64
		notes       string
		recommended bool
	}{
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "Claude Haiku 4.5", 200000, 1.00, 5.00, "2nb tested default", true},
		{"us.anthropic.claude-sonnet-4-6", "Claude Sonnet 4.6", 1000000, 3.00, 15.00, "1M context", true},
		{"us.anthropic.claude-sonnet-5", "Claude Sonnet 5", 1000000, 0, 0, stagedRolloutNote, true},
		{"us.anthropic.claude-opus-4-6-v1", "Claude Opus 4.6", 1000000, 0, 0, "1M context", false},
		{"us.anthropic.claude-opus-4-8", "Claude Opus 4.8", 1000000, 0, 0, stagedRolloutNote, true},
		{"amazon.nova-micro-v1:0", "Amazon Nova Micro", 128000, 0.035, 0.14, "text-only, fastest", false},
		{"amazon.nova-lite-v1:0", "Amazon Nova Lite", 300000, 0.06, 0.24, "", false},
		{"amazon.nova-pro-v1:0", "Amazon Nova Pro", 300000, 0.80, 3.20, "", false},
	} {
		models = append(models, ModelInfo{
			ID:             m.id,
			Name:           m.name,
			Provider:       "bedrock",
			Type:           "generation",
			ContextLen:     m.ctxLen,
			PriceIn:        m.priceIn,
			PriceOut:       m.priceOut,
			Local:          false,
			Tier:           TierVerified,
			Recommended:    m.recommended,
			ConfigHint:     configHint("bedrock", "generation", m.id),
			Notes:          m.notes,
			InvokeStrategy: StrategyBedrockConverse,
		})
	}

	// --- OpenRouter Embedding (OpenAI-compatible embeddings API) ---
	// Threshold 0.60: vision-language embedder trained contrastively at 1024d.
	// Estimated from training objective + dimensionality — not measured on a
	// real vault. Users should run `2nb models calibrate` to refine.
	models = append(models, ModelInfo{
		ID:                             "nvidia/llama-nemotron-embed-vl-1b-v2:free",
		Name:                           "Nemotron Embed VL 1B v2",
		Provider:                       "openrouter",
		Type:                           "embedding",
		Dimensions:                     1024,
		ContextLen:                     4096,
		PriceIn:                        0,
		PriceOut:                       0,
		Local:                          false,
		Tier:                           TierVerified,
		Recommended:                    true,
		ConfigHint:                     configHint("openrouter", "embedding", "nvidia/llama-nemotron-embed-vl-1b-v2:free"),
		RecommendedSimilarityThreshold: 0.60,
		InvokeStrategy:                 StrategyOpenRouterEmbeddings,
	})

	// --- OpenRouter Generation (OpenAI-compatible chat completions) ---
	// OpenRouter normalizes to a standard format so most models work.
	// Anthropic entries mirror the Bedrock line (Opus 4.7 dropped for 4.8,
	// Sonnet 5 added); newer versions ship unpriced so live OpenRouter
	// /models pricing resolves them.
	for _, m := range []struct {
		id, name    string
		ctxLen      int
		priceIn     float64
		priceOut    float64
		recommended bool
	}{
		{"google/gemma-3-4b-it:free", "Gemma 3 4B (free)", 131072, 0, 0, false},
		{"google/gemma-4-31b-it:free", "Gemma 4 31B (free)", 262144, 0, 0, true},
		{"meta-llama/llama-3.3-70b-instruct:free", "Llama 3.3 70B (free)", 131072, 0, 0, false},
		{"qwen/qwen3.6-plus", "Qwen 3.6 Plus", 1000000, 0.33, 1.95, false},
		{"anthropic/claude-haiku-4-5", "Claude Haiku 4.5", 200000, 0.80, 4.00, true},
		{"anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6", 1000000, 3.00, 15.00, true},
		{"anthropic/claude-sonnet-5", "Claude Sonnet 5", 1000000, 0, 0, false},
		{"anthropic/claude-opus-4-8", "Claude Opus 4.8", 1000000, 0, 0, false},
		{"openai/gpt-4o-mini", "GPT-4o Mini", 128000, 0.15, 0.60, false},
		{"openai/gpt-4o", "GPT-4o", 128000, 2.50, 10.00, false},
	} {
		models = append(models, ModelInfo{
			ID:             m.id,
			Name:           m.name,
			Provider:       "openrouter",
			Type:           "generation",
			ContextLen:     m.ctxLen,
			PriceIn:        m.priceIn,
			PriceOut:       m.priceOut,
			Local:          false,
			Tier:           TierVerified,
			Recommended:    m.recommended,
			ConfigHint:     configHint("openrouter", "generation", m.id),
			InvokeStrategy: StrategyOpenRouterChat,
		})
	}

	// --- Ollama Embedding (local) ---
	// Thresholds are calibration-informed estimates from each model's training
	// objective and typical unrelated-pair cosine reported in the MTEB
	// benchmark. Smaller-dim models (all-minilm at 384d) spread wider — low
	// threshold is meaningful. Larger 1024d models trained with contrastive
	// or Matryoshka losses cluster tighter. Users should run
	// `2nb models calibrate` to tune for their own vault.
	for _, m := range []struct {
		id          string
		dims        int
		threshold   float64
		notes       string
		recommended bool
	}{
		// 768d Matryoshka: moderate spread, ~0.35–0.45 random-pair cosine typical.
		{"nomic-embed-text", 768, 0.50, "", true},
		// 1024d contrastive + Matryoshka (Mixedbread): tighter than nomic.
		{"mxbai-embed-large", 1024, 0.55, "", false},
		// 1024d retrieval-tuned (Snowflake Arctic).
		{"snowflake-arctic-embed", 1024, 0.55, "", false},
		// 384d MiniLM: small/wide-spread; lower threshold is meaningful.
		{"all-minilm", 384, 0.35, "", false},
		// 1024d multi-granularity (BGE M3); dense channel similar to other 1024d.
		{"bge-m3", 1024, 0.55, "", false},
	} {
		models = append(models, ModelInfo{
			ID:                             m.id,
			Name:                           m.id,
			Provider:                       "ollama",
			Type:                           "embedding",
			Dimensions:                     m.dims,
			Local:                          true,
			Tier:                           TierVerified,
			Recommended:                    m.recommended,
			ConfigHint:                     configHint("ollama", "embedding", m.id),
			RecommendedSimilarityThreshold: m.threshold,
			Notes:                          m.notes,
			InvokeStrategy:                 StrategyOllamaEmbeddings,
		})
	}

	// --- Ollama Generation (local) ---
	for _, m := range []struct {
		id, name    string
		ctxLen      int
		recommended bool
	}{
		{"gemma3:4b", "Gemma 3 4B", 131072, true},
		{"gemma3:1b", "Gemma 3 1B", 32768, false},
		{"qwen2.5:0.5b", "Qwen 2.5 0.5B", 32768, false},
		{"qwen3:30b-a3b", "Qwen3 30B MoE", 32768, false},
		{"llama3.2:3b", "Llama 3.2 3B", 131072, false},
		{"phi4-mini", "Phi-4 Mini", 131072, false},
	} {
		models = append(models, ModelInfo{
			ID:             m.id,
			Name:           m.name,
			Provider:       "ollama",
			Type:           "generation",
			ContextLen:     m.ctxLen,
			Local:          true,
			Tier:           TierVerified,
			Recommended:    m.recommended,
			ConfigHint:     configHint("ollama", "generation", m.id),
			InvokeStrategy: StrategyOllamaGenerate,
		})
	}

	// --- llama-local Embedding (bundled llama.cpp engine) ---
	// EmbeddingGemma is a symmetric bi-encoder (unlike Nova's asymmetric
	// query/index purpose), so its noise floor doesn't collapse — seed a
	// mid-range threshold and let `2nb models calibrate` tune it per vault.
	models = append(models, ModelInfo{
		ID:                             "embeddinggemma-300m",
		Name:                           "EmbeddingGemma 300M",
		Provider:                       llamaProviderName,
		Type:                           "embedding",
		Dimensions:                     768,
		SupportedDimensions:            []int{128, 256, 512, 768}, // Matryoshka
		ContextLen:                     2048,
		Local:                          true,
		Tier:                           TierVerified,
		Recommended:                    true,
		ConfigHint:                     configHint(llamaProviderName, "embedding", "embeddinggemma-300m"),
		RecommendedSimilarityThreshold: 0.55,
		Notes:                          "768d Matryoshka (→512/256/128); 2K context; symmetric — run `2nb models calibrate`",
		InvokeStrategy:                 StrategyLlamaEmbeddings,
	})

	// --- llama-local Generation (bundled llama.cpp engine) ---
	for _, m := range []struct {
		id, name    string
		ctxLen      int
		recommended bool
	}{
		{"gemma4-e4b", "Gemma 4 E4B", 131072, false},
		{"gemma4-e2b", "Gemma 4 E2B", 131072, true},
	} {
		models = append(models, ModelInfo{
			ID:             m.id,
			Name:           m.name,
			Provider:       llamaProviderName,
			Type:           "generation",
			ContextLen:     m.ctxLen,
			Local:          true,
			Tier:           TierVerified,
			Recommended:    m.recommended,
			ConfigHint:     configHint(llamaProviderName, "generation", m.id),
			InvokeStrategy: StrategyLlamaChat,
		})
	}

	// --- Rerank (cross-encoder; optional stage, default OFF — see internal/retrieve) ---
	// Surfaced so the CLI/GUI can list + select a reranker. Measured to HURT
	// retrieval at small-vault scale (Recall@10 already ~1.0), so it ships
	// default-off; listing it does not recommend enabling it.
	models = append(models, ModelInfo{
		ID:           "cohere.rerank-v3-5:0",
		Name:         "Cohere Rerank 3.5",
		Provider:     "bedrock",
		Type:         "rerank",
		ContextLen:   4096,
		PriceRequest: 0.002, // ~$2 / 1,000 rerank queries (up to 100 docs each); us-east-1 in-region only
		Local:        false,
		Tier:         TierVerified,
		ConfigHint:   configHint("bedrock", "rerank", "cohere.rerank-v3-5:0"),
		Notes:        "cross-encoder reranker; us-east-1 only; default OFF (measured to hurt at small scale)",
	})
	models = append(models, ModelInfo{
		ID:         "bge-reranker-v2-m3",
		Name:       "BGE Reranker v2 m3",
		Provider:   llamaProviderName,
		Type:       "rerank",
		ContextLen: 8192,
		Local:      true,
		Tier:       TierVerified,
		ConfigHint: configHint(llamaProviderName, "rerank", "bge-reranker-v2-m3"),
		Notes:      "local cross-encoder reranker (bundled llama.cpp); default OFF",
	})

	for i := range models {
		switch {
		case models[i].Local:
			models[i].PriceSource = "builtin"
		case models[i].PriceIn > 0 || models[i].PriceOut > 0 || models[i].PriceRequest > 0:
			models[i].PriceSource = "builtin"
		case models[i].Provider == "openrouter" && strings.HasSuffix(models[i].ID, ":free"):
			models[i].PriceSource = "builtin"
		}
	}

	return models
}

// configHint returns a human-readable command to switch to a model.
func configHint(provider, modelType, modelID string) string {
	switch modelType {
	case "embedding":
		return fmt.Sprintf("2nb config set ai.provider %s && 2nb config set ai.embedding_model %s", provider, modelID)
	case "rerank":
		// The reranker resolves from the active provider; enabling it + naming the
		// model is the actionable step (rerank ships default OFF).
		return fmt.Sprintf("2nb config set ai.rerank.model %s && 2nb config set ai.rerank.enabled true", modelID)
	default: // generation
		return fmt.Sprintf("2nb config set ai.provider %s && 2nb config set ai.generation_model %s", provider, modelID)
	}
}

// catalogKey is the composite identity key for a ModelInfo: provider + id
// separated by NUL so neither field can spoof the other. Shared by catalogIndex
// and the user-catalog overlay logic.
func catalogKey(provider, id string) string {
	return provider + "\x00" + id
}

// catalogIndex returns a set of catalogKey(provider, id) entries for fast deduplication.
func catalogIndex(catalog []ModelInfo) map[string]bool {
	idx := make(map[string]bool, len(catalog))
	for _, m := range catalog {
		idx[catalogKey(m.Provider, m.ID)] = true
	}
	return idx
}
