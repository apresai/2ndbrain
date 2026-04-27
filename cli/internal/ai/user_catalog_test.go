package ai

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// setupHome redirects $HOME and $XDG_CONFIG_HOME to a temp dir so the tests
// never touch the developer's real ~/.config/2nb/models.yaml.
func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	return home
}

func TestSaveAndLoad_GlobalRoundTrip(t *testing.T) {
	setupHome(t)

	entry := ModelInfo{
		ID:         "titan-embed-text-v2",
		Name:       "Titan Embed v2",
		Provider:   "bedrock",
		Type:       "embedding",
		Dimensions: 1024,
		PriceIn:    0.02,
		Tier:       TierUserVerified,
		TestedAt:   "2026-04-17T10:00:00Z",
	}

	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	models := LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	got := models[0]
	if got.ID != entry.ID || got.Provider != entry.Provider {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.Tier != TierUserVerified {
		t.Fatalf("expected user_verified tier, got %q", got.Tier)
	}
	if got.PriceSource != "user" {
		t.Fatalf("expected price_source=user (derived), got %q", got.PriceSource)
	}
	if !got.PriceOverride {
		t.Fatal("expected non-zero user price to infer price_override=true")
	}
}

func TestSaveAndLoad_ReplacesExisting(t *testing.T) {
	setupHome(t)

	original := ModelInfo{ID: "foo", Provider: "bedrock", Type: "generation", PriceIn: 1.0}
	updated := ModelInfo{ID: "foo", Provider: "bedrock", Type: "generation", PriceIn: 2.0}

	if err := SaveUserCatalogEntry(ScopeGlobal, "", original); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", updated); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	models := LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected single entry after in-place replace, got %d", len(models))
	}
	if models[0].PriceIn != 2.0 {
		t.Fatalf("expected updated price 2.0, got %v", models[0].PriceIn)
	}
}

func TestSaveUserCatalogEntry_ConcurrentWritesPreserveEntries(t *testing.T) {
	setupHome(t)

	const total = 24
	var wg sync.WaitGroup
	errs := make(chan error, total)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
				ID:       "concurrent-" + string(rune('a'+i)),
				Provider: "openrouter",
				Type:     "generation",
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("save failed: %v", err)
		}
	}

	models := LoadUserCatalog("")
	if len(models) != total {
		t.Fatalf("loaded %d models, want %d: %+v", len(models), total, models)
	}
	seen := make(map[string]bool, total)
	for _, m := range models {
		seen[m.ID] = true
	}
	for i := 0; i < total; i++ {
		id := "concurrent-" + string(rune('a'+i))
		if !seen[id] {
			t.Fatalf("missing concurrently saved model %q", id)
		}
	}
}

func TestSaveUserCatalogEntry_CorruptCatalogQuarantineFailureDoesNotOverwrite(t *testing.T) {
	home := setupHome(t)
	path := filepath.Join(home, ".config", "2nb", userCatalogFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	corrupt := []byte("models: [\n")
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if err := os.Mkdir(path+".bak", 0o755); err != nil {
		t.Fatalf("mkdir bak blocker: %v", err)
	}

	err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{ID: "new", Provider: "bedrock", Type: "generation"})
	if err == nil {
		t.Fatal("expected save to fail when corrupt catalog cannot be quarantined")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("catalog should remain readable after failed save: %v", readErr)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt catalog was overwritten after quarantine failure\nbefore: %q\nafter: %q", corrupt, after)
	}
}

func TestLoadUserCatalog_MissingFilesAreNotErrors(t *testing.T) {
	setupHome(t)
	// No files written.
	models := LoadUserCatalog("")
	if models != nil && len(models) != 0 {
		t.Fatalf("expected empty slice when no catalog files exist, got %+v", models)
	}
}

