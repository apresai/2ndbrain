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

// TestLiveDiscoveryUnionTwoRegions: with an included second region, the
// bedrock discovery listing must be a superset of the single-region listing
// with no duplicate IDs. Control-plane calls only (free).
func TestLiveDiscoveryUnionTwoRegions(t *testing.T) {
	requireBedrockLiveIsolatedFile(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	ctx := context.Background()
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Profile: "default", Region: "us-east-1"}}

	countBedrock := func(models []ModelInfo) (int, map[string]int) {
		ids := map[string]int{}
		n := 0
		for _, m := range models {
			if m.Provider == "bedrock" {
				n++
				ids[m.ID]++
			}
		}
		return n, ids
	}

	single, _ := discoverVendorModels(ctx, cfg, true)
	n1, _ := countBedrock(single)
	if n1 == 0 {
		t.Skip("bedrock discovery returned nothing (control plane unreachable?)")
	}

	if err := WriteBedrockFile(BedrockFile{Regions: []string{"us-west-2"}}); err != nil {
		t.Fatal(err)
	}
	union, _ := discoverVendorModels(ctx, cfg, true)
	n2, ids := countBedrock(union)
	if n2 < n1 {
		t.Fatalf("union (%d) lost models vs single-region (%d)", n2, n1)
	}
	for id, c := range ids {
		if c > 1 {
			t.Fatalf("duplicate ID %q in union listing", id)
		}
	}
	t.Logf("bedrock discovery: %d single-region, %d union", n1, n2)
}
