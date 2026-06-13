package cli

import (
	"context"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
	"github.com/apresai/2ndbrain/internal/vault"
)

// TestMCPServer_InitsAIProviders is the regression guard for the fix where the
// MCP server never called initAIProviders, so kb_create / kb_append /
// kb_replace_section / kb_index could not embed inline (the embedder lookup
// failed and every embed silently skipped, leaving agent-authored docs without
// a vector embedding until a later `2nb index`).
//
// It mirrors exactly what runMCPServer now does on startup: open the vault and
// call initAIProviders. After that, the embedder for the configured provider
// must resolve. Gated on real Bedrock credentials (no-mock policy; skips
// without them, since registration calls the provider's init path).
func TestMCPServer_InitsAIProviders(t *testing.T) {
	ctx := context.Background()
	if !ai.CheckBedrockCredentials(ctx, ai.DefaultAIConfig().Bedrock) {
		t.Skip("AWS credentials not configured for Bedrock")
	}

	_, root := newContractVault(t)
	v, err := vault.Open(root)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer v.Close()

	// This is the startup sequence runMCPServer performs (the line this test
	// guards). Without it, the embedder below would be unavailable.
	initAIProviders(v)

	if _, err := ai.DefaultRegistry.Embedder(v.Config.AI.Provider); err != nil {
		t.Fatalf("embedder for provider %q unavailable after MCP server init: %v\n"+
			"this means MCP write/index tools cannot embed inline", v.Config.AI.Provider, err)
	}
}