func TestLoadUserCatalog_CorruptFileIsQuarantined(t *testing.T) {
	home := setupHome(t)
	path := filepath.Join(home, ".config", "2nb", userCatalogFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("::: not valid yaml :::"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	models := LoadUserCatalog("")
	if len(models) != 0 {
		t.Fatalf("expected empty slice from corrupt file, got %+v", models)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("expected .bak quarantine file, got: %v", err)
	}
}

func TestVaultCatalogOverridesGlobal(t *testing.T) {
	setupHome(t)
	vaultRoot := t.TempDir()

	global := ModelInfo{
		ID: "m1", Provider: "bedrock", Type: "generation",
		Name: "Global Name", PriceIn: 1.0,
	}
	perVault := ModelInfo{
		ID: "m1", Provider: "bedrock", Type: "generation",
		Name: "Vault Name", PriceIn: 5.0,
	}

	if err := SaveUserCatalogEntry(ScopeGlobal, "", global); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if err := SaveUserCatalogEntry(ScopeVault, vaultRoot, perVault); err != nil {
		t.Fatalf("save vault: %v", err)
	}

	models := LoadUserCatalog(vaultRoot)
	if len(models) != 1 {
		t.Fatalf("expected single merged entry, got %d: %+v", len(models), models)
	}
	if models[0].Name != "Vault Name" || models[0].PriceIn != 5.0 {
		t.Fatalf("vault should override global, got %+v", models[0])
	}
}

func TestOverlay_AppendsNewEntries(t *testing.T) {
	base := []ModelInfo{
		{ID: "a", Provider: "bedrock"},
	}
	top := []ModelInfo{
		{ID: "b", Provider: "bedrock"},
	}
	out := overlay(base, top)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
}

func TestOverlay_PreservesTierElevation(t *testing.T) {
	base := []ModelInfo{
		{ID: "x", Provider: "bedrock", Tier: TierVerified},
	}
	// A user catalog entry with TierUserVerified should not demote the base entry.
	top := []ModelInfo{
		{ID: "x", Provider: "bedrock", Tier: TierUserVerified, PriceIn: 42},
	}
	out := overlay(base, top)
	if out[0].Tier != TierVerified {
		t.Fatalf("expected TierVerified preserved, got %q", out[0].Tier)
	}
	if out[0].PriceIn != 42 {
		t.Fatalf("expected overlay price applied, got %v", out[0].PriceIn)
	}
}

// End-to-end: a user catalog entry that overrides a real builtin model must
// keep the builtin's TierVerified and adopt the user's price + notes.
func TestLoadUserCatalog_LayersOnBuiltinKeepsTier(t *testing.T) {
	setupHome(t)

	builtin := BuiltinCatalog()
	if len(builtin) == 0 {
		t.Skip("no builtin catalog available")
	}
	target := builtin[0]

	// User sets a different price. PriceSource empty so the intent is implicit.
	override := ModelInfo{
		ID: target.ID, Provider: target.Provider, Type: target.Type,
		PriceIn: 999.0, Notes: "user override",
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", override); err != nil {
		t.Fatalf("save: %v", err)
	}

	merged := overlay(builtin, LoadUserCatalog(""))
	var found *ModelInfo
	for i := range merged {
		if merged[i].Provider == target.Provider && merged[i].ID == target.ID {
			found = &merged[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("target entry missing after overlay")
	}
	if found.Tier != TierVerified {
		t.Fatalf("builtin TierVerified should not be demoted, got %q", found.Tier)
	}
	if found.PriceIn != 999.0 {
		t.Fatalf("expected user price override, got %v", found.PriceIn)
	}
	if found.Notes != "user override" {
		t.Fatalf("expected user notes, got %q", found.Notes)
	}
}

// Regression: an explicit zero-price user override must override a non-zero
// builtin price only when the override marker is present.
func TestOverlay_ExplicitZeroPriceWinsWhenPriceSourceSet(t *testing.T) {
	base := []ModelInfo{
		{ID: "pricey", Provider: "bedrock", PriceIn: 10.0, PriceSource: "builtin"},
	}
	top := []ModelInfo{
		{ID: "pricey", Provider: "bedrock", PriceIn: 0, PriceSource: "user", PriceOverride: true, Tier: TierUserVerified},
	}
	out := overlay(base, top)
	if out[0].PriceIn != 0 {
		t.Fatalf("explicit zero-price override should win, got %v", out[0].PriceIn)
	}
	if out[0].PriceSource != "user" {
		t.Fatalf("expected price_source=user, got %q", out[0].PriceSource)
	}
	if !out[0].PriceOverride {
		t.Fatal("expected price_override=true to be preserved")
	}
}

func TestOverlay_LegacyZeroPriceUserEntryDoesNotOverride(t *testing.T) {
	base := []ModelInfo{
		{ID: "pricey", Provider: "bedrock", PriceIn: 10.0, PriceSource: "builtin"},
	}
	top := []ModelInfo{
		{ID: "pricey", Provider: "bedrock", PriceIn: 0, PriceSource: "user", Tier: TierUserVerified},
	}
	out := overlay(base, top)
	if out[0].PriceIn != 10.0 {
		t.Fatalf("legacy zero-price user entry should not wipe builtin price, got %v", out[0].PriceIn)
	}
	if out[0].PriceSource != "builtin" {
		t.Fatalf("expected builtin price_source to survive, got %q", out[0].PriceSource)
	}
}

func TestLoadUserCatalog_LegacyZeroPriceUserEntryIsRecovered(t *testing.T) {
	setupHome(t)
	entry := ModelInfo{
		ID:          "deepseek.v3.2",
		Provider:    "bedrock",
		Type:        "generation",
		PriceSource: "user",
		Tier:        TierUserVerified,
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}

	models := LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].PriceSource != "" {
		t.Fatalf("legacy zero-price user entry should load as unpriced, got %q", models[0].PriceSource)
	}
	if models[0].PriceOverride {
		t.Fatal("legacy zero-price user entry should not infer price_override")
	}
}

func TestRemoveUserCatalogEntry(t *testing.T) {
	setupHome(t)
	_ = SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{ID: "keep", Provider: "bedrock"})
	_ = SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{ID: "drop", Provider: "bedrock"})

	if err := RemoveUserCatalogEntry(ScopeGlobal, "", "bedrock", "drop"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	models := LoadUserCatalog("")
	if len(models) != 1 || models[0].ID != "keep" {
		t.Fatalf("expected only 'keep' to remain, got %+v", models)
	}
}

func TestRemoveUserCatalogEntry_MissingFileIsNoOp(t *testing.T) {
	setupHome(t)
	if err := RemoveUserCatalogEntry(ScopeGlobal, "", "bedrock", "nope"); err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
}

// Removing a non-existent entry from a populated catalog must not rewrite
// the file. The caller's catalog on disk should be byte-identical.
func TestRemoveUserCatalogEntry_AbsentEntryPreservesFile(t *testing.T) {
	setupHome(t)
	_ = SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{ID: "stays", Provider: "bedrock"})

	before, err := os.ReadFile(globalCatalogPath())
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	if err := RemoveUserCatalogEntry(ScopeGlobal, "", "bedrock", "never-existed"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after, err := os.ReadFile(globalCatalogPath())
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("file mutated on no-op remove\nbefore: %s\nafter: %s", before, after)
	}
}

func TestXDGConfigHomeOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	entry := ModelInfo{ID: "xdg-test", Provider: "openrouter", Type: "generation"}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	want := filepath.Join(xdg, "2nb", userCatalogFileName)
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected file at %s, got: %v", want, err)
	}
}

