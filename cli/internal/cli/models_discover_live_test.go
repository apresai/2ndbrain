package cli

import (
	"os"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

// TestLive_ModelsDiscoverAndAddMantle_CredGated drives the real discover flow
// against live Bedrock: a cold-cache run IS the live walk (classic control
// plane + the mantle /v1/models listings), and --add of a mantle-listed id
// must persist its routing into the vault catalog, the durable fix for
// "explicit mantle ids silently classic-probe". Costs nothing: discovery and
// --add never invoke a model.
func TestLive_ModelsDiscoverAndAddMantle_CredGated(t *testing.T) {
	if os.Getenv("AWS_BEARER_TOKEN_BEDROCK") == "" {
		t.Skip("set AWS_BEARER_TOKEN_BEDROCK to run the live discover walk")
	}
	_, root := newContractVault(t)
	// Cold cache dir: the first run performs the live walk and re-warms it.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	report := discoverJSON(t, root)
	if !report.FirstRun {
		t.Fatal("cold baseline must report first_run")
	}
	if len(report.Models) == 0 {
		t.Fatalf("live discovery returned no models (sources: %+v)", report.Sources)
	}
	classicWarm, mantleRows := false, 0
	for _, s := range report.Sources {
		if s.Source == "classic" && s.Region == "us-east-1" && s.Exists && !s.Stale {
			classicWarm = true
		}
		if s.Source == "mantle" {
			mantleRows++
		}
	}
	if !classicWarm {
		t.Fatalf("the walk must warm the classic primary cache: %+v", report.Sources)
	}
	if mantleRows == 0 {
		t.Fatalf("with a bearer token the mantle sources must be reported: %+v", report.Sources)
	}

	// Pick a mantle-listed discovery (prefer the known xai.grok-4.6, the
	// dual-plane id from the 2026-08-21 self-heal incident) and add it.
	var pick, pickRegion string
	for _, m := range report.Models {
		if m.InvokeStrategy != ai.StrategyBedrockMantleResponses {
			continue
		}
		if pick == "" || m.ID == "xai.grok-4.6" {
			pick, pickRegion = m.ID, m.Region
		}
		if pick == "xai.grok-4.6" {
			break
		}
	}
	if pick == "" {
		t.Skip("no mantle-listed discoveries for this account; nothing to add")
	}

	added := discoverJSON(t, root, "--add", pick)
	if len(added.Added) != 1 || added.Added[0] != pick {
		t.Fatalf("added = %v, want exactly %s", added.Added, pick)
	}
	// Look the row up by its full ROUTE. Before routes this was (provider,
	// id), which could not distinguish the mantle row from a classic row of
	// the same id — exactly the collision that let a classic save clobber a
	// passing mantle entry.
	entry, ok := ai.UserCatalogEntry(ai.ScopeVault, root, ai.RouteKey{
		Provider: "bedrock", ID: pick, Plane: ai.PlaneMantle, Region: pickRegion,
	})
	if !ok {
		t.Fatalf("--add did not persist a vault catalog entry for %s@mantle/%s", pick, pickRegion)
	}
	if entry.InvokeStrategy != ai.StrategyBedrockMantleResponses {
		t.Fatalf("persisted invoke_strategy = %q, want the mantle strategy", entry.InvokeStrategy)
	}
	if entry.Region != pickRegion {
		t.Fatalf("persisted region = %q, want the listing region %q", entry.Region, pickRegion)
	}
	if entry.Tier != ai.TierUnverified {
		t.Fatalf("persisted tier = %q, want unverified until a probe passes", entry.Tier)
	}

	// A follow-up run reads warm caches, is no longer the first run, and must
	// not badge the adopted model GONE (it graduated into the catalog).
	second := discoverJSON(t, root)
	if second.FirstRun {
		t.Fatal("second run must not report first_run")
	}
	for _, g := range second.Gone {
		if g == "bedrock|"+pick {
			t.Fatalf("adopted %s must not be badged GONE: %v", pick, second.Gone)
		}
	}
}
