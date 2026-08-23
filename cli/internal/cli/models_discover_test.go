package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
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

	write("bedrock-us-east-1-default.json", "us-east-1", []ai.ModelInfo{{
		ID: "fake.classic-model-v1", Provider: "bedrock", Type: "generation", Tier: ai.TierUnverified,
	}})
	write("bedrock-mantle-us-east-1-default.json", "us-east-1", mantleUSEast1Models)
	write("bedrock-mantle-us-east-2-default.json", "us-east-2", []ai.ModelInfo{{
		ID: "fake.mantle-east2-filler", Provider: "bedrock", Type: "generation", Tier: ai.TierUnverified,
		InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: "us-east-2",
	}})
	write("bedrock-mantle-us-west-2-default.json", "us-west-2", []ai.ModelInfo{{
		ID: "fake.mantle-west2-filler", Provider: "bedrock", Type: "generation", Tier: ai.TierUnverified,
		InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: "us-west-2",
	}})
	return dir
}

func fakeMantleRow(id string) ai.ModelInfo {
	return ai.ModelInfo{
		ID: id, Provider: "bedrock", Type: "generation", Tier: ai.TierUnverified,
		InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: "us-east-1",
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
	entry, ok := ai.UserCatalogEntry(ai.ScopeVault, root, "bedrock", "fake.mantle-model-v1")
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