// TestLoadUserCatalog_OldYamlWithoutNewFields verifies back-compat: a
// pre-existing yaml file with no invoke_strategy / benchmark / enabled
// fields must load without error and leave those fields at zero values,
// so nothing inadvertently re-serializes them with defaults.
func TestLoadUserCatalog_OldYamlWithoutNewFields(t *testing.T) {
	home := setupHome(t)
	path := filepath.Join(home, ".config", "2nb", userCatalogFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldYAML := []byte(`version: 1
models:
  - id: legacy.model
    provider: bedrock
    type: generation
    name: Legacy Model
    context_length: 8192
    tested_at: "2026-01-15T10:00:00Z"
`)
	if err := os.WriteFile(path, oldYAML, 0o644); err != nil {
		t.Fatalf("write old yaml: %v", err)
	}

	models := LoadUserCatalog("")
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	m := models[0]
	if m.ID != "legacy.model" || m.ContextLen != 8192 || m.TestedAt != "2026-01-15T10:00:00Z" {
		t.Fatalf("legacy fields lost in round-trip: %+v", m)
	}
	if m.InvokeStrategy != "" {
		t.Errorf("InvokeStrategy should be empty for legacy yaml, got %q", m.InvokeStrategy)
	}
	if m.TestLatencyMs != 0 || m.TestError != "" {
		t.Errorf("new test fields should be zero, got latency=%d err=%q", m.TestLatencyMs, m.TestError)
	}
	if m.Benchmark != nil {
		t.Errorf("Benchmark should be nil for legacy yaml, got %+v", m.Benchmark)
	}
	if m.Enabled != nil {
		t.Errorf("Enabled should be nil for legacy yaml, got %v", *m.Enabled)
	}
}

// TestSaveAndLoad_NewFieldsRoundTrip verifies the new phase-1 fields
// survive a yaml write/read cycle with their values intact.
func TestSaveAndLoad_NewFieldsRoundTrip(t *testing.T) {
	setupHome(t)

	disabled := false
	entry := ModelInfo{
		ID:             "new.model",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockConverse,
		TestedAt:       "2026-04-24T12:00:00Z",
		TestLatencyMs:  420,
		TestError:      "",
		Benchmark: &BenchmarkSummary{
			RanAt:         "2026-04-24T12:05:00Z",
			AvgLatencyMs:  380,
			QualityScore:  0.72,
			VaultDocCount: 42,
		},
		Enabled: &disabled,
	}

	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded := LoadUserCatalog("")
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded))
	}
	m := loaded[0]
	if m.InvokeStrategy != StrategyBedrockConverse {
		t.Errorf("InvokeStrategy: got %q", m.InvokeStrategy)
	}
	if m.TestLatencyMs != 420 {
		t.Errorf("TestLatencyMs: got %d", m.TestLatencyMs)
	}
	if m.Benchmark == nil {
		t.Fatal("Benchmark nil after round-trip")
	}
	if m.Benchmark.QualityScore != 0.72 || m.Benchmark.VaultDocCount != 42 {
		t.Errorf("Benchmark fields: got %+v", m.Benchmark)
	}
	if m.Enabled == nil || *m.Enabled != false {
		t.Errorf("Enabled: got %v", m.Enabled)
	}
}

