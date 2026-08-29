package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// initAIProviders registers AI providers based on vault config.
// Idempotent — skips if the provider is already registered.
func initAIProviders(v *vault.Vault) {
	initAIProvidersFor(v.Config.AI, v.Root)
}

// initAIProvidersFor is initAIProviders over a bare config + vault root, so a
// caller that has no open vault can still register providers. `2nb doctor`'s
// credential and model tier depends on this: it must probe the provider
// without vault.Open's side effects (creating .2ndbrain/, appending to
// .gitignore, creating index.db). vaultRoot may be "" — it only scopes
// user-catalog lookups.
// reportUnroutedSlot prints an unrouted-slot refusal and reports whether it
// handled the error.
//
// This one bypasses --porcelain on purpose, unlike every other init warning.
// The others are environmental (no credentials, provider unreachable) and
// degrading quietly to keyword search is the right answer for them. An
// unrouted slot is a CONFIGURATION defect with a copy-paste fix, and
// suppressing it leaves the user with nothing but a downstream "generation
// provider bedrock not registered" — the same cause-free failure this whole
// change exists to remove. Worse, no provider registers at all (the embedding
// slot resolves first and InitBedrock returns immediately), so `ask` dies and
// `search` silently drops to BM25 while `doctor` stays green, because doctor
// probes models directly rather than through InitBedrock.
func reportUnroutedSlot(err error) bool {
	var unrouted *ai.UnroutedSlotError
	if !errors.As(err, &unrouted) {
		return false
	}
	fmt.Fprintf(os.Stderr, "error: %v\n\nAI is unavailable until this is set; search falls back to keyword-only.\n", unrouted)
	return true
}

func initAIProvidersFor(cfg ai.AIConfig, vaultRoot string) {
	// Skip if already registered (safe for repeated calls in MCP server)
	if _, err := ai.DefaultRegistry.Embedder(cfg.Provider); err == nil {
		return
	}

	ctx := context.Background()

	switch cfg.Provider {
	case "bedrock":
		if err := ai.InitBedrock(ctx, ai.DefaultRegistry, cfg.Bedrock, cfg, vaultRoot); err != nil {
			if !reportUnroutedSlot(err) && !flagPorcelain {
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
	case "llama-local":
		if err := ai.InitLlama(ctx, ai.DefaultRegistry, cfg.Llama, cfg); err != nil {
			if !flagPorcelain {
				fmt.Fprintf(os.Stderr, "warning: llama-local init: %v\n", err)
			}
		}
	}
}
