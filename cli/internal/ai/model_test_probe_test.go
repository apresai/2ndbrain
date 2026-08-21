package ai

import "testing"

func TestInferProvider(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "bedrock"},
		{"amazon.nova-2-multimodal-embeddings-v1:0", "bedrock"},
		{"eu.anthropic.claude-sonnet-4-20250514-v1:0", "bedrock"},
		{"openai.gpt-5.5", "bedrock"},
		{"xai.grok-4.3", "bedrock"},
		{"anthropic/claude-haiku-4-5", "openrouter"},
		{"google/gemma-4-31b-it:free", "openrouter"},
		{"openai/gpt-4o", "openrouter"},
		{"gemma3:4b", "ollama"},
		{"nomic-embed-text", "ollama"},
		{"qwen2.5:0.5b", "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := InferProvider(tt.modelID)
			if got != tt.expected {
				t.Errorf("InferProvider(%q) = %q, want %q", tt.modelID, got, tt.expected)
			}
		})
	}
}

func TestInferModelType(t *testing.T) {
	tests := []struct {
		modelID  string
		expected string
	}{
		{"amazon.nova-2-multimodal-embeddings-v1:0", "embedding"},
		{"nomic-embed-text", "embedding"},
		{"nvidia/llama-nemotron-embed-vl-1b-v2:free", "embedding"},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "generation"},
		{"google/gemma-4-31b-it:free", "generation"},
		{"gemma3:4b", "generation"},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			got := InferModelType(tt.modelID)
			if got != tt.expected {
				t.Errorf("InferModelType(%q) = %q, want %q", tt.modelID, got, tt.expected)
			}
		})
	}
}

// TestProbeBudgetConstantsPinned holds the probe output budget offline where
// plain CI always runs (the live guard, TestLiveGrok46ClassicConverse_CredGated,
// is credential-gated and so invisible to CI). The budget is load-bearing:
// always-reasoning models bill reasoning against the output budget (a live
// "what is 2+2" on grok-4.6 cost 180 output tokens), so a revert toward the
// old 32-token cap truncates mid-reasoning and fails a working model. The
// three values are pinned together so the cost estimate can never
// under-report what a probe may bill and the classic-plane budget can never
// drop below the mantle client's floor, which exists for the same cause.
func TestProbeBudgetConstantsPinned(t *testing.T) {
	if probeGenMaxTokens < 256 {
		t.Errorf("probeGenMaxTokens = %d; below 256 truncates always-reasoning models mid-answer (measured 180 tokens for a trivial prompt)", probeGenMaxTokens)
	}
	if got := DefaultProbeSpec(ProbeTest).OutputTokens; got != probeGenMaxTokens {
		t.Errorf("ProbeTest cost spec OutputTokens = %d, want probeGenMaxTokens (%d); the estimate must not under-report what a probe may bill", got, probeGenMaxTokens)
	}
	if probeGenMaxTokens < mantleMinOutputTokens {
		t.Errorf("probeGenMaxTokens = %d below mantleMinOutputTokens = %d; the classic probe budget must cover the same reasoning overhead the mantle floor exists for", probeGenMaxTokens, mantleMinOutputTokens)
	}
}
