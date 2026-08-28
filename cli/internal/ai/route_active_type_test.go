package ai

import "testing"

// TestMarkActiveRoutesDoesNotLeakAcrossTypes covers the type component of the
// winners key.
//
// winners is keyed by routeKey, which carries no Type, so without the type
// suffix one id configured in BOTH slots with a different best route per slot
// let one slot's winner mark the other slot's row — telling the user they are
// running an endpoint they never selected for that slot.
func TestMarkActiveRoutesDoesNotLeakAcrossTypes(t *testing.T) {
	setupHome(t)
	cfg := AIConfig{
		Provider: "bedrock", Bedrock: BedrockConfig{Region: "us-east-1"},
		GenerationModel: "zz.both", GenerationPlane: PlaneClassic, GenerationRegion: "us-west-2",
		EmbeddingModel: "zz.both", EmbeddingPlane: PlaneClassic, EmbeddingRegion: "us-east-1",
	}
	rows := []ModelInfo{
		{Provider: "bedrock", ID: "zz.both", Type: "generation", Plane: PlaneClassic, Region: "us-west-2"},
		{Provider: "bedrock", ID: "zz.both", Type: "generation", Plane: PlaneClassic, Region: "us-east-1"},
		{Provider: "bedrock", ID: "zz.both", Type: "embedding", Plane: PlaneClassic, Region: "us-east-1"},
		{Provider: "bedrock", ID: "zz.both", Type: "embedding", Plane: PlaneClassic, Region: "us-west-2"},
	}
	var empty []ModelInfo
	markActiveRoutes(cfg, &rows, &empty)

	active := map[string]string{}
	n := 0
	for _, m := range rows {
		if m.Active {
			n++
			active[m.Type] = m.Region
		}
	}
	if n != 2 {
		t.Fatalf("active rows = %d, want exactly one per slot", n)
	}
	if active["generation"] != "us-west-2" || active["embedding"] != "us-east-1" {
		t.Errorf("each slot must mark its OWN configured route, got %v", active)
	}
}
