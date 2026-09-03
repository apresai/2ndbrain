package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	out := overlay(base, top, true)
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
	out := overlay(base, top, true)
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

	merged := overlay(builtin, LoadUserCatalog(""), true)
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
	out := overlay(base, top, true)
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
	out := overlay(base, top, true)
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

	n, err := RemoveUserCatalogEntry(ScopeGlobal, "", RouteKey{Provider: "bedrock", ID: "drop"})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	models := LoadUserCatalog("")
	if len(models) != 1 || models[0].ID != "keep" {
		t.Fatalf("expected only 'keep' to remain, got %+v", models)
	}
}

func TestRemoveUserCatalogEntry_MissingFileIsNoOp(t *testing.T) {
	setupHome(t)
	n, err := RemoveUserCatalogEntry(ScopeGlobal, "", RouteKey{Provider: "bedrock", ID: "nope"})
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if n != 0 {
		t.Fatalf("removed %d from a missing file, want 0", n)
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
	n, err := RemoveUserCatalogEntry(ScopeGlobal, "", RouteKey{Provider: "bedrock", ID: "never-existed"})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n != 0 {
		t.Fatalf("removed %d for an absent id, want 0", n)
	}
	after, err := os.ReadFile(globalCatalogPath())
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("file mutated on no-op remove\nbefore: %s\nafter: %s", before, after)
	}
}

func TestSaveUserCatalogEntryUpgradesPrerouteRow(t *testing.T) {
	setupHome(t)
	id := "amazon.nova-2-multimodal-embeddings-v1:0"
	enabled := false
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: id, Provider: "bedrock", Type: "embedding",
		Tier: TierUserVerified, TestedAt: "2026-08-01T00:00:00Z",
		Enabled: &enabled, PriceOverride: true, PriceIn: 1.25, PriceSource: "user",
	}); err != nil {
		t.Fatal(err)
	}
	// A routed save of the same model must replace, not append.
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: id, Provider: "bedrock", Type: "embedding",
		Tier: TierUserVerified, Plane: PlaneClassic,
		TestedAt: "2026-08-01T00:00:00Z",
		Enabled:  &enabled, PriceOverride: true, PriceIn: 1.25, PriceSource: "user",
		RecommendedSimilarityThreshold: 0.33,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(globalCatalogPath())
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "id: "+id); n != 1 {
		t.Fatalf("file has %d rows for %s, want 1 (appended a routed twin):\n%s", n, id, data)
	}
	got, ok := UserCatalogEntry(ScopeGlobal, "", RouteKey{Provider: "bedrock", ID: id, Plane: PlaneClassic})
	if !ok {
		t.Fatal("routed lookup missed the upgraded pre-route row")
	}
	if got.RecommendedSimilarityThreshold != 0.33 {
		t.Errorf("threshold = %v, want 0.33", got.RecommendedSimilarityThreshold)
	}
	if got.TestedAt != "2026-08-01T00:00:00Z" || got.Enabled == nil || *got.Enabled {
		t.Errorf("stored fields lost: tested_at=%q enabled=%v", got.TestedAt, got.Enabled)
	}
}

