package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddingDimensionsFor(t *testing.T) {
	def := DefaultAIConfig()

	// The default embedding model is in the builtin catalog with its dimension.
	if got := EmbeddingDimensionsFor("", def.Provider, def.EmbeddingModel); got != def.Dimensions {
		t.Errorf("EmbeddingDimensionsFor(default embed) = %d, want %d", got, def.Dimensions)
	}
	// An unknown model declares no dimension; callers must leave dims untouched.
	if got := EmbeddingDimensionsFor("", "bedrock", "does-not-exist"); got != 0 {
		t.Errorf("EmbeddingDimensionsFor(unknown) = %d, want 0", got)
	}
	// Empty provider/model is always 0.
	if got := EmbeddingDimensionsFor("", "", ""); got != 0 {
		t.Errorf("EmbeddingDimensionsFor(empty) = %d, want 0", got)
	}
}

func TestEmbeddingDimensionsFor_UserCatalogOverride(t *testing.T) {
	// Isolate the global catalog so this test can't read the developer's real
	// ~/.config/2nb/models.yaml (the vault-scoped entry below is what matters).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".2ndbrain"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A user-catalog embedding entry supplies its own dimension; the
	// vaultRoot != "" branch of EmbeddingDimensionsFor / catalogProviderFor
	// must consult it (the builtin-only path is covered above).
	const customID = "custom-embed-v1"
	if err := SaveUserCatalogEntry(ScopeVault, root, ModelInfo{
		ID: customID, Provider: "bedrock", Type: "embedding", Dimensions: 384,
	}); err != nil {
		t.Fatalf("SaveUserCatalogEntry: %v", err)
	}

	if got := EmbeddingDimensionsFor(root, "bedrock", customID); got != 384 {
		t.Errorf("EmbeddingDimensionsFor(user entry) = %d, want 384 (user-catalog branch not consulted)", got)
	}
	if p, ok := catalogProviderFor(root, "embedding", customID); !ok || p != "bedrock" {
		t.Errorf("catalogProviderFor(user entry) = (%q, %v), want (bedrock, true)", p, ok)
	}
}

func TestAIConfig_Validate(t *testing.T) {
	// A consistent default config (bedrock provider, bedrock models) is clean.
	if issues := DefaultAIConfig().Validate(""); len(issues) != 0 {
		t.Errorf("default config Validate() = %v, want no issues", issues)
	}

	// Orphaned slot: provider switched to openrouter while the embedding model
	// still belongs to bedrock — the exact GUI "Set Active" / generation-switch
	// bug that silently kills semantic search.
	cfg := DefaultAIConfig()
	cfg.Provider = "openrouter"
	issues := cfg.Validate("")
	if len(issues) == 0 {
		t.Fatal("Validate() with mismatched embedding provider = no issues, want at least one")
	}
	if joined := strings.Join(issues, " "); !strings.Contains(joined, "embedding") {
		t.Errorf("Validate() issues = %v, want one mentioning the embedding slot", issues)
	}

	// Unknown models (in no catalog) must NOT be flagged — they may be a user's
	// legitimate discovered models. "Unknown" is not "wrong".
	cfg2 := DefaultAIConfig()
	cfg2.EmbeddingModel = "totally-custom-model"
	cfg2.GenerationModel = "totally-custom-gen"
	if issues := cfg2.Validate(""); len(issues) != 0 {
		t.Errorf("Validate() with unknown models = %v, want no issues (unknown != wrong)", issues)
	}
}

func TestSetProviderDisabled(t *testing.T) {
	cfg := DefaultAIConfig() // bedrock enabled; ollama + openrouter ship disabled
	cfg.SetProviderDisabled("ollama", false)
	if cfg.Ollama.Disabled {
		t.Error("SetProviderDisabled(ollama, false) did not clear the flag")
	}
	cfg.SetProviderDisabled("bedrock", true)
	if !cfg.Bedrock.Disabled {
		t.Error("SetProviderDisabled(bedrock, true) did not set the flag")
	}
	// Unknown provider is a no-op (must not panic).
	cfg.SetProviderDisabled("nope", true)
}

func TestIsKnownProvider(t *testing.T) {
	for _, p := range KnownProviders {
		if !IsKnownProvider(p) {
			t.Errorf("IsKnownProvider(%q) = false, want true", p)
		}
	}
	if IsKnownProvider("bedrok") {
		t.Error("IsKnownProvider(\"bedrok\" typo) = true, want false")
	}
}
