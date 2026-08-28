package ai

import (
	"strings"
	"testing"
)

// TestMantleBaseURLHonorsResolvedRegion is the regression test for the one
// place a resolved route was still overridden at invoke time.
//
// mantleBaseURL asked the catalog for the region BEFORE looking at the
// explicit override. InitBedrock sets RegionOverride from the resolved route
// and ResolveBedrockConfig maps it onto Region, but the catalog lookup is
// exact-id, plane-blind, and first-match-wins — and discovery now emits one row
// per region, so "any row" was routinely the wrong one. A config pinned to
// @mantle/us-west-2 built a client against whichever region sorted first.
//
// The mantle plane is the one whose per-model region pinning motivated routes
// in the first place, which is what made this the worst place to still infer.
func TestMantleBaseURLHonorsResolvedRegion(t *testing.T) {
	setupHome(t)

	// A catalog row pins a DIFFERENT region than the caller resolved.
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: "zz.dual-model", Provider: "bedrock", Type: "generation",
		Plane: PlaneMantle, InvokeStrategy: StrategyBedrockMantleResponses, Region: "us-east-2",
	}); err != nil {
		t.Fatal(err)
	}

	cfg := ResolveBedrockConfig(BedrockConfig{Region: "us-east-1", RegionOverride: "us-west-2"})
	got, err := mantleBaseURL(cfg, "zz.dual-model", "")
	if err != nil {
		t.Fatalf("mantleBaseURL: %v", err)
	}
	want := "https://bedrock-mantle.us-west-2.api.aws"
	if got != want {
		t.Errorf("base URL = %q, want %q (the resolved route's region, not the catalog's)", got, want)
	}
}

// TestMantleBaseURLFallsBackToCatalogPin keeps the other half working: with
// nothing explicitly resolved, a bare mantle builtin still reaches its pinned
// region.
func TestMantleBaseURLFallsBackToCatalogPin(t *testing.T) {
	setupHome(t)
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: "zz.pinned-model", Provider: "bedrock", Type: "generation",
		Plane: PlaneMantle, InvokeStrategy: StrategyBedrockMantleResponses, Region: "us-east-2",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := ResolveBedrockConfig(BedrockConfig{Region: "us-east-1"})
	got, err := mantleBaseURL(cfg, "zz.pinned-model", "")
	if err != nil {
		t.Fatalf("mantleBaseURL: %v", err)
	}
	if got != "https://bedrock-mantle.us-east-2.api.aws" {
		t.Errorf("base URL = %q, want the catalog pin when nothing is resolved", got)
	}
}

// TestMarkActiveRoutesElectsExactlyOne is the regression test for multi-active
// marking. isActiveModel falls back to id-only matching when the config names
// no plane or region — which is every vault upgrading from before routes — so
// marking row by row flagged EVERY route of the model, and the picker showed
// one model three times.
func TestMarkActiveRoutesElectsExactlyOne(t *testing.T) {
	setupHome(t)
	cfg := AIConfig{Provider: "bedrock", GenerationModel: "us.acme.gen-1", Bedrock: BedrockConfig{Region: "us-east-1"}}
	rows := []ModelInfo{
		{Provider: "bedrock", ID: "us.acme.gen-1", Type: "generation", Plane: PlaneClassic, Region: "us-east-2"},
		{Provider: "bedrock", ID: "us.acme.gen-1", Type: "generation", Plane: PlaneClassic, Region: "us-west-2"},
		{Provider: "bedrock", ID: "us.acme.gen-1", Type: "generation", Plane: PlaneClassic, Region: "us-east-1"},
	}
	var empty []ModelInfo
	markActiveRoutes(cfg, &rows, &empty)

	active := []ModelInfo{}
	for _, m := range rows {
		if m.Active {
			active = append(active, m)
		}
	}
	if len(active) != 1 {
		t.Fatalf("active rows = %d, want exactly 1: %+v", len(active), active)
	}
	// And it must be the route the invoke path would pick: the primary region.
	if active[0].Region != "us-east-1" {
		t.Errorf("active route = %s, want the primary region so the GUI shows what actually runs",
			active[0].Route().String())
	}
}

