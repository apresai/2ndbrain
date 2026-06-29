package cli

import (
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestSetConfigValue_HybridWeights locks the hybrid-weight config guard: the
// RRF bm25/vector weights accept non-negative numbers and reject negatives and
// non-numbers (0 resolves to the 1.0 default downstream).
func TestSetConfigValue_HybridWeights(t *testing.T) {
	cfg := ai.AIConfig{Provider: "bedrock", EmbeddingModel: "amazon.nova-2-multimodal-embeddings-v1:0"}
	for _, k := range []string{"ai.bm25_weight", "ai.vector_weight"} {
		if err := setConfigValue(&cfg, k, "2.0"); err != nil {
			t.Errorf("setConfigValue(%s, 2.0) = %v, want nil", k, err)
		}
		if err := setConfigValue(&cfg, k, "0"); err != nil {
			t.Errorf("setConfigValue(%s, 0) = %v, want nil (0 = default)", k, err)
		}
		if err := setConfigValue(&cfg, k, "-1"); err == nil {
			t.Errorf("setConfigValue(%s, -1) = nil, want error (negative)", k)
		}
		if err := setConfigValue(&cfg, k, "abc"); err == nil {
			t.Errorf("setConfigValue(%s, abc) = nil, want error (non-number)", k)
		}
		// NaN/Inf parse without error but would scramble RRF ranking — reject them.
		for _, bad := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
			if err := setConfigValue(&cfg, k, bad); err == nil {
				t.Errorf("setConfigValue(%s, %s) = nil, want error (non-finite)", k, bad)
			}
		}
	}
}

// TestSetConfigValue_RAGBudget locks the RAG context-budget guard: non-negative
// rune counts up to 400000 are accepted (0 resolves to the default), negatives /
// over-cap / non-ints are rejected.
func TestSetConfigValue_RAGBudget(t *testing.T) {
	cfg := ai.AIConfig{}
	for _, k := range []string{"ai.rag_context_budget", "ai.rag_note_budget"} {
		if err := setConfigValue(&cfg, k, "30000"); err != nil {
			t.Errorf("setConfigValue(%s, 30000) = %v, want nil", k, err)
		}
		if err := setConfigValue(&cfg, k, "0"); err != nil {
			t.Errorf("setConfigValue(%s, 0) = %v, want nil (0 = default)", k, err)
		}
		for _, bad := range []string{"-1", "999999", "abc", "1.5"} {
			if err := setConfigValue(&cfg, k, bad); err == nil {
				t.Errorf("setConfigValue(%s, %s) = nil, want error", k, bad)
			}
		}
	}
}
