package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// discoverReportView decodes the `models discover --json` envelope in tests.
type discoverReportView struct {
	Sources []struct {
		Source string `json:"source"`
		Region string `json:"region"`
		Exists bool   `json:"exists"`
		Stale  bool   `json:"stale"`
	} `json:"sources"`
	Models []struct {
		ID             string `json:"id"`
		Provider       string `json:"provider"`
		InvokeStrategy string `json:"invoke_strategy"`
		Region         string `json:"region"`
	} `json:"models"`
	New []struct {
		ID string `json:"id"`
	} `json:"new"`
	Gone     []string `json:"gone"`
	FirstRun bool     `json:"first_run"`
	Added    []string `json:"added"`
}

// seedDiscoverCaches writes fresh synthetic discovery cache entries, one
// classic us-east-1 listing and one mantle listing per documented mantle
// region, under a per-test XDG_CACHE_HOME, and sets a dummy bearer token so
// the mantle discovery goroutine runs (it skips entirely without one). Every
// region the cached read-through consults is seeded fresh, so the whole
// discover walk is served from disk and no test ever calls AWS.
func seedDiscoverCaches(t *testing.T, mantleUSEast1Models []ai.ModelInfo) string {
	t.Helper()
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "ABSK-contract-test-dummy")
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")
	t.Setenv("OPENROUTER_API_KEY", "")

	dir := filepath.Join(cache, "2nb", "discovery")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, region string, models []ai.ModelInfo) {
		t.Helper()
		data, err := json.Marshal(struct {
			Version int            `json:"version"`
			Region  string         `json:"region"`
			Models  []ai.ModelInfo `json:"models"`
		}{ai.DiscoveryCacheVersion, region, models})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Rows carry their full ROUTE, matching what a live listing now produces.
	write("bedrock-us-east-1-default.json", "us-east-1", []ai.ModelInfo{{
		ID: "fake.classic-model-v1", Provider: "bedrock", Type: "generation", Tier: ai.TierUnverified,
		Plane: ai.PlaneClassic, Region: "us-east-1",
	}})
	write("bedrock-mantle-us-east-1-default.json", "us-east-1", mantleUSEast1Models)
	write("bedrock-mantle-us-east-2-default.json", "us-east-2", []ai.ModelInfo{
		fakeMantleRowIn("fake.mantle-east2-filler", "us-east-2"),
	})
	write("bedrock-mantle-us-west-2-default.json", "us-west-2", []ai.ModelInfo{
		fakeMantleRowIn("fake.mantle-west2-filler", "us-west-2"),
	})
	return dir
}

func fakeMantleRow(id string) ai.ModelInfo { return fakeMantleRowIn(id, "us-east-1") }

func fakeMantleRowIn(id, region string) ai.ModelInfo {
	return ai.ModelInfo{
		ID: id, Provider: "bedrock", Type: "generation", Tier: ai.TierUnverified,
		Plane: ai.PlaneMantle, InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: region,
	}
}

func discoverJSON(t *testing.T, root string, extra ...string) discoverReportView {
	t.Helper()
	args := append([]string{"models", "discover"}, extra...)
	args = append(args, "--json", "--porcelain")
	got, err := runCLIArgs(t, root, args...)
	if err != nil {
		t.Fatalf("models discover: %v (out=%s)", err, truncate(got, 500))
	}
	var report discoverReportView
	if err := json.Unmarshal(got, &report); err != nil {
		t.Fatalf("report parse: %v (body=%s)", err, truncate(got, 500))
	}
	return report
}

// TestContract_ModelsDiscoverValidateRequiresAdd pins the flag guard:
// --validate probes what --add names, so alone it is a usage error.
func TestContract_ModelsDiscoverValidateRequiresAdd(t *testing.T) {
	_, root := newContractVault(t)
	_, err := runCLIArgs(t, root, "models", "discover", "--validate")
	if err == nil {
		t.Fatal("expected --validate without --add to error")
	}
	if !strings.Contains(err.Error(), "--add") {
		t.Fatalf("error should point at --add, got: %v", err)
	}
}