// TestMergeFields_NewFieldsOverlay verifies mergeFields propagates the
// new fields from overlay to base, matching the documented semantics:
// non-empty strategy overrides, non-nil Benchmark overrides, non-nil
// Enabled overrides, and test result fields move as a unit with TestedAt.
func TestMergeFields_NewFieldsOverlay(t *testing.T) {
	base := ModelInfo{
		ID: "m", Provider: "bedrock", Type: "generation",
		InvokeStrategy: StrategyBedrockInvokeAnthropic,
		TestedAt:       "2026-01-01T00:00:00Z",
		TestLatencyMs:  1000,
	}
	trueVal := true
	top := ModelInfo{
		ID: "m", Provider: "bedrock",
		InvokeStrategy: StrategyBedrockConverse,
		TestedAt:       "2026-04-24T00:00:00Z",
		TestLatencyMs:  200,
		Benchmark: &BenchmarkSummary{
			AvgLatencyMs: 180,
			QualityScore: 0.8,
		},
		Enabled: &trueVal,
	}

	out := mergeFields(base, top)
	if out.InvokeStrategy != StrategyBedrockConverse {
		t.Errorf("strategy: got %q", out.InvokeStrategy)
	}
	if out.TestedAt != "2026-04-24T00:00:00Z" || out.TestLatencyMs != 200 {
		t.Errorf("test fields didn't move as a unit: %+v", out)
	}
	if out.Benchmark == nil || out.Benchmark.QualityScore != 0.8 {
		t.Errorf("benchmark: %+v", out.Benchmark)
	}
	if out.Enabled == nil || !*out.Enabled {
		t.Errorf("enabled: %v", out.Enabled)
	}

	// Empty-overlay case: base fields must be preserved.
	noop := ModelInfo{ID: "m", Provider: "bedrock"}
	out2 := mergeFields(base, noop)
	if out2.InvokeStrategy != StrategyBedrockInvokeAnthropic {
		t.Errorf("empty overlay wiped base strategy: got %q", out2.InvokeStrategy)
	}
	if out2.TestLatencyMs != 1000 {
		t.Errorf("empty overlay wiped base latency: got %d", out2.TestLatencyMs)
	}
}

