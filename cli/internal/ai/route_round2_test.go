package ai

import "testing"

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

// TestProbeUsesCandidateRegion pins that a probe goes to the endpoint its
// candidate names. verify selects a specific route (bestRoutePerModel), and
// the fan-out used to ask the catalog for the pin instead, discarding that
// selection.
func TestProbeUsesCandidateRegion(t *testing.T) {
	// regionAttempts is the seam: a candidate carrying us-west-2 must put it
	// in the attempt list even when the configured set does not contain it.
	got := regionAttempts([]string{"us-east-1"}, "us-west-2")
	found := false
	for _, r := range got {
		if r == "us-west-2" {
			found = true
		}
	}
	if !found {
		t.Errorf("attempts = %v, want the candidate's own region included", got)
	}
}

// TestProbeResultCarriesRoute pins that a verdict names the full endpoint it
// came from. Without the plane, a save keyed on the probed route could not
// distinguish two planes of the same model.
func TestProbeResultCarriesRoute(t *testing.T) {
	r := TestProbeResult{Provider: "bedrock", ModelID: "m", Plane: PlaneMantle, Region: "us-west-2"}
	want := RouteKey{Provider: "bedrock", ID: "m", Plane: PlaneMantle, Region: "us-west-2"}
	if r.Route() != want {
		t.Errorf("probe route = %+v, want %+v", r.Route(), want)
	}
}
