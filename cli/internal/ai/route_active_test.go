package ai

import "testing"

// TestActiveSurvivesRetire is the regression test for a defect the audit
// found: Active was marked once, near the top of BuildModelList, BEFORE
// discovery and before retire. Once discovery yields concrete per-region rows,
// the user's configured row is usually the unpinned template that retire
// removes, so the flag vanished with it and nothing came back marked.
//
// Not cosmetic: the GUI shows no current model, and `models verify` skips any
// model that is neither recommended nor active, so it silently stopped
// covering the user's own configured model.
func TestActiveSurvivesRetire(t *testing.T) {
	setupHome(t) // the ai package has no TestMain: without this the test reads the developer's real ~/.config/2nb/models.yaml
	cfg := AIConfig{Provider: "bedrock", GenerationModel: "m"}

	verified := []ModelInfo{
		{Provider: "bedrock", ID: "m", Type: "generation", Plane: PlaneClassic},
	}
	unverified := []ModelInfo{
		{Provider: "bedrock", ID: "m", Type: "generation", Plane: PlaneClassic, Region: "us-east-1"},
	}
	retireSupersededTemplates(&verified, &unverified)

	// Re-marking is what BuildModelList does after retire.
	all := append(append([]ModelInfo{}, verified...), unverified...)
	active := 0
	for i := range all {
		if isActiveModel(all[i], cfg) {
			active++
		}
	}
	if active == 0 {
		t.Fatal("the configured model has no surviving row that reads as active")
	}
}

// TestIsActiveModelUnpinnedConfigMatchesAnyRoute pins the compatibility rule
// that makes the above work: a config written before routes names no plane or
// region, so it must still match its model's row whatever route that row
// carries. Without this an upgraded vault would show nothing as active until
// the user re-picked.
func TestIsActiveModelUnpinnedConfigMatchesAnyRoute(t *testing.T) {
	cfg := AIConfig{Provider: "bedrock", GenerationModel: "m"}
	row := ModelInfo{Provider: "bedrock", ID: "m", Type: "generation", Plane: PlaneMantle, Region: "us-west-2"}
	if !isActiveModel(row, cfg) {
		t.Error("an unpinned legacy config must still mark its model active")
	}
}

// TestIsActiveModelPinnedConfigMatchesOnlyItsRoute is the other half: once the
// route IS pinned, only that endpoint is active. Marking every route of the
// model would tell the user they are running three endpoints at once.
func TestIsActiveModelPinnedConfigMatchesOnlyItsRoute(t *testing.T) {
	cfg := AIConfig{Provider: "bedrock", GenerationModel: "m", GenerationPlane: PlaneMantle, GenerationRegion: "us-west-2"}
	mine := ModelInfo{Provider: "bedrock", ID: "m", Type: "generation", Plane: PlaneMantle, Region: "us-west-2"}
	sibling := ModelInfo{Provider: "bedrock", ID: "m", Type: "generation", Plane: PlaneMantle, Region: "us-east-1"}
	if !isActiveModel(mine, cfg) {
		t.Error("the configured route must be active")
	}
	if isActiveModel(sibling, cfg) {
		t.Error("a sibling region must NOT read as active")
	}
}
