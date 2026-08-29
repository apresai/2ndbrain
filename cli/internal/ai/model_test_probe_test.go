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
// floor is 1024, not the earlier 256: the probe measures entitlement, not
// budget, so it needs headroom for a deeper reasoning-by-default model to
// still emit answer text after its reasoning overhead, not just enough to
// match one measured 180-token sample. The three values are pinned together
// so the cost estimate can never under-report what a probe may bill and the
// classic-plane budget can never drop below the mantle client's floor, which
// exists for the same cause.
func TestProbeBudgetConstantsPinned(t *testing.T) {
	if probeGenMaxTokens < 1024 {
		t.Errorf("probeGenMaxTokens = %d; below 1024 leaves too little headroom for a deep reasoning-by-default model to emit answer text after its reasoning overhead (measured 180 tokens for grok-4.6 on a trivial prompt, but the probe must cover models with much larger reasoning overhead too)", probeGenMaxTokens)
	}
	if got := DefaultProbeSpec(ProbeTest).OutputTokens; got != probeGenMaxTokens {
		t.Errorf("ProbeTest cost spec OutputTokens = %d, want probeGenMaxTokens (%d); the estimate must not under-report what a probe may bill", got, probeGenMaxTokens)
	}
	if probeGenMaxTokens < mantleMinOutputTokens {
		t.Errorf("probeGenMaxTokens = %d below mantleMinOutputTokens = %d; the classic probe budget must cover the same reasoning overhead the mantle floor exists for", probeGenMaxTokens, mantleMinOutputTokens)
	}
}

func TestAssignResolvedEmbedStrategyOverridesCatalogLookup(t *testing.T) {
	// A discovered-only id has no catalog row, so the constructor would
	// store an empty strategy and fall through to detectEmbedFormat. The
	// probe already resolved the candidate's InvokeStrategy; that value
	// must win.
	e := &BedrockEmbedder{model: "vendor.custom-embed-v9"}
	assignResolvedEmbedStrategy(e, StrategyBedrockInvokeCohereEmbed)
	if e.strategy != StrategyBedrockInvokeCohereEmbed {
		t.Fatalf("strategy = %q, want the resolved Cohere embed strategy", e.strategy)
	}
	got := e.resolvedEmbedFormat()
	want, ok := bedrockEmbedFormatFromStrategy(StrategyBedrockInvokeCohereEmbed)
	if !ok {
		t.Fatal("Cohere embed strategy should map to a format")
	}
	if got != want {
		t.Errorf("resolvedEmbedFormat = %v, want %v (the resolved strategy's format, not id detection)", got, want)
	}
}

func TestClassicRegionFanOutIgnoresSiblingMantleRow(t *testing.T) {
	setupHome(t)
	id := "xai.grok-4.6"
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: id, Provider: "bedrock", Type: "generation",
		Plane: PlaneMantle, InvokeStrategy: StrategyBedrockMantleResponses,
		Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}
	classic := ModelInfo{ID: id, Provider: "bedrock", Type: "generation", Plane: PlaneClassic}
	if !usesClassicRegionFanOut(classic, "") {
		t.Fatal("@classic candidate skipped classic region fan-out because a sibling mantle row won the plane-blind lookup")
	}
	mantle := ModelInfo{ID: id, Provider: "bedrock", Type: "generation", Plane: PlaneMantle}
	if usesClassicRegionFanOut(mantle, "") {
		t.Fatal("named mantle plane must not fan out across classic regions")
	}
}
