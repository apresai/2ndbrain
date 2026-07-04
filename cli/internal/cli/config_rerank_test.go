package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestConfigSet_Rerank round-trips the ai.rerank.* keys and checks the
// candidate_docs bounds (0..100, the Bedrock per-query cap).
func TestConfigSet_Rerank(t *testing.T) {
	cfg := ai.DefaultAIConfig()

	// Default: rerank off, resolves to the built-in model + candidate pool.
	if cfg.RerankEnabled() {
		t.Error("rerank should default to off")
	}
	if got := cfg.ResolveRerankModel(); got != ai.DefaultRerankModel {
		t.Errorf("default model = %q, want %q", got, ai.DefaultRerankModel)
	}
	if got := cfg.ResolveRerankCandidateDocs(); got != ai.DefaultRerankCandidateDocs {
		t.Errorf("default candidate_docs = %d, want %d", got, ai.DefaultRerankCandidateDocs)
	}

	if err := setConfigValue(&cfg, "ai.rerank.enabled", "true"); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if !cfg.Rerank.Enabled {
		t.Error("enabled not set")
	}
	if err := setConfigValue(&cfg, "ai.rerank.model", "cohere.rerank-v3-5:0"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	if cfg.Rerank.Model != "cohere.rerank-v3-5:0" {
		t.Errorf("model = %q", cfg.Rerank.Model)
	}
	if err := setConfigValue(&cfg, "ai.rerank.candidate_docs", "40"); err != nil {
		t.Fatalf("set candidate_docs: %v", err)
	}
	if cfg.Rerank.CandidateDocs != 40 {
		t.Errorf("candidate_docs = %d, want 40", cfg.Rerank.CandidateDocs)
	}

	// Over the Bedrock 100-doc/query cap is rejected.
	if err := setConfigValue(&cfg, "ai.rerank.candidate_docs", "200"); err == nil {
		t.Error("candidate_docs=200 should be rejected (>100 cap)")
	}
	// Non-numeric rejected.
	if err := setConfigValue(&cfg, "ai.rerank.candidate_docs", "lots"); err == nil {
		t.Error("candidate_docs=lots should be rejected")
	}

	// Getters round-trip.
	if got, _ := getConfigValue(cfg, "ai.rerank.enabled"); got != "true" {
		t.Errorf("get enabled = %q", got)
	}
	if got, _ := getConfigValue(cfg, "ai.rerank.candidate_docs"); got != "40" {
		t.Errorf("get candidate_docs = %q", got)
	}
}
