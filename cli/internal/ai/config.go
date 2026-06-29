package ai

import (
	"fmt"
	"math"
)

// AIConfig holds AI provider configuration from vault config.yaml.
type AIConfig struct {
	Provider        string `yaml:"provider" json:"provider"` // ollama, bedrock, openrouter
	EmbeddingModel  string `yaml:"embedding_model" json:"embedding_model"`
	GenerationModel string `yaml:"generation_model" json:"generation_model"`
	Dimensions      int    `yaml:"dimensions" json:"dimensions"`
	// SimilarityThreshold is the minimum cosine similarity for a vector
	// search hit to be included in results. Below this, results are
	// treated as noise and dropped from the RRF merge so they don't pad
	// the output with low-quality neighbors. Default: 0.20.
	SimilarityThreshold float64 `yaml:"similarity_threshold" json:"similarity_threshold"`
	// BM25Weight / VectorWeight bias the hybrid-search RRF fusion toward keyword
	// or semantic recall. Each defaults to 1.0 (equal-weight RRF) when unset;
	// raise VectorWeight to favor the semantic channel. Resolved via
	// ResolveHybridWeights.
	BM25Weight   float64          `yaml:"bm25_weight,omitempty" json:"bm25_weight,omitempty"`
	VectorWeight float64          `yaml:"vector_weight,omitempty" json:"vector_weight,omitempty"`
	Ollama       OllamaConfig     `yaml:"ollama,omitempty" json:"ollama,omitempty"`
	Bedrock      BedrockConfig    `yaml:"bedrock,omitempty" json:"bedrock,omitempty"`
	OpenRouter   OpenRouterConfig `yaml:"openrouter,omitempty" json:"openrouter,omitempty"`
}

// ResolveHybridWeights returns the BM25 and vector weights for RRF fusion, each
// defaulting to 1.0 (classic equal-weight RRF) when unset (<= 0) or non-finite.
// The non-finite guard is belt-and-suspenders against a NaN/Inf that bypassed
// config-set validation (e.g. a hand-edited config.yaml): a NaN weight would
// otherwise make every fused score NaN and scramble the ranking silently.
func (c AIConfig) ResolveHybridWeights() (bm25, vector float64) {
	return normHybridWeight(c.BM25Weight), normHybridWeight(c.VectorWeight)
}

func normHybridWeight(w float64) float64 {
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
		return 1.0
	}
	return w
}

