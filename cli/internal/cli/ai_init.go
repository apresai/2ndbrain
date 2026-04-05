package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// initAIProviders registers AI providers based on vault config.
// Idempotent — skips if the provider is already registered.
func initAIProviders(v *vault.Vault) {
	cfg := v.Config.AI

	// Skip if already registered (safe for repeated calls in MCP server)
	if _, err := ai.DefaultRegistry.Embedder(cfg.Provider); err == nil {
		return
	}

	ctx := context.Background()

	switch cfg.Provider {
	case "bedrock":
		if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg); err != nil {
			if !flagPorcelain {
				fmt.Fprintf(os.Stderr, "warning: bedrock init: %v\n", err)
			}
		}
	case "ollama":
		if err := ai.InitOllama(ctx, ai.DefaultRegistry, cfg.Ollama, cfg); err != nil {
			if !flagPorcelain {
				fmt.Fprintf(os.Stderr, "warning: ollama init: %v\n", err)
			}
		}
	case "openrouter":
		if err := ai.InitOpenRouter(ctx, ai.DefaultRegistry, cfg.OpenRouter, cfg); err != nil {
			if !flagPorcelain {
				fmt.Fprintf(os.Stderr, "warning: openrouter init: %v\n", err)
			}
		}
	}
}