// TestResolveInvokeStrategy_BuiltinLookups verifies known builtins expose
// their declared strategy — the wizard / dispatcher will query this on
// every model selection.
func TestResolveInvokeStrategy_BuiltinLookups(t *testing.T) {
	setupHome(t) // ensure user catalog is empty so we're testing builtins only

	cases := []struct {
		provider, modelID string
		want              string
	}{
		{"bedrock", "amazon.nova-2-multimodal-embeddings-v1:0", StrategyBedrockInvokeNovaEmbed},
		{"bedrock", "amazon.titan-embed-text-v2:0", StrategyBedrockInvokeTitanEmbed},
		{"bedrock", "cohere.embed-english-v3", StrategyBedrockInvokeCohereEmbed},
		{"bedrock", "us.anthropic.claude-haiku-4-5-20251001-v1:0", StrategyBedrockConverse},
		// Inference-profile prefix should resolve to the non-prefixed builtin.
		{"bedrock", "eu.anthropic.claude-haiku-4-5-20251001-v1:0", StrategyBedrockConverse},
		{"openrouter", "anthropic/claude-sonnet-4", StrategyOpenRouterChat},
		{"ollama", "nomic-embed-text", StrategyOllamaEmbeddings},
		{"bedrock", "not.a.known.model", ""},
	}
	for _, tc := range cases {
		t.Run(tc.provider+":"+tc.modelID, func(t *testing.T) {
			got := ResolveInvokeStrategy(tc.provider, tc.modelID, "")
			if got != tc.want {
				t.Errorf("ResolveInvokeStrategy(%q,%q) = %q, want %q", tc.provider, tc.modelID, got, tc.want)
			}
		})
	}
}

// TestResolveInvokeStrategy_UserCatalogOverrides verifies a user-catalog
// entry for a model NOT in the builtin set exposes its declared strategy.
// A user catalog entry for a builtin model should also win when both
// declare a strategy (user intent beats builtin default).
func TestResolveInvokeStrategy_UserCatalogOverrides(t *testing.T) {
	setupHome(t)

	// Brand-new model not in builtin, user declares strategy.
	custom := ModelInfo{
		ID:             "vendor/custom-gen-v9",
		Provider:       "openrouter",
		Type:           "generation",
		InvokeStrategy: StrategyOpenRouterChat,
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", custom); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := ResolveInvokeStrategy("openrouter", "vendor/custom-gen-v9", ""); got != StrategyOpenRouterChat {
		t.Errorf("custom user entry: got %q", got)
	}

	// User override of a builtin: builtin says StrategyBedrockConverse,
	// user catalog says something else (simulated). User entry should win.
	override := ModelInfo{
		ID:             "us.anthropic.claude-haiku-4-5-20251001-v1:0",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockInvokeAnthropic,
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", override); err != nil {
		t.Fatalf("save override: %v", err)
	}
	if got := ResolveInvokeStrategy("bedrock", "us.anthropic.claude-haiku-4-5-20251001-v1:0", ""); got != StrategyBedrockInvokeAnthropic {
		t.Errorf("user override of builtin: got %q", got)
	}
}

// TestBedrockEmbedFormatFromStrategy round-trips the strategy↔format
// map so new strategies don't accidentally drop their format binding.
func TestBedrockEmbedFormatFromStrategy(t *testing.T) {
	cases := []struct {
		strategy string
		wantFmt  bedrockEmbedFmt
		wantOK   bool
	}{
		{StrategyBedrockInvokeNovaEmbed, fmtNova, true},
		{StrategyBedrockInvokeTitanEmbed, fmtTitanV2, true},
		{StrategyBedrockInvokeCohereEmbed, fmtCohere, true},
		{StrategyBedrockInvokeMarengo27, fmtTwelveLabs27, true},
		{StrategyBedrockInvokeMarengo30, fmtTwelveLabs30, true},
		{StrategyBedrockConverse, 0, false},
		{"unrecognized", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := bedrockEmbedFormatFromStrategy(tc.strategy)
		if ok != tc.wantOK {
			t.Errorf("%q: ok = %v, want %v", tc.strategy, ok, tc.wantOK)
			continue
		}
		if ok && got != tc.wantFmt {
			t.Errorf("%q: format = %v, want %v", tc.strategy, got, tc.wantFmt)
		}
	}
}

// TestKnownInvokeStrategies_AllAccounted is a cheap tripwire: if someone
// adds a Strategy* constant but forgets KnownInvokeStrategies(), this
// test stays green but the wizard won't list the new strategy. Keep
// the numeric check loose so adding a new strategy is a one-liner.
func TestKnownInvokeStrategies_AllAccounted(t *testing.T) {
	got := KnownInvokeStrategies()
	if len(got) < 15 {
		t.Errorf("KnownInvokeStrategies returned %d entries; expected at least 15", len(got))
	}
	for _, s := range got {
		if !IsKnownInvokeStrategy(s) {
			t.Errorf("%q is in KnownInvokeStrategies but IsKnownInvokeStrategy rejects it", s)
		}
	}
	if IsKnownInvokeStrategy("") || IsKnownInvokeStrategy("made_up_strategy") {
		t.Error("IsKnownInvokeStrategy should reject empty and unknown values")
	}
}
