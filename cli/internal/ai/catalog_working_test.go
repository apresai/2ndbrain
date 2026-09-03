package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func findCatalogModel(models []ModelInfo, id string) (ModelInfo, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// TestWorkingSet_VerifiedTierIsNotEvidence is the whole point of the flag:
// a builtin tier=verified entry means 2nb has a harness for the model, not
// that this AWS account is entitled to invoke it. Only a passing probe puts
// a model in the working set.
func TestWorkingSet_VerifiedTierIsNotEvidence(t *testing.T) {
	setupHome(t)
	cfg := DefaultAIConfig()
	// Sonnet 4.6 ships verified + recommended but is neither active nor
	// probed on a fresh machine.
	const unprobedID = "us.anthropic.claude-sonnet-4-6"

	result, err := BuildModelList(context.Background(), MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	m, ok := findCatalogModel(result.Verified, unprobedID)
	if !ok {
		t.Fatalf("builtin catalog no longer ships %s; pick another unprobed verified model", unprobedID)
	}
	if m.Tier != TierVerified {
		t.Fatalf("%s tier = %q, want verified (the premise of this test)", unprobedID, m.Tier)
	}
	if m.Working {
		t.Errorf("%s is verified but never probed — it must NOT be in the working set", unprobedID)
	}
}

// TestWorkingSet_PassingProbeAdmits checks the positive case end to end
// through the real user-catalog path: a saved passing probe promotes the
// model into the working set, and a saved FAILING probe does not.
func TestWorkingSet_PassingProbeAdmits(t *testing.T) {
	setupHome(t)
	cfg := DefaultAIConfig()
	const passID = "us.anthropic.claude-sonnet-4-6"
	const failID = "us.anthropic.claude-opus-4-6"
	now := time.Now().UTC().Format(time.RFC3339)

	mustSave := func(entry ModelInfo) {
		t.Helper()
		if err := SaveUserCatalogEntry(ScopeGlobal, "", entry); err != nil {
			t.Fatalf("save %s: %v", entry.ID, err)
		}
	}
	mustSave(ModelInfo{
		ID: passID, Provider: "bedrock", Type: "generation",
		Tier: TierUserVerified, TestedAt: now, TestLatencyMs: 420,
	})
	mustSave(ModelInfo{
		ID: failID, Provider: "bedrock", Type: "generation",
		Tier: TierVerified, TestedAt: now,
		TestError: "403 access denied", TestErrorCode: string(TestErrAccessDenied),
	})

	result, err := BuildModelList(context.Background(), MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	pass, ok := findCatalogModel(result.Verified, passID)
	if !ok {
		t.Fatalf("%s missing from merged catalog", passID)
	}
	if !pass.Working {
		t.Errorf("%s has a passing probe on record but is not in the working set: %+v", passID, pass)
	}
	fail, ok := findCatalogModel(result.Verified, failID)
	if !ok {
		t.Fatalf("%s missing from merged catalog", failID)
	}
	if fail.Working {
		t.Errorf("%s last probe FAILED (%s) — it must not be in the working set", failID, fail.TestErrorCode)
	}
}

// TestWorkingSet_ActiveModelsAlwaysIncluded pins the "picker is never empty"
// guarantee: on a freshly bound vault nothing has been probed, yet the two
// models the user is already running must still be selectable.
func TestWorkingSet_ActiveModelsAlwaysIncluded(t *testing.T) {
	setupHome(t)
	cfg := DefaultAIConfig()

	result, err := BuildModelList(context.Background(), MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	for _, id := range []string{cfg.EmbeddingModel, cfg.GenerationModel} {
		m, ok := findCatalogModel(result.Verified, id)
		if !ok {
			t.Fatalf("active model %s missing from merged catalog", id)
		}
		if m.TestedAt != "" {
			t.Fatalf("active model %s is unexpectedly probed; this test needs an untested vault", id)
		}
		if !m.Working {
			t.Errorf("active %s model %s must be in the working set even untested", m.Type, id)
		}
	}
	working := 0
	for _, m := range result.Verified {
		if m.Working {
			working++
		}
	}
	if working != 2 {
		t.Errorf("untested vault should have exactly the 2 active models working, got %d", working)
	}
}

// TestWorkingSet_DisabledAndIncompatibleExcluded covers the two subtractive
// rules: an explicit disable and a static incompatibility both remove a
// model that would otherwise qualify on its passing probe.
func TestWorkingSet_DisabledAndIncompatibleExcluded(t *testing.T) {
	setupHome(t) // the ai package has no TestMain: without this the test reads the developer's real ~/.config/2nb/models.yaml
	cfg := DefaultAIConfig()
	now := time.Now().UTC().Format(time.RFC3339)
	passing := ModelInfo{
		ID: "us.anthropic.claude-sonnet-4-6", Provider: "bedrock",
		Type: "generation", TestedAt: now, Compatible: true,
	}

	tests := []struct {
		name    string
		mutate  func(*ModelInfo)
		working bool
	}{
		{name: "passing probe", mutate: func(*ModelInfo) {}, working: true},
		{name: "explicitly disabled", mutate: func(m *ModelInfo) { m.Enabled = Ptr(false) }},
		{name: "explicitly enabled", mutate: func(m *ModelInfo) { m.Enabled = Ptr(true) }, working: true},
		{name: "enabled unset", mutate: func(m *ModelInfo) { m.Enabled = nil }, working: true},
		{name: "incompatible", mutate: func(m *ModelInfo) { m.Compatible = false }},
		{name: "never probed", mutate: func(m *ModelInfo) { m.TestedAt = "" }},
		{name: "untested active is working", mutate: func(m *ModelInfo) { m.TestedAt = ""; m.Active = true }, working: true},
		{name: "active failed probe is not working", mutate: func(m *ModelInfo) {
			m.Active = true
			m.TestError = "403 access denied"
			m.TestErrorCode = string(TestErrAccessDenied)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := passing
			tc.mutate(&m)
			if got := catalogWorking(m, cfg); got != tc.working {
				t.Errorf("catalogWorking = %v, want %v for %+v", got, tc.working, m)
			}
		})
	}
}

// TestKnownVendorSlugs_StaticVocabularyWithoutCatalogRows is the sticky-vendor
// contract: a policy states intent about FUTURE models, so xAI and OpenAI must
// be nameable on a provider whose catalog contributes no rows at all.
func TestKnownVendorSlugs_StaticVocabularyWithoutCatalogRows(t *testing.T) {
	known := KnownVendorSlugs("bedrock", nil)
	for _, slug := range []string{"xai", "openai", "anthropic", "amazon", "deepseek"} {
		if !known[slug] {
			t.Errorf("bedrock known vendors missing %q with an empty catalog: %v", slug, known)
		}
	}
	// The static vocabulary is provider-scoped: it must not leak to others.
	if other := KnownVendorSlugs("ollama", nil); len(other) != 0 {
		t.Errorf("ollama known vendors should be catalog-derived only, got %v", other)
	}
}

// TestWorkingSet_JSONIncludesFalseWorking pins the encode contract: a
// non-working row must include `"working":false`. omitempty would drop it
// and Swift working==nil would mean both "old CLI" and "not working".
func TestWorkingSet_JSONIncludesFalseWorking(t *testing.T) {
	setupHome(t) // the ai package has no TestMain: without this the test reads the developer's real ~/.config/2nb/models.yaml
	body, err := json.Marshal(ModelInfo{ID: "us.anthropic.claude-sonnet-5", Working: false})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(body, []byte(`"working":false`)) {
		t.Errorf("non-working row must emit working:false, got %s", body)
	}
	trueBody, err := json.Marshal(ModelInfo{ID: "haiku", Working: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(trueBody, []byte(`"working":true`)) {
		t.Errorf("working row must emit working:true, got %s", trueBody)
	}
}

// TestWorkingSet_ActiveFailedProbeIsNotWorking is the access_denied-on-active
// case: the model stays in the full catalog (the picker can still select
// "current") but working is false so it does not mean "this account can
// invoke it".
func TestWorkingSet_ActiveFailedProbeIsNotWorking(t *testing.T) {
	setupHome(t)
	cfg := DefaultAIConfig()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := SaveUserCatalogEntry(ScopeGlobal, "", ModelInfo{
		ID: cfg.GenerationModel, Provider: "bedrock", Type: "generation",
		TestedAt: now, TestError: "403 access denied",
		TestErrorCode: string(TestErrAccessDenied),
	}); err != nil {
		t.Fatalf("save failed probe on active: %v", err)
	}

	result, err := BuildModelList(context.Background(), MergedListOptions{Config: cfg})
	if err != nil {
		t.Fatalf("BuildModelList: %v", err)
	}
	m, ok := findCatalogModel(result.Verified, cfg.GenerationModel)
	if !ok {
		t.Fatalf("active generation model %s missing from the full list", cfg.GenerationModel)
	}
	if m.Working {
		t.Errorf("active + access_denied must not be working: %+v", m)
	}
	if m.TestErrorCode != string(TestErrAccessDenied) {
		t.Errorf("test_error_code = %q, want %s", m.TestErrorCode, TestErrAccessDenied)
	}
}
