package ai

import "testing"

// TestConfiguredPlaneBeatsCatalogStrategy is the regression test for the worst
// defect the audit of this change found.
//
// effectiveInvokeStrategy consults two plane-blind exact-id catalog lookups
// BEFORE it reads the strategy hint, so an explicitly configured route lost to
// whatever the catalog happened to say. With `ai.generation_plane: classic` on
// openai.gpt-5.5 (whose builtin is a mantle row) the slot resolved classic and
// the client dispatched MANTLE: the same silent misroute route identity exists
// to remove, reintroduced one layer below it.
//
// The route must win. NewBedrockGenerationForRoute dispatches on the resolved
// plane and runs no catalog lookup at all.
func TestConfiguredPlaneBeatsCatalogStrategy(t *testing.T) {
	setupHome(t)

	// openai.gpt-5.5 is a MANTLE builtin; the config pins it to classic.
	pinnedClassic := SlotRoute{Route: RouteKey{
		Provider: "bedrock", ID: "openai.gpt-5.5", Plane: PlaneClassic, Region: "us-east-1",
	}}

	// Prove the legacy resolver still disagrees, so this test is measuring a
	// real conflict rather than a coincidence. If a later change deletes that
	// resolver, this assertion is what says the conflict is gone.
	legacy := effectiveInvokeStrategy("bedrock",
		ModelInfo{ID: "openai.gpt-5.5", Provider: "bedrock"}, "")
	if legacy != StrategyBedrockMantleResponses {
		t.Logf("note: the legacy resolver no longer says mantle (%q); the conflict this guards may be gone", legacy)
	}

	gen, err := NewBedrockGenerationForRoute(t.Context(), BedrockConfig{Region: "us-east-1"}, pinnedClassic, "")
	if err != nil {
		t.Fatalf("construct classic-pinned generator: %v", err)
	}
	if _, isMantle := gen.(*BedrockMantleGenerator); isMantle {
		t.Fatal("config pinned the CLASSIC plane but the mantle client was built")
	}
	if _, isClassic := gen.(*BedrockGenerator); !isClassic {
		t.Fatalf("want the classic Converse client, got %T", gen)
	}
}

// TestConfiguredMantlePlaneBuildsMantleClient is the mirror: a classic-builtin
// id pinned to mantle must build the mantle client, so the route is
// authoritative in BOTH directions rather than merely biased one way.
func TestConfiguredMantlePlaneBuildsMantleClient(t *testing.T) {
	setupHome(t)
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "ABSK-route-dispatch-test")
	t.Setenv("2NB_BEDROCK_SKIP_KEYCHAIN", "1")

	pinnedMantle := SlotRoute{Route: RouteKey{
		Provider: "bedrock", ID: "us.anthropic.claude-sonnet-5", Plane: PlaneMantle, Region: "us-west-2",
	}}
	gen, err := NewBedrockGenerationForRoute(t.Context(), BedrockConfig{Region: "us-east-1"}, pinnedMantle, "")
	if err != nil {
		t.Fatalf("construct mantle-pinned generator: %v", err)
	}
	if _, isMantle := gen.(*BedrockMantleGenerator); !isMantle {
		t.Fatalf("config pinned the MANTLE plane, got %T", gen)
	}
}

// TestAdoptRoutingHintsKeepsClassicRegion is the regression test for the
// second critical defect: AdoptRoutingHints adopted Region only for mantle
// rows, so a classic per-region route could never be persisted. `discover
// --add` printed `some.model@classic/us-west-2` and stored
// `some.model@classic`, which meant the three-region classic sweep produced
// rows the user catalog could not hold and the invoke path never saw.
func TestAdoptRoutingHintsKeepsClassicRegion(t *testing.T) {
	discovered := ModelInfo{
		ID: "some.classic-model", Provider: "bedrock", Type: "generation",
		Plane: PlaneClassic, Region: "us-west-2",
	}
	entry := ModelInfo{ID: discovered.ID, Provider: discovered.Provider, Type: discovered.Type}
	AdoptRoutingHints(&entry, discovered)

	if entry.Route() != discovered.Route() {
		t.Errorf("persisted route %s != discovered route %s; the route shown to the user must be the route stored",
			entry.Route().String(), discovered.Route().String())
	}
}

// TestAdoptRoutingHintsNeverOverwritesAuthoredRegion keeps the fill-only-empty
// discipline: widening the region clause must not let a sibling region's
// listing repoint a route the user authored.
func TestAdoptRoutingHintsNeverOverwritesAuthoredRegion(t *testing.T) {
	entry := ModelInfo{ID: "m", Provider: "bedrock", Plane: PlaneMantle, Region: "us-east-2"}
	AdoptRoutingHints(&entry, ModelInfo{ID: "m", Provider: "bedrock", Plane: PlaneMantle, Region: "us-west-2"})
	if entry.Region != "us-east-2" {
		t.Errorf("authored region was overwritten: %q", entry.Region)
	}
}
