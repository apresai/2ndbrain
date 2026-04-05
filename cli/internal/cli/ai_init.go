package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// initAIProviders registers AI providers based on vault config.
// Called by commands that need embedding or generation.
func initAIProviders(v *vault.Vault) {
	cfg := v.Config.AI
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
