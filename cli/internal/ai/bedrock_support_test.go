package ai

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestBedrockPreflightModel_MantleBypass verifies a model whose resolved
// strategy is bedrock_mantle_responses skips BOTH preflight checks: the
// static allowlist (which can't know mantle IDs) and the GetFoundationModel
// lifecycle lookup (mantle models are invisible to the classic control plane
// and would 404). No credentials and no network: the bypass must return
// before any AWS client is built, which the short deadline enforces.
func TestBedrockPreflightModel_MantleBypass(t *testing.T) {
	setupHome(t)

	// Non-builtin ID so the bypass is proven to come from the user-catalog
	// entry, not from the builtin gpt-5.5/grok-4.3 entries.
	entry := ModelInfo{
		ID:             "acme.frontier-1",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         "us-east-2",
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "acme.frontier-1", "generation", ""); err != nil {
		t.Errorf("mantle model should bypass preflight, got %v", err)
	}

	// Builtin mantle entries bypass with no user-catalog entry at all.
	if err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "openai.gpt-5.5", "generation", ""); err != nil {
		t.Errorf("builtin mantle model should bypass preflight, got %v", err)
	}

	// Contrast: without a mantle strategy the same unknown ID still fails the
	// static allowlist (deterministically, before any network call).
	err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "openai.gpt-5.5-no-catalog-entry", "generation", "")
	var incompatible *IncompatibleModelError
	if !errors.As(err, &incompatible) {
		t.Errorf("non-mantle unknown model should fail the static allowlist, got %v", err)
	}
}

// TestBedrockPreflightModel_VaultScopedMantleBypass is the regression test
// for the PR #178 carryover: the preflight used to resolve the invoke
// strategy with vaultRoot "", so a VAULT-scoped mantle entry was not bypassed
// and hit the static allowlist. The vault root must be threaded through.
func TestBedrockPreflightModel_VaultScopedMantleBypass(t *testing.T) {
	setupHome(t)
	vaultRoot := t.TempDir()

	entry := ModelInfo{
		ID:             "acme.vault-frontier-1",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         "us-west-2",
	}
	if err := SaveUserCatalogEntry(ScopeVault, vaultRoot, entry); err != nil {
		t.Fatalf("save vault entry: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "acme.vault-frontier-1", "generation", vaultRoot); err != nil {
		t.Errorf("vault-scoped mantle entry should bypass preflight, got %v", err)
	}

	// Without the vault root the entry is invisible and the allowlist refuses
	// the unknown ID — the exact pre-fix failure mode.
	err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "acme.vault-frontier-1", "generation", "")
	var incompatible *IncompatibleModelError
	if !errors.As(err, &incompatible) {
		t.Errorf("without the vault root the entry should hit the static allowlist, got %v", err)
	}
}

func TestBedrockContextLenHint(t *testing.T) {
	tests := []struct {
		id   string
		want int
	}{
		// Anthropic current line (inference-profile IDs strip the geo prefix).
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", 200_000},
		{"anthropic.claude-3-haiku-20240307-v1:0", 200_000},
		{"us.anthropic.claude-sonnet-4-6", 1_000_000},
		{"global.anthropic.claude-sonnet-5", 1_000_000},
		{"us.anthropic.claude-opus-4-8", 1_000_000},
		{"us.anthropic.claude-fable-5", 1_000_000},
		// Pre-4.6 versions stay 200K, including date-suffixed IDs.
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", 200_000},
		{"us.anthropic.claude-sonnet-4-20250514-v1:0", 200_000},
		{"us.anthropic.claude-opus-4-1-20250805-v1:0", 200_000},
		{"us.anthropic.claude-opus-4-5-20251101-v1:0", 200_000},
		// Amazon + others.
		{"amazon.nova-micro-v1:0", 128_000},
		{"amazon.nova-lite-v1:0", 300_000},
		{"amazon.nova-pro-v1:0", 300_000},
		{"amazon.nova-premier-v1:0", 1_000_000},
		{"amazon.titan-embed-text-v2:0", 8192},
		{"amazon.nova-2-multimodal-embeddings-v1:0", 8192},
		{"cohere.embed-english-v3", 512},
		{"meta.llama4-scout-17b-instruct-v1:0", 128_000},
		{"us.meta.llama3-3-70b-instruct-v1:0", 128_000},
		{"mistral.pixtral-large-2502-v1:0", 128_000},
		{"mistral.mistral-large-2407-v1:0", 128_000},
		// Unknown families stay honest.
		{"ai21.jamba-1-5-large-v1:0", 0},
		{"deepseek.v3.2", 0},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := bedrockContextLenHint(tt.id); got != tt.want {
				t.Errorf("bedrockContextLenHint(%q) = %d, want %d", tt.id, got, tt.want)
			}
		})
	}
}

// TestBedrockContextLenHint_AgreesWithBuiltinCatalog pins the hint map to the
// curated catalog: every builtin bedrock entry with a declared context length
// must get the same value from the discovery hint, so a user sees consistent
// numbers whether a model arrived via the catalog or via discovery.
func TestBedrockContextLenHint_AgreesWithBuiltinCatalog(t *testing.T) {
	for _, m := range BuiltinCatalog() {
		if m.Provider != "bedrock" || m.ContextLen == 0 || m.Type == "rerank" {
			continue
		}
		if hint := bedrockContextLenHint(m.ID); hint != 0 && hint != m.ContextLen {
			t.Errorf("hint for %s = %d disagrees with builtin catalog %d", m.ID, hint, m.ContextLen)
		}
	}
}
