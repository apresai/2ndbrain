package ai

import "testing"

// TestBedrockDiscoveryRegionsCoversDocumentedSet pins that the classic plane is
// swept across the documented US regions, not just the user's primary. Before
// routes, classic was listed in exactly one region while mantle was listed in
// three, so a model served only in us-west-2 on classic was invisible to a
// us-east-1 user with no signal that a region had gone unlooked-at.
func TestBedrockDiscoveryRegionsCoversDocumentedSet(t *testing.T) {
	got := BedrockDiscoveryRegions(BedrockConfig{Region: "us-east-1"})

	// Assert the LITERAL expected set, not "every element of the slice the
	// implementation iterates". The earlier version of this test looped over
	// bedrockDiscoveryRegions and checked each was present, which is the
	// function's own loop body restated: it passed for any contents of that
	// slice, including a slice narrowed back to one region, which is exactly
	// the regression it was meant to catch.
	want := []string{"us-east-1", "us-east-2", "us-west-2"}
	if len(got) != len(want) {
		t.Fatalf("regions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("regions = %v, want %v (primary first, then documented order)", got, want)
		}
	}
	assertNoDuplicateRegions(t, got)
}

// TestBedrockDiscoveryRegionsIsWiderThanConfigured is the property that
// actually matters and that the literal set above cannot express on its own:
// the classic sweep must cover MORE than the user configured, or a model
// served only in another US region stays invisible. This fails if
// BedrockDiscoveryRegions is ever reduced back to ResolveBedrockRegions.
func TestBedrockDiscoveryRegionsIsWiderThanConfigured(t *testing.T) {
	setupHome(t)
	cfg := BedrockConfig{Region: "us-east-1"}
	configured := ResolveBedrockRegions(cfg)
	swept := BedrockDiscoveryRegions(cfg)
	if len(swept) <= len(configured) {
		t.Fatalf("sweep %v must be wider than the configured set %v", swept, configured)
	}
	for _, c := range configured {
		found := false
		for _, s := range swept {
			if s == c {
				found = true
			}
		}
		if !found {
			t.Errorf("configured region %q dropped from the sweep %v", c, swept)
		}
	}
}

// TestBedrockDiscoveryRegionsIncludesForeignPrimary covers the case the mantle
// side must NOT do: a user whose primary region is outside the documented set
// still has their own region swept on the classic plane, because their models
// genuinely live there.
func TestBedrockDiscoveryRegionsIncludesForeignPrimary(t *testing.T) {
	got := BedrockDiscoveryRegions(BedrockConfig{Region: "eu-west-1"})
	want := []string{"eu-west-1", "us-east-1", "us-east-2", "us-west-2"}
	if len(got) != len(want) {
		t.Fatalf("regions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("regions = %v, want %v", got, want)
		}
	}
	assertNoDuplicateRegions(t, got)
}

// TestMantleDiscoveryRegionsExcludesForeignPrimary is the counterpart, and the
// reason the two functions differ. The mantle host is derived as
// bedrock-mantle.<region>.api.aws, so walking a region AWS does not serve
// resolves a host that does not exist. The resulting failure would raise a
// spurious partial-listing warning, which the discover diff's GONE shield then
// acts on by suppressing every bedrock GONE entry.
func TestMantleDiscoveryRegionsExcludesForeignPrimary(t *testing.T) {
	got := mantleDiscoveryRegions(BedrockConfig{Region: "eu-west-1"})
	want := []string{"us-east-1", "us-east-2", "us-west-2"}
	if len(got) != len(want) {
		t.Fatalf("regions = %v, want exactly the documented mantle set %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("regions = %v, want %v", got, want)
		}
	}
	// The classic sweep DOES include the foreign primary; the two functions
	// must not converge, or walking a host AWS does not serve comes back.
	if len(mantleDiscoveryRegions(BedrockConfig{Region: "eu-west-1"})) ==
		len(BedrockDiscoveryRegions(BedrockConfig{Region: "eu-west-1"})) {
		t.Error("mantle and classic sweeps must differ for a foreign primary")
	}
	assertNoDuplicateRegions(t, got)
}

func TestMantleDiscoveryRegionsPrimaryFirst(t *testing.T) {
	got := mantleDiscoveryRegions(BedrockConfig{Region: "us-west-2"})
	if got[0] != "us-west-2" {
		t.Errorf("a primary that IS a mantle region must lead, got %v", got)
	}
	assertNoDuplicateRegions(t, got)
}

func assertNoDuplicateRegions(t *testing.T, regions []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, r := range regions {
		if seen[r] {
			t.Errorf("duplicate region %q in %v (a region would be listed twice)", r, regions)
		}
		seen[r] = true
	}
}