// TestContract_ModelsDiscoverSeededCache drives the full discover flow with
// every listing served from seeded cache files (zero AWS calls): first-run
// silent seed, steady-state no-changes, a NEW arrival, --add persisting
// routing, and the adopted model NOT reported GONE afterwards.
func TestContract_ModelsDiscoverSeededCache(t *testing.T) {
	_, root := newContractVault(t)
	cacheDir := seedDiscoverCaches(t, []ai.ModelInfo{fakeMantleRow("fake.mantle-model-v1")})

	find := func(r discoverReportView, id string) (struct {
		ID             string `json:"id"`
		Provider       string `json:"provider"`
		InvokeStrategy string `json:"invoke_strategy"`
		Region         string `json:"region"`
	}, bool) {
		for _, m := range r.Models {
			if m.ID == id {
				return m, true
			}
		}
		var zero struct {
			ID             string `json:"id"`
			Provider       string `json:"provider"`
			InvokeStrategy string `json:"invoke_strategy"`
			Region         string `json:"region"`
		}
		return zero, false
	}

	// First run: the pool comes from the seeded caches, the baseline seeds
	// silently (no NEW badge), and the source ages report the fresh files.
	first := discoverJSON(t, root)
	if !first.FirstRun {
		t.Fatal("first run must report first_run")
	}
	if len(first.New) != 0 || len(first.Gone) != 0 {
		t.Fatalf("first run must badge nothing: new=%v gone=%v", first.New, first.Gone)
	}
	m, ok := find(first, "fake.mantle-model-v1")
	if !ok {
		t.Fatalf("seeded mantle model missing from the pool: %+v", first.Models)
	}
	if m.InvokeStrategy != ai.StrategyBedrockMantleResponses || m.Region != "us-east-1" {
		t.Fatalf("discovered mantle row lost its routing hints: %+v", m)
	}
	if _, ok := find(first, "fake.classic-model-v1"); !ok {
		t.Fatalf("seeded classic model missing from the pool: %+v", first.Models)
	}
	classicFresh := false
	mantleRows := 0
	for _, s := range first.Sources {
		if s.Source == "classic" && s.Region == "us-east-1" && s.Exists && !s.Stale {
			classicFresh = true
		}
		if s.Source == "mantle" {
			mantleRows++
		}
	}
	if !classicFresh {
		t.Fatalf("classic us-east-1 source should report a fresh cache: %+v", first.Sources)
	}
	if mantleRows != 3 {
		t.Fatalf("expected all 3 documented mantle regions reported with a token set, got %d: %+v", mantleRows, first.Sources)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "discovery-seen-bedrock-default.json")); err != nil {
		t.Fatalf("first run must save the seen baseline: %v", err)
	}

	// Steady state: nothing changed, nothing badged.
	second := discoverJSON(t, root)
	if second.FirstRun || len(second.New) != 0 || len(second.Gone) != 0 {
		t.Fatalf("steady-state run must badge nothing: first_run=%v new=%v gone=%v",
			second.FirstRun, second.New, second.Gone)
	}

	// A new model appears in the mantle listing: exactly it is badged NEW.
	seedDiscoverCaches(t, []ai.ModelInfo{
		fakeMantleRow("fake.mantle-model-v1"),
		fakeMantleRow("fake.mantle-model-v2"),
	})
	// Re-seeding replaced XDG_CACHE_HOME with a fresh dir, wiping the seen
	// baseline; re-run once to re-seed it, then diff.
	reseeded := discoverJSON(t, root)
	if !reseeded.FirstRun {
		t.Fatal("fresh cache dir means a fresh baseline: expected first_run")
	}
	// Drop v2 from the listing and re-add it, against the SAME cache dir.
	dir := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "2nb", "discovery")
	writeListing := func(models []ai.ModelInfo) {
		data, err := json.Marshal(struct {
			Version int            `json:"version"`
			Region  string         `json:"region"`
			Models  []ai.ModelInfo `json:"models"`
		}{ai.DiscoveryCacheVersion, "us-east-1", models})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bedrock-mantle-us-east-1-default.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeListing([]ai.ModelInfo{fakeMantleRow("fake.mantle-model-v1")})
	afterDrop := discoverJSON(t, root)
	if len(afterDrop.Gone) != 1 || afterDrop.Gone[0] != "bedrock|fake.mantle-model-v2" {
		t.Fatalf("delisted model must be reported GONE: %+v", afterDrop.Gone)
	}
	writeListing([]ai.ModelInfo{fakeMantleRow("fake.mantle-model-v1"), fakeMantleRow("fake.mantle-model-v2")})
	afterReturn := discoverJSON(t, root)
	if len(afterReturn.New) != 1 || afterReturn.New[0].ID != "fake.mantle-model-v2" {
		t.Fatalf("returning model must be badged NEW: %+v", afterReturn.New)
	}

	// --add persists the discovered routing into the vault catalog: after
	// this, plain resolution routes the id over the mantle plane, the
	// durable fix for "explicit mantle ids silently classic-probe".
	added := discoverJSON(t, root, "--add", "fake.mantle-model-v1")
	if len(added.Added) != 1 || added.Added[0] != "fake.mantle-model-v1" {
		t.Fatalf("added = %v, want exactly fake.mantle-model-v1", added.Added)
	}
	entry, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{Provider: "bedrock", ID: "fake.mantle-model-v1", Plane: ai.PlaneMantle, Region: "us-east-1"})
	if !ok {
		t.Fatal("--add did not persist a vault catalog entry")
	}
	if entry.InvokeStrategy != ai.StrategyBedrockMantleResponses {
		t.Fatalf("persisted invoke_strategy = %q, want the mantle strategy", entry.InvokeStrategy)
	}
	if entry.Region != "us-east-1" {
		t.Fatalf("persisted region = %q, want the listing region", entry.Region)
	}
	if entry.Tier != ai.TierUnverified {
		t.Fatalf("persisted tier = %q, want unverified until a probe passes", entry.Tier)
	}
	if entry.Enabled != nil {
		t.Fatalf("--add must not persist an Enabled override (policy verdicts stay policy-owned), got %v", *entry.Enabled)
	}

	// The adopted model left the pool by graduating into the catalog: it
	// must NOT be reported GONE, and the run must not error.
	afterAdd := discoverJSON(t, root)
	if len(afterAdd.Gone) != 0 {
		t.Fatalf("adopted model must not be badged GONE: %+v", afterAdd.Gone)
	}
	if _, stillPooled := find(afterAdd, "fake.mantle-model-v1"); stillPooled {
		t.Fatal("added model should have graduated out of the discovered pool")
	}
}

