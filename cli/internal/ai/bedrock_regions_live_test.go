package ai

import (
	"context"
	"testing"
)

// requireBedrockLiveIsolatedFile gates on real credentials while isolating the
// machine bedrock.json (so tests can write regions without scribbling on the
// developer's real file). The ambient env bearer token / SigV4 chain still
// provides auth — only the FILE is redirected.
func requireBedrockLiveIsolatedFile(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if !CheckBedrockCredentials(context.Background(), BedrockConfig{Profile: "default", Region: "us-east-1"}) {
		t.Skip("AWS credentials not configured")
	}
}

// TestLiveBedrockRegionOverrideRouting proves a RegionOverride actually routes
// the classic generator and embedder to the overridden regional endpoint —
// the seam multi-region verify stands on. Costs fractions of a cent.
func TestLiveBedrockRegionOverrideRouting(t *testing.T) {
	requireBedrockLiveIsolatedFile(t)
	ctx := context.Background()
	cfg := BedrockConfig{Profile: "default", Region: "us-east-1", RegionOverride: "us-west-2"}

	gen, err := NewBedrockGenerator(ctx, cfg, "us.anthropic.claude-haiku-4-5-20251001-v1:0")
	if err != nil {
		t.Skipf("AWS config unavailable: %v", err)
	}
	if gen.region != "us-west-2" {
		t.Fatalf("generator region = %q, want us-west-2", gen.region)
	}
	out, err := gen.Generate(ctx, "Reply with the word ok.", GenOpts{MaxTokens: 8})
	if err != nil {
		t.Fatalf("generate via us-west-2: %v", err)
	}
	if out == "" {
		t.Fatal("empty generation")
	}

	emb, err := NewBedrockEmbedder(ctx, cfg, "amazon.titan-embed-text-v2:0", 1024)
	if err != nil {
		t.Skipf("AWS config unavailable: %v", err)
	}
	if emb.region != "us-west-2" {
		t.Fatalf("embedder region = %q, want us-west-2", emb.region)
	}
	vecs, err := emb.Embed(ctx, []string{"region override probe"})
	if err != nil {
		t.Fatalf("embed via us-west-2: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) == 0 {
		t.Fatalf("bad embedding shape: %d", len(vecs))
	}
}

// TestLiveProbeModelInRegionsStopsAtFirstPass: a widely-available model must
// pass in the PRIMARY region and never fan out, and the result must carry the
// region it was probed in.
func TestLiveProbeModelInRegionsStopsAtFirstPass(t *testing.T) {
	requireBedrockLiveIsolatedFile(t)
	ctx := context.Background()
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Profile: "default", Region: "us-east-1"}}
	const model = "us.anthropic.claude-haiku-4-5-20251001-v1:0"

	res, err := TestProbeModelInRegions(ctx, cfg, model, "bedrock", "generation", "", []string{"us-east-1", "us-west-2"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !res.OK {
		t.Skipf("account cannot invoke %s (%s); cannot assert stop-at-first-pass", model, res.Code)
	}
	if res.Region != "us-east-1" {
		t.Fatalf("first-pass region = %q, want the primary us-east-1", res.Region)
	}

	// A plain single-region probe stamps its region too.
	single, err := TestProbeModel(ctx, cfg, model, "bedrock", "generation", "")
	if err != nil {
		t.Fatalf("single probe: %v", err)
	}
	if single.Region != "us-east-1" {
		t.Fatalf("single-probe region = %q, want us-east-1", single.Region)
	}
}

// TestLiveStalePinSelfHeals: a model carrying a stale non-primary catalog
// Region pin must still re-check PRIMARY on a single-region probe, so primary
// access that comes back is noticed. persistProbedRegion no longer CLEARS the
// pin (region is part of the row's identity now, so clearing it would write a
// second row rather than replace one); the recovery is that the primary route
// records its own fresh verdict, which PreferRoutes then ranks above the
// pinned sibling. Before the fix, a single-region probe honored the pin and
// primary was never re-checked at all.
func TestLiveStalePinSelfHeals(t *testing.T) {
	requireBedrockLiveIsolatedFile(t)
	vaultRoot := t.TempDir()
	ctx := context.Background()
	const model = "us.anthropic.claude-haiku-4-5-20251001-v1:0"

	// Seed a vault-scoped stale pin to a non-primary region.
	if err := SaveUserCatalogEntry(ScopeVault, vaultRoot, ModelInfo{
		ID: model, Provider: "bedrock", Type: "generation", Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}
	if got := resolveModelRegion("bedrock", model, vaultRoot); got != "us-west-2" {
		t.Fatalf("pin not seeded: %q", got)
	}

	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Profile: "default", Region: "us-east-1"}}
	res, err := TestProbeModelInRegions(ctx, cfg, model, "bedrock", "generation", vaultRoot, []string{"us-east-1"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !res.OK {
		t.Skipf("account cannot invoke %s (%s); cannot assert self-heal", model, res.Code)
	}
	if res.Region != "us-east-1" {
		t.Fatalf("pinned model probed %q, want the PRIMARY us-east-1 (self-heal re-check)", res.Region)
	}
}

// TestLiveDiscoveryUnionTwoRegions: with an included second region, the
// bedrock discovery listing must be a superset of the single-region listing,
// with every row a distinct ROUTE. Control-plane calls only (free).
//
// This test used to assert no duplicate IDs. That invariant is exactly what
// routes removed: a model served in three regions is now three rows, because
// Bedrock entitlement is per-region and those routes succeed and fail
// independently. Route-uniqueness is the invariant that replaced it, and it
// is the stronger one: it still catches a genuine double-append, while
// allowing the per-region rows the whole change exists to preserve.
func TestLiveDiscoveryUnionTwoRegions(t *testing.T) {
	requireBedrockLiveIsolatedFile(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctx := context.Background()
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Profile: "default", Region: "us-east-1"}}

	countBedrock := func(models []ModelInfo) (int, map[string]int, map[string]bool) {
		routes := map[string]int{}
		ids := map[string]bool{}
		n := 0
		for _, m := range models {
			if m.Provider == "bedrock" {
				n++
				routes[routeKey(m.Route())]++
				ids[m.ID] = true
			}
		}
		return n, routes, ids
	}

	single, _ := discoverVendorModels(ctx, cfg, true, false)
	n1, _, ids1 := countBedrock(single)
	if n1 == 0 {
		t.Skip("bedrock discovery returned nothing (control plane unreachable?)")
	}

	if err := WriteBedrockFile(BedrockFile{Regions: []string{"us-west-2"}}); err != nil {
		t.Fatal(err)
	}
	union, _ := discoverVendorModels(ctx, cfg, true, false)
	n2, routes, ids2 := countBedrock(union)
	if n2 < n1 {
		t.Fatalf("union (%d) lost rows vs single-region (%d)", n2, n1)
	}
	// Every distinct model still present: widening regions adds routes, it
	// must never drop a model.
	for id := range ids1 {
		if !ids2[id] {
			t.Errorf("model %q disappeared from the union listing", id)
		}
	}
	for k, c := range routes {
		if c > 1 {
			t.Fatalf("duplicate ROUTE %q in union listing (%d copies)", k, c)
		}
	}
	// Every bedrock row must carry its route, or the invoke path is back to
	// guessing which region it belongs to.
	for _, m := range union {
		if m.Provider != "bedrock" {
			continue
		}
		if m.Plane == "" || m.Region == "" {
			t.Fatalf("discovered bedrock row %q has no route (plane=%q region=%q)", m.ID, m.Plane, m.Region)
		}
	}
	t.Logf("bedrock discovery: %d single-region rows, %d union rows, %d distinct models", n1, n2, len(ids2))
}
