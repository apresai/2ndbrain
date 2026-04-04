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
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg VaultConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