// TestContract_ModelsDiscoverHumanOutput pins the human rendering: source
// ages in the "source region: age" shape, the pool table, the first-run
// seeding note, and the NEW badge with its --add hint on a later arrival.
func TestContract_ModelsDiscoverHumanOutput(t *testing.T) {
	_, root := newContractVault(t)
	seedDiscoverCaches(t, []ai.ModelInfo{fakeMantleRow("fake.mantle-model-v1")})

	out, err := runCLIArgs(t, root, "models", "discover")
	if err != nil {
		t.Fatalf("models discover: %v (out=%s)", err, truncate(out, 500))
	}
	for _, want := range []string{
		"Discovery sources (24h cache):",
		"classic us-east-1: just now",
		"mantle us-east-1: just now",
		"fake.mantle-model-v1",
		"mantle us-east-1", // the ROUTE column for the hint-carrying row
		"First run: recorded",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}

	// A new arrival is badged with the --add hint.
	dir := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "2nb", "discovery")
	data, err := json.Marshal(struct {
		Version int            `json:"version"`
		Region  string         `json:"region"`
		Models  []ai.ModelInfo `json:"models"`
	}{ai.DiscoveryCacheVersion, "us-east-1", []ai.ModelInfo{
		fakeMantleRow("fake.mantle-model-v1"), fakeMantleRow("fake.mantle-model-v2"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bedrock-mantle-us-east-1-default.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runCLIArgs(t, root, "models", "discover")
	if err != nil {
		t.Fatalf("second discover: %v (out=%s)", err, truncate(out, 500))
	}
	for _, want := range []string{
		"NEW since last check (1):",
		"fake.mantle-model-v2",
		"--add <id> --validate",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("NEW badge output missing %q:\n%s", want, out)
		}
	}
}

// TestContract_ModelsDiscoverAddErrors pins the two --add refusals: an id
// nowhere in the listing, and an id already in the merged catalog.
func TestContract_ModelsDiscoverAddErrors(t *testing.T) {
	_, root := newContractVault(t)
	seedDiscoverCaches(t, []ai.ModelInfo{fakeMantleRow("fake.mantle-model-v1")})

	_, err := runCLIArgs(t, root, "models", "discover", "--add", "no.such-model")
	if err == nil || !strings.Contains(err.Error(), "not in the discovered pool") {
		t.Fatalf("unknown id must refuse with guidance, got: %v", err)
	}

	// openai.gpt-5.5 is a builtin catalog entry (mantle plane), so it can
	// never be "added from discovery": the catalog already routes it.
	_, err = runCLIArgs(t, root, "models", "discover", "--add", "openai.gpt-5.5")
	if err == nil || !strings.Contains(err.Error(), "already in the catalog") {
		t.Fatalf("catalog id must refuse with the verify hint, got: %v", err)
	}

	// The provider-qualified form adds exactly its row end-to-end.
	report := discoverJSON(t, root, "--add", "bedrock|fake.mantle-model-v1")
	if len(report.Added) != 1 || report.Added[0] != "fake.mantle-model-v1" {
		t.Fatalf("qualified --add must persist the row: %+v", report.Added)
	}
}

// TestDiscoverMatchAddID pins the --add resolution rules: a bare id unique
// in the pool resolves; a bare id two providers both list returns BOTH
// matches (the command refuses, never first-match-wins); a provider-
// qualified "provider|id" resolves exactly its row.
func TestDiscoverMatchAddID(t *testing.T) {
	pool := []ai.ModelInfo{
		{ID: "foo", Provider: "openrouter"},
		{ID: "foo", Provider: "ollama"},
		{ID: "bar", Provider: "bedrock"},
	}
	if got := discoverMatchAddID(pool, "bar"); len(got) != 1 || got[0].Provider != "bedrock" {
		t.Fatalf("unique bare id must resolve: %+v", got)
	}
	if got := discoverMatchAddID(pool, "foo"); len(got) != 2 {
		t.Fatalf("cross-provider bare id must return both matches for refusal: %+v", got)
	}
	if got := discoverMatchAddID(pool, "ollama|foo"); len(got) != 1 || got[0].Provider != "ollama" {
		t.Fatalf("qualified id must resolve exactly its provider's row: %+v", got)
	}
	if got := discoverMatchAddID(pool, "bedrock|foo"); len(got) != 0 {
		t.Fatalf("qualified id with no matching provider row must not match: %+v", got)
	}
}

// TestDiscoverBaselineSavable_UnrelatedWarningsDoNotBlock pins the seen-
// baseline save gate: it keys on classified DISCOVERY failures, never on
// BuildModelList warnings wholesale. An empty pool plus only non-discovery
// warnings (a vendor-policy active-model note, a quarantined policy file)
// must still save, so first-run seeding happens and a graduated-empty pool
// can never leave GONE badges sticky. The saved baseline is verified on disk
// through the same ai functions the command calls.
func TestDiscoverBaselineSavable_UnrelatedWarningsDoNotBlock(t *testing.T) {
	unrelated := []string{
		`vendor policy (bedrock): active generation model us.anthropic.claude-haiku-4-5-20251001-v1:0 stays enabled although vendor "anthropic" is not in the enable-only list`,
		"vendor policy file /x/models-policy.yaml was unreadable and is inactive (a malformed file is quarantined to /x/models-policy.yaml.bak); re-apply it with `2nb models policy set`",
	}
	failed := ai.FailedDiscoveryProviders(unrelated)
	if len(failed) != 0 {
		t.Fatalf("unrelated warnings misclassified as discovery failures: %v", failed)
	}
	if !discoverBaselineSavable(true, nil, failed) {
		t.Fatal("first-run empty pool with only unrelated warnings must seed the baseline")
	}

	// The savable verdict seeds a first run on disk: the exact save sequence
	// the command performs, under a sandboxed XDG cache dir.
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	cfg := ai.BedrockConfig{}
	seen, err := ai.LoadDiscoverySeen(cfg)
	if err != nil || seen != nil {
		t.Fatalf("fresh cache dir must have no baseline, got (%+v, %v)", seen, err)
	}
	if err := ai.SaveDiscoverySeen(cfg, ai.NextSeenKeys(nil, seen, failed)); err != nil {
		t.Fatalf("save: %v", err)
	}
	seeded, err := ai.LoadDiscoverySeen(cfg)
	if err != nil || seeded == nil {
		t.Fatalf("baseline did not seed: (%+v, %v)", seeded, err)
	}
	if d := ai.DiffAgainstSeen(nil, nil, seeded, nil); d.FirstRun {
		t.Fatal("a seeded baseline must end first-run mode")
	}
}

// TestDiscoverBaselineGate_DiscoveryFailureBlocksAndCarries pins the kept
// conservatism around the gate: a partial-region "<source> discovery failed"
// warning still blocks an empty-pool save, and when a save does proceed
// (non-empty pool) the failed source's keys ride forward through NextSeenKeys
// and stay shielded from GONE, while a succeeded source's genuinely delisted
// key drops and is badged.
func TestDiscoverBaselineGate_DiscoveryFailureBlocksAndCarries(t *testing.T) {
	warnings := []string{
		"bedrock-mantle discovery failed: partial listing: region(s) us-west-2 failed: connection reset",
		`vendor policy (bedrock): active generation model m stays enabled although vendor "x" is not in the enable-only list`,
	}
	failed := ai.FailedDiscoveryProviders(warnings)
	if !failed["bedrock"] || len(failed) != 1 {
		t.Fatalf("partial mantle failure must classify as a bedrock discovery failure (and the policy note must not): %v", failed)
	}
	if discoverBaselineSavable(true, nil, failed) {
		t.Fatal("a FIRST-RUN empty pool with a failed discovery source must not seed the baseline")
	}
	// On a subsequent run the same condition SAVES: NextSeenKeys carries the
	// failed source's keys forward, so the save cannot lose them, and blocking
	// instead froze the baseline forever whenever an optional provider (a
	// machine with no local Ollama daemon) fails on every listing, which
	// re-reported the identical GONE set indefinitely.
	if !discoverBaselineSavable(false, nil, failed) {
		t.Fatal("a subsequent-run empty pool must save even alongside a failed source (NextSeenKeys is the conservatism)")
	}

	pool := []ai.ModelInfo{{ID: "fresh", Provider: "ollama"}}
	if !discoverBaselineSavable(true, pool, failed) {
		t.Fatal("a non-empty pool seeds even alongside a failed source")
	}
	seen := &ai.DiscoverySeen{Keys: []string{"bedrock|mantle-only-model", "ollama|stale-model"}}
	got := ai.NextSeenKeys(pool, seen, failed)
	want := []string{"bedrock|mantle-only-model", "ollama|fresh"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NextSeenKeys = %v, want %v (failed bedrock key carried, succeeded ollama's delisted key dropped)", got, want)
	}
	d := ai.DiffAgainstSeen(pool, nil, seen, failed)
	if len(d.Gone) != 1 || d.Gone[0] != "ollama|stale-model" {
		t.Fatalf("Gone = %v, want only the succeeded source's delisted key (failed bedrock shielded)", d.Gone)
	}
}

// writeDiscoverCacheListing rewrites one discovery cache file in the current
// XDG sandbox with the given models (fresh mtime, current cache version).
func writeDiscoverCacheListing(t *testing.T, name, region string, models []ai.ModelInfo) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "2nb", "discovery")
	data, err := json.Marshal(struct {
		Version int            `json:"version"`
		Region  string         `json:"region"`
		Models  []ai.ModelInfo `json:"models"`
	}{ai.DiscoveryCacheVersion, region, models})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestContract_ModelsDiscoverHumanGoneWithEmptyPool pins the human rendering
// when the discovered pool is EMPTY because every listing already graduated
// into the catalog: the GONE section must still render (the old early return
// dropped it even though the JSON envelope carried the entries), alongside a
// clear nothing-new line for the pool itself.
func TestContract_ModelsDiscoverHumanGoneWithEmptyPool(t *testing.T) {
	_, root := newContractVault(t)
	seedDiscoverCaches(t, []ai.ModelInfo{fakeMantleRow("fake.mantle-model-v1")})
	// Pin ollama to a dead port so its discovery outcome is deterministic: a
	// live local ollama would otherwise contribute rows and unempty the pool.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.ollama.endpoint", "http://127.0.0.1:9"); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}

	// First run: non-empty pool, the baseline seeds with the fake keys.
	first := discoverJSON(t, root)
	if !first.FirstRun || len(first.Models) == 0 {
		t.Fatalf("expected a first run with a non-empty pool: %+v", first)
	}

	// Rewrite every cached listing to carry ONLY routes the merged catalog
	// already knows, so the pool is empty while every bedrock source still
	// answers from cache. (Empty cached listings would not work:
	// readDiscoveryCache rejects zero-model entries, which would force a live
	// walk.)
	//
	// "Already known" is now per-ROUTE, not per-id: a listing of gpt-5.5 in
	// three regions is three routes, and only the one the builtin declares
	// (us-east-2) is known. So each region is seeded with a route the catalog
	// genuinely holds — the two mantle builtins for their own regions, and a
	// vault row saved below for the third.
	knownEast1 := ai.ModelInfo{
		ID: "openai.gpt-5.5", Provider: "bedrock", Type: "generation", Tier: ai.TierUserVerified,
		Plane: ai.PlaneMantle, InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: "us-east-1",
	}
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, knownEast1); err != nil {
		t.Fatalf("seed known us-east-1 route: %v", err)
	}
	writeDiscoverCacheListing(t, "bedrock-us-east-1-default.json", "us-east-1", []ai.ModelInfo{{
		ID: "cohere.rerank-v3-5:0", Provider: "bedrock", Type: "rerank", Tier: ai.TierUnverified,
		Plane: ai.PlaneClassic, Region: "us-east-1",
	}})
	for _, r := range []struct{ region, id string }{
		{"us-east-1", "openai.gpt-5.5"},
		{"us-east-2", "openai.gpt-5.5"},
		{"us-west-2", "xai.grok-4.3"},
	} {
		writeDiscoverCacheListing(t, "bedrock-mantle-"+r.region+"-default.json", r.region, []ai.ModelInfo{{
			ID: r.id, Provider: "bedrock", Type: "generation", Tier: ai.TierUnverified,
			Plane: ai.PlaneMantle, InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: r.region,
		}})
	}

	out, err := runCLIArgs(t, root, "models", "discover")
	if err != nil {
		t.Fatalf("models discover: %v (out=%s)", err, truncate(out, 500))
	}
	for _, want := range []string{
		"No discovered models outside your catalog.",
		"Gone from discovery since last check",
		"bedrock|fake.classic-model-v1",
		"bedrock|fake.mantle-model-v1",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("empty-pool human output missing %q:\n%s", want, out)
		}
	}

	// The baseline ADVANCED on that run even though the pool was empty and
	// ollama's discovery failed (dead port): NextSeenKeys carried ollama's
	// keys and dropped the graduated bedrock ones, so the follow-up run
	// reports the departure exactly once instead of the same GONE set
	// forever (the sticky-GONE staleness a global empty-pool block caused).
	report := discoverJSON(t, root)
	if len(report.Models) != 0 {
		t.Fatalf("pool should be empty after graduation: %+v", report.Models)
	}
	if len(report.Gone) != 0 {
		t.Fatalf("gone must clear once the advanced baseline is saved (reported once, not forever): %+v", report)
	}
}

