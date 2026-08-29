package cli

import (
	"strings"
	"testing"

	"github.com/apresai/2ndbrain/internal/ai"
)

func TestParsePlaneValue(t *testing.T) {
	if p, err := parsePlaneValue("mantle", "generation"); err != nil || p != ai.PlaneMantle {
		t.Errorf("generation/mantle = %q, %v; want mantle, nil", p, err)
	}
	if p, err := parsePlaneValue("classic", "embedding"); err != nil || p != ai.PlaneClassic {
		t.Errorf("embedding/classic = %q, %v; want classic, nil", p, err)
	}
	// Empty clears the pin, which is how a slot is deliberately unrouted.
	if p, err := parsePlaneValue("", "generation"); err != nil || p != "" {
		t.Errorf("empty = %q, %v; want empty, nil", p, err)
	}
	// A closed set: an unknown plane has no dispatch path at all, so
	// accepting it would just defer a guaranteed failure.
	if _, err := parsePlaneValue("warp", "generation"); err == nil {
		t.Error("unknown plane must be rejected")
	}
	// The mantle plane has no embedding or rerank client in 2nb.
	for _, slot := range []string{"embedding", "rerank"} {
		if _, err := parsePlaneValue("mantle", slot); err == nil {
			t.Errorf("%s must reject the generation-only mantle plane", slot)
		} else if !strings.Contains(err.Error(), "generation-only") {
			t.Errorf("%s rejection should explain why: %v", slot, err)
		}
	}
}

// TestValidateRegionValue guards the value that gets interpolated into the
// mantle host, which the bearer token is then sent to.
func TestValidateRegionValue(t *testing.T) {
	if err := validateRegionValue("us-west-2"); err != nil {
		t.Errorf("a bare region label must be accepted: %v", err)
	}
	if err := validateRegionValue(""); err != nil {
		t.Errorf("empty must be accepted (clears the pin): %v", err)
	}
	for _, bad := range []string{
		"us_west_2",
		"evil.example.com",
		"us-west-2/../..",
		"us-west-2 ",
	} {
		if err := validateRegionValue(bad); err == nil {
			t.Errorf("region %q must be rejected before it reaches a URL", bad)
		}
	}
}

// TestConfigSetGenerationModelRefusesAmbiguousRoute is the one new refusal in
// the config surface, and the reason for it: silently picking one of several
// endpoints is exactly how a mantle-only model ended up dispatched over
// classic Converse.
func TestConfigSetGenerationModelRefusesAmbiguousRoute(t *testing.T) {
	_, root := newContractVault(t)

	// Two real routes for one id, as a discovery walk now produces.
	for _, m := range []ai.ModelInfo{
		{ID: "fake.dual-plane", Provider: "bedrock", Type: "generation", Tier: ai.TierUserVerified,
			Plane: ai.PlaneMantle, InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: "us-west-2"},
		{ID: "fake.dual-plane", Provider: "bedrock", Type: "generation", Tier: ai.TierUserVerified,
			Plane: ai.PlaneClassic, Region: "us-east-1"},
	} {
		if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, m); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runCLIArgs(t, root, "config", "set", "ai.generation_model", "fake.dual-plane")
	if err == nil {
		t.Fatalf("an ambiguous bare id must be refused, got success: %s", string(out))
	}
	for _, want := range []string{
		"fake.dual-plane@mantle/us-west-2",
		"fake.dual-plane@classic/us-east-1",
		"nothing was changed",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("refusal must name %q so the user can paste it:\n%s", want, string(out))
		}
	}

	// Nothing was written.
	got, err := runCLIArgs(t, root, "config", "get", "ai.generation_model")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "fake.dual-plane") {
		t.Errorf("the refused value was written anyway: %s", string(got))
	}

	// Qualifying it succeeds and writes all three keys as one unit.
	if _, err := runCLIArgs(t, root, "config", "set", "ai.generation_model", "fake.dual-plane@mantle/us-west-2"); err != nil {
		t.Fatalf("a qualified route must be accepted: %v", err)
	}
	for _, tc := range []struct{ key, want string }{
		{"ai.generation_model", "fake.dual-plane"},
		{"ai.generation_plane", "mantle"},
		{"ai.generation_region", "us-west-2"},
	} {
		got, err := runCLIArgs(t, root, "config", "get", tc.key)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), tc.want) {
			t.Errorf("%s = %q, want %q — the slot must never be left half-routed", tc.key, strings.TrimSpace(string(got)), tc.want)
		}
	}
}

