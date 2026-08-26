package ai

import "testing"

// TestBedrockDiscoveryRegionsCoversDocumentedSet pins that the classic plane is
// swept across the documented US regions, not just the user's primary. Before
// routes, classic was listed in exactly one region while mantle was listed in
// three, so a model served only in us-west-2 on classic was invisible to a
// us-east-1 user with no signal that a region had gone unlooked-at.
func TestBedrockDiscoveryRegionsCoversDocumentedSet(t *testing.T) {
	got := BedrockDiscoveryRegions(BedrockConfig{Region: "us-east-1"})
	if got[0] != "us-east-1" {
		t.Errorf("primary region must come first, got %v", got)
	}
	for _, want := range bedrockDiscoveryRegions {
		found := false
		for _, r := range got {
			if r == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("documented region %q missing from the classic sweep: %v", want, got)
		}
	}
	assertNoDuplicateRegions(t, got)
}

// TestBedrockDiscoveryRegionsIncludesForeignPrimary covers the case the mantle
// side must NOT do: a user whose primary region is outside the documented set
// still has their own region swept on the classic plane, because their models
// genuinely live there.
func TestBedrockDiscoveryRegionsIncludesForeignPrimary(t *testing.T) {
	got := BedrockDiscoveryRegions(BedrockConfig{Region: "eu-west-1"})
	if got[0] != "eu-west-1" {
		t.Fatalf("foreign primary must still be swept first, got %v", got)
	}
	if len(got) != len(bedrockDiscoveryRegions)+1 {
		t.Errorf("want the documented set plus the foreign primary, got %v", got)
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
	if len(got) != len(bedrockDiscoveryRegions) {
		t.Fatalf("mantle must walk only the documented regions, got %v", got)
	}
	for _, r := range got {
		if r == "eu-west-1" {
			t.Errorf("mantle must not walk a region the plane does not serve: %v", got)
		}
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