// TestContract_ModelsDiscoverOfflineEnvelope asserts the command stays
// decodable and exits 0 with no reachable discovery source at all, and that
// a listing that produced nothing but warnings does NOT save a baseline
// (so recovery re-seeds rather than re-announcing everything as NEW).
func TestContract_ModelsDiscoverOfflineEnvelope(t *testing.T) {
	_, root := newContractVault(t)
	neutralizeAWSCredentials(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("OPENROUTER_API_KEY", "")
	// Dead Ollama endpoint through the real config path, like the verify
	// offline contract test.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.ollama.endpoint", "http://127.0.0.1:9"); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}

	got, err := runCLIArgs(t, root, "models", "discover", "--json", "--porcelain")
	if err != nil {
		t.Fatalf("models discover offline: %v (out=%s)", err, truncate(got, 500))
	}
	var report discoverReportView
	if err := json.Unmarshal(got, &report); err != nil {
		t.Fatalf("report parse: %v (body=%s)", err, truncate(got, 500))
	}
	if !report.FirstRun {
		t.Fatal("no baseline exists, so even an empty listing reports first_run")
	}
	classicSeen := false
	for _, s := range report.Sources {
		if s.Source == "classic" && s.Region == "us-east-1" {
			classicSeen = true
			if s.Exists {
				t.Fatalf("offline run cannot have cached anything: %+v", s)
			}
		}
	}
	if !classicSeen {
		t.Fatalf("classic primary-region source row must always be reported: %+v", report.Sources)
	}
	// JSON must carry [] for the collection fields, never null (the envelope
	// is pretty-printed, hence the space after the colon).
	for _, key := range []string{`"models": [`, `"new": [`, `"gone": [`, `"sources": [`} {
		if !strings.Contains(string(got), key) {
			t.Fatalf("envelope missing %s (null instead of []?): %s", key, truncate(got, 500))
		}
	}
}