// TestConfigSetGenerationModelUniqueRouteIsExplicit covers the common case:
// one route, so the user still types a bare id, and the file still ends up
// naming the endpoint explicitly.
func TestConfigSetGenerationModelUniqueRouteIsExplicit(t *testing.T) {
	_, root := newContractVault(t)
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
		ID: "fake.single-route", Provider: "bedrock", Type: "generation", Tier: ai.TierUserVerified,
		Plane: ai.PlaneMantle, InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: "us-east-2",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := runCLIArgs(t, root, "config", "set", "ai.generation_model", "fake.single-route"); err != nil {
		t.Fatalf("a bare id with one route must be accepted: %v", err)
	}
	for _, tc := range []struct{ key, want string }{
		{"ai.generation_plane", "mantle"},
		{"ai.generation_region", "us-east-2"},
	} {
		got, err := runCLIArgs(t, root, "config", "get", tc.key)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), tc.want) {
			t.Errorf("%s = %q, want the resolved route component %q", tc.key, strings.TrimSpace(string(got)), tc.want)
		}
	}
}

// TestConfigSetEmbeddingModelRefusesResolvedMantlePlane covers the second of
// the two write paths into a slot's plane.
//
// parsePlaneValue refuses mantle for embedding and rerank (2nb has no client
// for it there), and the explicit path ran it — but a BARE id resolving to a
// mantle row wrote that plane straight through, so `config set
// ai.embedding_plane mantle` errored while `config set ai.embedding_model
// <mantle-only-model>` quietly produced the same state. Two write paths, one
// validator. This fix had no shipped test until now.
func TestConfigSetEmbeddingModelRefusesResolvedMantlePlane(t *testing.T) {
	_, root := newContractVault(t)
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
		ID: "fake.mantle-only-embed", Provider: "bedrock", Type: "embedding", Tier: ai.TierUserVerified,
		Plane: ai.PlaneMantle, InvokeStrategy: ai.StrategyBedrockMantleResponses, Region: "us-west-2",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := runCLIArgs(t, root, "config", "set", "ai.embedding_model", "fake.mantle-only-embed")
	if err == nil {
		t.Fatalf("a bare id resolving to a mantle row must be refused for the embedding slot: %s", string(out))
	}
	if !strings.Contains(string(out), "generation-only") {
		t.Errorf("refusal should say why the plane is invalid here:\n%s", string(out))
	}
	// Nothing written.
	got, err := runCLIArgs(t, root, "config", "get", "ai.embedding_plane")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "mantle") {
		t.Errorf("the refused plane was written anyway: %s", string(got))
	}
}

// TestConfigSetEmbeddingModelClassicRouteStillWorks is the other half: the
// guard must not block a legitimate classic embedding route.
func TestConfigSetEmbeddingModelClassicRouteStillWorks(t *testing.T) {
	_, root := newContractVault(t)
	if err := ai.SaveUserCatalogEntry(ai.ScopeVault, root, ai.ModelInfo{
		ID: "fake.classic-embed", Provider: "bedrock", Type: "embedding", Tier: ai.TierUserVerified,
		Plane: ai.PlaneClassic, Region: "us-east-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLIArgs(t, root, "config", "set", "ai.embedding_model", "fake.classic-embed"); err != nil {
		t.Fatalf("a classic embedding route must still be settable: %v", err)
	}
	got, err := runCLIArgs(t, root, "config", "get", "ai.embedding_region")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "us-east-1") {
		t.Errorf("embedding region = %q, want the resolved route's region", strings.TrimSpace(string(got)))
	}
}

// TestConfigSetGenerationModelUnknownModelStillAllowed preserves the existing
// doctrine that naming a model 2nb's catalog does not know is legitimate: a
// model can exist before the catalog learns about it.
func TestConfigSetGenerationModelUnknownModelStillAllowed(t *testing.T) {
	_, root := newContractVault(t)
	if _, err := runCLIArgs(t, root, "config", "set", "ai.generation_model", "vendor.not-in-any-catalog"); err != nil {
		t.Fatalf("an unknown model must still be settable: %v", err)
	}
	got, err := runCLIArgs(t, root, "config", "get", "ai.generation_model")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "vendor.not-in-any-catalog") {
		t.Errorf("unknown model was not stored: %s", string(got))
	}
}
