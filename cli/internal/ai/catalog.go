package ai

import "fmt"

// BuiltinCatalog returns all models that 2nb has verified API harnesses for.
// These models have been tested with the current provider implementations
// and are known to work correctly.
func BuiltinCatalog() []ModelInfo {
	var models []ModelInfo

	// --- Bedrock Embedding (Nova Embeddings v2 format) ---
	models = append(models, ModelInfo{
		ID:         "amazon.nova-2-multimodal-embeddings-v1:0",
		Name:       "Amazon Nova Embeddings v2",
		Provider:   "bedrock",
		Type:       "embedding",
		Dimensions: 1024,
		ContextLen: 2048,
		PriceIn:    0.135,
		PriceOut:   0,
		Local:      false,
		Tier:       TierVerified,
		ConfigHint: configHint("bedrock", "embedding", "amazon.nova-2-multimodal-embeddings-v1:0"),
	})

	// --- Bedrock Generation (Converse API — works across all Bedrock models) ---
	for _, m := range []struct {
		id, name   string
		ctxLen     int
		priceIn    float64
		priceOut   float64
		notes      string
	}{
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "Claude Haiku 4.5", 200000, 0.80, 4.00, ""},
		{"us.anthropic.claude-sonnet-4-20250514-v1:0", "Claude Sonnet 4", 200000, 3.00, 15.00, ""},
		{"us.anthropic.claude-opus-4-20250514-v1:0", "Claude Opus 4", 200000, 15.00, 75.00, ""},
		{"us.anthropic.claude-3-5-haiku-20241022-v1:0", "Claude 3.5 Haiku", 200000, 0.80, 4.00, ""},
		{"us.anthropic.claude-3-5-sonnet-20241022-v2:0", "Claude 3.5 Sonnet v2", 200000, 3.00, 15.00, ""},
		{"amazon.nova-micro-v1:0", "Amazon Nova Micro", 128000, 0.035, 0.14, "text-only, fastest"},
		{"amazon.nova-lite-v1:0", "Amazon Nova Lite", 300000, 0.06, 0.24, ""},
		{"amazon.nova-pro-v1:0", "Amazon Nova Pro", 300000, 0.80, 3.20, ""},
	} {
		models = append(models, ModelInfo{
			ID:         m.id,
			Name:       m.name,
			Provider:   "bedrock",
			Type:       "generation",
			ContextLen: m.ctxLen,
			PriceIn:    m.priceIn,
			PriceOut:   m.priceOut,
			Local:      false,
			Tier:       TierVerified,
			ConfigHint: configHint("bedrock", "generation", m.id),
			Notes:      m.notes,
		})
	}

	// --- OpenRouter Embedding (OpenAI-compatible embeddings API) ---
	models = append(models, ModelInfo{
		ID:         "nvidia/llama-nemotron-embed-vl-1b-v2:free",
		Name:       "Nemotron Embed VL 1B v2",
		Provider:   "openrouter",
		Type:       "embedding",
		Dimensions: 1024,
		ContextLen: 4096,
		PriceIn:    0,
		PriceOut:   0,
		Local:      false,
		Tier:       TierVerified,
		ConfigHint: configHint("openrouter", "embedding", "nvidia/llama-nemotron-embed-vl-1b-v2:free"),
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
		{"anthropic/claude-sonnet-4", "Claude Sonnet 4", 200000, 3.00, 15.00},
		{"anthropic/claude-opus-4", "Claude Opus 4", 200000, 15.00, 75.00},
		{"openai/gpt-4o-mini", "GPT-4o Mini", 128000, 0.15, 0.60},
		{"openai/gpt-4o", "GPT-4o", 128000, 2.50, 10.00},
	} {
		models = append(models, ModelInfo{
			ID:         m.id,
			Name:       m.name,
			Provider:   "openrouter",
			Type:       "generation",
			ContextLen: m.ctxLen,
			PriceIn:    m.priceIn,
			PriceOut:   m.priceOut,
			Local:      false,
			Tier:       TierVerified,
			ConfigHint: configHint("openrouter", "generation", m.id),
		})
	}

	// --- Ollama Embedding (local) ---
	for _, m := range []struct {
		id   string
		dims int
	}{
		{"nomic-embed-text", 768},
		{"mxbai-embed-large", 1024},
		{"snowflake-arctic-embed", 1024},
		{"all-minilm", 384},
		{"bge-m3", 1024},
	} {
		models = append(models, ModelInfo{
			ID:         m.id,
			Name:       m.id,
			Provider:   "ollama",
			Type:       "embedding",
			Dimensions: m.dims,
			Local:      true,
			Tier:       TierVerified,
			ConfigHint: configHint("ollama", "embedding", m.id),
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
			ID:         m.id,
			Name:       m.name,
			Provider:   "ollama",
			Type:       "generation",
			ContextLen: m.ctxLen,
			Local:      true,
			Tier:       TierVerified,
			ConfigHint: configHint("ollama", "generation", m.id),
		})
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
