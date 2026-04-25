package ai

import (
	"context"
	"testing"
)

func TestBuildModelList_VerifiedOnly(t *testing.T) {
	ctx := context.Background()
	result, err := BuildModelList(ctx, MergedListOptions{
		Config: DefaultAIConfig(),
	})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	if len(result.Verified) == 0 {
		t.Fatal("expected verified models from catalog")
	}
	if len(result.Unverified) != 0 {
		t.Errorf("expected no unverified models without --discover, got %d", len(result.Unverified))
	}
}

func TestBuildModelList_ActiveMarking(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultAIConfig() // bedrock, haiku, nova embed
	result, err := BuildModelList(ctx, MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}

	var activeCount int
	for _, m := range result.Verified {
		if m.Active {
			activeCount++
			if m.Provider != cfg.Provider {
				t.Errorf("active model %s has provider %s, expected %s", m.ID, m.Provider, cfg.Provider)
			}
			if m.ID != cfg.EmbeddingModel && m.ID != cfg.GenerationModel {
				t.Errorf("active model %s doesn't match config embedding=%s or generation=%s",
					m.ID, cfg.EmbeddingModel, cfg.GenerationModel)
			}
		}
	}
	if activeCount != 2 {
		t.Errorf("expected 2 active models (embed + gen), got %d", activeCount)
	}
}

func TestBuildModelList_DerivedPickerFields(t *testing.T) {
	setupHome(t)
	result, err := BuildModelList(context.Background(), MergedListOptions{Config: DefaultAIConfig()})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	for _, m := range result.Verified {
		if m.Vendor == "" {
			t.Fatalf("%s missing Vendor", m.ID)
		}
		if m.VendorDisplay == "" {
			t.Fatalf("%s missing VendorDisplay", m.ID)
		}
		if m.VersionSortKey == "" {
			t.Fatalf("%s missing VersionSortKey", m.ID)
		}
		if !m.Compatible {
			t.Fatalf("builtin model %s should be compatible: %s", m.ID, m.CompatibilityReason)
		}
	}
}

func TestCatalogCompatibility_BedrockUnsupportedReason(t *testing.T) {
	ok, reason := catalogCompatibility(ModelInfo{
		ID:       "amazon.nova-canvas-v1:0",
		Provider: "bedrock",
		Type:     "generation",
	})
	if ok {
		t.Fatal("nova canvas should be incompatible with text generation")
	}
	if reason == "" {
		t.Fatal("expected compatibility reason")
	}
}

func TestBuildModelList_Sorting(t *testing.T) {
	ctx := context.Background()
	result, err := BuildModelList(ctx, MergedListOptions{
		Config: DefaultAIConfig(),
	})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}

	for i := 1; i < len(result.Verified); i++ {
		a, b := result.Verified[i-1], result.Verified[i]
		if a.Provider > b.Provider {
			t.Errorf("sort violation: %s/%s before %s/%s (provider order)",
				a.Provider, a.ID, b.Provider, b.ID)
		}
		if a.Provider == b.Provider {
			if a.Type == "generation" && b.Type == "embedding" {
				t.Errorf("sort violation: %s/%s (generation) before %s/%s (embedding)",
					a.Provider, a.ID, b.Provider, b.ID)
			}
			if a.Provider == b.Provider && a.Type == b.Type && a.ID > b.ID {
				t.Errorf("sort violation: %s/%s before %s/%s (id order)",
					a.Provider, a.ID, b.Provider, b.ID)
			}
		}
	}
}

func TestIsActiveModel(t *testing.T) {
	cfg := DefaultAIConfig()

	tests := []struct {
		name   string
		model  ModelInfo
		expect bool
	}{
		{
			name:   "matching embed",
			model:  ModelInfo{Provider: "bedrock", Type: "embedding", ID: cfg.EmbeddingModel},
			expect: true,
		},
		{
			name:   "matching gen",
			model:  ModelInfo{Provider: "bedrock", Type: "generation", ID: cfg.GenerationModel},
			expect: true,
		},
		{
			name:   "wrong provider",
			model:  ModelInfo{Provider: "ollama", Type: "generation", ID: cfg.GenerationModel},
			expect: false,
		},
		{
			name:   "wrong model",
			model:  ModelInfo{Provider: "bedrock", Type: "generation", ID: "some-other-model"},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isActiveModel(tt.model, cfg)
			if got != tt.expect {
				t.Errorf("isActiveModel(%s/%s) = %v, want %v", tt.model.Provider, tt.model.ID, got, tt.expect)
			}
		})
	}
}

func TestSortModels(t *testing.T) {
	models := []ModelInfo{
		{Provider: "ollama", Type: "generation", ID: "gemma3:4b"},
		{Provider: "bedrock", Type: "generation", ID: "z-model"},
		{Provider: "bedrock", Type: "embedding", ID: "a-embed"},
		{Provider: "bedrock", Type: "generation", ID: "a-model"},
		{Provider: "ollama", Type: "embedding", ID: "nomic"},
	}
	sortModels(models)

	expected := []string{
		"bedrock/embedding/a-embed",
		"bedrock/generation/a-model",
		"bedrock/generation/z-model",
		"ollama/embedding/nomic",
		"ollama/generation/gemma3:4b",
	}
	for i, m := range models {
		got := m.Provider + "/" + m.Type + "/" + m.ID
		if got != expected[i] {
			t.Errorf("position %d: got %s, want %s", i, got, expected[i])
		}
	}
}