// TestProbeUsesCandidateRegionAndDoesNotFanOut drives the REAL probe entry
// point, not the regionAttempts helper.
//
// The first version of this test only exercised regionAttempts, which the fix
// never touched: reverting the fix left it green. What must actually hold is
// that a candidate naming its own region is probed THERE and nowhere else.
// Fanning out would bill for endpoints the caller did not ask about and, worse,
// return the PRIMARY region's verdict on exhaustion — so `models verify
// <id>@classic/us-west-2` would probe us-east-1 first and persist its failure
// onto the us-west-2 row.
//
// No credentials are needed: the probe fails fast, and the result still
// reports the region and plane the client was built for, which is the property
// under test.
func TestProbeUsesCandidateRegionAndDoesNotFanOut(t *testing.T) {
	setupHome(t)
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Region: "us-east-1"}}
	candidate := ModelInfo{
		ID: "zz.route-probe", Provider: "bedrock", Type: "generation",
		Plane: PlaneClassic, Region: "us-west-2",
	}

	res, err := TestProbeModelInfoInRegions(t.Context(), cfg, candidate, "", []string{"us-east-1", "us-east-2"})
	if err != nil {
		t.Fatalf("probe returned a hard error: %v", err)
	}
	if res.Region != "us-west-2" {
		t.Errorf("probed region = %q, want the candidate's own us-west-2", res.Region)
	}
	// Fan-out annotates Detail with the other regions it tried; a named route
	// must not have tried any.
	if strings.Contains(res.Detail, "also failed in") {
		t.Errorf("a named route must not fan out, got: %s", res.Detail)
	}
}

// TestProbeResultCarriesRouteFromProbe pins that TestProbeModelInfo STAMPS the
// plane onto its result, which is what the save path keys on.
//
// The first version asserted on a hand-built struct literal, so deleting the
// assignment in the probe left it green. This drives the probe.
func TestProbeResultCarriesRouteFromProbe(t *testing.T) {
	setupHome(t)
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Region: "us-east-1"}}

	for _, tc := range []struct {
		name  string
		in    ModelInfo
		plane Plane
	}{
		{"candidate plane wins", ModelInfo{ID: "zz.a", Provider: "bedrock", Type: "generation", Plane: PlaneMantle, Region: "us-west-2"}, PlaneMantle},
		{"derived from strategy", ModelInfo{ID: "zz.b", Provider: "bedrock", Type: "generation"}, PlaneClassic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := TestProbeModelInfo(t.Context(), cfg, tc.in, "")
			if err != nil {
				t.Fatalf("probe returned a hard error: %v", err)
			}
			if res.Plane != tc.plane {
				t.Errorf("result plane = %q, want %q", res.Plane, tc.plane)
			}
			if res.Route().ID != tc.in.ID {
				t.Errorf("result route = %s, want it to name the probed model", res.Route().String())
			}
		})
	}
}

// TestProbeClassicCandidateDispatchesClassic covers the CLASSIC direction of
// the plane override, which the mantle-only test left uncovered: deleting the
// `case PlaneClassic` branch — the actual round-4 blocker fix — kept the suite
// green.
//
// The defect: effectiveInvokeStrategy is exact-id and plane-blind, so for a
// dual-plane id whose catalog row is mantle, a CLASSIC candidate was billed
// against the mantle endpoint and its verdict filed under a route that does
// not exist.
func TestProbeClassicCandidateDispatchesClassic(t *testing.T) {
	setupHome(t)
	// A catalog row says mantle for this id...
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: "zz.dualplane", Provider: "bedrock", Type: "generation",
		Plane: PlaneMantle, InvokeStrategy: StrategyBedrockMantleResponses, Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}
	// ...but the candidate names CLASSIC.
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Region: "us-east-1"}}
	res, err := TestProbeModelInfo(t.Context(), cfg, ModelInfo{
		ID: "zz.dualplane", Provider: "bedrock", Type: "generation",
		Plane: PlaneClassic, Region: "us-east-1",
	}, "")
	if err != nil {
		t.Fatalf("probe returned a hard error: %v", err)
	}
	if res.Plane != PlaneClassic {
		t.Errorf("recorded plane = %q, want classic (the plane the candidate named)", res.Plane)
	}
	if res.Strategy == StrategyBedrockMantleResponses {
		t.Error("a classic candidate dispatched the MANTLE strategy")
	}
	if res.Region != "us-east-1" {
		t.Errorf("recorded region = %q, want us-east-1", res.Region)
	}
}

// TestProbeMantleCandidateHonorsItsRegion covers the mantle side of the region
// override, which had to move above the mantle early return. Without it,
// `verify <id>@mantle/<region>` went wherever the plane-blind catalog pin said
// — on the plane where per-region entitlement differs most.
func TestProbeMantleCandidateHonorsItsRegion(t *testing.T) {
	setupHome(t)
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "ABSK-route-region-test")
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: "zz.mantlepin", Provider: "bedrock", Type: "generation",
		Plane: PlaneMantle, InvokeStrategy: StrategyBedrockMantleResponses, Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}
	cfg := AIConfig{Provider: "bedrock", Bedrock: BedrockConfig{Region: "us-east-1"}}
	res, err := TestProbeModelInfoInRegions(t.Context(), cfg, ModelInfo{
		ID: "zz.mantlepin", Provider: "bedrock", Type: "generation",
		Plane: PlaneMantle, Region: "us-east-2",
	}, "", []string{"us-east-1"})
	if err != nil {
		t.Fatalf("probe returned a hard error: %v", err)
	}
	if res.Region != "us-east-2" {
		t.Errorf("probed region = %q, want the candidate's own us-east-2 (not the catalog pin us-west-2)", res.Region)
	}
}