// OllamaConfig configures the local Ollama provider.
type OllamaConfig struct {
	Endpoint string `yaml:"endpoint" json:"endpoint"` // default: http://localhost:11434
	// Disabled silences the provider in the catalog and GUI selection
	// without removing credentials / endpoint. Absent == enabled.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// BedrockConfig configures the AWS Bedrock provider.
type BedrockConfig struct {
	Profile string `yaml:"profile" json:"profile"` // AWS profile name
	Region  string `yaml:"region" json:"region"`   // AWS region
	// Disabled silences the provider in the catalog and GUI selection
	// without removing credentials. Absent == enabled.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// OpenRouterConfig configures the OpenRouter provider.
type OpenRouterConfig struct {
	APIKeyEnv string `yaml:"api_key_env" json:"api_key_env"` // env var name
	// Disabled silences the provider in the catalog and GUI selection
	// without removing the API key. Absent == enabled.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}

// ProviderDisabled returns whether the named provider has been explicitly
// disabled in cfg. Unknown provider names return false (enabled by default).
func (cfg AIConfig) ProviderDisabled(name string) bool {
	switch name {
	case "bedrock":
		return cfg.Bedrock.Disabled
	case "openrouter":
		return cfg.OpenRouter.Disabled
	case "ollama":
		return cfg.Ollama.Disabled
	}
	return false
}

// SetProviderDisabled sets the disabled flag for the named provider. Unknown
// provider names are a no-op. Pointer receiver: it mutates cfg.
func (cfg *AIConfig) SetProviderDisabled(name string, disabled bool) {
	switch name {
	case "bedrock":
		cfg.Bedrock.Disabled = disabled
	case "openrouter":
		cfg.OpenRouter.Disabled = disabled
	case "ollama":
		cfg.Ollama.Disabled = disabled
	}
}

// DefaultSimilarityThreshold is the conservative floor for semantic search.
// Cosine similarity below this value usually indicates an unrelated neighbor
// rather than a real match, and padding the result list with those hurts more
// than it helps. Tuned by watching the raw cosine reported in search output
// against real vaults — raise it (to ~0.35) if too many irrelevant hits slip
// through, lower it (to ~0.15) if legitimate matches are being cut.
const DefaultSimilarityThreshold = 0.20

// DefaultAIConfig returns sensible defaults. SimilarityThreshold is left at
// zero so ResolveSimilarityThresholdFull falls through to the active
// embedding model's RecommendedSimilarityThreshold (e.g., Nova-2 → 0.25)
// instead of permanently shadowing it with the conservative global default.
func DefaultAIConfig() AIConfig {
	return AIConfig{
		Provider:        "bedrock",
		EmbeddingModel:  "amazon.nova-2-multimodal-embeddings-v1:0",
		GenerationModel: "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		Dimensions:      1024,
		// Ollama (local) and OpenRouter are opt-in: disabled by default so a
		// fresh vault presents only the Bedrock default in selection UIs. The
		// setup wizard / AI Hub clears Disabled when the user enables them.
		// (Disabled only hides a provider's models from catalog dropdowns; it
		// does not prevent an explicitly-chosen active provider from running.)
		Ollama: OllamaConfig{
			Endpoint: "http://localhost:11434",
			Disabled: true,
		},
		Bedrock: BedrockConfig{
			Profile: "default",
			Region:  "us-east-1",
		},
		OpenRouter: OpenRouterConfig{
			APIKeyEnv: "OPENROUTER_API_KEY",
			Disabled:  true,
		},
	}
}

// ResolvedThresholdSource labels where ResolveSimilarityThreshold picked its
// value from — useful for surfacing the reason in `2nb ai status` so a user
// who can't figure out why search is noisy/empty can trace the setting.
type ResolvedThresholdSource string

const (
	ThresholdSourceVaultConfig     ResolvedThresholdSource = "vault config"
	ThresholdSourceUserCalibration ResolvedThresholdSource = "user calibration"
	ThresholdSourceModel           ResolvedThresholdSource = "model recommendation"
	ThresholdSourceDefault         ResolvedThresholdSource = "default"
)

// ResolveSimilarityThresholdFull returns the active minimum cosine similarity
// for semantic search along with the provenance of the chosen value:
//
//  1. Vault config (ai.similarity_threshold > 0)     → ThresholdSourceVaultConfig
//  2. User catalog (global or per-vault models.yaml) → ThresholdSourceUserCalibration
//  3. Builtin catalog recommendation                  → ThresholdSourceModel
//  4. DefaultSimilarityThreshold                      → ThresholdSourceDefault
//
// Pass vaultRoot="" when there's no open vault; the user-catalog layer is
// skipped in that case.
func (c AIConfig) ResolveSimilarityThresholdFull(vaultRoot string) (float64, ResolvedThresholdSource) {
	if c.SimilarityThreshold > 0 {
		return c.SimilarityThreshold, ThresholdSourceVaultConfig
	}
	if vaultRoot != "" {
		if t := userCatalogSimilarityThreshold(vaultRoot, c.Provider, c.EmbeddingModel); t > 0 {
			return t, ThresholdSourceUserCalibration
		}
	}
	if t := RecommendedSimilarityThresholdFor(c.Provider, c.EmbeddingModel); t > 0 {
		return t, ThresholdSourceModel
	}
	return DefaultSimilarityThreshold, ThresholdSourceDefault
}

// RecommendedSimilarityThresholdFor returns the builtin catalog's recommended
// threshold for (provider, modelID). Returns 0 when the model isn't in the
// catalog or has no recommendation. User-added catalog entries are NOT
// consulted here — use ResolveSimilarityThresholdFull for that.
func RecommendedSimilarityThresholdFor(provider, modelID string) float64 {
	if provider == "" || modelID == "" {
		return 0
	}
	for _, m := range BuiltinCatalog() {
		if m.Type == "embedding" && m.Provider == provider && m.ID == modelID {
			return m.RecommendedSimilarityThreshold
		}
	}
	return 0
}

// userCatalogSimilarityThreshold scans the user catalog (global + per-vault)
// for an entry matching the active embedding model. Zero means "not set by
// the user" — callers fall through to the builtin catalog.
func userCatalogSimilarityThreshold(vaultRoot, provider, modelID string) float64 {
	if provider == "" || modelID == "" {
		return 0
	}
	for _, m := range LoadUserCatalog(vaultRoot) {
		if m.Type == "embedding" && m.Provider == provider && m.ID == modelID && m.RecommendedSimilarityThreshold > 0 {
			return m.RecommendedSimilarityThreshold
		}
	}
	return 0
}

// EmbeddingDimensionsFor returns the declared output dimension for the
// embedding model (provider, modelID) from the merged catalog — the user
// catalog (global + per-vault) overlaying the builtin catalog. Returns 0 when
// the model isn't in the catalog or declares no dimension, in which case
// callers should leave ai.dimensions untouched rather than guess. Pass
// vaultRoot="" to consult only the builtin catalog.
func EmbeddingDimensionsFor(vaultRoot, provider, modelID string) int {
	if provider == "" || modelID == "" {
		return 0
	}
	if vaultRoot != "" {
		for _, m := range LoadUserCatalog(vaultRoot) {
			if m.Type == "embedding" && m.Provider == provider && m.ID == modelID && m.Dimensions > 0 {
				return m.Dimensions
			}
		}
	}
	for _, m := range BuiltinCatalog() {
		if m.Type == "embedding" && m.Provider == provider && m.ID == modelID && m.Dimensions > 0 {
			return m.Dimensions
		}
	}
	return 0
}

// SupportedDimensionsFor returns the Matryoshka output widths a model can emit
// (provider, modelID), from the merged catalog — the user catalog overlaying
// the builtin. Returns nil when the model declares none, which callers treat as
// "no constraint" (don't validate). Pass vaultRoot="" for builtin only. Used to
// reject an `ai.dimensions` value the provider would error on at embed time.
func SupportedDimensionsFor(vaultRoot, provider, modelID string) []int {
	if provider == "" || modelID == "" {
		return nil
	}
	if vaultRoot != "" {
		for _, m := range LoadUserCatalog(vaultRoot) {
			if m.Type == "embedding" && m.Provider == provider && m.ID == modelID && len(m.SupportedDimensions) > 0 {
				return m.SupportedDimensions
			}
		}
	}
	for _, m := range BuiltinCatalog() {
		if m.Type == "embedding" && m.Provider == provider && m.ID == modelID && len(m.SupportedDimensions) > 0 {
			return m.SupportedDimensions
		}
	}
	return nil
}

// catalogProviderFor returns the provider a model of the given type is
// registered under in the merged catalog, when the model ID is known. The
// found result is false when the ID appears in no catalog (e.g. a user's
// freshly discovered model) — callers must not treat "unknown" as "wrong".
func catalogProviderFor(vaultRoot, modelType, modelID string) (string, bool) {
	if modelID == "" {
		return "", false
	}
	if vaultRoot != "" {
		for _, m := range LoadUserCatalog(vaultRoot) {
			if m.Type == modelType && m.ID == modelID {
				return m.Provider, true
			}
		}
	}
	for _, m := range BuiltinCatalog() {
		if m.Type == modelType && m.ID == modelID {
			return m.Provider, true
		}
	}
	return "", false
}

// Validate reports internal-consistency problems with the active AI selection
// that would silently break semantic search or generation. It is advisory:
// callers (config set, and a future config doctor / ai status) surface the
// issues, but the config is still saved — a model the catalog doesn't know
// (a user's own discovered model) is legitimate and must not be blocked.
//
// Today it catches the orphaned-slot bug: 2nb resolves both the embedder and
// the generator from the single ai.provider, so an embedding or generation
// model the catalog registers under a DIFFERENT provider can never be served,
// and search/generation silently dies. Returns nil when consistent. Pass
// vaultRoot="" to validate against the builtin catalog only.
func (c AIConfig) Validate(vaultRoot string) []string {
	var issues []string
	check := func(slot, modelID string) {
		p, ok := catalogProviderFor(vaultRoot, slot, modelID)
		if ok && p != c.Provider {
			issues = append(issues, fmt.Sprintf(
				"%s model %q belongs to provider %q but ai.provider is %q, which cannot serve it; switch the %s model to a %q model, or set ai.provider to %q.",
				slot, modelID, p, c.Provider, slot, c.Provider, p))
		}
	}
	check("embedding", c.EmbeddingModel)
	check("generation", c.GenerationModel)
	return issues
}
