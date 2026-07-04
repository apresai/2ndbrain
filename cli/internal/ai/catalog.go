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
	for _, m := range []struct {
		id, name string
		ctxLen   int
		priceIn  float64
		priceOut float64
		notes    string
	}{
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "Claude Haiku 4.5", 200000, 0.80, 4.00, ""},
		{"us.anthropic.claude-sonnet-4-6", "Claude Sonnet 4.6", 1000000, 3.00, 15.00, "1M context"},
		{"us.anthropic.claude-opus-4-6-v1", "Claude Opus 4.6", 1000000, 15.00, 75.00, "1M context; Opus 4.7 is Mantle-only and not reachable via Converse"},
		{"amazon.nova-micro-v1:0", "Amazon Nova Micro", 128000, 0.035, 0.14, "text-only, fastest"},
		{"amazon.nova-lite-v1:0", "Amazon Nova Lite", 300000, 0.06, 0.24, ""},
		{"amazon.nova-pro-v1:0", "Amazon Nova Pro", 300000, 0.80, 3.20, ""},
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
		ConfigHint:                     configHint("openrouter", "embedding", "nvidia/llama-nemotron-embed-vl-1b-v2:free"),
		RecommendedSimilarityThreshold: 0.60,
		InvokeStrategy:                 StrategyOpenRouterEmbeddings,
	})

	// --- OpenRouter Generation (OpenAI-compatible chat completions) ---
	// OpenRouter normalizes to a standard format so most models work.
	for _, m := range []struct {
		id, name string
		ctxLen   int
		priceIn  float64
		priceOut float64
	}{
		{"google/gemma-3-4b-it:free", "Gemma 3 4B (free)", 131072, 0, 0},
		{"google/gemma-4-31b-it:free", "Gemma 4 31B (free)", 262144, 0, 0},
		{"meta-llama/llama-3.3-70b-instruct:free", "Llama 3.3 70B (free)", 131072, 0, 0},
		{"qwen/qwen3.6-plus", "Qwen 3.6 Plus", 1000000, 0.33, 1.95},
		{"anthropic/claude-haiku-4-5", "Claude Haiku 4.5", 200000, 0.80, 4.00},
		{"anthropic/claude-sonnet-4-6", "Claude Sonnet 4.6", 1000000, 3.00, 15.00},
		{"anthropic/claude-opus-4-7", "Claude Opus 4.7", 1000000, 15.00, 75.00},
		{"openai/gpt-4o-mini", "GPT-4o Mini", 128000, 0.15, 0.60},
		{"openai/gpt-4o", "GPT-4o", 128000, 2.50, 10.00},
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
		id        string
		dims      int
		threshold float64
		notes     string
	}{
		// 768d Matryoshka: moderate spread, ~0.35–0.45 random-pair cosine typical.
		{"nomic-embed-text", 768, 0.50, ""},
		// 1024d contrastive + Matryoshka (Mixedbread): tighter than nomic.
		{"mxbai-embed-large", 1024, 0.55, ""},
		// 1024d retrieval-tuned (Snowflake Arctic).
		{"snowflake-arctic-embed", 1024, 0.55, ""},
		// 384d MiniLM: small/wide-spread; lower threshold is meaningful.
		{"all-minilm", 384, 0.35, ""},
		// 1024d multi-granularity (BGE M3); dense channel similar to other 1024d.
		{"bge-m3", 1024, 0.55, ""},
	} {
		models = append(models, ModelInfo{
			ID:                             m.id,
			Name:                           m.id,
			Provider:                       "ollama",
			Type:                           "embedding",
			Dimensions:                     m.dims,
			Local:                          true,
			Tier:                           TierVerified,
			ConfigHint:                     configHint("ollama", "embedding", m.id),
			RecommendedSimilarityThreshold: m.threshold,
			Notes:                          m.notes,
			InvokeStrategy:                 StrategyOllamaEmbeddings,
		})
	}

	// --- Ollama Generation (local) ---
	for _, m := range []struct {
		id, name string
		ctxLen   int
	}{
		{"gemma3:4b", "Gemma 3 4B", 131072},
		{"gemma3:1b", "Gemma 3 1B", 32768},
		{"qwen2.5:0.5b", "Qwen 2.5 0.5B", 32768},
		{"qwen3:30b-a3b", "Qwen3 30B MoE", 32768},
		{"llama3.2:3b", "Llama 3.2 3B", 131072},
		{"phi4-mini", "Phi-4 Mini", 131072},
	} {
		models = append(models, ModelInfo{
			ID:             m.id,
			Name:           m.name,
			Provider:       "ollama",
			Type:           "generation",
			ContextLen:     m.ctxLen,
			Local:          true,
			Tier:           TierVerified,
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
		ConfigHint:                     configHint(llamaProviderName, "embedding", "embeddinggemma-300m"),
		RecommendedSimilarityThreshold: 0.55,
		Notes:                          "768d Matryoshka (→512/256/128); 2K context; symmetric — run `2nb models calibrate`",
		InvokeStrategy:                 StrategyLlamaEmbeddings,
	})

	// --- llama-local Generation (bundled llama.cpp engine) ---
	for _, m := range []struct {
		id, name string
		ctxLen   int
	}{
		{"gemma4-e4b", "Gemma 4 E4B", 131072},
		{"gemma4-e2b", "Gemma 4 E2B", 131072},
	} {
		models = append(models, ModelInfo{
			ID:             m.id,
			Name:           m.name,
			Provider:       llamaProviderName,
			Type:           "generation",
			ContextLen:     m.ctxLen,
			Local:          true,
			Tier:           TierVerified,
			ConfigHint:     configHint(llamaProviderName, "generation", m.id),
			InvokeStrategy: StrategyLlamaChat,
		})
	}

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
	field := "ai.generation_model"
	if modelType == "embedding" {
		field = "ai.embedding_model"
	}
	return fmt.Sprintf("2nb config set ai.provider %s && 2nb config set %s %s", provider, field, modelID)
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
