package ai

import "testing"

// TestTemplateFactsSurviveRetirement is the regression test for authored user
// input being silently discarded.
//
// `models add --price-in 42` writes a MODEL-level row (no route). Once any
// probe creates a concrete sibling, retireSupersededTemplates drops that row —
// and it used to carry only `Enabled` across, so the price override, notes,
// name, and context length vanished from the merged view while still sitting
// on disk. `models add` reported success and the override never appeared in
// `models list` or in any cost estimate.
func TestTemplateFactsSurviveRetirement(t *testing.T) {
	id := "zz.priced"
	verified := []ModelInfo{
		// The authored template.
		{
			ID: id, Provider: "bedrock", Type: "generation", Tier: TierUserVerified,
			Name: "Authored Name", Notes: "authored note", ContextLen: 4242,
			PriceIn: 42, PriceOut: 84, PriceSource: "user", PriceOverride: true,
		},
		// A concrete route that already carries a vendor price, so a
		// fill-only-empty copy would leave the override invisible.
		{
			ID: id, Provider: "bedrock", Type: "generation", Tier: TierUserVerified,
			Plane: PlaneClassic, Region: "us-east-1",
			PriceIn: 1, PriceOut: 5, PriceSource: "vendor",
		},
	}
	var unverified []ModelInfo
	retireSupersededTemplates(&verified, &unverified)

	if len(verified) != 1 {
		t.Fatalf("want the template retired and the concrete route kept, got %d rows", len(verified))
	}
	got := verified[0]
	if got.Region != "us-east-1" {
		t.Fatalf("kept the wrong row: %s", got.Route().String())
	}
	if got.PriceIn != 42 || got.PriceOut != 84 {
		t.Errorf("price override lost: in=%v out=%v, want 42/84", got.PriceIn, got.PriceOut)
	}
	if got.PriceSource != "user" || !got.PriceOverride {
		t.Errorf("override provenance lost: source=%q override=%v", got.PriceSource, got.PriceOverride)
	}
	// Fill-only-empty facts travel too.
	if got.Name != "Authored Name" || got.Notes != "authored note" || got.ContextLen != 4242 {
		t.Errorf("model facts lost: name=%q notes=%q ctx=%d", got.Name, got.Notes, got.ContextLen)
	}
}

// TestConcreteRouteFactsBeatTheTemplate keeps the direction right: a concrete
// route's OWN authored value must not be overwritten by a template's, except
// for an explicit user price override, which is the whole point of an override.
func TestConcreteRouteFactsBeatTheTemplate(t *testing.T) {
	id := "zz.named"
	verified := []ModelInfo{
		{ID: id, Provider: "bedrock", Type: "generation", Name: "Template Name", Notes: "template note"},
		{ID: id, Provider: "bedrock", Type: "generation", Plane: PlaneClassic, Region: "us-east-1",
			Name: "Concrete Name", Notes: "concrete note"},
	}
	var unverified []ModelInfo
	retireSupersededTemplates(&verified, &unverified)

	if len(verified) != 1 {
		t.Fatalf("want 1 row, got %d", len(verified))
	}
	if verified[0].Name != "Concrete Name" || verified[0].Notes != "concrete note" {
		t.Errorf("the template overwrote the concrete route's own values: %+v", verified[0])
	}
}
