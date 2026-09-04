package ai

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDiscoverySkipsDisabledProviders: a provider the user has silenced costs
// nothing and says nothing. filterDisabledProviders already dropped every row
// such a provider could contribute, so walking it was pure cost, and the walk
// warned about it every time: one real machine logged 19 "vendor discovery
// failed provider=openrouter" warnings in a day, one per Models-tab refresh,
// for a provider the user had deliberately turned off.
//
// All three providers are disabled here, so the test makes no network call at
// all and is safe in credential-free CI. Before the fix this same call warns
// (and tries to reach each provider), which is what the proof run shows.
func TestDiscoverySkipsDisabledProviders(t *testing.T) {
	setupDiscoveryCacheDir(t)

	cfg := AIConfig{}
	cfg.SetProviderDisabled("bedrock", true)
	cfg.SetProviderDisabled("openrouter", true)
	cfg.SetProviderDisabled("ollama", true)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, warnings := discoverVendorModels(ctx, cfg, true, false)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none: a provider the user turned off is not a failure", warnings)
	}
	if len(models) != 0 {
		t.Errorf("models = %d rows, want 0: a disabled provider contributes nothing", len(models))
	}
}

// TestDiscoveryIncludesDisabledProvidersForTheWizard: the setup wizard is the
// one surface where a disabled provider must still appear, since it is where an
// opt-in provider gets turned ON. Proven against the SEEDED cache so no vendor
// API is called.
func TestDiscoveryIncludesDisabledProvidersForTheWizard(t *testing.T) {
	setupDiscoveryCacheDir(t)

	cfg := AIConfig{}
	cfg.SetProviderDisabled("bedrock", true)
	cfg.SetProviderDisabled("openrouter", true)
	cfg.SetProviderDisabled("ollama", true)
	seedClassicDiscoveryCache(t, cfg.Bedrock)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, _ := discoverVendorModels(ctx, cfg, true, true)
	if !hasSyntheticCachedRow(models) {
		t.Errorf("the wizard's include-disabled walk returned %d rows and none of them cached; a disabled provider must still be reachable there", len(models))
	}
}

// TestDiscoverServesTheSeededCache is the positive form of "no network call":
// with a warm cache, a cached discovery returns exactly what the cache holds.
// This is the read `models list --discover` now takes by default, which is what
// makes the macOS app's Models tab stop re-walking both Bedrock planes across
// three regions on every reload (about 11 seconds, measured).
func TestDiscoverServesTheSeededCache(t *testing.T) {
	setupDiscoveryCacheDir(t)

	cfg := AIConfig{}
	// Only Bedrock stays enabled: the other two would reach the network and
	// this test is about the Bedrock cache.
	cfg.SetProviderDisabled("openrouter", true)
	cfg.SetProviderDisabled("ollama", true)
	seedClassicDiscoveryCache(t, cfg.Bedrock)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, warnings := discoverVendorModels(ctx, cfg, true, false)
	if !hasSyntheticCachedRow(models) {
		t.Fatalf("cached discovery returned %d rows, none from the seeded cache (warnings: %v)", len(models), warnings)
	}
	for _, w := range warnings {
		if strings.Contains(w, "openrouter") || strings.Contains(w, "ollama") {
			t.Errorf("warning from a disabled provider: %q", w)
		}
	}
}

// seedClassicDiscoveryCache writes a fresh synthetic cache entry for every
// classic region the walk covers, so a cached discovery has something to serve.
func seedClassicDiscoveryCache(t *testing.T, bcfg BedrockConfig) {
	t.Helper()
	regions := BedrockDiscoveryRegions(bcfg)
	if len(regions) == 0 {
		t.Fatal("no classic discovery regions; the seed would be a no-op")
	}
	for _, region := range regions {
		path, err := classicDiscoveryCachePathForRegion(bcfg, region)
		if err != nil {
			t.Fatalf("cache path for %s: %v", region, err)
		}
		writeSyntheticDiscoveryCache(t, path, region, time.Now())
	}
}

// hasSyntheticCachedRow reports whether the seeded row survived to the result.
// writeSyntheticDiscoveryCache names its row "fake.<region>".
func hasSyntheticCachedRow(models []ModelInfo) bool {
	for _, m := range models {
		if strings.HasPrefix(m.ID, "fake.") {
			return true
		}
	}
	return false
}
