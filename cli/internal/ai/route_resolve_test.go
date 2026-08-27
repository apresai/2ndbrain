package ai

import (
	"errors"
	"strings"
	"testing"
)

func grokRoutes() []ModelInfo {
	return []ModelInfo{
		{Provider: "bedrock", ID: "xai.grok-4.6", Type: "generation", Plane: PlaneMantle,
			InvokeStrategy: StrategyBedrockMantleResponses, Region: "us-west-2"},
		{Provider: "bedrock", ID: "xai.grok-4.6", Type: "generation", Plane: PlaneClassic,
			Region: "us-east-1"},
	}
}

// TestResolveSlotRouteRefusesWhenAmbiguous is the regression test for the
// failure that motivated route identity.
//
// The live symptom was:
//
//	ValidationException: Invocation of model ID xai.grok-4.6 with on-demand
//	throughput isn't supported.
//
// A config naming only the bare id used to fall through to classic Converse.
// Now it refuses before any request is sent, and names the pick commands.
func TestResolveSlotRouteRefusesWhenAmbiguous(t *testing.T) {
	want := RouteKey{Provider: "bedrock", ID: "xai.grok-4.6"}
	_, err := ResolveSlotRoute("generation", want, grokRoutes())

	var unrouted *UnroutedSlotError
	if !errors.As(err, &unrouted) {
		t.Fatalf("an unrouted slot with several endpoints must refuse, got %v", err)
	}
	msg := unrouted.Error()
	for _, s := range []string{
		"2nb config set ai.generation_model xai.grok-4.6@mantle/us-west-2",
		"2nb config set ai.generation_model xai.grok-4.6@classic/us-east-1",
		"2nb models discover",
	} {
		if !strings.Contains(msg, s) {
			t.Errorf("refusal must be actionable, missing %q:\n%s", s, msg)
		}
	}
}

// TestResolveSlotRoutePinnedConfigWins is the "nothing is inferred at invoke
// time" case: the config named the endpoint, so no catalog agreement is
// needed and no preference order runs.
func TestResolveSlotRoutePinnedConfigWins(t *testing.T) {
	want := RouteKey{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneMantle, Region: "us-west-2"}
	got, err := ResolveSlotRoute("generation", want, grokRoutes())
	if err != nil {
		t.Fatalf("a fully pinned config must resolve: %v", err)
	}
	if got.Route != want {
		t.Errorf("route = %v, want the configured %v", got.Route, want)
	}
	if got.Strategy != StrategyBedrockMantleResponses {
		t.Errorf("strategy = %q, want the mantle dialect from the catalog row", got.Strategy)
	}
}

// TestResolveSlotRoutePinnedButUncatalogued: an endpoint 2nb has never
// catalogued is still callable. The user named it; refusing would make the
// catalog a gatekeeper on invocation, which it is not.
func TestResolveSlotRoutePinnedButUncatalogued(t *testing.T) {
	want := RouteKey{Provider: "bedrock", ID: "vendor.brand-new", Plane: PlaneMantle, Region: "us-east-2"}
	got, err := ResolveSlotRoute("generation", want, nil)
	if err != nil {
		t.Fatalf("a pinned but uncatalogued route must still resolve: %v", err)
	}
	if got.Route != want {
		t.Errorf("route = %v, want %v", got.Route, want)
	}
	// Plane mantle implies the dialect even with no catalog row to read it
	// from, or the pin would resolve to a classic client.
	if got.Strategy != StrategyBedrockMantleResponses {
		t.Errorf("strategy = %q, want the mantle dialect implied by the plane", got.Strategy)
	}
}

// TestResolveSlotRoutePartialPinNarrows: pinning just the plane is enough when
// it leaves exactly one endpoint.
func TestResolveSlotRoutePartialPinNarrows(t *testing.T) {
	want := RouteKey{Provider: "bedrock", ID: "xai.grok-4.6", Plane: PlaneMantle}
	got, err := ResolveSlotRoute("generation", want, grokRoutes())
	if err != nil {
		t.Fatalf("a plane pin that leaves one route must resolve: %v", err)
	}
	if got.Route.Region != "us-west-2" {
		t.Errorf("region = %q, want the only mantle route's region", got.Route.Region)
	}
}

// TestResolveSlotRouteSingleRouteNeedsNoPin keeps the common case ergonomic:
// most models have one endpoint, and naming the model is enough.
func TestResolveSlotRouteSingleRouteNeedsNoPin(t *testing.T) {
	rows := []ModelInfo{{Provider: "bedrock", ID: "solo.model", Type: "generation",
		Plane: PlaneClassic, Region: "us-east-1"}}
	got, err := ResolveSlotRoute("generation", RouteKey{Provider: "bedrock", ID: "solo.model"}, rows)
	if err != nil {
		t.Fatalf("a single-route model must resolve from a bare id: %v", err)
	}
	if got.Route.Plane != PlaneClassic || got.Route.Region != "us-east-1" {
		t.Errorf("route = %v, want the model's only endpoint", got.Route)
	}
}

// TestResolveSlotRouteUnknownModelIsNotAnError preserves the doctrine that a
// model can exist before 2nb's catalog knows it. Provider defaults apply, as
// they did before routes.
func TestResolveSlotRouteUnknownModelIsNotAnError(t *testing.T) {
	want := RouteKey{Provider: "bedrock", ID: "vendor.unknown"}
	got, err := ResolveSlotRoute("generation", want, grokRoutes())
	if err != nil {
		t.Fatalf("an uncatalogued model must not be refused: %v", err)
	}
	if got.Route != want {
		t.Errorf("route = %v, want the config's own values passed through", got.Route)
	}
}

// TestResolveSlotRouteDuplicateRowsAreNotAmbiguous: the same endpoint listed
// twice (a cached and a live path both emitting it) must not manufacture a
// refusal out of what is really one route.
func TestResolveSlotRouteDuplicateRowsAreNotAmbiguous(t *testing.T) {
	row := ModelInfo{Provider: "bedrock", ID: "dup.model", Type: "generation",
		Plane: PlaneMantle, InvokeStrategy: StrategyBedrockMantleResponses, Region: "us-east-1"}
	got, err := ResolveSlotRoute("generation", RouteKey{Provider: "bedrock", ID: "dup.model"}, []ModelInfo{row, row})
	if err != nil {
		t.Fatalf("duplicate rows for one endpoint must not be ambiguous: %v", err)
	}
	if got.Route.Region != "us-east-1" {
		t.Errorf("route = %v, want the single real endpoint", got.Route)
	}
}

// TestResolveSlotRouteEmptySlot: an unset slot resolves to NOTHING, and in
// particular must not adopt some other model's route.
//
// The earlier version only asserted "does not error", which the early return
// on an empty ID makes unfailable. The assertion that matters is that the
// returned route stays empty: a slot with no model must not come back pointed
// at a catalog row, or an unconfigured rerank slot could silently start
// invoking whatever sorted first.
func TestResolveSlotRouteEmptySlot(t *testing.T) {
	got, err := ResolveSlotRoute("rerank", RouteKey{Provider: "bedrock"}, grokRoutes())
	if err != nil {
		t.Fatalf("an unset slot must not error: %v", err)
	}
	if got.Route.ID != "" || got.Route.Plane != "" || got.Route.Region != "" || got.Strategy != "" {
		t.Errorf("an unset slot resolved to a real endpoint: %+v", got)
	}
}
