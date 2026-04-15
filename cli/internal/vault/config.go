package vault

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apresai/2ndbrain/internal/ai"
	"gopkg.in/yaml.v3"
)

type VaultConfig struct {
	Name    string          `yaml:"name" json:"name"`
	Version string          `yaml:"version" json:"version"`
	Embed   EmbeddingConfig `yaml:"embedding" json:"embedding"`
	AI      ai.AIConfig     `yaml:"ai,omitempty" json:"ai,omitempty"`

	// Recovered is set transiently when LoadConfig had to regenerate
	// the config from defaults (missing file or corrupt YAML). Not
	// serialized — callers (vault.Open) check it to log a stderr
	// warning once on open so the user knows the vault self-healed.
	// Values: "", "config_missing", "config_corrupt_backup".
	Recovered string `yaml:"-" json:"-"`
}

type EmbeddingConfig struct {
	Model      string `yaml:"model" json:"model"`
	Dimensions int    `yaml:"dimensions" json:"dimensions"`
	BatchSize  int    `yaml:"batch_size" json:"batch_size"`
}

func DefaultConfig(name string) *VaultConfig {
	return &VaultConfig{
		Name:    name,
		Version: "1",
		Embed: EmbeddingConfig{
			Model:      "nomic-embed-text-v1.5.Q8_0.gguf",
			Dimensions: 768,
			BatchSize:  100,
		},
		AI: ai.DefaultAIConfig(),
	}
}

func LoadConfig(dotDir string) (*VaultConfig, error) {
	path := filepath.Join(dotDir, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Config file missing — don't brick the vault, regenerate
			// defaults and mark the cfg as Recovered so the caller can
			// surface a one-line stderr warning. The DB and markdown
			// files remain authoritative for actual vault content.
			cfg := DefaultConfig(filepath.Base(filepath.Dir(dotDir)))
			if saveErr := cfg.Save(dotDir); saveErr == nil {
				cfg.Recovered = "config_missing"
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg VaultConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		// Corrupt YAML — back the broken file up with a .bak suffix
		// so the user can recover it manually, then write fresh
		// defaults. Same self-healing spirit as the missing-file case.
		_ = os.Rename(path, path+".bak")
		recovered := DefaultConfig(filepath.Base(filepath.Dir(dotDir)))
		if saveErr := recovered.Save(dotDir); saveErr == nil {
			recovered.Recovered = "config_corrupt_backup"
		}
		return recovered, nil
	}

	// Backfill AI config defaults for vaults created before AI support
	if cfg.AI.Provider == "" {
		cfg.AI = ai.DefaultAIConfig()
	}

	return &cfg, nil
}

func (c *VaultConfig) Save(dotDir string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(filepath.Join(dotDir, "config.yaml"), data, 0o644)
}
