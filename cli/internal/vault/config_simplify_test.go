package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultConfig_NoLegacyEmbeddingBlock asserts the dead `embedding:` block
// is gone and the AI defaults (Bedrock; Ollama/OpenRouter opt-in) are present.
func TestDefaultConfig_NoLegacyEmbeddingBlock(t *testing.T) {
	dir := t.TempDir()
	if err := DefaultConfig("myvault").Save(dir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "embedding:") {
		t.Errorf("config.yaml still writes a legacy embedding block:\n%s", data)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AI.Provider != "bedrock" {
		t.Errorf("default provider = %q, want bedrock", cfg.AI.Provider)
	}
	if !cfg.AI.Ollama.Disabled || !cfg.AI.OpenRouter.Disabled {
		t.Errorf("Ollama/OpenRouter should ship disabled (opt-in): ollama=%v openrouter=%v",
			cfg.AI.Ollama.Disabled, cfg.AI.OpenRouter.Disabled)
	}
	if cfg.AI.Bedrock.Disabled {
		t.Error("Bedrock should be enabled by default")
	}
}

// TestLoadConfig_IgnoresLegacyEmbeddingKey confirms a pre-existing config.yaml
// that still has the old `embedding:` block loads cleanly (no self-heal, no
// error) — yaml just ignores the now-unknown key.
func TestLoadConfig_IgnoresLegacyEmbeddingKey(t *testing.T) {
	dir := t.TempDir()
	legacy := "name: old\nversion: \"1\"\n" +
		"embedding:\n  model: nomic-embed-text\n  dimensions: 768\n  batch_size: 100\n" +
		"ai:\n  provider: bedrock\n  embedding_model: amazon.nova-2-multimodal-embeddings-v1:0\n" +
		"  generation_model: us.anthropic.claude-haiku-4-5-20251001-v1:0\n  dimensions: 1024\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("a config with a legacy embedding block should load, not error: %v", err)
	}
	if cfg.AI.Provider != "bedrock" || cfg.AI.EmbeddingModel == "" {
		t.Errorf("AI config not loaded from a legacy file (did it self-heal away?): %+v", cfg.AI)
	}
}