// TestBuildModelList_EnabledOnly_FiltersDisabled verifies that when EnabledOnly
// is true, entries whose Enabled field is explicitly false are excluded, while
// nil-Enabled entries (the default for untouched builtins) remain present.
func TestBuildModelList_EnabledOnly_FiltersDisabled(t *testing.T) {
	setupHome(t)

	// Pick the first builtin model and disable it in the user catalog.
	builtin := BuiltinCatalog()
	if len(builtin) == 0 {
		t.Skip("no builtin catalog entries")
	}
	target := builtin[0]
	disabled := false
	entry := ModelInfo{
		ID:       target.ID,
		Provider: target.Provider,
		Tier:     TierUserVerified,
		Enabled:  &disabled,
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	ctx := context.Background()
	result, err := BuildModelList(ctx, MergedListOptions{
		Config:      DefaultAIConfig(),
		EnabledOnly: true,
	})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}

	// The disabled entry must be absent.
	for _, m := range result.Verified {
		if m.Provider == target.Provider && m.ID == target.ID {
			t.Errorf("disabled model %s/%s appeared in EnabledOnly list", target.Provider, target.ID)
		}
	}

	// Other builtin models (nil Enabled) must remain.
	if len(result.Verified) == 0 {
		t.Error("all models were filtered — nil-Enabled entries should survive EnabledOnly")
	}
	// Verify at least one nil-Enabled builtin is present.
	foundNil := false
	for _, m := range result.Verified {
		if m.Enabled == nil {
			foundNil = true
			break
		}
	}
	if !foundNil {
		t.Error("expected at least one nil-Enabled entry to survive EnabledOnly filter")
	}
}

// TestBuildModelList_EnabledOnly_False_IncludesAll verifies that when
// EnabledOnly is false (the default), disabled models are still returned.
func TestBuildModelList_EnabledOnly_False_IncludesAll(t *testing.T) {
	setupHome(t)

	builtin := BuiltinCatalog()
	if len(builtin) == 0 {
		t.Skip("no builtin catalog entries")
	}
	target := builtin[0]
	disabled := false
	entry := ModelInfo{
		ID:       target.ID,
		Provider: target.Provider,
		Tier:     TierUserVerified,
		Enabled:  &disabled,
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	ctx := context.Background()
	result, err := BuildModelList(ctx, MergedListOptions{
		Config:      DefaultAIConfig(),
		EnabledOnly: false, // default — all models included
	})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}

	found := false
	for _, m := range result.Verified {
		if m.Provider == target.Provider && m.ID == target.ID {
			found = true
			if m.Enabled == nil || *m.Enabled != false {
				t.Errorf("expected Enabled=false on the entry, got %v", m.Enabled)
			}
			break
		}
	}
	if !found {
		t.Errorf("disabled model %s/%s should appear when EnabledOnly=false", target.Provider, target.ID)
	}
}

// TestBuildModelList_DropsDisabledProvider verifies that a provider-level
// disable silences every model from that provider, regardless of tier
// or the per-model Enabled flag.
func TestBuildModelList_DropsDisabledProvider(t *testing.T) {
	cfg := AIConfig{
		Provider: "bedrock",
		Bedrock:  BedrockConfig{Disabled: true},
	}
	got, err := BuildModelList(context.Background(), MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	for _, m := range got.Verified {
		if m.Provider == "bedrock" {
			t.Errorf("bedrock entry %s leaked through disabled-provider filter", m.ID)
		}
	}
	// Non-disabled providers should still surface entries.
	hasOther := false
	for _, m := range got.Verified {
		if m.Provider != "bedrock" {
			hasOther = true
			break
		}
	}
	if !hasOther {
		t.Error("no non-bedrock entries surfaced — disabled filter may have dropped everything")
	}
}

// TestProviderDisabled_UnknownNameIsFalse tripwires the switch so adding
// a new provider requires touching ProviderDisabled.
func TestProviderDisabled_UnknownNameIsFalse(t *testing.T) {
	cfg := AIConfig{}
	if cfg.ProviderDisabled("made-up") {
		t.Error("unknown provider should default to enabled (false)")
	}
}

// TestProviderDisabled_EachProviderToggles verifies each slot maps right.
func TestProviderDisabled_EachProviderToggles(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*AIConfig)
	}{
		{"bedrock", func(c *AIConfig) { c.Bedrock.Disabled = true }},
		{"openrouter", func(c *AIConfig) { c.OpenRouter.Disabled = true }},
		{"ollama", func(c *AIConfig) { c.Ollama.Disabled = true }},
	}
	for _, tc := range cases {
		cfg := AIConfig{}
		tc.apply(&cfg)
		if !cfg.ProviderDisabled(tc.name) {
			t.Errorf("ProviderDisabled(%q) = false after setting disabled=true", tc.name)
		}
		for _, other := range []string{"bedrock", "openrouter", "ollama"} {
			if other == tc.name {
				continue
			}
			if cfg.ProviderDisabled(other) {
				t.Errorf("setting %q disabled leaked into %q", tc.name, other)
			}
		}
	}
}