func TestRemoveQualifiedRouteHitsPrerouteRow(t *testing.T) {
	setupHome(t)
	id := "amazon.nova-2-multimodal-embeddings-v1:0"
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: id, Provider: "bedrock", Type: "embedding", Tier: TierUserVerified,
	}); err != nil {
		t.Fatal(err)
	}
	n, err := RemoveUserCatalogEntry(ScopeGlobal, "", RouteKey{
		Provider: "bedrock", ID: id, Plane: PlaneClassic,
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("remove @classic of a pre-route row removed %d, want 1", n)
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

	out := mergeFields(base, top, true)
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
	out2 := mergeFields(base, noop, true)
	if out2.InvokeStrategy != StrategyBedrockInvokeAnthropic {
		t.Errorf("empty overlay wiped base strategy: got %q", out2.InvokeStrategy)
	}
	if out2.TestLatencyMs != 1000 {
		t.Errorf("empty overlay wiped base latency: got %d", out2.TestLatencyMs)
	}
}

// TestSaveAndLoad_RegionEndpointRoundTrip verifies the per-model Region and
// Endpoint pins survive both serialization paths: YAML through the user
// catalog (the mantle groundwork's persistence surface) and JSON (the CLI
// --json / GUI payload surface).
func TestSaveAndLoad_RegionEndpointRoundTrip(t *testing.T) {
	setupHome(t)

	entry := ModelInfo{
		ID:             "openai.gpt-5.5",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         "us-east-2",
		Endpoint:       "https://bedrock-mantle.us-east-2.api.aws",
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded := LoadUserCatalog("")
	if len(loaded) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(loaded))
	}
	m := loaded[0]
	if m.Region != "us-east-2" {
		t.Errorf("Region after yaml round-trip: got %q", m.Region)
	}
	if m.Endpoint != "https://bedrock-mantle.us-east-2.api.aws" {
		t.Errorf("Endpoint after yaml round-trip: got %q", m.Endpoint)
	}
	if m.InvokeStrategy != StrategyBedrockMantleResponses {
		t.Errorf("InvokeStrategy after yaml round-trip: got %q", m.InvokeStrategy)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var fromJSON ModelInfo
	if err := json.Unmarshal(data, &fromJSON); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if fromJSON.Region != entry.Region || fromJSON.Endpoint != entry.Endpoint {
		t.Errorf("json round-trip lost fields: region=%q endpoint=%q", fromJSON.Region, fromJSON.Endpoint)
	}

	// omitempty: an entry without the new fields must not serialize them, so
	// legacy catalogs and payloads stay byte-stable.
	plain, err := json.Marshal(ModelInfo{ID: "plain", Provider: "bedrock", Type: "generation"})
	if err != nil {
		t.Fatalf("json marshal plain: %v", err)
	}
	if strings.Contains(string(plain), "\"region\"") || strings.Contains(string(plain), "\"endpoint\"") {
		t.Errorf("empty Region/Endpoint should be omitted from json, got %s", plain)
	}
}

// TestMergeFields_RegionEndpointOverlay verifies mergeFields carries a
// non-empty Region/Endpoint from the overlay and preserves the base values
// when the overlay omits them.
func TestMergeFields_RegionEndpointOverlay(t *testing.T) {
	base := ModelInfo{
		ID: "m", Provider: "bedrock", Type: "generation",
		Region:   "us-east-1",
		Endpoint: "https://bedrock-mantle.us-east-1.api.aws",
	}
	top := ModelInfo{
		ID: "m", Provider: "bedrock",
		Region:   "us-west-2",
		Endpoint: "https://bedrock-mantle.us-west-2.api.aws",
	}
	out := mergeFields(base, top, true)
	if out.Region != "us-west-2" {
		t.Errorf("overlay region not applied: got %q", out.Region)
	}
	if out.Endpoint != "https://bedrock-mantle.us-west-2.api.aws" {
		t.Errorf("overlay endpoint not applied: got %q", out.Endpoint)
	}

	noop := ModelInfo{ID: "m", Provider: "bedrock"}
	out2 := mergeFields(base, noop, true)
	if out2.Region != "us-east-1" || out2.Endpoint != "https://bedrock-mantle.us-east-1.api.aws" {
		t.Errorf("empty overlay wiped base pins: region=%q endpoint=%q", out2.Region, out2.Endpoint)
	}
}

// TestResolveModelRegion verifies the region pin resolves through the same
// user-catalog-over-builtin chain as resolveInvokeStrategy.
func TestResolveModelRegion(t *testing.T) {
	setupHome(t)

	// Builtin pin: the Cohere reranker is us-east-1 in-region only.
	if got := resolveModelRegion("bedrock", DefaultRerankModel, ""); got != "us-east-1" {
		t.Errorf("builtin pin: got %q, want us-east-1", got)
	}

	// Unset: a builtin without a pin resolves empty (provider default).
	if got := resolveModelRegion("bedrock", "us.anthropic.claude-haiku-4-5-20251001-v1:0", ""); got != "" {
		t.Errorf("unpinned builtin should resolve empty, got %q", got)
	}
	// Unknown model: empty.
	if got := resolveModelRegion("bedrock", "not.a.known.model", ""); got != "" {
		t.Errorf("unknown model should resolve empty, got %q", got)
	}

	// User catalog entry for a model not in the builtin set.
	custom := ModelInfo{
		ID:             "xai.grok-4.3",
		Provider:       "bedrock",
		Type:           "generation",
		InvokeStrategy: StrategyBedrockMantleResponses,
		Region:         "us-west-2",
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", custom); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := resolveModelRegion("bedrock", "xai.grok-4.3", ""); got != "us-west-2" {
		t.Errorf("user entry: got %q, want us-west-2", got)
	}

	// User override of a builtin pin wins.
	override := ModelInfo{
		ID:       DefaultRerankModel,
		Provider: "bedrock",
		Type:     "rerank",
		Region:   "eu-central-1",
	}
	if err := SaveUserCatalogEntry(ScopeGlobal, "", override); err != nil {
		t.Fatalf("save override: %v", err)
	}
	if got := resolveModelRegion("bedrock", DefaultRerankModel, ""); got != "eu-central-1" {
		t.Errorf("user override of builtin pin: got %q, want eu-central-1", got)
	}
}

// TestMergeFields_TestErrorCodeMovesWithTestedAt verifies TestErrorCode moves
// as a unit with TestedAt/TestError: a failing overlay carries its code over
// the base, and a later PASSING overlay (empty code, fresh TestedAt) clears a
// stale code rather than preserving it.
func TestMergeFields_TestErrorCodeMovesWithTestedAt(t *testing.T) {
	base := ModelInfo{
		ID: "m", Provider: "bedrock", Type: "generation",
	}
	failed := ModelInfo{
		ID: "m", Provider: "bedrock",
		TestedAt:      "2026-07-01T00:00:00Z",
		TestError:     "invoke m: AccessDeniedException: not available",
		TestErrorCode: string(TestErrAccessDenied),
	}
	out := mergeFields(base, failed, true)
	if out.TestErrorCode != string(TestErrAccessDenied) {
		t.Errorf("failing overlay didn't carry code: %+v", out)
	}

	passed := ModelInfo{
		ID: "m", Provider: "bedrock",
		TestedAt:      "2026-07-02T00:00:00Z",
		TestLatencyMs: 300,
	}
	out2 := mergeFields(out, passed, true)
	if out2.TestError != "" || out2.TestErrorCode != "" {
		t.Errorf("passing overlay didn't clear stale failure: error=%q code=%q", out2.TestError, out2.TestErrorCode)
	}
	if out2.TestedAt != "2026-07-02T00:00:00Z" {
		t.Errorf("TestedAt not updated: %q", out2.TestedAt)
	}
}

// TestMergeFields_CurationBelongsToTheBuiltin replaces the old add-only rule
// (a user row could promote, never demote). Over the builtin catalog the
// builtin's Recommended and ConfigHint now win outright, because `models
// verify` used to copy the merged row's curation back into the user file: the
// mirror could keep promoting a model the catalog had since demoted, and no
// command could clear it. Between the two USER scopes the add-only rule is
// still what the vault row gets.
func TestMergeFields_CurationBelongsToTheBuiltin(t *testing.T) {
	base := ModelInfo{ID: "m", Provider: "bedrock", Type: "generation", Recommended: true, ConfigHint: "builtin hint"}
	top := ModelInfo{ID: "m", Provider: "bedrock", TestedAt: "2026-07-01T00:00:00Z"}
	if out := mergeFields(base, top, true); !out.Recommended {
		t.Error("user overlay without recommended demoted a builtin recommendation")
	}

	base2 := ModelInfo{ID: "m2", Provider: "bedrock", Type: "generation"}
	top2 := ModelInfo{ID: "m2", Provider: "bedrock", Recommended: true, ConfigHint: "stale mirror"}
	out := mergeFields(base2, top2, true)
	if out.Recommended {
		t.Error("an unstamped user row promoted a model the builtin catalog does not recommend")
	}
	if out.ConfigHint != "" {
		t.Errorf("a user row overrode the builtin ConfigHint: got %q", out.ConfigHint)
	}

	// Between user scopes there is no builtin to defer to: the vault row wins.
	if out := mergeFields(base2, top2, false); !out.Recommended || out.ConfigHint != "stale mirror" {
		t.Errorf("vault-over-global overlay lost the vault row's curation: %+v", out)
	}
}

// TestMergeFields_ModelFactsNeedTheStamp is the mirrored-facts rule: over the
// builtin catalog, Name, Dimensions and ContextLen from a user row apply only
// when the user typed them (`models add --name/--dimensions/--context-length`
// stamps FactSourceUser). An unstamped copy came FROM the merged view via some
// earlier probe save, and letting it win is how a context_length of 2048
// outlived the builtin's own correction to 8192.
func TestMergeFields_ModelFactsNeedTheStamp(t *testing.T) {
	builtin := ModelInfo{
		ID: "amazon.nova-2-multimodal-embeddings-v1:0", Provider: "bedrock", Type: "embedding",
		Name: "Amazon Nova Embeddings v2", Dimensions: 1024, ContextLen: 8192,
	}
	unstamped := ModelInfo{
		ID: builtin.ID, Provider: "bedrock",
		Name: "Nova (stale copy)", Dimensions: 384, ContextLen: 2048,
	}
	out := mergeFields(builtin, unstamped, true)
	if out.ContextLen != 8192 {
		t.Errorf("unstamped context_length won over the builtin: got %d, want 8192", out.ContextLen)
	}
	if out.Dimensions != 1024 {
		t.Errorf("unstamped dimensions won over the builtin: got %d, want 1024", out.Dimensions)
	}
	if out.Name != "Amazon Nova Embeddings v2" {
		t.Errorf("unstamped name won over the builtin: got %q", out.Name)
	}
	if out.FactSource != "" {
		t.Errorf("an ignored overlay left a stamp behind: got %q", out.FactSource)
	}

	stamped := unstamped
	stamped.FactSource = FactSourceUser
	out = mergeFields(builtin, stamped, true)
	if out.ContextLen != 2048 || out.Dimensions != 384 || out.Name != "Nova (stale copy)" {
		t.Errorf("a stamped user row did not override the builtin facts: %+v", out)
	}
	if out.FactSource != FactSourceUser {
		t.Errorf("the stamp did not travel with the facts: got %q", out.FactSource)
	}

	// Between two user scopes neither side owns the facts, so the vault row
	// still wins on any non-zero value, and the stamp travels with them.
	out = mergeFields(builtin, unstamped, false)
	if out.ContextLen != 2048 {
		t.Errorf("vault-over-global overlay dropped the vault context_length: got %d", out.ContextLen)
	}
}

// TestSaveUserCatalogEntry_PassAfterFailClearsCode verifies the write path:
// SaveUserCatalogEntry replaces the whole entry, so a passing probe result
// saved after a failure leaves no stale test_error_code in models.yaml.
func TestSaveUserCatalogEntry_PassAfterFailClearsCode(t *testing.T) {
	setupHome(t) // the ai package has no TestMain: without this the test reads the developer's real ~/.config/2nb/models.yaml
	root := t.TempDir()
	failEntry := ModelInfo{
		ID: "m1", Provider: "bedrock", Type: "generation",
		Tier:          TierUnverified,
		TestedAt:      "2026-07-01T00:00:00Z",
		TestError:     "AccessDeniedException: not available",
		TestErrorCode: string(TestErrAccessDenied),
	}
	if err := SaveUserCatalogEntry(ScopeVault, root, failEntry); err != nil {
		t.Fatalf("save fail entry: %v", err)
	}

	passEntry := ModelInfo{
		ID: "m1", Provider: "bedrock", Type: "generation",
		Tier:          TierUserVerified,
		TestedAt:      "2026-07-02T00:00:00Z",
		TestLatencyMs: 250,
	}
	if err := SaveUserCatalogEntry(ScopeVault, root, passEntry); err != nil {
		t.Fatalf("save pass entry: %v", err)
	}

	models := LoadUserCatalog(root)
	var got *ModelInfo
	for i := range models {
		if models[i].ID == "m1" {
			got = &models[i]
			break
		}
	}
	if got == nil {
		t.Fatal("entry m1 not found after save")
	}
	if got.TestError != "" || got.TestErrorCode != "" {
		t.Errorf("stale failure survived a passing save: error=%q code=%q", got.TestError, got.TestErrorCode)
	}
	if got.Tier != TierUserVerified {
		t.Errorf("tier not promoted: %q", got.Tier)
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
		// Mantle-plane models resolve to the mantle strategy — the value that
		// drives mantle-aware remediation and the GUI console-button suppression.
		{"bedrock", "xai.grok-4.3", StrategyBedrockMantleResponses},
		{"bedrock", "openai.gpt-5.5", StrategyBedrockMantleResponses},
		{"openrouter", "anthropic/claude-sonnet-4-6", StrategyOpenRouterChat},
		{"ollama", "nomic-embed-text", StrategyOllamaEmbeddings},
		{"bedrock", "not.a.known.model", ""},
	}
	for _, tc := range cases {
		t.Run(tc.provider+":"+tc.modelID, func(t *testing.T) {
			got := resolveInvokeStrategy(tc.provider, tc.modelID, "")
			if got != tc.want {
				t.Errorf("resolveInvokeStrategy(%q,%q) = %q, want %q", tc.provider, tc.modelID, got, tc.want)
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
	if got := resolveInvokeStrategy("openrouter", "vendor/custom-gen-v9", ""); got != StrategyOpenRouterChat {
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
	if got := resolveInvokeStrategy("bedrock", "us.anthropic.claude-haiku-4-5-20251001-v1:0", ""); got != StrategyBedrockInvokeAnthropic {
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
