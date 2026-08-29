package ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// setupDiscoveryCacheDir sandboxes the discovery cache dir (and HOME, so the
// bedrock machine file and Keychain can't leak in) and returns the directory
// discoveryCacheDir() will resolve to.
func setupDiscoveryCacheDir(t *testing.T) string {
	t.Helper()
	setupHome(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv(bedrockBearerTokenEnv, "")
	t.Setenv(bedrockSkipKeychainEnv, "1")
	return filepath.Join(cache, "2nb", "discovery")
}

func writeSyntheticDiscoveryCache(t *testing.T, path, region string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cachedDiscovery{
		Version: DiscoveryCacheVersion,
		Region:  region,
		Models:  []ModelInfo{{ID: "fake." + region, Provider: "bedrock", Type: "generation", Tier: TierUnverified}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverySeenRoundTrip(t *testing.T) {
	setupDiscoveryCacheDir(t)
	cfg := BedrockConfig{}

	// Missing baseline is the first-run signal, not an error.
	seen, err := LoadDiscoverySeen(cfg)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if seen != nil {
		t.Fatalf("missing baseline must load as nil, got %+v", seen)
	}

	// Save with duplicates, empties, and unsorted order; load must come back
	// sorted and deduped with the version stamp.
	if err := SaveDiscoverySeen(cfg, []string{"bedrock|zeta", "bedrock|alpha", "", "bedrock|zeta", "ollama|m1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	seen, err = LoadDiscoverySeen(cfg)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if seen == nil {
		t.Fatal("saved baseline did not load")
	}
	want := []string{"bedrock|alpha", "bedrock|zeta", "ollama|m1"}
	if !reflect.DeepEqual(seen.Keys, want) {
		t.Fatalf("keys = %v, want sorted deduped %v", seen.Keys, want)
	}
	if seen.Version != discoverySeenVersion {
		t.Fatalf("version = %d, want %d", seen.Version, discoverySeenVersion)
	}
	if seen.SavedAt == "" {
		t.Fatal("SavedAt not stamped")
	}
	if _, err := time.Parse(time.RFC3339, seen.SavedAt); err != nil {
		t.Fatalf("SavedAt %q is not RFC3339: %v", seen.SavedAt, err)
	}
}

func TestDiscoverySeen_ProfileKeyedFileName(t *testing.T) {
	dir := setupDiscoveryCacheDir(t)
	cfg := BedrockConfig{Profile: "Work/Acct"}
	if err := SaveDiscoverySeen(cfg, []string{"bedrock|m"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// The profile is sanitized into the filename so it can't escape the dir.
	if _, err := os.Stat(filepath.Join(dir, "discovery-seen-bedrock-work_acct.json")); err != nil {
		t.Fatalf("profile-keyed seen file missing: %v", err)
	}
	// A different profile has its own baseline.
	other, err := LoadDiscoverySeen(BedrockConfig{})
	if err != nil {
		t.Fatalf("load default profile: %v", err)
	}
	if other != nil {
		t.Fatalf("default profile must have no baseline, got %+v", other)
	}
}

func TestLoadDiscoverySeen_CorruptAndVersionMismatchReseed(t *testing.T) {
	dir := setupDiscoveryCacheDir(t)
	cfg := BedrockConfig{}
	path := filepath.Join(dir, "discovery-seen-bedrock-default.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if seen, err := LoadDiscoverySeen(cfg); err != nil || seen != nil {
		t.Fatalf("corrupt baseline must load as (nil, nil), got (%+v, %v)", seen, err)
	}

	if err := os.WriteFile(path, []byte(`{"version":99,"keys":["bedrock|m"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if seen, err := LoadDiscoverySeen(cfg); err != nil || seen != nil {
		t.Fatalf("version-mismatched baseline must load as (nil, nil), got (%+v, %v)", seen, err)
	}
}

func TestDiffAgainstSeen_FirstRunSeedsSilently(t *testing.T) {
	current := []ModelInfo{
		{ID: "xai.grok-4.6", Provider: "bedrock"},
		{ID: "openai.gpt-5.5", Provider: "bedrock"},
	}
	d := DiffAgainstSeen(current, nil, nil, nil)
	if !d.FirstRun {
		t.Fatal("nil baseline must report FirstRun")
	}
	if len(d.New) != 0 || len(d.Gone) != 0 {
		t.Fatalf("first run must badge nothing: new=%v gone=%v", d.New, d.Gone)
	}
	if d.New == nil || d.Gone == nil {
		t.Fatal("New/Gone must be non-nil so JSON emits [] not null")
	}
}

func TestDiffAgainstSeen_NewAndGone(t *testing.T) {
	seen := &DiscoverySeen{Version: discoverySeenVersion, Keys: []string{"bedrock|old-model", "bedrock|kept-model"}}
	current := []ModelInfo{
		{ID: "kept-model", Provider: "bedrock"},
		{ID: "brand-new", Provider: "bedrock"},
	}
	d := DiffAgainstSeen(current, nil, seen, nil)
	if d.FirstRun {
		t.Fatal("existing baseline must not report FirstRun")
	}
	if len(d.New) != 1 || d.New[0].ID != "brand-new" {
		t.Fatalf("New = %+v, want exactly brand-new", d.New)
	}
	if len(d.Gone) != 1 || d.Gone[0] != "bedrock|old-model" {
		t.Fatalf("Gone = %v, want exactly bedrock|old-model", d.Gone)
	}
}

func TestDiffAgainstSeen_FailedProviderShieldsGone(t *testing.T) {
	seen := &DiscoverySeen{Version: discoverySeenVersion, Keys: []string{
		"ollama|local-model", "bedrock|delisted-model",
	}}
	current := []ModelInfo{{ID: "fresh", Provider: "bedrock"}}
	failed := map[string]bool{"ollama": true}
	d := DiffAgainstSeen(current, nil, seen, failed)
	if len(d.Gone) != 1 || d.Gone[0] != "bedrock|delisted-model" {
		t.Fatalf("Gone = %v: a failed source's keys are unknown, not gone; a succeeded source's absent key is gone", d.Gone)
	}
	if len(d.New) != 1 || d.New[0].ID != "fresh" {
		t.Fatalf("New = %+v, want exactly fresh", d.New)
	}
}

func TestDiffAgainstSeen_AdoptedModelIsNotGone(t *testing.T) {
	// A seen model absent from the pool because it graduated into the merged
	// catalog (via --add or a verify save) was adopted, not delisted: without
	// the catalog exclusion, every --add would badge its own model GONE on
	// the very next run.
	seen := &DiscoverySeen{Version: discoverySeenVersion, Keys: []string{
		"bedrock|xai.grok-4.7", "bedrock|truly-delisted",
	}}
	catalog := []ModelInfo{{ID: "xai.grok-4.7", Provider: "bedrock"}}
	d := DiffAgainstSeen(nil, catalog, seen, nil)
	if len(d.Gone) != 1 || d.Gone[0] != "bedrock|truly-delisted" {
		t.Fatalf("Gone = %v, want only the truly delisted key (the adopted one graduated)", d.Gone)
	}
	if len(d.New) != 0 {
		t.Fatalf("catalog rows must never be badged NEW, got %+v", d.New)
	}
}

func TestNextSeenKeys_CarriesFailedProvidersForward(t *testing.T) {
	seen := &DiscoverySeen{Version: discoverySeenVersion, Keys: []string{
		"ollama|local-model", "bedrock|delisted-model",
	}}
	current := []ModelInfo{{ID: "fresh", Provider: "bedrock"}}
	got := NextSeenKeys(current, seen, map[string]bool{"ollama": true})
	want := []string{"bedrock|fresh", "ollama|local-model"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NextSeenKeys = %v, want %v (failed provider carried, succeeded provider's delisted key dropped)", got, want)
	}

	// First run: just the current keys.
	got = NextSeenKeys(current, nil, map[string]bool{"ollama": true})
	if !reflect.DeepEqual(got, []string{"bedrock|fresh"}) {
		t.Fatalf("first-run NextSeenKeys = %v, want just the current keys", got)
	}
}

func TestFailedDiscoveryProviders(t *testing.T) {
	warnings := []string{
		"bedrock discovery failed: expired token",
		"bedrock-mantle discovery failed: 401",
		"openrouter discovery failed: API key not configured",
		"policy: unknown vendor slug",
		"model xai.grok discovery failed somewhere else entirely",
	}
	got := FailedDiscoveryProviders(warnings)
	want := map[string]bool{"bedrock": true, "openrouter": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FailedDiscoveryProviders = %v, want %v (mantle folds into bedrock; non-discovery warnings ignored)", got, want)
	}
}

func TestDiscoveryCacheAges_SyntheticFiles(t *testing.T) {
	dir := setupDiscoveryCacheDir(t)
	cfg := BedrockConfig{}

	// Classic us-east-1 (the resolved primary): fetched 2h ago: fresh.
	writeSyntheticDiscoveryCache(t, filepath.Join(dir, "bedrock-us-east-1-default.json"),
		"us-east-1", time.Now().Add(-2*time.Hour))
	// Mantle us-west-2: fetched 30h ago: past the 24h TTL, stale. With no
	// bearer token, mantle rows appear only for existing cache files, so this
	// is the single mantle row expected.
	writeSyntheticDiscoveryCache(t, filepath.Join(dir, "bedrock-mantle-us-west-2-default.json"),
		"us-west-2", time.Now().Add(-30*time.Hour))

	ages := DiscoveryCacheAges(cfg)

	var classic, mantle *DiscoverySourceAge
	mantleRows := 0
	for i := range ages {
		switch {
		case ages[i].Source == "classic" && ages[i].Region == "us-east-1":
			classic = &ages[i]
		case ages[i].Source == "mantle":
			mantleRows++
			if ages[i].Region == "us-west-2" {
				mantle = &ages[i]
			}
		}
	}

	if classic == nil {
		t.Fatalf("no classic us-east-1 row in %+v", ages)
	}
	if !classic.Exists || classic.Stale {
		t.Fatalf("classic row = %+v, want exists and fresh", *classic)
	}
	if classic.AgeSeconds < 7100 || classic.AgeSeconds > 7300 {
		t.Fatalf("classic age = %ds, want ~7200 (mtime is the fetch time)", classic.AgeSeconds)
	}
	if _, err := time.Parse(time.RFC3339, classic.FetchedAt); err != nil {
		t.Fatalf("classic FetchedAt %q is not RFC3339: %v", classic.FetchedAt, err)
	}

	if mantle == nil {
		t.Fatalf("no mantle us-west-2 row in %+v", ages)
	}
	if !mantle.Exists || !mantle.Stale {
		t.Fatalf("mantle row = %+v, want exists and stale (30h > 24h TTL)", *mantle)
	}
	// SigV4-only setup (no token): mantle regions without a cache file are
	// omitted, not reported as missing: the plane is unreachable, so
	// "missing" would read as a defect.
	if mantleRows != 1 {
		t.Fatalf("expected exactly 1 mantle row (only the cached region) without a bearer token, got %d in %+v", mantleRows, ages)
	}
}

func TestDiscoveryCacheAges_TokenRevealsUncachedMantleRegions(t *testing.T) {
	setupDiscoveryCacheDir(t)
	t.Setenv(bedrockBearerTokenEnv, "ABSK-test-dummy-token")

	ages := DiscoveryCacheAges(BedrockConfig{})
	mantleRegions := map[string]bool{}
	for _, a := range ages {
		if a.Source == "mantle" {
			mantleRegions[a.Region] = true
			if a.Exists {
				t.Fatalf("no cache files were written, but %+v reports Exists", a)
			}
		}
	}
	for _, r := range bedrockDiscoveryRegions {
		if !mantleRegions[r] {
			t.Fatalf("with a bearer token every documented mantle region must be reported; missing %s in %+v", r, ages)
		}
	}
	// The classic primary region row is always present, cached or not.
	if ages[0].Source != "classic" || ages[0].Exists {
		t.Fatalf("first row should be the uncached classic primary, got %+v", ages[0])
	}
}

func TestInvalidateDiscoveryCache_RemovesCachesKeepsBaseline(t *testing.T) {
	dir := setupDiscoveryCacheDir(t)
	cfg := BedrockConfig{}

	classicPath := filepath.Join(dir, "bedrock-us-east-1-default.json")
	mantlePath := filepath.Join(dir, "bedrock-mantle-us-east-2-default.json")
	writeSyntheticDiscoveryCache(t, classicPath, "us-east-1", time.Now())
	writeSyntheticDiscoveryCache(t, mantlePath, "us-east-2", time.Now())
	if err := SaveDiscoverySeen(cfg, []string{"bedrock|m"}); err != nil {
		t.Fatal(err)
	}

	removed, err := InvalidateDiscoveryCache(cfg)
	if err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %v, want both cache files", removed)
	}
	for _, p := range []string{classicPath, mantlePath} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Fatalf("cache file %s survived invalidation", p)
		}
	}
	// The seen baseline is the diff baseline, not a cache: it must survive.
	if seen, loadErr := LoadDiscoverySeen(cfg); loadErr != nil || seen == nil {
		t.Fatalf("seen baseline must survive invalidation, got (%+v, %v)", seen, loadErr)
	}

	// Idempotent: nothing left to remove, still no error.
	removed, err = InvalidateDiscoveryCache(cfg)
	if err != nil || len(removed) != 0 {
		t.Fatalf("second invalidate = (%v, %v), want (none, nil)", removed, err)
	}
}

// TestFailedDiscoveryProviders_PartialRegionWarning pins that a PARTIAL
// region failure (some regions listed, some did not) engages the GONE/seen
// shield exactly like a total failure: the region loops emit the recognized
// "<source> discovery failed" shape for partial listings, because a silently
// missing region would report its exclusive models GONE and drop them from
// the seen baseline (per-region mantle catalogs genuinely differ).
func TestFailedDiscoveryProviders_PartialRegionWarning(t *testing.T) {
	warnings := []string{
		"bedrock-mantle discovery failed: partial listing: region(s) us-west-2 failed: connection reset",
	}
	failed := FailedDiscoveryProviders(warnings)
	if !failed["bedrock"] {
		t.Fatalf("partial mantle region failure did not engage the bedrock shield: %v", failed)
	}
	classic := FailedDiscoveryProviders([]string{
		"bedrock discovery failed: partial listing: region(s) us-east-2 failed: timeout",
	})
	if !classic["bedrock"] {
		t.Fatalf("partial classic region failure did not engage the shield: %v", classic)
	}
}
