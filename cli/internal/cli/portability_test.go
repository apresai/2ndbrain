package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestDerivePortability covers every branch of the vault portability state
// machine that backs `vault status` and `ai status`. Before this test,
// these state strings were only exercised end-to-end via search warnings;
// the rendering of the portability label itself had no coverage.
//
// The state strings are a public contract — the macOS editor banner and
// the SKILL.md error-recovery playbook both match on them.
func TestDerivePortability(t *testing.T) {
	embedder768 := &fakeEmbedder{name: "fake", dims: 768, available: true}
	embedderUnavail := &fakeEmbedder{name: "fake", dims: 768, available: false}

	tests := []struct {
		name         string
		cfg          ai.AIConfig
		embedder     ai.EmbeddingProvider
		vaultDim     int
		vaultModels  []string
		totalDocs    int
		embeddedDocs int
		// embeddableUnembedded = docs with content (a chunk) but no embedding.
		// Defaults to 0; cases that model unindexed/stale real content set it
		// explicitly so empty notes (totalDocs-embeddedDocs-embeddableUnembedded)
		// are not mistaken for work the user can fix with `2nb index`.
		embeddableUnembedded int
		wantStatus           string
		wantAction           string
	}{
		{
			name:       "empty_vault",
			totalDocs:  0,
			wantStatus: "empty_vault",
			wantAction: "Create documents",
		},
		{
			name:                 "unindexed_no_provider",
			cfg:                  ai.AIConfig{Provider: ""},
			totalDocs:            3,
			embeddableUnembedded: 3, // 3 real docs awaiting embeddings
			wantStatus:           "no_provider",
			wantAction:           "2nb ai setup",
		},
		{
			name:                 "unindexed_with_provider",
			cfg:                  ai.AIConfig{Provider: "bedrock"},
			totalDocs:            3,
			embeddableUnembedded: 3, // 3 real docs awaiting embeddings
			wantStatus:           "unindexed",
			wantAction:           "2nb index",
		},
		{
			name:         "provider_unreachable",
			cfg:          ai.AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			embedder:     embedderUnavail,
			vaultDim:     768,
			vaultModels:  []string{"nomic-embed-text"},
			totalDocs:    2,
			embeddedDocs: 2,
			wantStatus:   "provider_unavailable",
			wantAction:   "unreachable",
		},
		{
			name:         "dimension_break",
			cfg:          ai.AIConfig{Provider: "openrouter", EmbeddingModel: "large-model"},
			embedder:     embedder768,
			vaultDim:     1024,
			vaultModels:  []string{"large-model"},
			totalDocs:    2,
			embeddedDocs: 2,
			wantStatus:   "dimension_break",
			wantAction:   "--force-reembed",
		},
		{
			name:         "mixed_models",
			cfg:          ai.AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			embedder:     embedder768,
			vaultDim:     768,
			vaultModels:  []string{"nomic-embed-text", "all-minilm"},
			totalDocs:    2,
			embeddedDocs: 2,
			wantStatus:   "mixed",
			wantAction:   "--force-reembed",
		},
		{
			name:         "model_mismatch_same_dim",
			cfg:          ai.AIConfig{Provider: "ollama", EmbeddingModel: "bge-m3"},
			embedder:     embedder768,
			vaultDim:     768,
			vaultModels:  []string{"nomic-embed-text"},
			totalDocs:    2,
			embeddedDocs: 2,
			wantStatus:   "model_mismatch",
			wantAction:   "bge-m3",
		},
		{
			name:                 "stale_partial_embed",
			cfg:                  ai.AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			embedder:             embedder768,
			vaultDim:             768,
			vaultModels:          []string{"nomic-embed-text"},
			totalDocs:            5,
			embeddedDocs:         3,
			embeddableUnembedded: 2, // 2 docs with content still need embeddings
			wantStatus:           "stale",
			wantAction:           "2nb index",
		},
		{
			// The reported bug: every doc with content is embedded; the gap is
			// empty notes (no chunk) the embed pass skips. Must NOT be "stale"
			// with a no-op `2nb index` hint — that nags forever. (115/117 ->
			// 2 empty notes.)
			name:                 "ok_with_skipped_empty_notes",
			cfg:                  ai.AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			embedder:             embedder768,
			vaultDim:             768,
			vaultModels:          []string{"nomic-embed-text"},
			totalDocs:            117,
			embeddedDocs:         115,
			embeddableUnembedded: 0,
			wantStatus:           "ok",
			wantAction:           "empty notes skipped",
		},
		{
			// Both empties AND real content awaiting embeddings -> stale wins,
			// and the denominator counts only embeddable docs (5 of 8, not 5/10).
			name:                 "stale_counts_only_embeddable",
			cfg:                  ai.AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			embedder:             embedder768,
			vaultDim:             768,
			vaultModels:          []string{"nomic-embed-text"},
			totalDocs:            10,
			embeddedDocs:         5,
			embeddableUnembedded: 3, // 2 empty notes also present
			wantStatus:           "stale",
			wantAction:           "5 of 8 docs",
		},
		{
			// A vault of only empty notes (embeddedDocs==0, none embeddable) is
			// as embedded as it can be — "ok", not a dead-end "unindexed".
			name:                 "all_empty_notes",
			cfg:                  ai.AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			embedder:             embedder768,
			vaultDim:             768,
			totalDocs:            2,
			embeddedDocs:         0,
			embeddableUnembedded: 0,
			wantStatus:           "ok",
			wantAction:           "empty notes skipped",
		},
		{
			// All-empty vault but no provider configured: the onboarding nudge
			// wins over "ok" — semantic search isn't set up, so "fully embedded"
			// would mislead. (Guards the provider-first ordering in the
			// embeddedDocs==0 branch.)
			name:                 "all_empty_notes_no_provider",
			cfg:                  ai.AIConfig{Provider: ""},
			totalDocs:            2,
			embeddedDocs:         0,
			embeddableUnembedded: 0,
			wantStatus:           "no_provider",
			wantAction:           "2nb ai setup",
		},
		{
			// Happy path (ai_cmd.go:219) — fully healthy vault returns ("ok", "").
			// A regression that renames this label to "healthy" would silently
			// break the macOS app's portability banner, which matches on "ok".
			name:         "ok_happy_path",
			cfg:          ai.AIConfig{Provider: "ollama", EmbeddingModel: "nomic-embed-text"},
			embedder:     embedder768,
			vaultDim:     768,
			vaultModels:  []string{"nomic-embed-text"},
			totalDocs:    2,
			embeddedDocs: 2,
			wantStatus:   "ok",
			wantAction:   "",
		},
		{
			// Second no_provider branch (ai_cmd.go:197-199) — distinct from the
			// pre-embedding variant at line 192-194. This one triggers after
			// someone has indexed, then stripped the provider config.
			name:         "no_provider_with_embeddings",
			cfg:          ai.AIConfig{Provider: ""},
			embedder:     nil,
			vaultDim:     768,
			vaultModels:  []string{"nomic-embed-text"},
			totalDocs:    2,
			embeddedDocs: 2,
			wantStatus:   "no_provider",
			wantAction:   "2nb ai setup",
		},
		{
			// ai_cmd.go:200-201 — provider name configured but not registered
			// (can happen after a downgrade that dropped a provider build tag).
			name:         "embedder_nil",
			cfg:          ai.AIConfig{Provider: "bedrock", EmbeddingModel: "titan"},
			embedder:     nil,
			vaultDim:     768,
			vaultModels:  []string{"titan"},
			totalDocs:    2,
			embeddedDocs: 2,
			wantStatus:   "no_provider",
			wantAction:   "not registered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStatus, gotAction := derivePortability(context.Background(), tt.cfg, tt.embedder, tt.vaultDim, tt.vaultModels, tt.totalDocs, tt.embeddedDocs, tt.embeddableUnembedded)
			if gotStatus != tt.wantStatus {
				t.Errorf("status = %q, want %q", gotStatus, tt.wantStatus)
			}
			if tt.wantAction != "" && !strings.Contains(gotAction, tt.wantAction) {
				t.Errorf("action should contain %q, got %q", tt.wantAction, gotAction)
			}
		})
	}
}
