package ai

import (
	"context"
	"testing"
)

// TestBuildModelList_IncludeDisabledProviders guards the regression where the
// setup wizard dead-ended: with Ollama/OpenRouter shipping disabled by default,
// the wizard's candidate list filtered out every opt-in provider AND the
// unreachable Bedrock, yielding zero candidates. IncludeDisabledProviders keeps
// disabled providers' models so the wizard (the enablement surface) can offer
// them.
func TestBuildModelList_IncludeDisabledProviders(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultAIConfig() // ollama + openrouter ship disabled

	count := func(models []ModelInfo, provider string) int {
		n := 0
		for _, m := range models {
			if m.Provider == provider {
				n++
			}
		}
		return n
	}

	without, err := BuildModelList(ctx, MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	if got := count(without.Verified, "ollama"); got != 0 {
		t.Errorf("default build should drop disabled ollama models, got %d", got)
	}

	with, err := BuildModelList(ctx, MergedListOptions{Config: cfg, IncludeDisabledProviders: true})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	if got := count(with.Verified, "ollama"); got == 0 {
		t.Error("IncludeDisabledProviders should keep ollama models so the wizard isn't a dead-end")
	}
}
