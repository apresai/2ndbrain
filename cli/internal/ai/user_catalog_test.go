package ai

import (
	"os"
	"path/filepath"
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

// Regression: a user catalog entry marked as "free" (price_in=0) with an
// explicit price_source="user" must override a non-zero builtin price.
func TestOverlay_ExplicitZeroPriceWinsWhenPriceSourceSet(t *testing.T) {
	base := []ModelInfo{
		{ID: "pricey", Provider: "bedrock", PriceIn: 10.0, PriceSource: "builtin"},
	}
	top := []ModelInfo{
		{ID: "pricey", Provider: "bedrock", PriceIn: 0, PriceSource: "user", Tier: TierUserVerified},
	}
	out := overlay(base, top)
	if out[0].PriceIn != 0 {
		t.Fatalf("explicit price_source=user with price=0 should win, got %v", out[0].PriceIn)
	}
	if out[0].PriceSource != "user" {
		t.Fatalf("expected price_source=user, got %q", out[0].PriceSource)
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
