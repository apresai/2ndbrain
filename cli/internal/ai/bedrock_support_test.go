package ai

import (
	"context"
	"errors"
	"strings"
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

	// Contrast: without a mantle strategy, a model the static gate actually
	// denies (an image-generation model, not merely an unknown vendor — the
	// generation catch-all is default-allow since this PR) still fails
	// deterministically, before any network call.
	err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "amazon.nova-canvas-no-catalog-entry", "generation", "")
	var incompatible *IncompatibleModelError
	if !errors.As(err, &incompatible) {
		t.Errorf("non-mantle image-generation model should fail the static gate, got %v", err)
	}
}

// TestBedrockPreflightModel_VaultScopedMantleBypass is the regression test
// for the PR #178 carryover: the preflight used to resolve the invoke
// strategy with vaultRoot "", so a VAULT-scoped mantle entry was not bypassed
// and hit the static allowlist. The vault root must be threaded through.
func TestBedrockPreflightModel_VaultScopedMantleBypass(t *testing.T) {
	setupHome(t)
	vaultRoot := t.TempDir()

	// ID carries an "amazon.nova-canvas" prefix so that, absent the mantle
	// catalog entry, it is genuinely denied by the static gate's
	// image-generation deny arm rather than merely being an unrecognized
	// vendor (which the default-allow catch-all now admits since this PR).
	entry := ModelInfo{
		ID:             "amazon.nova-canvas-vault-frontier-1",
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
	if err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "amazon.nova-canvas-vault-frontier-1", "generation", vaultRoot); err != nil {
		t.Errorf("vault-scoped mantle entry should bypass preflight, got %v", err)
	}

	// Without the vault root the entry is invisible and the static gate's
	// image-generation deny arm refuses it — the exact pre-fix failure mode.
	err := BedrockPreflightModel(ctx, BedrockConfig{Region: "us-east-1"}, "amazon.nova-canvas-vault-frontier-1", "generation", "")
	var incompatible *IncompatibleModelError
	if !errors.As(err, &incompatible) {
		t.Errorf("without the vault root the entry should hit the static gate's deny arm, got %v", err)
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

// Grok 4.6 (2026-08-19) is the first xAI model on the classic Converse
// plane; earlier Groks were mantle-only and bypass this gate entirely via
// their invoke strategy. The us./global. profile forms and the bare id must
// all pass. An unknown vendor is ALSO admitted now — the generation
// catch-all defaults to allow (TestBedrockModelSupported_GenerationDefaultAllow
// below pins that behavior directly) — so this test no longer asserts a
// refusal for it.
func TestBedrockModelSupported_XAIGrokConverse(t *testing.T) {
	for _, id := range []string{"us.xai.grok-4.6", "global.xai.grok-4.6", "xai.grok-4.6"} {
		if ok, why := bedrockModelSupported(id, "generation"); !ok {
			t.Errorf("%s should be Converse-supported, got refused: %s", id, why)
		}
	}
	if ok, why := bedrockModelSupported("unknownvendor.some-model", "generation"); !ok {
		t.Errorf("unknown vendor should now be admitted by the default-allow catch-all, got refused: %s", why)
	}
}

// TestBedrockModelSupported_GenerationDefaultAllow pins the classic-plane
// generation gate's post-widening shape (this PR): the five conceptual deny
// categories (image-generation, video-generation, rerank-as-generation,
// video-understanding, Palmyra Vision) still refuse, and everything else —
// including vendor families 2nb has never invoked — is admitted by default
// with no per-vendor allowlist entry required. This is the regression test
// for the behavioral widening: a future new vendor must NOT need a code
// change here to become discoverable and probeable.
func TestBedrockModelSupported_GenerationDefaultAllow(t *testing.T) {
	denyCases := []struct {
		modelID string
		wantSub string
	}{
		{"amazon.nova-canvas-v1:0", "image-generation"},
		{"amazon.nova-reel-v1:0", "video-generation"},
		{"stability.stable-image-core-v1:0", "image-generation"},
		{"amazon.titan-image-generator-v1", "image-generation"},
		{"cohere.rerank-v3-5:0", "reranker"},
		{"us.twelvelabs.pegasus-1-2-v1:0", "video-understanding"},
		{"twelvelabs.pegasus-1:0", "video-understanding"},
		{"writer.palmyra-vision-7b", "Palmyra Vision"},
	}
	for _, tc := range denyCases {
		t.Run("deny/"+tc.modelID, func(t *testing.T) {
			ok, reason := bedrockModelSupported(tc.modelID, "generation")
			if ok {
				t.Fatalf("bedrockModelSupported(%q, generation) = true, want denied (%s)", tc.modelID, tc.wantSub)
			}
			if !strings.Contains(reason, tc.wantSub) {
				t.Fatalf("bedrockModelSupported(%q, generation) reason = %q, want substring %q", tc.modelID, reason, tc.wantSub)
			}
		})
	}

	// Vendors 2nb has always known about, and vendors it has never seen,
	// must both pass now with no distinction between them.
	allowIDs := []string{
		"anthropic.claude-3-5-haiku-20241022-v1:0",
		"amazon.nova-micro-v1:0",
		"meta.llama3-8b-instruct-v1:0",
		"mistral.mistral-7b-instruct-v0:2",
		"deepseek.v3.2",
		"xai.grok-4.6",
		// Vendor families formerly covered by an explicit allow arm — the
		// widened catch-all admits them with no per-vendor logic left.
		"qwen.qwen3-235b-a22b-v1:0",
		"zai.glm-4-6-v1:0",
		"moonshotai.kimi-k2-v1:0",
		"minimax.m2-v1:0",
		"nvidia.nemotron-super-49b-v1:0",
		"google.gemma-3-27b-it-v1:0",
		"openai.gpt-oss-120b-v1:0",
		// A vendor 2nb has genuinely never invoked or added a case for.
		"unknownvendor.some-model",
		"acme.brand-new-frontier-model-v7",
	}
	for _, id := range allowIDs {
		t.Run("allow/"+id, func(t *testing.T) {
			if ok, why := bedrockModelSupported(id, "generation"); !ok {
				t.Errorf("bedrockModelSupported(%q, generation) should be admitted by the default-allow catch-all, got refused: %s", id, why)
			}
		})
	}
}

// TestBedrockModelSupported_EmbeddingRerankStillAllowlisted pins that ONLY
// the classic-plane generation gate widened in this PR — embedding and
// rerank keep their explicit per-family allowlists (each vendor's InvokeModel
// body shape is genuinely different, so "unknown" cannot default to
// "compatible" the way it can for the uniform Converse dialect).
func TestBedrockModelSupported_EmbeddingRerankStillAllowlisted(t *testing.T) {
	if ok, reason := bedrockModelSupported("unknownvendor.some-embedder", "embedding"); ok {
		t.Error("unknown embedding vendor should still be refused")
	} else if !strings.Contains(reason, "2nb doesn't support this Bedrock embedding invoke format yet") {
		t.Errorf("embedding catch-all reason changed: %q (pinned by JSONDecodingTests.swift:445, do not alter)", reason)
	}
	if ok, _ := bedrockModelSupported("unknownvendor.some-reranker", "rerank"); ok {
		t.Error("unknown rerank vendor should still be refused")
	}
}

func TestBuiltinCatalogCarriesGrok46Classic(t *testing.T) {
	for _, m := range BuiltinCatalog() {
		if m.ID == "us.xai.grok-4.6" {
			if m.Provider != "bedrock" || m.Type != "generation" {
				t.Fatalf("wrong shape: %+v", m)
			}
			if m.ContextLen != 500000 {
				t.Errorf("context = %d, want 500000 (model card)", m.ContextLen)
			}
			if m.PriceIn != 2.20 || m.PriceOut != 6.60 {
				t.Errorf("pricing = %v/%v, want 2.20/6.60 (geo, model card 2026-08-20)", m.PriceIn, m.PriceOut)
			}
			return
		}
	}
	t.Fatal("us.xai.grok-4.6 missing from the builtin catalog")
}
